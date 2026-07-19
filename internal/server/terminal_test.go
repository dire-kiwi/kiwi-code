package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivan/dire-mux/internal/project"
)

func TestTerminalEndpointDoesNotStartTmuxForPlainHTTP(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]

	marker := filepath.Join(t.TempDir(), "tmux-was-called")
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ntouch " + shellQuote(marker) + "\nexit 1\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "plain-http-test")
	handler.tmuxPath = fakeTmux

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal", handler.serve)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+item.ID+"/threads/"+thread.ID+"/terminal?tool=terminal",
		nil,
	)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("plain HTTP terminal status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("plain HTTP request invoked tmux: %v", err)
	}
}

func TestChildThreadPiTerminalIsReadOnly(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Child", project.AddThreadOptions{
		ParentThreadID: item.Threads[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "tmux-was-called")
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ntouch " + shellQuote(marker) + "\nexit 1\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "child-pi-terminal-test")
	handler.tmuxPath = fakeTmux

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal", handler.serve)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+item.ID+"/threads/"+child.ID+"/terminal?tool=pi&agent=pi",
		nil,
	)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("child Pi terminal status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child Pi terminal request invoked tmux: %v", err)
	}
}

func TestBoundedDimension(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback uint16
		want     uint16
	}{
		{name: "valid", raw: "132", fallback: 80, want: 132},
		{name: "empty", raw: "", fallback: 80, want: 80},
		{name: "too small", raw: "1", fallback: 24, want: 24},
		{name: "too large", raw: "1001", fallback: 24, want: 24},
		{name: "not a number", raw: "wide", fallback: 80, want: 80},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := boundedDimension(test.raw, test.fallback); got != test.want {
				t.Fatalf("boundedDimension(%q, %d) = %d, want %d", test.raw, test.fallback, got, test.want)
			}
		})
	}
}

func TestCommandForShellAndUnknownTool(t *testing.T) {
	t.Setenv("SHELL", "/bin/test-shell")
	for _, tool := range []string{"terminal", "process"} {
		command, args, notice, err := commandFor(tool)
		if err != nil {
			t.Fatal(err)
		}
		if command != "/bin/test-shell" || len(args) != 1 || args[0] != "-l" || notice != "" {
			t.Fatalf("unexpected %s command: %q %#v %q", tool, command, args, notice)
		}
	}

	if _, _, _, err := commandFor("unknown"); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestSharedToolCommandExposesTmuxTargets(t *testing.T) {
	t.Setenv("SHELL", "/bin/test-shell")
	t.Setenv("PATH", "")
	handler := &terminalHandler{envPath: "/usr/bin/env"}
	command, args, notice, err := handler.commandForTmuxWindow(
		project.Project{ID: "project"},
		project.Thread{ID: "thread"},
		"nvim",
		"",
		"dire-mux-project-thread-tools",
	)
	if err != nil {
		t.Fatal(err)
	}
	if command != "/usr/bin/env" || notice == "" {
		t.Fatalf("unexpected command: %q %#v %q", command, args, notice)
	}
	joined := strings.Join(args, "\n")
	for _, expected := range []string{
		"DIRE_MUX_TMUX_SESSION=dire-mux-project-thread-tools",
		"DIRE_MUX_TMUX_WINDOW=nvim",
		"/bin/test-shell",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("shared tool environment %q does not contain %q", joined, expected)
		}
	}
}

func TestPiCommandReceivesChildThreadCapability(t *testing.T) {
	directory := t.TempDir()
	piPath := filepath.Join(directory, "pi")
	if err := os.WriteFile(piPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	handler := &terminalHandler{
		envPath:          "/usr/bin/env",
		piExtensionPaths: []string{"/extension/child-threads.ts"},
		agentToken:       "agent-capability",
	}
	command, args, notice, err := handler.commandForCodingAgentPane(
		project.Project{ID: "project"},
		project.Thread{ID: "child", ParentThreadID: "parent"},
		codingAgentPi,
		"http://127.0.0.1:8080/api/projects/project/threads/child",
		"dire-mux-project-child-tools",
	)
	if err != nil {
		t.Fatal(err)
	}
	if command != "/usr/bin/env" || notice != "" {
		t.Fatalf("unexpected Pi command: %q %#v %q", command, args, notice)
	}
	joined := strings.Join(args, "\n")
	for _, expected := range []string{
		"--extension",
		"/extension/child-threads.ts",
		"DIRE_MUX_AGENT_TOKEN=agent-capability",
		"DIRE_MUX_PARENT_THREAD_ID=parent",
		piPath,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Pi environment %q does not contain %q", joined, expected)
		}
	}
}

func TestClaudeCommandUsesTheFixedPiWindow(t *testing.T) {
	directory := t.TempDir()
	claudePath := filepath.Join(directory, codingAgentClaude)
	piPath := filepath.Join(directory, codingAgentPi)
	for _, path := range []string{claudePath, piPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	handler := &terminalHandler{envPath: "/usr/bin/env", claudePluginPath: "/plugin/dire-mux", agentToken: "pi-only-token"}
	command, args, notice, err := handler.commandForCodingAgentPane(
		project.Project{ID: "project"},
		project.Thread{ID: "thread", ParentThreadID: "parent"},
		codingAgentClaude,
		"http://127.0.0.1:8080/api/projects/project/threads/thread",
		"dire-mux-project-thread-tools",
	)
	if err != nil {
		t.Fatal(err)
	}
	if command != "/usr/bin/env" || notice != "" {
		t.Fatalf("unexpected Claude command: %q %#v %q", command, args, notice)
	}
	joined := strings.Join(args, "\n")
	for _, expected := range []string{
		"--plugin-dir",
		"/plugin/dire-mux",
		"--dangerously-skip-permissions",
		"--settings",
		`{"skipDangerousModePermissionPrompt":true}`,
		"DIRE_MUX_TMUX_SESSION=dire-mux-project-thread-tools",
		"DIRE_MUX_TMUX_WINDOW=pi",
		"DIRE_MUX_THREAD_ENDPOINT=http://127.0.0.1:8080/api/projects/project/threads/thread",
		"DIRE_MUX_PROJECT_ID=project",
		"DIRE_MUX_THREAD_ID=thread",
		"DIRE_MUX_PI_PATH=" + piPath,
		claudePath,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Claude environment %q does not contain %q", joined, expected)
		}
	}
	for _, forbidden := range []string{"DIRE_MUX_AGENT_TOKEN=", "DIRE_MUX_PARENT_THREAD_ID=", "DIRE_MUX_CLAUDE_PATH="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Claude environment %q unexpectedly contains Pi-only child metadata %q", joined, forbidden)
		}
	}
}

func TestCodingAgentExitDedupeStateIsPrunedSafely(t *testing.T) {
	handler := &terminalHandler{
		agentWatches:        make(map[codingAgentWatchKey]struct{}),
		agentExits:          make(map[codingAgentExitKey]tmuxPaneExitState),
		agentExitLogs:       make(map[codingAgentExitKey]struct{}),
		agentExitSuppressed: make(map[codingAgentExitKey]tmuxPaneExitState),
	}
	key := codingAgentExitKey{
		ProjectID: "project", ThreadID: "thread", Agent: codingAgentPi, PaneID: "%1", ServerPID: "42",
	}
	state := tmuxPaneExitState{ServerPID: key.ServerPID, Dead: true, Status: "7", Found: true}
	handler.agentExits[key] = state
	handler.agentExitLogs[key] = struct{}{}
	handler.agentExitSuppressed[key] = state
	watchKey := codingAgentWatchKey{ServerPID: key.ServerPID, PaneID: key.PaneID}
	handler.agentWatches[watchKey] = struct{}{}

	handler.clearCodingAgentExits(key.ProjectID, key.ThreadID, key.Agent)
	if len(handler.agentExits) != 0 || len(handler.agentExitLogs) != 0 {
		t.Fatalf("cleared exit state leaked: exits=%#v logs=%#v", handler.agentExits, handler.agentExitLogs)
	}
	if _, found := handler.agentExitSuppressed[key]; !found {
		t.Fatal("active watcher suppression was pruned too early")
	}

	delete(handler.agentWatches, watchKey)
	handler.clearCodingAgentExits(key.ProjectID, key.ThreadID, key.Agent)
	if len(handler.agentExitSuppressed) != 0 {
		t.Fatalf("inactive watcher suppression leaked: %#v", handler.agentExitSuppressed)
	}

	// A pane without a watcher has no late observer to suppress, so its key
	// must not be retained indefinitely.
	handler.suppressCodingAgentExit(key.ProjectID, key.ThreadID, key.Agent, key.PaneID, state)
	if len(handler.agentExitSuppressed) != 0 {
		t.Fatalf("unwatched suppression key leaked: %#v", handler.agentExitSuppressed)
	}
}

func TestTmuxSessionNameAndShellCommand(t *testing.T) {
	if tmuxSocketName != "dire-mux" {
		t.Fatalf("tmuxSocketName = %q, want dire-mux", tmuxSocketName)
	}
	if got := tmuxSessionName("abc123", "thread456", "terminal"); got != "dire-mux-abc123-thread456-terminal" {
		t.Fatalf("shell tmuxSessionName() = %q", got)
	}
	for _, tool := range []string{"nvim", "lazygit", "pi", "process"} {
		if got := tmuxSessionName("abc123", "thread456", tool); got != "dire-mux-abc123-thread456-tools" {
			t.Fatalf("%s tmuxSessionName() = %q", tool, got)
		}
	}

	command := shellCommand("/path with spaces/shell", []string{"-l", "quote'check"})
	if want := "'/path with spaces/shell' '-l' 'quote'\"'\"'check'"; command != want {
		t.Fatalf("shellCommand() = %q, want %q", command, want)
	}
	target, err := parseTmuxWindowTarget([]byte("3\t@12\t456\n"))
	if err != nil || target.Index != 3 || target.ID != "@12" || target.ServerPID != "456" {
		t.Fatalf("parseTmuxWindowTarget() = %#v, %v", target, err)
	}
	incarnation, err := parseTmuxPaneIncarnation([]byte("%9\t456\n"))
	if err != nil || incarnation.PaneID != "%9" || incarnation.ServerPID != "456" {
		t.Fatalf("parseTmuxPaneIncarnation() = %#v, %v", incarnation, err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	if got, want := threadEndpointURL(request, "project", "thread"), "http://127.0.0.1:8080/api/projects/project/threads/thread"; got != want {
		t.Fatalf("threadEndpointURL() = %q, want %q", got, want)
	}
	environment := strings.Join(direMuxThreadEnvironment("http://127.0.0.1:9090/api/projects/project/threads/thread", "project", "thread"), "\n")
	for _, expected := range []string{"DIRE_MUX_PORT=9090", "DIRE_MUX_PROJECT_ID=project", "DIRE_MUX_THREAD_ID=thread"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("thread environment %q does not contain %q", environment, expected)
		}
	}
}

func TestNormalizeTerminalTool(t *testing.T) {
	tests := map[string]string{
		"":         "terminal",
		"terminal": "terminal",
		"nvim":     "nvim",
		"lazygit":  "lazygit",
		"pi":       "pi",
		"process":  "process",
	}
	for input, want := range tests {
		got, err := normalizeTerminalTool(input)
		if err != nil || got != want {
			t.Fatalf("normalizeTerminalTool(%q) = %q, %v; want %q, nil", input, got, err, want)
		}
	}

	if _, err := normalizeTerminalTool("unknown"); err == nil {
		t.Fatal("expected an error for an unknown terminal tool")
	}
}

func TestNormalizeCodingAgent(t *testing.T) {
	for input, want := range map[string]string{
		"":           codingAgentPi,
		"pi":         codingAgentPi,
		"claude":     codingAgentClaude,
		"claude-gpt": codingAgentClaudeGPT,
	} {
		got, err := normalizeCodingAgent(input)
		if err != nil || got != want {
			t.Fatalf("normalizeCodingAgent(%q) = %q, %v; want %q, nil", input, got, err, want)
		}
	}
	if _, err := normalizeCodingAgent("unknown"); err == nil {
		t.Fatal("expected an error for an unknown coding agent")
	}
}

func TestTerminalEnvironmentOverridesCapabilities(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "false")
	t.Setenv("TERM_PROGRAM", "something-else")

	environment := terminalEnvironment()
	wants := map[string]string{
		"TERM":         "xterm-256color",
		"COLORTERM":    "truecolor",
		"TERM_PROGRAM": "dire-mux",
	}
	for key, value := range wants {
		prefix := key + "="
		count := 0
		for _, entry := range environment {
			if strings.HasPrefix(entry, prefix) {
				count++
				if entry != prefix+value {
					t.Fatalf("%s = %q, want %q", key, entry, prefix+value)
				}
			}
		}
		if count != 1 {
			t.Fatalf("found %d entries for %s, want 1", count, key)
		}
	}
}

func TestTmuxEnvironmentDoesNotNestTheParentSession(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,123,0")
	for _, entry := range tmuxEnvironment() {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatalf("tmux environment contains parent session: %q", entry)
		}
	}
}

func TestTmuxNativeLogConfiguration(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "dmv-log-config")
	if handler.tmuxLogErr != nil {
		t.Fatal(handler.tmuxLogErr)
	}
	wantDirectory := filepath.Join(store.DataDirectory(), tmuxLogDirectoryName, "dmv-log-config")
	if handler.tmuxLogDirectory != wantDirectory {
		t.Fatalf("tmux log directory = %q, want %q", handler.tmuxLogDirectory, wantDirectory)
	}
	info, err := os.Stat(wantDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("tmux log path mode = %v, want a private directory", info.Mode())
	}

	handler.tmuxPath = "/test/tmux"
	for _, test := range []struct {
		name       string
		args       []string
		wantArgs   string
		wantLogDir bool
	}{
		{
			name:       "server creating client",
			args:       []string{"new-session", "-d"},
			wantArgs:   "/test/tmux -v -L dmv-log-config new-session -d",
			wantLogDir: true,
		},
		{
			name:       "attached client",
			args:       []string{"attach-session", "-t", "=session"},
			wantArgs:   "/test/tmux -v -L dmv-log-config attach-session -t =session",
			wantLogDir: true,
		},
		{
			name:     "frequent inspection client",
			args:     []string{"has-session", "-t", "=session"},
			wantArgs: "/test/tmux -L dmv-log-config has-session -t =session",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := handler.tmuxCommand(test.args...)
			if got := strings.Join(command.Args, " "); got != test.wantArgs {
				t.Fatalf("tmux arguments = %q, want %q", got, test.wantArgs)
			}
			if test.wantLogDir {
				if command.Dir != wantDirectory {
					t.Fatalf("tmux command directory = %q, want %q", command.Dir, wantDirectory)
				}
			} else if command.Dir != "" {
				t.Fatalf("non-verbose tmux command directory = %q, want empty", command.Dir)
			}
		})
	}
}
