package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/headless"
	"github.com/ivan/dire-mux/internal/project"
)

func TestHeadlessClientExercisesMultipleClientsEndToEnd(t *testing.T) {
	terminal, _ := newIsolatedTmuxHandler(t)
	serverState := &Server{
		projects:   terminal.projects,
		terminal:   terminal,
		piActivity: newPiActivityTracker(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", serverState.health)
	mux.HandleFunc("GET /api/events", serverState.streamEvents)
	mux.HandleFunc("POST /api/projects", serverState.addProject)
	mux.HandleFunc("DELETE /api/projects/{id}", serverState.deleteProject)
	mux.HandleFunc("POST /api/projects/{id}/threads", serverState.addThread)
	mux.HandleFunc("PATCH /api/projects/{id}/threads/{threadId}", serverState.updateThread)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", serverState.deleteThread)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/pi/activity", serverState.updatePiActivity)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal", terminal.serve)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := headless.Run(ctx, headless.Options{
		BaseURL:         testServer.URL,
		Clients:         3,
		ProjectPath:     t.TempDir(),
		Output:          &output,
		IsolationWindow: 75 * time.Millisecond,
	}); err != nil {
		t.Fatalf("headless client: %v\n%s", err, output.String())
	}
	for _, expected := range []string{
		"opened 3 global event clients",
		"working heartbeat and rapid status transitions",
		"tmux output reached attached clients",
		"project deletion reached every client and closed its terminal streams",
		"multi-client API check passed",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("headless output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestHeadlessClientCanSkipTerminalChecks(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "headless-client-test")
	terminal.tmuxPath = ""
	serverState := &Server{projects: store, terminal: terminal, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", serverState.health)
	mux.HandleFunc("GET /api/events", serverState.streamEvents)
	mux.HandleFunc("POST /api/projects", serverState.addProject)
	mux.HandleFunc("DELETE /api/projects/{id}", serverState.deleteProject)
	mux.HandleFunc("POST /api/projects/{id}/threads", serverState.addThread)
	mux.HandleFunc("PATCH /api/projects/{id}/threads/{threadId}", serverState.updateThread)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", serverState.deleteThread)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/pi/activity", serverState.updatePiActivity)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := headless.Run(ctx, headless.Options{
		BaseURL:      testServer.URL,
		Clients:      2,
		ProjectPath:  t.TempDir(),
		Output:       &output,
		SkipTerminal: true,
	}); err != nil {
		t.Fatalf("headless client without terminals: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "terminal fan-out and isolation skipped") {
		t.Fatalf("headless output did not report skipped terminals:\n%s", output.String())
	}
}
