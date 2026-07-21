package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	maxBrowserActionBodyBytes int64 = 1 << 20
	maxBrowserFrameBytes            = 15 << 20
	maxBrowserFrameDimension        = 16384
	browserCleanupTimeout           = 3 * time.Second
	browserCleanupConcurrency       = 4
)

var allowedBrowserOperations = map[string]struct{}{
	"session.status":     {},
	"session.start":      {},
	"session.disconnect": {},
	"session.stop":       {},
	"tabs.list":          {},
	"tabs.new":           {},
	"tabs.select":        {},
	"tabs.close":         {},
	"navigate.goto":      {},
	"navigate.back":      {},
	"navigate.forward":   {},
	"navigate.reload":    {},
	"snapshot":           {},
	"click":              {},
	"fill":               {},
	"key":                {},
	"wait":               {},
	"evaluate":           {},
	"screenshot":         {},
	"cdp":                {},
	"preview":            {},
}

type browserProviderErrorResponse struct {
	Status  int
	Message string
}

var browserProviderErrorResponses = map[string]browserProviderErrorResponse{
	"blocked_command":        {Status: http.StatusForbidden, Message: "That browser command is blocked by the page security boundary."},
	"blocked_origin":         {Status: http.StatusForbidden, Message: "Navigation to a protected Kiwi Code origin is blocked."},
	"element_not_found":      {Status: http.StatusNotFound, Message: "Browser element not found. Take a new snapshot or check the selector."},
	"invalid_params":         {Status: http.StatusBadRequest, Message: "Invalid browser action parameters."},
	"invalid_url":            {Status: http.StatusBadRequest, Message: "Invalid browser URL."},
	"navigation_unavailable": {Status: http.StatusConflict, Message: "Browser history navigation is unavailable."},
	"operation_failed":       {Status: http.StatusBadGateway, Message: "The browser could not complete the operation."},
	"operation_timeout":      {Status: http.StatusGatewayTimeout, Message: "The browser operation timed out."},
	"output_too_large":       {Status: http.StatusRequestEntityTooLarge, Message: "The browser result exceeds the size limit."},
	"page_not_found":         {Status: http.StatusNotFound, Message: "Browser page not found. Open or select a tab and try again."},
	"stale_ref":              {Status: http.StatusConflict, Message: "The browser element reference is stale. Take a new snapshot and try again."},
	"tab_limit_reached":      {Status: http.StatusConflict, Message: "The browser tab limit has been reached."},
	"unsupported_operation":  {Status: http.StatusBadRequest, Message: "Unsupported browser operation."},
	"wait_timeout":           {Status: http.StatusRequestTimeout, Message: "The browser wait condition timed out."},
}

type browserActionInput struct {
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

type browserActionOutput struct {
	Result json.RawMessage `json:"result"`
}

type browserPreviewResult struct {
	Data       string `json:"data"`
	MIMEType   string `json:"mimeType"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Generation int64  `json:"generation"`
	CapturedAt string `json:"capturedAt"`
}

func (s *Server) browserStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}
	result, err := s.browser.Action(r.Context(), browsercontrol.Request{
		ProjectID: projectID,
		ThreadID:  threadID,
		Operation: "session.status",
		Params:    json.RawMessage(`{}`),
	})
	if err != nil {
		s.writeBrowserProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, browserActionOutput{Result: result})
}

func (s *Server) browserAction(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}
	mediaType, _, mediaTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaTypeErr != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Browser actions require Content-Type application/json.")
		return
	}

	input, tooLarge, err := decodeBrowserAction(w, r)
	if err != nil {
		if tooLarge {
			writeError(w, http.StatusRequestEntityTooLarge, "Browser actions must be 1 MB or smaller.")
		} else {
			writeError(w, http.StatusBadRequest, "Invalid browser action.")
		}
		return
	}
	if _, allowed := allowedBrowserOperations[input.Operation]; !allowed {
		writeError(w, http.StatusBadRequest, "Unsupported browser operation.")
		return
	}
	if len(input.Params) == 0 {
		input.Params = json.RawMessage(`{}`)
	} else if !jsonObject(input.Params) {
		writeError(w, http.StatusBadRequest, "Browser action params must be an object.")
		return
	}

	result, err := s.browser.Action(r.Context(), browsercontrol.Request{
		ProjectID: projectID,
		ThreadID:  threadID,
		Operation: input.Operation,
		Params:    input.Params,
	})
	if err != nil {
		s.writeBrowserProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, browserActionOutput{Result: result})
}

func (s *Server) browserFrame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}

	result, err := s.browser.Action(r.Context(), browsercontrol.Request{
		ProjectID: projectID,
		ThreadID:  threadID,
		Operation: "preview",
		Params:    json.RawMessage(`{"format":"jpeg","quality":70}`),
	})
	if err != nil {
		s.writeBrowserProviderError(w, err)
		return
	}
	frame, err := decodeBrowserFrame(result)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Browser provider is unavailable.")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(frame)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(frame)
}

func (s *Server) browserThreadExists(w http.ResponseWriter, projectID, threadID string) bool {
	_, _, err := s.projects.GetThread(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Project not found.")
		return false
	}
	if errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return false
	}
	return true
}

func (s *Server) writeBrowserProviderError(w http.ResponseWriter, err error) {
	if code, ok := browsercontrol.OperationErrorCode(err); ok {
		if response, known := browserProviderErrorResponses[code]; known {
			writeError(w, response.Status, response.Message)
			return
		}
	}
	switch {
	case errors.Is(err, browsercontrol.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "Browser session not found.")
	case errors.Is(err, browsercontrol.ErrPreviewNotReady):
		writeError(w, http.StatusNotFound, "Browser preview is not ready.")
	case errors.Is(err, browsercontrol.ErrProvider), errors.Is(err, browsercontrol.ErrRequestTooLarge):
		writeError(w, http.StatusBadGateway, "Browser provider returned an error.")
	default:
		writeError(w, http.StatusServiceUnavailable, "Browser provider is unavailable.")
	}
}

func decodeBrowserAction(w http.ResponseWriter, r *http.Request) (browserActionInput, bool, error) {
	if r.ContentLength > maxBrowserActionBodyBytes {
		return browserActionInput{}, true, errors.New("request body is too large")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBrowserActionBodyBytes))
	decoder.DisallowUnknownFields()
	var input browserActionInput
	if err := decoder.Decode(&input); err != nil {
		var maxBytesError *http.MaxBytesError
		return browserActionInput{}, errors.As(err, &maxBytesError), err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		var maxBytesError *http.MaxBytesError
		return browserActionInput{}, errors.As(err, &maxBytesError), err
	}
	if input.Operation == "" {
		return browserActionInput{}, false, errors.New("operation is required")
	}
	return input, false, nil
}

func jsonObject(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]json.RawMessage
	return decoder.Decode(&value) == nil && value != nil
}

func decodeBrowserFrame(raw json.RawMessage) ([]byte, error) {
	var preview browserPreviewResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&preview); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid preview JSON")
	}
	if preview.MIMEType != "image/jpeg" || preview.Width <= 0 || preview.Width > maxBrowserFrameDimension ||
		preview.Height <= 0 || preview.Height > maxBrowserFrameDimension || preview.Generation <= 0 {
		return nil, errors.New("invalid preview metadata")
	}
	if _, err := time.Parse(time.RFC3339, preview.CapturedAt); err != nil {
		return nil, errors.New("invalid preview timestamp")
	}
	if len(preview.Data) > base64.StdEncoding.EncodedLen(maxBrowserFrameBytes) ||
		base64.StdEncoding.DecodedLen(len(preview.Data)) > maxBrowserFrameBytes {
		return nil, errors.New("preview is too large")
	}
	contents, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil || len(contents) == 0 || len(contents) > maxBrowserFrameBytes {
		return nil, errors.New("invalid preview data")
	}
	return contents, nil
}

func (s *Server) stopDeletedBrowserSessions(projectID string, threadIDs []string) {
	if s.browser == nil || len(threadIDs) == 0 {
		return
	}
	jobs := make(chan string)
	var workers sync.WaitGroup
	workerCount := min(browserCleanupConcurrency, len(threadIDs))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for threadID := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), browserCleanupTimeout)
				_, err := s.browser.Action(ctx, browsercontrol.Request{
					ProjectID: projectID,
					ThreadID:  threadID,
					Operation: "session.stop",
					Params:    json.RawMessage(`{}`),
				})
				cancel()
				if err != nil && !errors.Is(err, browsercontrol.ErrSessionNotFound) {
					// This runs only after Store publication. Provider cleanup must never
					// alter or roll back the durable deletion result.
					log.Printf("stop deleted browser session: project=%q thread=%q error=%v", projectID, threadID, err)
				}
			}
		}()
	}
	for _, threadID := range threadIDs {
		jobs <- threadID
	}
	close(jobs)
	workers.Wait()
}

func browserProjectThreadIDs(item project.Project) []string {
	ids := make([]string, 0, len(item.Threads))
	for _, thread := range item.Threads {
		ids = append(ids, thread.ID)
	}
	return ids
}
