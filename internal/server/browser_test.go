package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const browserProviderTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type recordedBrowserAction struct {
	ProjectID string          `json:"projectId"`
	ThreadID  string          `json:"threadId"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

func TestBrowserStatusAndActionForwarding(t *testing.T) {
	var mu sync.Mutex
	var requests []recordedBrowserAction
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/action" {
			t.Errorf("provider request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+browserProviderTestToken {
			t.Errorf("provider Authorization = %q", got)
		}
		if got := r.Header.Get(agentTokenHeader); got != "" {
			t.Errorf("agent token was forwarded to provider: %q", got)
		}
		var action recordedBrowserAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		mu.Lock()
		requests = append(requests, action)
		mu.Unlock()
		if action.Operation == "session.status" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"backend":"electron","pages":0,"currentTargetId":"","reachable":false}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"operation":%q,"params":%s}}`, action.Operation, action.Params)
	}))
	defer provider.Close()

	store, item, thread, handler := newBrowserHTTPFixture(t, provider.URL)
	_ = store
	basePath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser"

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, basePath, nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status response = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}
	var statusBody struct {
		Result struct {
			Backend         string `json:"backend"`
			Pages           int    `json:"pages"`
			CurrentTargetID string `json:"currentTargetId"`
			Reachable       bool   `json:"reachable"`
		} `json:"result"`
	}
	if err := json.NewDecoder(statusResponse.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.Result.Backend != "electron" || statusBody.Result.Pages != 0 || statusBody.Result.CurrentTargetID != "" || statusBody.Result.Reachable {
		t.Fatalf("status result = %#v", statusBody.Result)
	}
	if strings.Contains(statusResponse.Body.String(), browserProviderTestToken) {
		t.Fatal("status response exposed provider token")
	}

	actionRequest := httptest.NewRequest(http.MethodPost, basePath+"/actions", strings.NewReader(`{"operation":"navigate.goto","params":{"url":"https://example.com"}}`))
	// Browser actions use the app's same-origin trust model, while optional
	// managed-agent capability headers remain valid for Pi callers.
	actionRequest.Header.Set("Content-Type", "application/json")
	actionRequest.Header.Set(agentTokenHeader, "optional-agent-token")
	actionResponse := httptest.NewRecorder()
	handler.ServeHTTP(actionResponse, actionRequest)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("action response = %d, body = %s", actionResponse.Code, actionResponse.Body.String())
	}
	var actionBody struct {
		Result struct {
			Operation string          `json:"operation"`
			Params    json.RawMessage `json:"params"`
		} `json:"result"`
	}
	if err := json.NewDecoder(actionResponse.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if actionBody.Result.Operation != "navigate.goto" || string(actionBody.Result.Params) != `{"url":"https://example.com"}` {
		t.Fatalf("action result = %#v", actionBody.Result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider request count = %d", len(requests))
	}
	for _, action := range requests {
		if action.ProjectID != item.ID || action.ThreadID != thread.ID {
			t.Fatalf("provider identity = project %q thread %q", action.ProjectID, action.ThreadID)
		}
	}
	if requests[0].Operation != "session.status" || string(requests[0].Params) != `{}` {
		t.Fatalf("status provider request = %#v", requests[0])
	}
}

func TestBrowserOperationAllowlist(t *testing.T) {
	want := []string{
		"session.status", "session.start", "session.disconnect", "session.stop",
		"tabs.list", "tabs.new", "tabs.select", "tabs.close",
		"navigate.goto", "navigate.back", "navigate.forward", "navigate.reload",
		"snapshot", "click", "fill", "key", "wait", "evaluate", "screenshot", "cdp", "preview",
	}
	got := make([]string, 0, len(allowedBrowserOperations))
	for operation := range allowedBrowserOperations {
		got = append(got, operation)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("browser operation allowlist = %v, want %v", got, want)
	}
}

func TestBrowserActionValidationAndLimits(t *testing.T) {
	var providerCalls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer provider.Close()
	_, item, thread, handler := newBrowserHTTPFixture(t, provider.URL)
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser/actions"

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "unknown operation", body: `{"operation":"browser.destroy"}`, status: http.StatusBadRequest},
		{name: "unknown field", body: `{"operation":"tabs.list","extra":true}`, status: http.StatusBadRequest},
		{name: "missing operation", body: `{"params":{}}`, status: http.StatusBadRequest},
		{name: "params array", body: `{"operation":"tabs.list","params":[]}`, status: http.StatusBadRequest},
		{name: "params null", body: `{"operation":"tabs.list","params":null}`, status: http.StatusBadRequest},
		{name: "trailing json", body: `{"operation":"tabs.list"}{}`, status: http.StatusBadRequest},
		{name: "malformed", body: `{`, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("response = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	unsupportedResponse := httptest.NewRecorder()
	unsupportedRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"operation":"tabs.list"}`))
	unsupportedRequest.Header.Set("Content-Type", "text/plain")
	handler.ServeHTTP(unsupportedResponse, unsupportedRequest)
	if unsupportedResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported media response = %d, body = %s", unsupportedResponse.Code, unsupportedResponse.Body.String())
	}

	oversized := `{"operation":"fill","params":{"value":"` + strings.Repeat("x", int(maxBrowserActionBodyBytes)) + `"}}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(oversized))
	request.ContentLength = -1 // Exercise MaxBytesReader, not only the header check.
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized response = %d, body = %s", response.Code, response.Body.String())
	}
	if providerCalls != 0 {
		t.Fatalf("provider received %d invalid actions", providerCalls)
	}
}

func TestBrowserRoutesValidateProjectAndThreadBeforeProvider(t *testing.T) {
	var providerCalls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer provider.Close()
	_, item, _, handler := newBrowserHTTPFixture(t, provider.URL)

	for _, path := range []string{
		"/api/projects/missing/threads/thread/browser",
		"/api/projects/" + item.ID + "/threads/missing/browser/actions",
		"/api/projects/" + item.ID + "/threads/missing/browser/frame",
	} {
		method := http.MethodGet
		var body *strings.Reader
		if strings.HasSuffix(path, "/actions") {
			method = http.MethodPost
			body = strings.NewReader(`{"operation":"session.status"}`)
		} else {
			body = strings.NewReader("")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, body))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s response = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	if providerCalls != 0 {
		t.Fatalf("provider received %d requests for missing resources", providerCalls)
	}
}

func TestBrowserProviderUnavailableIsSanitized(t *testing.T) {
	_, item, thread, handler := newBrowserHTTPFixture(t, "")
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"Browser provider is unavailable.\"}\n" {
		t.Fatalf("unavailable response = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), browsercontrol.ConfigFileName) {
		t.Fatal("unavailable response exposed provider config path")
	}
}

func TestBrowserActionMapsAllowlistedProviderErrors(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"stale_ref"}}`))
	}))
	defer provider.Close()
	_, item, thread, handler := newBrowserHTTPFixture(t, provider.URL)
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser/actions"
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(
		`{"operation":"click","params":{"ref":"e1"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "reference is stale") {
		t.Fatalf("stale-ref response = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestBrowserFrame(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F', 0xff, 0xd9}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action recordedBrowserAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			t.Errorf("decode provider action: %v", err)
		}
		if action.Operation != "preview" || string(action.Params) != `{"format":"jpeg","quality":70}` {
			t.Errorf("preview request = %#v", action)
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"data":%q,"mimeType":"image/jpeg","width":1280,"height":800,"generation":1,"capturedAt":"2026-01-02T03:04:05Z"}}`, base64.StdEncoding.EncodeToString(jpeg))
	}))
	defer provider.Close()
	_, item, thread, handler := newBrowserHTTPFixture(t, provider.URL)
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser/frame"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("frame response = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "image/jpeg" {
		t.Fatalf("frame Content-Type = %q", contentType)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("frame Cache-Control = %q", cacheControl)
	}
	if !bytes.Equal(response.Body.Bytes(), jpeg) {
		t.Fatalf("frame bytes = %v, want %v", response.Body.Bytes(), jpeg)
	}
}

func TestBrowserFrameProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   int
		error  string
	}{
		{name: "session missing", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"session_not_found"}}`, want: http.StatusNotFound, error: "Browser session not found."},
		{name: "preview not ready", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"preview_not_ready"}}`, want: http.StatusNotFound, error: "Browser preview is not ready."},
		{name: "canonical frame unavailable", status: http.StatusNotFound, body: `{"ok":false,"error":{"code":"frame_unavailable"}}`, want: http.StatusNotFound, error: "Browser preview is not ready."},
		{name: "provider failure", status: http.StatusInternalServerError, body: `secret upstream failure`, want: http.StatusServiceUnavailable, error: "Browser provider is unavailable."},
		{name: "malformed success", status: http.StatusOK, body: `{"ok":true,"result":{"data":"bad"}}`, want: http.StatusServiceUnavailable, error: "Browser provider is unavailable."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()
			_, item, thread, handler := newBrowserHTTPFixture(t, provider.URL)
			path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/browser/frame"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != test.want || response.Body.String() != fmt.Sprintf("{\"error\":%q}\n", test.error) {
				t.Fatalf("frame response = %d, body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("error Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestDecodeBrowserFrameBoundsDataAndMetadata(t *testing.T) {
	validData := base64.StdEncoding.EncodeToString([]byte("jpeg"))
	tests := []struct {
		name string
		raw  string
	}{
		{name: "wrong mime", raw: fmt.Sprintf(`{"data":%q,"mimeType":"image/png","width":1,"height":1,"generation":1,"capturedAt":"2026-01-02T03:04:05Z"}`, validData)},
		{name: "bad base64", raw: `{"data":"!","mimeType":"image/jpeg","width":1,"height":1,"generation":1,"capturedAt":"2026-01-02T03:04:05Z"}`},
		{name: "wide", raw: fmt.Sprintf(`{"data":%q,"mimeType":"image/jpeg","width":16385,"height":1,"generation":1,"capturedAt":"2026-01-02T03:04:05Z"}`, validData)},
		{name: "bad timestamp", raw: fmt.Sprintf(`{"data":%q,"mimeType":"image/jpeg","width":1,"height":1,"generation":1,"capturedAt":"today"}`, validData)},
		{name: "unknown field", raw: fmt.Sprintf(`{"data":%q,"mimeType":"image/jpeg","width":1,"height":1,"generation":1,"capturedAt":"2026-01-02T03:04:05Z","token":"secret"}`, validData)},
		{name: "oversized", raw: fmt.Sprintf(`{"data":%q,"mimeType":"image/jpeg","width":1,"height":1,"generation":1,"capturedAt":"2026-01-02T03:04:05Z"}`, strings.Repeat("A", base64.StdEncoding.EncodedLen(maxBrowserFrameBytes)+4))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeBrowserFrame(json.RawMessage(test.raw)); err == nil {
				t.Fatal("decodeBrowserFrame() unexpectedly succeeded")
			}
		})
	}
}

func TestThreadDeletionStopsBrowserSessionsAfterPublication(t *testing.T) {
	var mu sync.Mutex
	var stopped []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action recordedBrowserAction
		_ = json.NewDecoder(r.Body).Decode(&action)
		if action.Operation == "session.stop" {
			mu.Lock()
			stopped = append(stopped, action.ThreadID)
			mu.Unlock()
		}
		// Provider cleanup failure must not change durable deletion semantics.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"already_stopped"}}`))
	}))
	defer provider.Close()
	store, item, root, handler := newBrowserHTTPFixture(t, provider.URL)
	child, err := store.AddThreadWithOptions(item.ID, "Child", project.AddThreadOptions{ParentThreadID: root.ID})
	if err != nil {
		t.Fatal(err)
	}

	path := "/api/projects/" + item.ID + "/threads/" + root.ID
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete response = %d, body = %s", response.Code, response.Body.String())
	}
	if _, _, err := store.GetThread(item.ID, root.ID); !errors.Is(err, project.ErrThreadNotFound) {
		t.Fatalf("deleted root still exists: %v", err)
	}
	mu.Lock()
	sort.Strings(stopped)
	got := append([]string(nil), stopped...)
	mu.Unlock()
	want := []string{child.ID, root.ID}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stopped browser threads = %v, want %v", got, want)
	}
}

func TestProjectDeletionStopsAllBrowserSessions(t *testing.T) {
	var mu sync.Mutex
	var stopped []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action recordedBrowserAction
		_ = json.NewDecoder(r.Body).Decode(&action)
		if action.Operation == "session.stop" {
			mu.Lock()
			stopped = append(stopped, action.ThreadID)
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"stopped":true}}`))
	}))
	defer provider.Close()
	store, item, first, handler := newBrowserHTTPFixture(t, provider.URL)
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete response = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := store.Get(item.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleted project still exists: %v", err)
	}
	mu.Lock()
	sort.Strings(stopped)
	got := append([]string(nil), stopped...)
	mu.Unlock()
	want := []string{first.ID, second.ID}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stopped browser threads = %v, want %v", got, want)
	}
}

func TestDeletedProjectRetryUsesDurableBrowserThreadRecipe(t *testing.T) {
	var mu sync.Mutex
	var stopped []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action recordedBrowserAction
		_ = json.NewDecoder(r.Body).Decode(&action)
		if action.Operation == "session.stop" {
			mu.Lock()
			stopped = append(stopped, action.ThreadID)
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"stopped":true}}`))
	}))
	defer provider.Close()
	store, item, first, handler := newBrowserHTTPFixture(t, provider.URL)
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	threadIDs := []string{first.ID, second.ID}
	lease, err := newTerminalStopManager(store.DataDirectory()).beginProject(item.ID, threadIDs, []string{"exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(item.ID); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("retry response = %d, body = %s", response.Code, response.Body.String())
	}
	mu.Lock()
	got := append([]string(nil), stopped...)
	mu.Unlock()
	sort.Strings(got)
	sort.Strings(threadIDs)
	if strings.Join(got, ",") != strings.Join(threadIDs, ",") {
		t.Fatalf("retried browser stops = %v, want %v", got, threadIDs)
	}
}

func newBrowserHTTPFixture(t *testing.T, providerURL string) (*project.Store, project.Project, project.Thread, http.Handler) {
	t.Helper()
	t.Setenv(browsercontrol.ConfigPathEnv, "")
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Threads) != 1 {
		t.Fatalf("initial threads = %d", len(item.Threads))
	}

	if providerURL != "" {
		_, portText, err := net.SplitHostPort(strings.TrimPrefix(providerURL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		config := fmt.Sprintf(`{"version":1,"pid":%d,"port":%d,"token":%q}`, os.Getpid(), port, browserProviderTestToken)
		if err := os.WriteFile(filepath.Join(store.DataDirectory(), browsercontrol.ConfigFileName), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Exercise deletion through the real durable Store/terminal-stop flow, but
	// replace the tmux executable before the first terminal command. Browser
	// tests must never inspect or create sessions on any tmux server.
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	var socketID [6]byte
	if _, err := rand.Read(socketID[:]); err != nil {
		t.Fatal(err)
	}
	socket := "dtest-browser-" + hex.EncodeToString(socketID[:])
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socket)
	terminal.tmuxPath = falsePath
	application := &Server{
		projects:        store,
		browser:         browsercontrol.New(browsercontrol.ConfigPath(store.DataDirectory())),
		terminal:        terminal,
		piActivity:      newPiActivityTracker(),
		contextStatuses: newContextStatusTracker(),
		threadMessages:  newChildThreadMessageStore(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}", application.deleteProject)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", application.deleteThread)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/browser", application.browserStatus)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/browser/actions", application.browserAction)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/browser/frame", application.browserFrame)
	return store, item, item.Threads[0], mux
}

func TestBrowserCleanupTimeoutIsBounded(t *testing.T) {
	// Keep the timeout constant explicit in tests so best-effort cleanup cannot
	// accidentally become an unbounded part of durable deletion.
	if browserCleanupTimeout <= 0 || browserCleanupTimeout > 5*time.Second {
		t.Fatalf("browser cleanup timeout = %s", browserCleanupTimeout)
	}
}
