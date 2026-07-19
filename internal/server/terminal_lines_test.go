package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTmuxLineLimit(t *testing.T) {
	for raw, want := range map[string]int{"": 200, "1": 1, "5000": 5000} {
		got, err := tmuxLineLimit(raw)
		if err != nil || got != want {
			t.Fatalf("tmuxLineLimit(%q) = %d, %v; want %d, nil", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "5001", "many"} {
		if _, err := tmuxLineLimit(raw); err == nil {
			t.Fatalf("tmuxLineLimit(%q) did not fail", raw)
		}
	}
}

func TestReadTmuxLinesCapturesPiAndProcessOutput(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	piToken := fmt.Sprintf("pi-lines-%d", time.Now().UnixNano())
	setMockPi(t, "#!/bin/sh\nprintf '"+piToken+"\\n'\nwhile :; do /bin/sleep 1; done\n")

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	piPaneID, _, _, err := handler.ensureCodingAgentPane(item, thread, codingAgentPi, "", sessionName)
	if err != nil {
		t.Fatal(err)
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: piPaneID}, piToken)

	shellToken := fmt.Sprintf("shell-lines-%d", time.Now().UnixNano())
	shellSession, _, _, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := handler.tmuxCommand(
		"send-keys", "-t", shellSession, "printf '"+shellToken+"\\n'", "Enter",
	).CombinedOutput(); err != nil {
		t.Fatalf("write shell token: %v: %s", err, output)
	}
	shellPaneID, found, err := handler.tmuxPaneID(shellSession)
	if err != nil || !found {
		t.Fatalf("find shell pane: found=%t err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: shellPaneID}, shellToken)

	processToken := fmt.Sprintf("process-lines-%d", time.Now().UnixNano())
	process, err := handler.newProcessWindow(
		item,
		thread,
		"worker",
		"printf '"+processToken+"\\n'; while :; do /bin/sleep 1; done",
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: process.TmuxID}, processToken)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal/lines", handler.readTmuxLines)
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/terminal/lines"

	piResponse := httptest.NewRecorder()
	mux.ServeHTTP(piResponse, httptest.NewRequest(http.MethodGet, path+"?tool=pi&agent=pi&lines=50", nil))
	if piResponse.Code != http.StatusOK {
		t.Fatalf("Pi lines status = %d, body = %s", piResponse.Code, piResponse.Body.String())
	}
	var piLines tmuxLinesResponse
	if err := json.NewDecoder(piResponse.Body).Decode(&piLines); err != nil {
		t.Fatal(err)
	}
	if piLines.Tool != "pi" || piLines.Agent != codingAgentPi || piLines.LineLimit != 50 || !strings.Contains(piLines.Output, piToken) {
		t.Fatalf("unexpected Pi lines: %#v", piLines)
	}

	shellResponse := httptest.NewRecorder()
	mux.ServeHTTP(shellResponse, httptest.NewRequest(http.MethodGet, path+"?tool=terminal&lines=60", nil))
	if shellResponse.Code != http.StatusOK {
		t.Fatalf("shell lines status = %d, body = %s", shellResponse.Code, shellResponse.Body.String())
	}
	var shellLines tmuxLinesResponse
	if err := json.NewDecoder(shellResponse.Body).Decode(&shellLines); err != nil {
		t.Fatal(err)
	}
	if shellLines.Tool != "terminal" || !strings.Contains(shellLines.Output, shellToken) {
		t.Fatalf("unexpected shell lines: %#v", shellLines)
	}

	processResponse := httptest.NewRecorder()
	mux.ServeHTTP(processResponse, httptest.NewRequest(
		http.MethodGet,
		path+"?tool=process&processId="+process.ID+"&lines=75",
		nil,
	))
	if processResponse.Code != http.StatusOK {
		t.Fatalf("process lines status = %d, body = %s", processResponse.Code, processResponse.Body.String())
	}
	var processLines tmuxLinesResponse
	if err := json.NewDecoder(processResponse.Body).Decode(&processLines); err != nil {
		t.Fatal(err)
	}
	if processLines.Process == nil || processLines.Process.ID != process.ID || !strings.Contains(processLines.Output, processToken) {
		t.Fatalf("unexpected process lines: %#v", processLines)
	}
}

func TestReadTmuxLinesDoesNotCreateMissingSession(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal/lines", handler.readTmuxLines)

	response := httptest.NewRecorder()
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/terminal/lines?tool=pi"
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing lines status = %d, body = %s", response.Code, response.Body.String())
	}
	if exists, err := handler.tmuxSessionExists(tmuxSessionName(item.ID, thread.ID, "pi")); err != nil || exists {
		t.Fatalf("read created missing session: exists=%t err=%v", exists, err)
	}
}
