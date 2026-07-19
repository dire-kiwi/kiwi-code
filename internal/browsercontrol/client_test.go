package browsercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testProviderToken = "0123456789abcdef0123456789ABCDEF0123456789abcdef0123456789ABCDEF"

func TestLoadConfigValidation(t *testing.T) {
	valid := fmt.Sprintf(`{"version":1,"pid":123,"port":4321,"token":%q}`, testProviderToken)
	tests := []struct {
		name     string
		contents string
		valid    bool
	}{
		{name: "valid mixed-case hex token", contents: valid, valid: true},
		{name: "wrong version", contents: strings.Replace(valid, `"version":1`, `"version":2`, 1)},
		{name: "zero pid", contents: strings.Replace(valid, `"pid":123`, `"pid":0`, 1)},
		{name: "negative pid", contents: strings.Replace(valid, `"pid":123`, `"pid":-1`, 1)},
		{name: "zero port", contents: strings.Replace(valid, `"port":4321`, `"port":0`, 1)},
		{name: "high port", contents: strings.Replace(valid, `"port":4321`, `"port":65536`, 1)},
		{name: "short token", contents: strings.Replace(valid, testProviderToken, "abcd", 1)},
		{name: "non hex token", contents: strings.Replace(valid, testProviderToken, strings.Repeat("z", 64), 1)},
		{name: "unknown field", contents: strings.TrimSuffix(valid, "}") + `,"secret":true}`},
		{name: "trailing value", contents: valid + `{}`},
		{name: "missing field", contents: `{"version":1,"pid":123,"port":4321}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ConfigFileName)
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig(path)
			if (err == nil) != test.valid {
				t.Fatalf("loadConfig() error = %v, valid = %t", err, test.valid)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := loadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
			t.Fatal("loadConfig() unexpectedly accepted a missing file")
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ConfigFileName)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxConfigBytes)+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil {
			t.Fatal("loadConfig() unexpectedly accepted an oversized file")
		}
	})
}

func TestConfigPathUsesOptionalEnvironmentOverride(t *testing.T) {
	t.Setenv(ConfigPathEnv, "")
	if got, want := ConfigPath("/data"), filepath.Join("/data", ConfigFileName); got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
	t.Setenv(ConfigPathEnv, "/isolated/provider.json")
	if got := ConfigPath("/data"); got != "/isolated/provider.json" {
		t.Fatalf("ConfigPath() override = %q", got)
	}
}

func TestClientForwardsAuthenticatedLoopbackAction(t *testing.T) {
	var received providerRequest
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/action" || r.URL.RawQuery != "" {
			t.Errorf("provider request = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testProviderToken {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"value":42}}`))
	}))
	defer provider.Close()

	configPath := writeProviderConfig(t, provider.URL)
	client := New(configPath)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("provider transport does not explicitly disable proxies")
	}
	result, err := client.Action(context.Background(), Request{
		ProjectID: "project-a",
		ThreadID:  "thread-b",
		Operation: "navigate.goto",
		Params:    json.RawMessage(`{"url":"https://example.com"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"value":42}` {
		t.Fatalf("result = %s", result)
	}
	if received.ProjectID != "project-a" || received.ThreadID != "thread-b" || received.Operation != "navigate.goto" ||
		string(received.Params) != `{"url":"https://example.com"}` {
		t.Fatalf("forwarded request = %#v", received)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()

	client := New(writeProviderConfig(t, provider.URL))
	_, err := client.Action(context.Background(), Request{Operation: "session.status"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect error = %v, want ErrUnavailable", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target received %d requests", redirected.Load())
	}
}

func TestClientUsesRequestContext(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(250 * time.Millisecond):
		}
	}))
	defer provider.Close()
	client := New(writeProviderConfig(t, provider.URL))
	client.timeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := client.Action(ctx, Request{Operation: "wait"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("context cancellation error = %v, want ErrUnavailable", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("context-aware request took %s", elapsed)
	}
}

func TestClientClassifiesAllowlistedProviderErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		want     error
		wantCode string
	}{
		{name: "session missing", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"session_not_found"}}`, want: ErrSessionNotFound},
		{name: "preview not ready", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"preview_not_ready"}}`, want: ErrPreviewNotReady},
		{name: "allowlisted operation error", status: http.StatusBadRequest, body: `{"ok":false,"error":{"code":"invalid_params"}}`, wantCode: "invalid_params"},
		{name: "unknown 404", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"something_else"}}`, want: ErrProvider},
		{name: "malformed", status: http.StatusOK, body: `{"ok":true}`, want: ErrUnavailable},
		{name: "unknown envelope field", status: http.StatusOK, body: `{"ok":true,"result":{},"token":"secret"}`, want: ErrUnavailable},
		{name: "server error", status: http.StatusInternalServerError, body: testProviderToken, want: ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()
			client := New(writeProviderConfig(t, provider.URL))
			_, err := client.Action(context.Background(), Request{Operation: "preview"})
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Action() error = %v, want %v", err, test.want)
			}
			if test.wantCode != "" {
				if code, ok := OperationErrorCode(err); !ok || code != test.wantCode {
					t.Fatalf("OperationErrorCode() = %q, %t; want %q, true (error %v)", code, ok, test.wantCode, err)
				}
			}
			if err != nil && strings.Contains(err.Error(), testProviderToken) {
				t.Fatalf("Action() error exposed provider token: %v", err)
			}
		})
	}
}

func TestClientBoundsRequestAndResponseBodies(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", int(MaxResponseBytes))))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer provider.Close()
	configPath := writeProviderConfig(t, provider.URL)
	client := New(configPath)

	_, err := client.Action(context.Background(), Request{
		Operation: "evaluate",
		Params:    json.RawMessage(`{"value":"` + strings.Repeat("x", MaxRequestBytes) + `"}`),
	})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("oversized request error = %v, want ErrRequestTooLarge", err)
	}

	_, err = client.Action(context.Background(), Request{Operation: "screenshot"})
	if !errors.Is(err, ErrResponseTooLarge) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized response error = %v, want ErrResponseTooLarge and ErrUnavailable", err)
	}
}

func TestClientErrorsDoNotExposeConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "secret-provider-"+testProviderToken+".json")
	if err := os.WriteFile(path, []byte(`{"version":1,"pid":1,"port":1,"token":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(path).Action(context.Background(), Request{Operation: "session.status"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Action() error = %v", err)
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), testProviderToken) || strings.Contains(err.Error(), "bad") {
		t.Fatalf("Action() exposed provider configuration: %v", err)
	}
}

func writeProviderConfig(t *testing.T, providerURL string) string {
	t.Helper()
	parsed, err := url.Parse(providerURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ConfigFileName)
	contents := fmt.Sprintf(`{"version":1,"pid":%d,"port":%d,"token":%q}`, os.Getpid(), port, testProviderToken)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
