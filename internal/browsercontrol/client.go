// Package browsercontrol provides the loopback-only client for the desktop
// browser provider. It deliberately does not start or manage a browser.
package browsercontrol

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ConfigFileName       = "browser-provider.json"
	ConfigPathEnv        = "KIWI_CODE_BROWSER_PROVIDER_CONFIG"
	maxConfigBytes int64 = 4 << 10

	// MaxRequestBytes bounds the JSON sent to the provider. Public action
	// requests are bounded separately at 1 MiB; this leaves room for the
	// project and thread identity added by the backend.
	MaxRequestBytes = 2 << 20
	// MaxResponseBytes accommodates a base64-encoded 15 MiB preview plus its
	// provider envelope while still placing a firm bound on every response.
	MaxResponseBytes int64 = 24 << 20

	defaultRequestTimeout = 75 * time.Second
)

var (
	ErrUnavailable      = errors.New("browser provider unavailable")
	ErrRequestTooLarge  = errors.New("browser provider request too large")
	ErrResponseTooLarge = errors.New("browser provider response too large")
	ErrInvalidResponse  = errors.New("invalid browser provider response")
	ErrProvider         = errors.New("browser provider rejected the operation")
	ErrSessionNotFound  = errors.New("browser session not found")
	ErrFrameUnavailable = errors.New("browser frame is unavailable")
	// ErrPreviewNotReady is retained as an internal compatibility alias for
	// providers that used the earlier preview_not_ready code.
	ErrPreviewNotReady = ErrFrameUnavailable
)

var allowedOperationErrorCodes = map[string]struct{}{
	"blocked_command":        {},
	"blocked_origin":         {},
	"element_not_found":      {},
	"invalid_params":         {},
	"invalid_url":            {},
	"navigation_unavailable": {},
	"operation_failed":       {},
	"operation_timeout":      {},
	"output_too_large":       {},
	"page_not_found":         {},
	"stale_ref":              {},
	"tab_limit_reached":      {},
	"unsupported_operation":  {},
	"wait_timeout":           {},
}

// OperationError reports a provider error code from a fixed local allowlist.
// Provider-controlled messages are never exposed across the backend boundary.
type OperationError struct {
	Code string
}

func (e *OperationError) Error() string {
	return "browser provider operation failed: " + e.Code
}

// OperationErrorCode returns a sanitized provider operation code.
func OperationErrorCode(err error) (string, bool) {
	var operationError *OperationError
	if !errors.As(err, &operationError) {
		return "", false
	}
	return operationError.Code, true
}

type providerConfig struct {
	Version int    `json:"version"`
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
}

// Request is the authenticated action request sent to the desktop provider.
type Request struct {
	ProjectID string
	ThreadID  string
	Operation string
	Params    json.RawMessage
}

type providerRequest struct {
	ProjectID string          `json:"projectId"`
	ThreadID  string          `json:"threadId"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

type providerResponse struct {
	OK     *bool           `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type providerError struct {
	Code string `json:"code"`
}

// Client loads the provider configuration for each action so an Electron
// restart can publish a new port and token without restarting the Go backend.
type Client struct {
	configPath string
	httpClient *http.Client
	timeout    time.Duration
}

// ConfigPath returns the provider configuration path. The environment override
// is intended for isolated development stacks; production uses the data
// directory by default.
func ConfigPath(dataDirectory string) string {
	if override := os.Getenv(ConfigPathEnv); override != "" {
		return override
	}
	return filepath.Join(dataDirectory, ConfigFileName)
}

func New(configPath string) *Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: defaultRequestTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		configPath: configPath,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout: defaultRequestTimeout,
	}
}

// Action sends one operation to the configured provider and returns only the
// raw JSON result, never the provider envelope or credentials.
func (c *Client) Action(ctx context.Context, request Request) (json.RawMessage, error) {
	config, err := loadConfig(c.configPath)
	if err != nil {
		return nil, fmt.Errorf("%w: configuration is not available", ErrUnavailable)
	}

	params := request.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(providerRequest{
		ProjectID: request.ProjectID,
		ThreadID:  request.ThreadID,
		Operation: request.Operation,
		Params:    params,
	})
	if err != nil {
		return nil, fmt.Errorf("encode browser provider request: %w", err)
	}
	if len(body) > MaxRequestBytes {
		return nil, ErrRequestTooLarge
	}

	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := "http://127.0.0.1:" + strconv.Itoa(config.Port) + "/v1/action"
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create browser provider request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+config.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: could not connect", ErrUnavailable)
	}
	defer response.Body.Close()

	contents, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: could not read response", ErrUnavailable)
	}
	if int64(len(contents)) > MaxResponseBytes {
		return nil, errors.Join(ErrUnavailable, ErrResponseTooLarge)
	}
	if response.StatusCode < 200 || response.StatusCode >= 500 ||
		(response.StatusCode >= 300 && response.StatusCode < 400) {
		// Redirects are never followed. Treat non-4xx statuses as unavailable
		// without parsing or exposing their untrusted response bodies.
		return nil, ErrUnavailable
	}

	var envelope providerResponse
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}
	if err := requireJSONEOF(decoder); err != nil || envelope.OK == nil {
		return nil, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}
	if *envelope.OK {
		if response.StatusCode < 200 || response.StatusCode >= 300 || len(envelope.Result) == 0 || len(envelope.Error) != 0 || !json.Valid(envelope.Result) {
			return nil, errors.Join(ErrUnavailable, ErrInvalidResponse)
		}
		return append(json.RawMessage(nil), envelope.Result...), nil
	}
	if response.StatusCode < 400 || response.StatusCode >= 500 || len(envelope.Result) != 0 || len(envelope.Error) == 0 {
		return nil, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}

	var providerFailure providerError
	errorDecoder := json.NewDecoder(bytes.NewReader(envelope.Error))
	errorDecoder.DisallowUnknownFields()
	if err := errorDecoder.Decode(&providerFailure); err != nil || requireJSONEOF(errorDecoder) != nil || strings.TrimSpace(providerFailure.Code) == "" {
		return nil, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}
	if response.StatusCode == http.StatusNotFound {
		switch providerFailure.Code {
		case "session_not_found":
			return nil, ErrSessionNotFound
		case "frame_unavailable", "preview_not_ready":
			return nil, ErrFrameUnavailable
		}
	}
	if _, allowed := allowedOperationErrorCodes[providerFailure.Code]; allowed {
		return nil, &OperationError{Code: providerFailure.Code}
	}
	// Unknown provider errors are deliberately collapsed to a sentinel. The
	// untrusted code and body never cross the backend boundary.
	return nil, ErrProvider
}

func loadConfig(path string) (providerConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return providerConfig{}, err
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return providerConfig{}, err
	}
	if int64(len(contents)) > maxConfigBytes {
		return providerConfig{}, errors.New("provider configuration is too large")
	}

	var config providerConfig
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return providerConfig{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return providerConfig{}, err
	}
	if config.Version != 1 {
		return providerConfig{}, errors.New("unsupported provider configuration version")
	}
	if config.PID <= 0 {
		return providerConfig{}, errors.New("invalid provider pid")
	}
	if config.Port < 1 || config.Port > 65535 {
		return providerConfig{}, errors.New("invalid provider port")
	}
	if len(config.Token) != 64 {
		return providerConfig{}, errors.New("invalid provider token")
	}
	decodedToken := make([]byte, hex.DecodedLen(len(config.Token)))
	if _, err := hex.Decode(decodedToken, []byte(config.Token)); err != nil {
		return providerConfig{}, errors.New("invalid provider token")
	}
	return config, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
