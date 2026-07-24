package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

func TestStartClaudeNativeProcessRejectsRollbackPendingThread(t *testing.T) {
	handler := &terminalHandler{}
	_, err := handler.startClaudeNativeProcess(
		project.Project{ID: "project"},
		project.Thread{ID: "thread", RollbackPending: true},
		"",
		codingAgentLaunchOptions{},
	)
	if !errors.Is(err, project.ErrThreadRollbackPending) {
		t.Fatalf("start rollback tombstone error = %v, want ErrThreadRollbackPending", err)
	}
}

func TestClaudeNativeArgumentsUseStreamJSONAndPreserveLaunchChoices(t *testing.T) {
	got, err := claudeNativeArguments(
		"/tmp/claude-plugin",
		"session-123",
		codingAgentLaunchOptions{
			Model: "opus", ThinkingLevel: "high", AppendSystemPrompt: "Extra context",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
		"--verbose",
		"--dangerously-skip-permissions",
		"--settings", `{"skipDangerousModePermissionPrompt":true}`,
		"--plugin-dir", "/tmp/claude-plugin",
		"--resume", "session-123",
		"--model", "opus",
		"--effort", "high",
		"--append-system-prompt", "Extra context",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeNativeArguments() = %#v, want %#v", got, want)
	}

	fresh, err := claudeNativeArguments("", "", codingAgentLaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, argument := range fresh {
		if argument == "--resume" || argument == "--plugin-dir" {
			t.Fatalf("fresh claudeNativeArguments() included %q: %#v", argument, fresh)
		}
	}
}

func TestNormalizeClaudeNativeClientMessage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		action  claudeNativeClientAction
		message claudeNativeClientMessage
		wantErr bool
	}{
		{name: "refresh", payload: `{"type":"refresh"}`, action: claudeNativeClientRefresh},
		{name: "get_state", payload: `{"type":"get_state"}`, action: claudeNativeClientState},
		{name: "abort", payload: `{"type":"abort"}`, action: claudeNativeClientAbort},
		{name: "new_session", payload: `{"type":"new_session"}`, action: claudeNativeClientNewSession},
		{
			name:    "restart with model and level",
			payload: `{"type":"restart","modelId":"opus","level":"high"}`,
			action:  claudeNativeClientRestart,
			message: claudeNativeClientMessage{ModelID: "opus", Level: "high"},
		},
		{
			name:    "set_model",
			payload: `{"type":"set_model","modelId":"sonnet"}`,
			action:  claudeNativeClientRestart,
			message: claudeNativeClientMessage{ModelID: "sonnet"},
		},
		{
			name:    "set_thinking_level",
			payload: `{"type":"set_thinking_level","level":"max"}`,
			action:  claudeNativeClientRestart,
			message: claudeNativeClientMessage{Level: "max"},
		},
		{name: "set_model without model", payload: `{"type":"set_model"}`, wantErr: true},
		{name: "set_model with unknown model", payload: `{"type":"set_model","modelId":"gpt-6"}`, wantErr: true},
		{name: "set_thinking_level without level", payload: `{"type":"set_thinking_level"}`, wantErr: true},
		{name: "restart with unknown level", payload: `{"type":"restart","level":"warp"}`, wantErr: true},
		{
			name:    "prompt",
			payload: `{"type":"prompt","message":"hello"}`,
			action:  claudeNativeClientPrompt,
			message: claudeNativeClientMessage{Message: "hello"},
		},
		{
			name:    "prompt with images only",
			payload: `{"type":"prompt","images":[{"path":"/tmp/kiwi-code-pi-clipboard-1.png"}]}`,
			action:  claudeNativeClientPrompt,
			message: claudeNativeClientMessage{Images: []piNativeClientImage{{Path: "/tmp/kiwi-code-pi-clipboard-1.png"}}},
		},
		{name: "empty prompt", payload: `{"type":"prompt","message":"   "}`, wantErr: true},
		{name: "prompt with NUL", payload: `{"type":"prompt","message":"a\u0000b"}`, wantErr: true},
		{name: "unknown", payload: `{"type":"compact"}`, wantErr: true},
		{name: "malformed", payload: `{`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, action, err := normalizeClaudeNativeClientMessage([]byte(test.payload))
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeClaudeNativeClientMessage(%s) succeeded, want error", test.payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeClaudeNativeClientMessage(%s) error = %v", test.payload, err)
			}
			if action != test.action {
				t.Fatalf("action = %d, want %d", action, test.action)
			}
			if !reflect.DeepEqual(message, test.message) {
				t.Fatalf("message = %#v, want %#v", message, test.message)
			}
		})
	}
}

func TestClaudeNativePromptContentBuildsTextBlocks(t *testing.T) {
	content, err := claudeNativePromptContent("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"type": "text", "text": "hello"}}
	if !reflect.DeepEqual(content, want) {
		t.Fatalf("claudeNativePromptContent() = %#v, want %#v", content, want)
	}

	if _, err := claudeNativePromptContent("", nil); err == nil {
		t.Fatal("empty prompt content did not error")
	}

	tooMany := make([]piNativeClientImage, claudeNativeMaxPromptImages+1)
	if _, err := claudeNativePromptContent("hello", tooMany); err == nil {
		t.Fatal("too many prompt images did not error")
	}
}

func TestReadClaudeNativeSessionID(t *testing.T) {
	directory := t.TempDir()
	if got := readClaudeNativeSessionID(directory); got != "" {
		t.Fatalf("missing session file returned %q", got)
	}
	path := filepath.Join(directory, claudeNativeSessionFileName)
	if err := os.WriteFile(path, []byte(" session-123 \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readClaudeNativeSessionID(directory); got != "session-123" {
		t.Fatalf("session id = %q, want %q", got, "session-123")
	}
	if err := os.WriteFile(path, []byte("-rf /\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readClaudeNativeSessionID(directory); got != "" {
		t.Fatalf("flag-like session id was accepted: %q", got)
	}
	if err := os.WriteFile(path, []byte("../escape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readClaudeNativeSessionID(directory); got != "" {
		t.Fatalf("path-like session id was accepted: %q", got)
	}
}

func TestClaudeNativeHistoryEventTypeSelection(t *testing.T) {
	tracked := [][2]string{{"assistant", ""}, {"user", ""}, {"result", "success"}, {"system", "init"}}
	for _, entry := range tracked {
		if !claudeNativeHistoryEventType(entry[0], entry[1]) {
			t.Fatalf("event %s/%s was not tracked in history", entry[0], entry[1])
		}
	}
	skipped := [][2]string{{"stream_event", ""}, {"control_response", ""}, {"system", "api_retry"}, {"claude_native_state", ""}}
	for _, entry := range skipped {
		if claudeNativeHistoryEventType(entry[0], entry[1]) {
			t.Fatalf("event %s/%s was tracked in history", entry[0], entry[1])
		}
	}
}

func TestClaudeNativeManagerStreamsEventsPersistsSessionAndResumes(t *testing.T) {
	directory := t.TempDir()
	fakeClaude := filepath.Join(directory, "fake-claude")
	argumentLog := filepath.Join(directory, "claude-args.log")
	agentLog := filepath.Join(directory, "claude-agent.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> ` + argumentLog + `
printf '%s\n' "$KIWI_CODE_CODING_AGENT" > ` + agentLog + `
printf '%s\n' '{"type":"system","subtype":"init","session_id":"session-abc","model":"claude-test-1","slash_commands":["compact","review"],"uuid":"evt-init"}'
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"interrupt"'*)
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"kiwi-code-interrupt-1"}}'
      ;;
    *'"type":"user"'*)
      printf '%s\n' '{"type":"assistant","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"Done"}]},"session_id":"session-abc","uuid":"evt-assistant"}'
      printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"session-abc","usage":{"input_tokens":100,"output_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":50},"total_cost_usd":0.25,"uuid":"evt-result"}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakeClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	var reportMu sync.Mutex
	var reportedSession string
	var reportedTotals threadUsageTotals
	manager := newClaudeNativeManager(filepath.Join(directory, "data"), "", nil)
	manager.claudePath = fakeClaude
	manager.usageReporter = func(_ piNativeProcessKey, sessionID string, totals threadUsageTotals) {
		reportMu.Lock()
		reportedSession = sessionID
		reportedTotals = totals
		reportMu.Unlock()
	}
	item := project.Project{ID: "project-a"}
	thread := project.Thread{ID: "thread-a", Cwd: directory}
	process, err := manager.getOrStart(
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{Model: "opus", ThinkingLevel: "high"},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.getOrStart(item, thread, "http://127.0.0.1:1", codingAgentLaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second != process {
		t.Fatal("a second native Claude process was started for the same thread")
	}

	subscription := process.events.Subscribe()
	defer subscription.Close()

	// The init event may already have been published before this subscription
	// existed; the persisted session id proves it was processed.
	sessionDirectory := filepath.Join(directory, "data", claudeNativeSessionDirectoryName, item.ID, thread.ID)
	waitForCondition(t, "session id file", func() bool {
		return readClaudeNativeSessionID(sessionDirectory) == "session-abc"
	})
	waitForCondition(t, "native Claude agent identity", func() bool {
		contents, err := os.ReadFile(agentLog)
		return err == nil && strings.TrimSpace(string(contents)) == codingAgentClaude
	})

	if err := process.sendPrompt("Review this", nil); err != nil {
		t.Fatal(err)
	}
	waitForClaudeNativeEvent(t, subscription.Events(), "assistant")
	waitForClaudeNativeEvent(t, subscription.Events(), "result")

	waitForCondition(t, "usage report", func() bool {
		reportMu.Lock()
		defer reportMu.Unlock()
		return reportedSession == "session-abc" && reportedTotals.TotalTokens == 200
	})
	reportMu.Lock()
	wantTotals := threadUsageTotals{
		InputTokens: 100, OutputTokens: 40,
		CacheReadTokens: 50, CacheWriteTokens: 10,
		TotalTokens: 200, CostUSD: 0.25,
	}
	if reportedTotals != wantTotals {
		reportMu.Unlock()
		t.Fatalf("reported usage totals = %#v, want %#v", reportedTotals, wantTotals)
	}
	reportMu.Unlock()

	waitForCondition(t, "history entries", func() bool {
		entries, err := process.historySnapshot()
		return err == nil && len(entries) >= 3
	})
	entries, err := process.historySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(entries))
	for _, entry := range entries {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(entry.Event, &event); err != nil {
			t.Fatalf("decode history entry: %v", err)
		}
		if entry.At <= 0 {
			t.Fatalf("history entry missing timestamp: %#v", entry)
		}
		types = append(types, event.Type)
	}
	if !reflect.DeepEqual(types, []string{"system", "assistant", "result"}) {
		t.Fatalf("history event types = %v", types)
	}

	if err := process.sendInterrupt(); err != nil {
		t.Fatal(err)
	}
	waitForClaudeNativeEvent(t, subscription.Events(), "control_response")

	original := process
	restarted, err := manager.restart(
		original,
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restarted == original {
		t.Fatal("native Claude restart reused the stopped process")
	}
	select {
	case <-original.done:
	case <-time.After(5 * time.Second):
		t.Fatal("original native Claude process did not stop during restart")
	}
	waitForCondition(t, "resume argument", func() bool {
		contents, err := os.ReadFile(argumentLog)
		return err == nil && strings.Contains(string(contents), "--resume session-abc")
	})
	staleRestart, err := manager.restart(
		original,
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if staleRestart != restarted {
		t.Fatal("a stale restart request replaced the current native Claude process")
	}
	process = restarted

	fresh, err := manager.restart(
		process,
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	// The reset removes the log; the fresh process appends its own init.
	waitForCondition(t, "reset history", func() bool {
		entries, err := fresh.historySnapshot()
		return err == nil && len(entries) == 1
	})
	process = fresh

	if err := manager.removeThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		t.Fatal("native Claude process did not stop with its thread")
	}
	if _, err := os.Stat(sessionDirectory); !os.IsNotExist(err) {
		t.Fatalf("native Claude session directory remained after thread removal: %v", err)
	}
}

func waitForClaudeNativeEvent(t *testing.T, events <-chan []byte, eventType string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case payload, open := <-events:
			if !open {
				t.Fatalf("native Claude event stream closed before %s", eventType)
			}
			var event struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode native Claude event: %v", err)
			}
			if event.Type == eventType {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for native Claude event %s", eventType)
		}
	}
}

func waitForCondition(t *testing.T, label string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}

func TestServeClaudeNativeEndToEnd(t *testing.T) {
	directory := t.TempDir()
	fakeClaude := filepath.Join(directory, "fake-claude")
	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"session-e2e","model":"claude-test-1","slash_commands":["compact"],"uuid":"evt-init"}'
while IFS= read -r line; do
  case "$line" in
    *'"type":"user"'*)
      printf '%s\n' '{"type":"assistant","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"Hello"}]},"session_id":"session-e2e","uuid":"evt-assistant"}'
      printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"session-e2e","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"total_cost_usd":0.01,"uuid":"evt-result"}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakeClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	store, err := project.NewStore(filepath.Join(directory, "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "Child", project.AddThreadOptions{
		ParentThreadID: thread.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "kcv-claude-native-test")
	// This flow must never reach tmux; fail loudly if it tries.
	handler.tmuxPath = ""
	handler.nativeClaude.claudePath = fakeClaude
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/claude/native", handler.serveClaudeNative)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer handler.nativeClaude.stopAll()

	baseURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if _, response, err := websocket.DefaultDialer.Dial(
		baseURL+"/api/projects/"+item.ID+"/threads/"+child.ID+"/claude/native", nil,
	); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("child thread dial: error=%v response=%v, want 403", err, response)
	}

	connection, _, err := websocket.DefaultDialer.Dial(
		baseURL+"/api/projects/"+item.ID+"/threads/"+thread.ID+"/claude/native?model=opus&thinking=high", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	readEvent := func(wanted string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := connection.SetReadDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			_, payload, err := connection.ReadMessage()
			if err != nil {
				t.Fatalf("read while waiting for %s: %v", wanted, err)
			}
			var event map[string]any
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode event while waiting for %s: %v", wanted, err)
			}
			if event["type"] == wanted {
				return event
			}
		}
	}

	readEvent("claude_native_ready")
	history := readEvent("claude_native_history")
	if _, ok := history["events"].([]any); !ok {
		t.Fatalf("history events missing: %#v", history)
	}
	readEvent("claude_native_state")

	if err := connection.WriteJSON(map[string]any{"type": "prompt", "message": "hi"}); err != nil {
		t.Fatal(err)
	}
	assistant := readEvent("assistant")
	if assistant["session_id"] != "session-e2e" {
		t.Fatalf("assistant event = %#v", assistant)
	}
	readEvent("result")

	if err := connection.WriteJSON(map[string]any{"type": "get_state"}); err != nil {
		t.Fatal(err)
	}
	state := readEvent("claude_native_state")
	if state["model"] != "claude-test-1" || state["sessionId"] != "session-e2e" || state["effort"] != "high" {
		t.Fatalf("state event = %#v", state)
	}

	if err := connection.WriteJSON(map[string]any{"type": "restart"}); err != nil {
		t.Fatal(err)
	}
	readEvent("claude_native_restarting")
	readEvent("claude_native_reloaded")
	restartHistory := readEvent("claude_native_history")
	events, ok := restartHistory["events"].([]any)
	if !ok || len(events) < 3 {
		t.Fatalf("history after restart = %#v", restartHistory)
	}
}
