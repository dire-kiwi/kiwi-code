package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
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
	"recording.start":    {},
	"recording.stop":     {},
	"recording.status":   {},
	"recording.delete":   {},
}

type browserProviderErrorResponse struct {
	Status  int
	Message string
}

var browserProviderErrorResponses = map[string]browserProviderErrorResponse{
	"blocked_command":                 {Status: http.StatusForbidden, Message: "That browser command is blocked by the page security boundary."},
	"blocked_origin":                  {Status: http.StatusForbidden, Message: "Navigation to a protected Kiwi Code origin is blocked."},
	"element_not_found":               {Status: http.StatusNotFound, Message: "Browser element not found. Take a new snapshot or check the selector."},
	"invalid_params":                  {Status: http.StatusBadRequest, Message: "Invalid browser action parameters."},
	"invalid_url":                     {Status: http.StatusBadRequest, Message: "Invalid browser URL."},
	"navigation_unavailable":          {Status: http.StatusConflict, Message: "Browser history navigation is unavailable."},
	"operation_failed":                {Status: http.StatusBadGateway, Message: "The browser could not complete the operation."},
	"operation_timeout":               {Status: http.StatusGatewayTimeout, Message: "The browser operation timed out."},
	"output_too_large":                {Status: http.StatusRequestEntityTooLarge, Message: "The browser result exceeds the size limit."},
	"page_not_found":                  {Status: http.StatusNotFound, Message: "Browser page not found. Open or select a tab and try again."},
	"recording_active":                {Status: http.StatusConflict, Message: "This browser session is already recording. Stop it before trying again."},
	"recording_failed":                {Status: http.StatusBadGateway, Message: "The browser recording could not be completed."},
	"recording_limit_reached":         {Status: http.StatusConflict, Message: "The browser recording limit has been reached."},
	"recording_not_active":            {Status: http.StatusConflict, Message: "This browser session is not recording."},
	"recording_not_found":             {Status: http.StatusNotFound, Message: "Browser recording not found."},
	"recording_range_not_satisfiable": {Status: http.StatusRequestedRangeNotSatisfiable, Message: "The requested browser recording range is not available."},
	"stale_ref":                       {Status: http.StatusConflict, Message: "The browser element reference is stale. Take a new snapshot and try again."},
	"tab_limit_reached":               {Status: http.StatusConflict, Message: "The browser tab limit has been reached."},
	"unsupported_operation":           {Status: http.StatusBadRequest, Message: "Unsupported browser operation."},
	"wait_timeout":                    {Status: http.StatusRequestTimeout, Message: "The browser wait condition timed out."},
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

type browserStreamLeaseStore struct {
	mu     sync.Mutex
	next   uint64
	leases map[string]uint64
}

func (s *browserStreamLeaseStore) acquire(key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases == nil {
		s.leases = make(map[string]uint64)
	}
	if _, exists := s.leases[key]; exists {
		return 0, false
	}
	s.next++
	s.leases[key] = s.next
	return s.next, true
}

func (s *browserStreamLeaseStore) release(key string, token uint64) {
	if token == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[key] == token {
		delete(s.leases, key)
	}
}

type browserStreamInput struct {
	Type       string  `json:"type"`
	Generation int64   `json:"generation,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Event      string  `json:"event,omitempty"`
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	DeltaX     float64 `json:"deltaX,omitempty"`
	DeltaY     float64 `json:"deltaY,omitempty"`
	Button     string  `json:"button,omitempty"`
	Buttons    int     `json:"buttons,omitempty"`
	ClickCount int     `json:"clickCount,omitempty"`
	Key        string  `json:"key,omitempty"`
	Code       string  `json:"code,omitempty"`
	Text       string  `json:"text,omitempty"`
	Modifiers  int     `json:"modifiers,omitempty"`
}

func (s *Server) browserStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}
	result, err := s.browser.Action(r.Context(), browsercontrol.Request{
		ProjectID:        projectID,
		ThreadID:         threadID,
		Operation:        "session.status",
		Params:           json.RawMessage(`{}`),
		ProtectedOrigins: browserRequestProtectedOrigins(r),
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
		ProjectID:        projectID,
		ThreadID:         threadID,
		Operation:        input.Operation,
		Params:           input.Params,
		ProtectedOrigins: browserRequestProtectedOrigins(r),
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
		ProjectID:        projectID,
		ThreadID:         threadID,
		Operation:        "preview",
		Params:           json.RawMessage(`{"format":"jpeg","quality":70}`),
		ProtectedOrigins: browserRequestProtectedOrigins(r),
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

func recordingFilename(title, recordingID string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		if character == ' ' || character == '-' || character == '_' {
			return '-'
		}
		return -1
	}, slug)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	if slug == "" {
		slug = "browser-recording"
	}
	suffix := recordingID
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	return slug + "-" + suffix + ".webm"
}

func (s *Server) browserRecording(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}
	disposition := "attachment"
	query := r.URL.Query()
	if len(query) > 0 {
		values, ok := query["disposition"]
		if !ok || len(query) != 1 || len(values) != 1 || values[0] != "inline" {
			writeError(w, http.StatusBadRequest, "Invalid browser recording request.")
			return
		}
		disposition = "inline"
	}
	recordingID := r.PathValue("recordingId")
	recording, err := s.browser.OpenRecordingRange(r.Context(), projectID, threadID, recordingID, r.Header.Get("Range"))
	if err != nil {
		var rangeError *browsercontrol.RecordingRangeError
		if errors.As(err, &rangeError) {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(rangeError.Size, 10))
			writeError(w, http.StatusRequestedRangeNotSatisfiable, "The requested browser recording range is not available.")
			return
		}
		s.writeBrowserProviderError(w, err)
		return
	}
	defer recording.Body.Close()
	w.Header().Set("Content-Type", recording.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(recording.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{
		"filename": recordingFilename(recording.Title, recordingID),
	}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	status := http.StatusOK
	if recording.Partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(recording.Start, 10)+"-"+strconv.FormatInt(recording.End, 10)+"/"+strconv.FormatInt(recording.TotalSize, 10))
	}
	w.WriteHeader(status)
	_, _ = io.CopyN(w, recording.Body, recording.Size)
}

func (s *Server) browserStream(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if !s.browserThreadExists(w, projectID, threadID) {
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		ReadBufferSize:   16 * 1024,
		WriteBufferSize:  64 * 1024,
		CheckOrigin:      s.browserOriginPolicy.allows,
	}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(64 << 10)

	key := projectID + "\x00" + threadID
	lease, controller := s.browserStreamLeases.acquire(key)
	if controller {
		defer s.browserStreamLeases.release(key, lease)
	}
	writer := newWebSocketWriter(connection)
	if err := writer.Write(websocket.TextMessage, []byte(fmt.Sprintf(
		`{"type":"control","controller":%t}`,
		controller,
	))); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	protectedOrigins := browserRequestProtectedOrigins(r)
	var generation atomic.Int64
	inputDone := make(chan error, 1)
	go func() {
		for {
			messageType, payload, readErr := connection.ReadMessage()
			if readErr != nil {
				inputDone <- readErr
				return
			}
			if messageType != websocket.TextMessage || !controller {
				continue
			}
			input, decodeErr := decodeBrowserStreamInput(payload)
			if decodeErr != nil || !validBrowserStreamInput(input, generation.Load()) {
				continue
			}
			params, marshalErr := json.Marshal(input)
			if marshalErr != nil {
				continue
			}
			if _, actionErr := s.browser.Action(ctx, browsercontrol.Request{
				ProjectID:        projectID,
				ThreadID:         threadID,
				Operation:        "stream.input",
				Params:           params,
				ProtectedOrigins: protectedOrigins,
			}); actionErr != nil && !errors.Is(actionErr, context.Canceled) {
				// Input failures are intentionally not reflected with provider text.
				// The next status/frame refresh will expose a sanitized state.
			}
		}
	}()

	frameTimer := time.NewTicker(250 * time.Millisecond)
	defer frameTimer.Stop()
	pingTimer := time.NewTicker(20 * time.Second)
	defer pingTimer.Stop()
	capture := func() error {
		result, actionErr := s.browser.Action(ctx, browsercontrol.Request{
			ProjectID:        projectID,
			ThreadID:         threadID,
			Operation:        "preview",
			Params:           json.RawMessage(`{"format":"jpeg","quality":70}`),
			ProtectedOrigins: protectedOrigins,
		})
		if actionErr != nil {
			return nil
		}
		frame, contents, decodeErr := decodeBrowserPreview(result)
		if decodeErr != nil {
			return nil
		}
		generation.Store(frame.Generation)
		metadata, _ := json.Marshal(map[string]any{
			"type":       "frame",
			"generation": frame.Generation,
			"width":      frame.Width,
			"height":     frame.Height,
		})
		if writeErr := writer.Write(websocket.TextMessage, metadata); writeErr != nil {
			return writeErr
		}
		return writer.Write(websocket.BinaryMessage, contents)
	}
	if err := capture(); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-inputDone:
			return
		case <-frameTimer.C:
			if err := capture(); err != nil {
				return
			}
		case <-pingTimer.C:
			if err := writer.Write(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func decodeBrowserStreamInput(payload []byte) (browserStreamInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var input browserStreamInput
	if err := decoder.Decode(&input); err != nil {
		return browserStreamInput{}, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return browserStreamInput{}, err
	}
	return input, nil
}

func validBrowserStreamInput(input browserStreamInput, currentGeneration int64) bool {
	if len(input.Type) > 32 || input.Modifiers < 0 || input.Modifiers > 15 {
		return false
	}
	if input.Type != "viewport" && input.Type != "focus" && input.Type != "blur" &&
		(input.Generation <= 0 || input.Generation != currentGeneration) {
		return false
	}
	switch input.Type {
	case "viewport":
		return input.Width >= 200 && input.Width <= 4096 && input.Height >= 150 && input.Height <= 4096 && input.Width*input.Height <= 16*1024*1024
	case "focus", "blur":
		return true
	case "text":
		return len(input.Text) <= 100_000
	case "pointer":
		return input.X >= 0 && input.X <= maxBrowserFrameDimension && input.Y >= 0 && input.Y <= maxBrowserFrameDimension &&
			(input.Event == "mouseMoved" || input.Event == "mousePressed" || input.Event == "mouseReleased") &&
			input.Buttons >= 0 && input.Buttons <= 31 && input.ClickCount >= 0 && input.ClickCount <= 3
	case "wheel":
		return input.X >= 0 && input.X <= maxBrowserFrameDimension && input.Y >= 0 && input.Y <= maxBrowserFrameDimension &&
			input.DeltaX >= -10_000 && input.DeltaX <= 10_000 && input.DeltaY >= -10_000 && input.DeltaY <= 10_000
	case "key":
		return len(input.Key) <= 64 && len(input.Code) <= 64 && len(input.Text) <= 16 &&
			(input.Event == "keyDown" || input.Event == "rawKeyDown" || input.Event == "keyUp")
	default:
		return false
	}
}

func browserRequestProtectedOrigins(r *http.Request) []string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Host == "" {
		return nil
	}
	return []string{scheme + "://" + r.Host}
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
	case errors.Is(err, browsercontrol.ErrRecordingNotFound):
		writeError(w, http.StatusNotFound, "Browser recording not found.")
	case errors.Is(err, browsercontrol.ErrRecordingRangeNotSatisfiable):
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "The requested browser recording range is not available.")
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
	_, contents, err := decodeBrowserPreview(raw)
	return contents, err
}

func decodeBrowserPreview(raw json.RawMessage) (browserPreviewResult, []byte, error) {
	var preview browserPreviewResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&preview); err != nil {
		return browserPreviewResult{}, nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return browserPreviewResult{}, nil, errors.New("invalid preview JSON")
	}
	if preview.MIMEType != "image/jpeg" || preview.Width <= 0 || preview.Width > maxBrowserFrameDimension ||
		preview.Height <= 0 || preview.Height > maxBrowserFrameDimension || preview.Generation <= 0 {
		return browserPreviewResult{}, nil, errors.New("invalid preview metadata")
	}
	if _, err := time.Parse(time.RFC3339, preview.CapturedAt); err != nil {
		return browserPreviewResult{}, nil, errors.New("invalid preview timestamp")
	}
	if len(preview.Data) > base64.StdEncoding.EncodedLen(maxBrowserFrameBytes) ||
		base64.StdEncoding.DecodedLen(len(preview.Data)) > maxBrowserFrameBytes {
		return browserPreviewResult{}, nil, errors.New("preview is too large")
	}
	contents, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil || len(contents) == 0 || len(contents) > maxBrowserFrameBytes {
		return browserPreviewResult{}, nil, errors.New("invalid preview data")
	}
	return preview, contents, nil
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
