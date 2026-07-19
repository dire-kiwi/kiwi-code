package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/broadcast"
	"github.com/ivan/dire-mux/internal/project"
)

func TestThreadEventStreamPushesMutationsAndReconcilesExternalChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "init")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "add", "README.md")
	serverGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Initial commit")
	initialBranch := strings.TrimSpace(serverGit(t, repositoryPath, "branch", "--show-current"))

	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	thread := item.Threads[0]
	threadPath := "/api/projects/" + item.ID + "/threads/" + thread.ID
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+threadPath+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("thread stream response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	initial := decodeThreadStatusEvent(t, readServerSentEvent(t, reader))
	if initial.GitBranches == nil || initial.GitBranches.Current != initialBranch {
		t.Fatalf("initial Git status = %#v, want branch %q", initial.GitBranches, initialBranch)
	}
	if initial.ContextStatuses == nil || initial.Processes == nil || initial.ShellWindows == nil || initial.Plans == nil {
		t.Fatalf("initial status collections must be present: %#v", initial)
	}

	putJSONForEventTest(
		t,
		ctx,
		server.Client(),
		server.URL+threadPath+"/context/status",
		`{"source":"pi-terminal","tokens":60000,"contextWindow":200000,"percent":30,"model":"openai-codex/gpt-test"}`,
		http.StatusOK,
		http.MethodPut,
	)
	contextUpdate := decodeThreadStatusEvent(t, readServerSentEvent(t, reader))
	contextStatus, found := contextUpdate.ContextStatuses[contextStatusSourcePiTerminal]
	if !found || contextStatus.Percent == nil || *contextStatus.Percent != 30 {
		t.Fatalf("pushed context status = %#v", contextUpdate.ContextStatuses)
	}

	putJSONForEventTest(
		t,
		ctx,
		server.Client(),
		server.URL+threadPath+"/git/branches",
		`{"name":"event-driven"}`,
		http.StatusCreated,
		http.MethodPost,
	)
	created := decodeThreadStatusEvent(t, readServerSentEvent(t, reader))
	if created.GitBranches == nil || created.GitBranches.Current != "event-driven" {
		t.Fatalf("pushed Git status = %#v", created.GitBranches)
	}

	// A terminal can change Git without going through the HTTP API. The stream's
	// low-frequency reconciliation must detect that change without browser polls.
	serverGit(t, repositoryPath, "switch", initialBranch)
	reconciled := decodeThreadStatusEvent(t, readServerSentEvent(t, reader))
	if reconciled.GitBranches == nil || reconciled.GitBranches.Current != initialBranch {
		t.Fatalf("reconciled Git status = %#v, want branch %q", reconciled.GitBranches, initialBranch)
	}
}

func TestThreadGitReconciliationDoesNotPollTmux(t *testing.T) {
	directory := t.TempDir()
	commandsPath := filepath.Join(directory, "tmux-commands")
	t.Setenv("TMUX_COMMANDS_FILE", commandsPath)
	fakeTmux := filepath.Join(directory, "tmux")
	if err := os.WriteFile(fakeTmux, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_COMMANDS_FILE\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	store, err := project.NewStore(filepath.Join(directory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", directory)
	if err != nil {
		t.Fatal(err)
	}
	terminal := &terminalHandler{projects: store, tmuxPath: fakeTmux, tmuxSocket: "event-test"}
	application := &Server{
		projects:            store,
		terminal:            terminal,
		threadStatusChanges: broadcast.NewBroker[threadStatusKey](broadcast.DefaultMaxPending),
	}
	terminal.threadStatusChanged = application.notifyThreadStatusChanged
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/events", func(w http.ResponseWriter, r *http.Request) {
		application.streamThreadEventsWithInterval(w, r, 10*time.Millisecond)
	})
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	thread := item.Threads[0]
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/projects/"+item.ID+"/threads/"+thread.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decodeThreadStatusEvent(t, readServerSentEvent(t, bufio.NewReader(response.Body)))

	baseline := waitForTmuxCommandCountToSettle(t, commandsPath)
	time.Sleep(100 * time.Millisecond)
	if current := tmuxCommandCount(commandsPath); current != baseline {
		t.Fatalf("Git reconciliation ran tmux commands: count changed from %d to %d", baseline, current)
	}
}

func waitForTmuxCommandCountToSettle(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	previous := -1
	unchangedSince := time.Now()
	for time.Now().Before(deadline) {
		current := tmuxCommandCount(path)
		if current != previous {
			previous = current
			unchangedSince = time.Now()
		} else if time.Since(unchangedSince) >= 50*time.Millisecond {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tmux command count did not settle")
	return 0
}

func tmuxCommandCount(path string) int {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.FieldsFunc(string(contents), func(character rune) bool {
		return character == '\n' || character == '\r'
	}))
}

func decodeThreadStatusEvent(t *testing.T, event serverSentEvent) threadStatusSnapshot {
	t.Helper()
	if event.Name != threadStatusEventName {
		t.Fatalf("thread event name = %q, want %q", event.Name, threadStatusEventName)
	}
	var snapshot threadStatusSnapshot
	if err := json.Unmarshal(event.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
