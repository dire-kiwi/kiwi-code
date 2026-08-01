package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

func TestPiNativeBrowserEventCoalesceKeyOnlyCompactsCumulativeUpdates(t *testing.T) {
	tests := []struct {
		payload string
		want    string
	}{
		{payload: `{"type":"message_update","message":{"role":"assistant"}}`, want: "message_update"},
		{payload: `{"type":"tool_execution_update","toolCallId":"call-1"}`, want: "tool_execution_update\x00call-1"},
		{payload: `{"type":"tool_execution_update"}`, want: ""},
		{payload: `{"type":"message_end"}`, want: ""},
		{payload: `{"type":`, want: ""},
	}
	for _, test := range tests {
		if got := piNativeBrowserEventCoalesceKey([]byte(test.payload)); got != test.want {
			t.Fatalf("coalesce key for %s = %q, want %q", test.payload, got, test.want)
		}
	}
}

func TestStartPiNativeProcessRejectsRollbackPendingThread(t *testing.T) {
	handler := &terminalHandler{}
	_, err := handler.startPiNativeProcess(
		project.Project{ID: "project"},
		project.Thread{ID: "thread", RollbackPending: true},
		"",
		codingAgentLaunchOptions{},
	)
	if !errors.Is(err, project.ErrThreadRollbackPending) {
		t.Fatalf("start rollback tombstone error = %v, want ErrThreadRollbackPending", err)
	}
}

func TestPiNativeArgumentsResumeTheSelectedSessionFile(t *testing.T) {
	got := piNativeArguments(
		"/tmp/sessions",
		"/tmp/sessions/active.jsonl",
		nil,
		"",
		codingAgentLaunchOptions{},
	)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--session /tmp/sessions/active.jsonl") {
		t.Fatalf("piNativeArguments() did not select the active session: %#v", got)
	}
	if strings.Contains(joined, "--continue") {
		t.Fatalf("piNativeArguments() combined --session with --continue: %#v", got)
	}
}

func TestPiNativeActiveSessionSurvivesManagerRestartAndCwdChange(t *testing.T) {
	sessionDirectory := t.TempDir()
	older := writePiNativeTestSession(t, sessionDirectory, "older.jsonl", "older", "/old/cwd")
	newer := writePiNativeTestSession(t, sessionDirectory, "newer.jsonl", "newer", "/old/cwd")
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	selected, err := piNativeActiveSessionFile(sessionDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if selected != newer {
		t.Fatalf("selected session = %q, want newest %q", selected, newer)
	}
	if err := rememberPiNativeActiveSession(sessionDirectory, older); err != nil {
		t.Fatal(err)
	}
	selected, err = piNativeActiveSessionFile(sessionDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if selected != older {
		t.Fatalf("selected marked session = %q, want %q", selected, older)
	}

	// A per-thread session must not depend on Pi matching the current cwd in the
	// saved header. Align the header before passing the explicit path back to Pi.
	movedCwd := t.TempDir()
	if err := alignPiNativeSessionCwd(selected, movedCwd); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(selected)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(bytes.SplitN(contents, []byte{'\n'}, 2)[0], &header); err != nil {
		t.Fatal(err)
	}
	if header.Cwd != movedCwd {
		t.Fatalf("aligned session cwd = %q, want %q", header.Cwd, movedCwd)
	}
	arguments := piNativeArguments(sessionDirectory, selected, nil, "", codingAgentLaunchOptions{})
	if !reflect.DeepEqual(arguments[:6], []string{
		"--mode", "rpc", "--session-dir", sessionDirectory, "--session", older,
	}) {
		t.Fatalf("restart arguments = %#v", arguments)
	}
}

func TestPiNativeActiveSessionKeepsAnUnmaterializedSessionSelected(t *testing.T) {
	sessionDirectory := t.TempDir()
	pending := filepath.Join(sessionDirectory, "pending.jsonl")
	if err := rememberPiNativeActiveSession(sessionDirectory, pending); err != nil {
		t.Fatal(err)
	}
	selected, err := piNativeActiveSessionFile(sessionDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if selected != pending {
		t.Fatalf("selected unmaterialized session = %q, want %q", selected, pending)
	}
	if err := alignPiNativeSessionCwd(selected, t.TempDir()); err != nil {
		t.Fatalf("aligning an unmaterialized session: %v", err)
	}
}

func TestPiNativeGetStateRemembersTheActiveSession(t *testing.T) {
	sessionDirectory := t.TempDir()
	sessionFile := writePiNativeTestSession(t, sessionDirectory, "active.jsonl", "active", t.TempDir())
	process := &piNativeProcess{
		nativeProcessCore: &nativeProcessCore{
			key:    piNativeProcessKey{ProjectID: "project", ThreadID: "thread"},
			events: broadcast.NewBroker[[]byte](4),
		},
		sessionDirectory: sessionDirectory,
		runs:             make(map[uint64]piNativeRunSnapshot),
	}
	payload, err := json.Marshal(map[string]any{
		"type": "response", "command": "get_state", "success": true,
		"data": map[string]any{"sessionFile": sessionFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	process.publishPiEvent(payload)
	contents, err := os.ReadFile(filepath.Join(sessionDirectory, piNativeActiveSessionMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != filepath.Base(sessionFile) {
		t.Fatalf("active session marker = %q, want %q", contents, filepath.Base(sessionFile))
	}
}

func TestRememberPiNativeActiveSessionRejectsOutsideFile(t *testing.T) {
	sessionDirectory := t.TempDir()
	outsideDirectory := t.TempDir()
	outside := writePiNativeTestSession(t, outsideDirectory, "outside.jsonl", "outside", t.TempDir())
	if err := rememberPiNativeActiveSession(sessionDirectory, outside); err == nil {
		t.Fatal("rememberPiNativeActiveSession() accepted a file outside the thread directory")
	}
}

func writePiNativeTestSession(t *testing.T, directory, name, id, cwd string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	header, err := json.Marshal(map[string]any{
		"type": "session", "version": 3, "id": id, "timestamp": time.Now().UTC(), "cwd": cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(header, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPiNativeThreadEnvironmentRoutesBrowserToolsToTheCurrentThread(t *testing.T) {
	environment := piNativeThreadEnvironment(
		"http://127.0.0.1:43210/api/projects/project/threads/thread",
		"project",
		"thread",
		"token",
		"http://127.0.0.1:43210/api/projects/project/threads/thread",
	)
	joined := strings.Join(environment, "\n")
	for _, want := range []string{
		"KIWI_CODE_THREAD_ENDPOINT=http://127.0.0.1:43210/api/projects/project/threads/thread",
		"KIWI_CODE_BROWSER_THREAD_ENDPOINT=http://127.0.0.1:43210/api/projects/project/threads/thread",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Pi environment does not contain %q: %#v", want, environment)
		}
	}
}

func TestNormalizePiNativeClientMessage(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		want       piNativeRPCCommand
		wantAction piNativeClientAction
		wantError  bool
	}{
		{
			name:    "prompt",
			payload: `{"type":"prompt","message":"Review this","streamingBehavior":"followUp"}`,
			want: piNativeRPCCommand{
				Type:              "prompt",
				Message:           piNativeTestString("Review this"),
				StreamingBehavior: "followUp",
			},
		},
		{name: "abort", payload: `{"type":"abort"}`, want: piNativeRPCCommand{Type: "abort"}},
		{name: "refresh", payload: `{"type":"refresh"}`, wantAction: piNativeClientRefresh},
		{name: "reload", payload: `{"type":"reload"}`, wantAction: piNativeClientRestart},
		{name: "restart", payload: `{"type":"restart"}`, wantAction: piNativeClientRestart},
		{
			name:       "reload preserves launch choices",
			payload:    `{"type":"reload","provider":" openai ","modelId":" gpt-5.6 ","level":" high "}`,
			want:       piNativeRPCCommand{Provider: "openai", ModelID: "gpt-5.6", Level: "high"},
			wantAction: piNativeClientRestart,
		},
		{name: "state probe", payload: `{"type":"get_state"}`, want: piNativeRPCCommand{Type: "get_state"}},
		{name: "commands", payload: `{"type":"get_commands"}`, want: piNativeRPCCommand{Type: "get_commands"}},
		{name: "models", payload: `{"type":"get_available_models"}`, want: piNativeRPCCommand{Type: "get_available_models"}},
		{name: "stats", payload: `{"type":"get_session_stats"}`, want: piNativeRPCCommand{Type: "get_session_stats"}},
		{name: "new session", payload: `{"type":"new_session"}`, want: piNativeRPCCommand{Type: "new_session"}},
		{
			name:    "compact",
			payload: `{"type":"compact","customInstructions":"  Preserve the test results.  "}`,
			want:    piNativeRPCCommand{Type: "compact", CustomInstructions: "Preserve the test results."},
		},
		{name: "compact without instructions", payload: `{"type":"compact"}`, want: piNativeRPCCommand{Type: "compact"}},
		{
			name:    "model",
			payload: `{"type":"set_model","provider":" openai ","modelId":" gpt-5.6/codex "}`,
			want:    piNativeRPCCommand{Type: "set_model", Provider: "openai", ModelID: "gpt-5.6/codex"},
		},
		{
			name:    "thinking level",
			payload: `{"type":"set_thinking_level","level":" high "}`,
			want:    piNativeRPCCommand{Type: "set_thinking_level", Level: "high"},
		},
		{name: "blank prompt", payload: `{"type":"prompt","message":"  "}`, wantError: true},
		{name: "bad queue", payload: `{"type":"prompt","message":"hello","streamingBehavior":"later"}`, wantError: true},
		{name: "missing model", payload: `{"type":"set_model"}`, wantError: true},
		{name: "provider path", payload: `{"type":"set_model","provider":"openai/other","modelId":"model"}`, wantError: true},
		{name: "model whitespace", payload: `{"type":"set_model","provider":"openai","modelId":"bad model"}`, wantError: true},
		{name: "missing thinking", payload: `{"type":"set_thinking_level"}`, wantError: true},
		{name: "unknown thinking", payload: `{"type":"set_thinking_level","level":"turbo"}`, wantError: true},
		{name: "restart missing model id", payload: `{"type":"restart","provider":"openai"}`, wantError: true},
		{name: "restart invalid thinking", payload: `{"type":"restart","level":"turbo"}`, wantError: true},
		{name: "compact nul", payload: `{"type":"compact","customInstructions":"bad\u0000value"}`, wantError: true},
		{
			name:      "compact too long",
			payload:   `{"type":"compact","customInstructions":"` + strings.Repeat("a", piNativeMaxCompactPrompt+1) + `"}`,
			wantError: true,
		},
		{name: "raw shell remains blocked", payload: `{"type":"bash","command":"pwd"}`, wantError: true},
		{name: "session switching remains blocked", payload: `{"type":"switch_session","sessionPath":"elsewhere"}`, wantError: true},
		{name: "unknown", payload: `{"type":"not_a_pi_command"}`, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, action, err := normalizePiNativeClientMessage([]byte(test.payload))
			if (err != nil) != test.wantError {
				t.Fatalf("normalizePiNativeClientMessage() error = %v, wantError %v", err, test.wantError)
			}
			if action != test.wantAction {
				t.Fatalf("normalizePiNativeClientMessage() action = %v, want %v", action, test.wantAction)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("normalizePiNativeClientMessage() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPiNativeDisplayHistoryPreservesActiveBranchAndPlacesCompactionAtBoundary(t *testing.T) {
	data := json.RawMessage(`{
		"entries": [
			{"type":"message","id":"root","parentId":null,"timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"old question","timestamp":1}},
			{"type":"message","id":"old-answer","parentId":"root","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"old answer"}],"timestamp":2}},
			{"type":"message","id":"abandoned","parentId":"old-answer","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":"abandoned branch","timestamp":3}},
			{"type":"message","id":"kept","parentId":"old-answer","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":"kept question","timestamp":4}},
			{"type":"compaction","id":"compact","parentId":"kept","timestamp":"2026-01-01T00:00:04Z","summary":"model context summary","firstKeptEntryId":"kept","tokensBefore":42000},
			{"type":"message","id":"new-answer","parentId":"compact","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"new answer"}],"timestamp":6}}
		],
		"leafId": "new-answer"
	}`)

	payload, err := piNativeDisplayHistoryEvent(data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("abandoned branch")) {
		t.Fatalf("display history included an abandoned branch: %s", payload)
	}

	var event piNativeHistoryEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "pi_native_history" || len(event.Data.Messages) != 5 {
		t.Fatalf("display history event = %#v", event)
	}
	roles := make([]string, 0, len(event.Data.Messages))
	for _, raw := range event.Data.Messages {
		var message struct {
			Role         string `json:"role"`
			Summary      string `json:"summary"`
			TokensBefore int64  `json:"tokensBefore"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, message.Role)
		if message.Role == "compactionSummary" && (message.Summary != "model context summary" || message.TokensBefore != 42000) {
			t.Fatalf("compaction display message = %#v", message)
		}
	}
	// The append-only compaction entry follows the retained question, but the
	// display summary belongs at the retained-context boundary. This lets the
	// subsequent transcript show work completed after the summarized snapshot.
	wantRoles := []string{"user", "assistant", "compactionSummary", "user", "assistant"}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Fatalf("display history roles = %#v, want %#v", roles, wantRoles)
	}
}

func TestPiNativeDisplayHistoryKeepsCompactionWithoutBoundaryAtAppendPosition(t *testing.T) {
	data := json.RawMessage(`{
		"entries": [
			{"type":"message","id":"root","parentId":null,"message":{"role":"user","content":"question"}},
			{"type":"compaction","id":"compact","parentId":"root","summary":"legacy summary","tokensBefore":100},
			{"type":"message","id":"answer","parentId":"compact","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}
		],
		"leafId": "answer"
	}`)

	payload, err := piNativeDisplayHistoryEvent(data)
	if err != nil {
		t.Fatal(err)
	}
	var event piNativeHistoryEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, len(event.Data.Messages))
	for _, raw := range event.Data.Messages {
		var message struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, message.Role)
	}
	wantRoles := []string{"user", "compactionSummary", "assistant"}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Fatalf("display history roles = %#v, want %#v", roles, wantRoles)
	}
}

func TestPiNativeDisplayHistoryRejectsBrokenLeafPath(t *testing.T) {
	_, err := piNativeDisplayHistoryEvent(json.RawMessage(`{
		"entries":[{"type":"message","id":"leaf","parentId":"missing","message":{"role":"user","content":"hello"}}],
		"leafId":"leaf"
	}`))
	if err == nil || !strings.Contains(err.Error(), "missing entry") {
		t.Fatalf("broken display history error = %v", err)
	}
}

func TestNormalizePiNativeClientPromptImages(t *testing.T) {
	imageContents := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	image, err := os.CreateTemp("", piImageTempPrefix+"*.png")
	if err != nil {
		t.Fatal(err)
	}
	imagePath := image.Name()
	t.Cleanup(func() { _ = os.Remove(imagePath) })
	if _, err := image.Write(imageContents); err != nil {
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(piNativeClientMessage{
		Type:   "prompt",
		Images: []piNativeClientImage{{Path: imagePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, action, err := normalizePiNativeClientMessage(payload)
	if err != nil {
		t.Fatal(err)
	}
	if action != piNativeClientSendCommand {
		t.Fatalf("image prompt action = %v, want send command", action)
	}
	want := piNativeRPCCommand{
		Type:    "prompt",
		Message: piNativeTestString(""),
		Images: []piNativeRPCImage{{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(imageContents),
			MIMEType: "image/png",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizePiNativeClientMessage() = %#v, want %#v", got, want)
	}
	serialized, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(serialized, []byte(`"message":""`)) {
		t.Fatalf("image-only Pi prompt omitted its required message field: %s", serialized)
	}
}

func TestNormalizePiNativeClientPromptRejectsInvalidImages(t *testing.T) {
	validPNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	outsidePath := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(outsidePath, validPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	unsupported, err := os.CreateTemp("", piImageTempPrefix+"*.png")
	if err != nil {
		t.Fatal(err)
	}
	unsupportedPath := unsupported.Name()
	t.Cleanup(func() { _ = os.Remove(unsupportedPath) })
	if _, err := unsupported.WriteString("not an image"); err != nil {
		t.Fatal(err)
	}
	if err := unsupported.Close(); err != nil {
		t.Fatal(err)
	}
	oversized, err := os.CreateTemp("", piImageTempPrefix+"*.png")
	if err != nil {
		t.Fatal(err)
	}
	oversizedPath := oversized.Name()
	t.Cleanup(func() { _ = os.Remove(oversizedPath) })
	if err := oversized.Truncate(maxPiImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		references []piNativeClientImage
	}{
		{name: "path outside upload directory", references: []piNativeClientImage{{Path: outsidePath}}},
		{name: "unsupported contents", references: []piNativeClientImage{{Path: unsupportedPath}}},
		{name: "oversized image", references: []piNativeClientImage{{Path: oversizedPath}}},
		{name: "too many images", references: make([]piNativeClientImage, piNativeMaxPromptImages+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(piNativeClientMessage{Type: "prompt", Images: test.references})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := normalizePiNativeClientMessage(payload); err == nil {
				t.Fatal("normalizePiNativeClientMessage() accepted invalid image references")
			}
		})
	}
}

func TestPiNativeRunTrackingMarksAssistantErrorsFailed(t *testing.T) {
	process := &piNativeProcess{
		activeRun: 1,
		nextRun:   1,
		runs: map[uint64]piNativeRunSnapshot{
			1: {ID: 1, State: "working", StartedAt: time.Now().UTC()},
		},
	}
	process.trackRunEvent([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"partial"}],"stopReason":"error","errorMessage":"provider failed"}}`))
	process.trackRunEvent([]byte(`{"type":"agent_settled"}`))
	run, found := process.runSnapshot(1)
	if !found || run.State != "failed" || run.Output != "partial" || run.Error != "provider failed" || run.FinishedAt == nil {
		t.Fatalf("tracked failed run = %#v, found=%t", run, found)
	}
}

func TestPiNativeAutomaticCompactionRefreshesDisplayHistory(t *testing.T) {
	writer := &piNativeTestWriteCloser{}
	process := &piNativeProcess{
		nativeProcessCore: &nativeProcessCore{
			spec:   piProcessSpec,
			stdin:  writer,
			done:   make(chan struct{}),
			events: broadcast.NewBroker[[]byte](1),
		},
	}
	process.publishPiEvent([]byte(`{"type":"compaction_end","reason":"threshold","aborted":false}`))

	var command piNativeRPCCommand
	if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "get_entries" || !strings.HasPrefix(command.ID, "kiwi-code-entries-") {
		t.Fatalf("automatic compaction wrote %#v", command)
	}

	writer.Reset()
	process.publishPiEvent([]byte(`{"type":"compaction_end","reason":"manual","aborted":false}`))
	if writer.Len() != 0 {
		t.Fatalf("manual compaction queued duplicate history refresh: %s", writer.Bytes())
	}
}

func TestPiNativeClientCommandsReceiveRequestIDs(t *testing.T) {
	writer := &piNativeTestWriteCloser{}
	process := &piNativeProcess{nativeProcessCore: &nativeProcessCore{
		spec:  piProcessSpec,
		stdin: writer,
		done:  make(chan struct{}),
	}}
	if err := process.sendClientCommand(piNativeRPCCommand{Type: "get_commands"}); err != nil {
		t.Fatal(err)
	}

	var command piNativeRPCCommand
	if err := json.Unmarshal(bytes.TrimSpace(writer.Bytes()), &command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "get_commands" || !strings.HasPrefix(command.ID, "kiwi-code-client-get-commands-") {
		t.Fatalf("sendClientCommand() wrote %#v", command)
	}
}

func TestPiNativeCommandChangesSession(t *testing.T) {
	for _, command := range []string{"compact", "new_session", "set_model", "set_thinking_level"} {
		if !piNativeCommandChangesSession(command) {
			t.Fatalf("piNativeCommandChangesSession(%q) = false", command)
		}
	}
	for _, command := range []string{"prompt", "get_state", "get_messages", "get_commands", "get_session_stats"} {
		if piNativeCommandChangesSession(command) {
			t.Fatalf("piNativeCommandChangesSession(%q) = true", command)
		}
	}
}

func TestPiNativeManagerStreamsRPCEventsAndStopsTheThread(t *testing.T) {
	directory := t.TempDir()
	fakePi := filepath.Join(directory, "fake-pi")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' '{"type":"response","command":"get_state","success":true,"data":{"isStreaming":false,"thinkingLevel":"off","messageCount":0,"pendingMessageCount":0}}'
      ;;
    *'"type":"get_messages"'*)
      printf '%s\n' '{"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}'
      ;;
    *'"type":"get_entries"'*)
      printf '%s\n' '{"type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{"totalMessages":3,"toolCalls":1,"tokens":{"input":1234,"output":567,"cacheRead":8901,"cacheWrite":234,"total":10936},"cost":0.123}}'
      ;;
    *'"type":"compact"'*)
      printf '%s\n' '{"type":"response","command":"compact","success":true,"data":{"summary":"Saved context"}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_start","message":{"role":"user","content":"Review this","timestamp":1}}'
      printf '%s\n' '{"type":"message_update","message":{"role":"assistant","content":[{"type":"text","text":"Working"}],"timestamp":2},"assistantMessageEvent":{"type":"text_delta","delta":"Working"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Done"}],"timestamp":2}}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	manager := newPiNativeManager(filepath.Join(directory, "data"), nil, nil, "test-agent-token", "")
	manager.piPath = fakePi
	item := project.Project{ID: "project-a"}
	thread := project.Thread{ID: "thread-a", Cwd: directory}
	process, err := manager.getOrStart(
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.getOrStart(item, thread, "http://127.0.0.1:1", codingAgentLaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second != process {
		t.Fatal("a second native Pi process was started for the same thread")
	}

	sessionDirectory := filepath.Join(directory, "data", piNativeSessionDirectoryName, item.ID, thread.ID)
	if info, err := os.Stat(sessionDirectory); err != nil || !info.IsDir() {
		t.Fatalf("native Pi session directory was not created: info=%v error=%v", info, err)
	}

	subscription := process.events.Subscribe()
	defer subscription.Close()
	if err := process.refresh(); err != nil {
		t.Fatal(err)
	}
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_state")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_messages")
	waitForPiNativeEvent(t, subscription.Events(), "pi_native_history", "")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_session_stats")

	if err := process.send(piNativeRPCCommand{Type: "prompt", Message: piNativeTestString("Review this")}); err != nil {
		t.Fatal(err)
	}
	waitForPiNativeEvent(t, subscription.Events(), "response", "prompt")
	waitForPiNativeEvent(t, subscription.Events(), "agent_start", "")
	waitForPiNativeEvent(t, subscription.Events(), "message_start", "")
	waitForPiNativeEvent(t, subscription.Events(), "message_update", "")
	waitForPiNativeEvent(t, subscription.Events(), "message_end", "")
	waitForPiNativeEvent(t, subscription.Events(), "agent_settled", "")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_session_stats")
	run, found := process.latestRunSnapshot()
	if !found || run.State != "finished" || run.Output != "Done" || run.FinishedAt == nil {
		t.Fatalf("tracked native Pi run = %#v, found=%t", run, found)
	}
	if err := process.sendClientCommand(piNativeRPCCommand{Type: "compact"}); err != nil {
		t.Fatal(err)
	}
	waitForPiNativeEvent(t, subscription.Events(), "response", "compact")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_state")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_messages")
	waitForPiNativeEvent(t, subscription.Events(), "pi_native_history", "")
	waitForPiNativeEvent(t, subscription.Events(), "response", "get_session_stats")

	original := process
	restarted, err := manager.restart(
		original,
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if restarted == original {
		t.Fatal("native Pi restart reused the stopped process")
	}
	select {
	case <-original.done:
	case <-time.After(5 * time.Second):
		t.Fatal("original native Pi process did not stop during restart")
	}
	if _, err := os.Stat(sessionDirectory); err != nil {
		t.Fatalf("native Pi restart removed the saved session directory: %v", err)
	}
	staleRestart, err := manager.restart(
		original,
		item,
		thread,
		"http://127.0.0.1:1/api/projects/project-a/threads/thread-a",
		codingAgentLaunchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if staleRestart != restarted {
		t.Fatal("a stale restart request replaced the current native Pi process")
	}
	process = restarted
	restartedSubscription := process.events.Subscribe()
	defer restartedSubscription.Close()
	if err := process.refresh(); err != nil {
		t.Fatal(err)
	}
	waitForPiNativeEvent(t, restartedSubscription.Events(), "response", "get_state")
	waitForPiNativeEvent(t, restartedSubscription.Events(), "response", "get_messages")
	waitForPiNativeEvent(t, restartedSubscription.Events(), "pi_native_history", "")
	waitForPiNativeEvent(t, restartedSubscription.Events(), "response", "get_session_stats")
	if current, err := manager.getOrStart(item, thread, "http://127.0.0.1:1", codingAgentLaunchOptions{}); err != nil || current != process {
		t.Fatalf("getOrStart after restart = %p, %v; want %p", current, err, process)
	}
	if err := manager.stopThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if !channelClosed(process.done) {
		t.Fatal("native Pi process remained running")
	}
	process, err = manager.getOrStart(item, thread, "http://127.0.0.1:1", codingAgentLaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.removeThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		t.Fatal("native Pi process did not stop with its thread")
	}
	if _, err := os.Stat(sessionDirectory); !os.IsNotExist(err) {
		t.Fatalf("native Pi session directory remained after thread removal: %v", err)
	}
}

func TestServePiNativeEndToEnd(t *testing.T) {
	harness := newPiNativeWebSocketHarness(t, `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' '{"type":"response","command":"get_state","success":true,"data":{"isStreaming":false,"thinkingLevel":"high","messageCount":0,"pendingMessageCount":0}}'
      ;;
    *'"type":"get_messages"'*)
      printf '%s\n' '{"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}'
      ;;
    *'"type":"get_entries"'*)
      printf '%s\n' '{"type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{"totalMessages":1,"toolCalls":0,"tokens":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"total":15},"cost":0.01}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_start","message":{"role":"user","content":"hi","timestamp":1}}'
      printf '%s\n' '{"type":"message_update","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}],"timestamp":2},"assistantMessageEvent":{"type":"text_delta","delta":"Hello"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}],"timestamp":2}}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"compact"'*)
      printf '%s\n' '{"type":"response","command":"compact","success":true,"data":{"summary":"Saved context"}}'
      ;;
  esac
done
`)

	connection, _, err := websocket.DefaultDialer.Dial(
		harness.websocketURL()+"?model=openai%2Fgpt-5.6&thinking=high",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	readPiNativeWebSocketEvent(t, connection, "pi_native_ready", "")
	state := readPiNativeWebSocketEvent(t, connection, "response", "get_state")
	data, ok := state["data"].(map[string]any)
	if !ok || data["thinkingLevel"] != "high" {
		t.Fatalf("initial Pi state = %#v", state)
	}
	readPiNativeWebSocketEvent(t, connection, "response", "get_messages")
	readPiNativeWebSocketEvent(t, connection, "pi_native_history", "")
	readPiNativeWebSocketEvent(t, connection, "response", "get_session_stats")

	if err := connection.WriteJSON(map[string]any{"type": "prompt", "message": "hi"}); err != nil {
		t.Fatal(err)
	}
	message := readPiNativeWebSocketEvent(t, connection, "message_end", "")
	assistant, ok := message["message"].(map[string]any)
	if !ok || assistant["role"] != "assistant" {
		t.Fatalf("assistant message = %#v", message)
	}
	readPiNativeWebSocketEvent(t, connection, "agent_settled", "")

	if err := connection.WriteJSON(map[string]any{"type": "restart"}); err != nil {
		t.Fatal(err)
	}
	readPiNativeWebSocketEvent(t, connection, "pi_native_restarting", "")
	readPiNativeWebSocketEvent(t, connection, "pi_native_reloaded", "")
	readPiNativeWebSocketEvent(t, connection, "response", "get_state")
}

func TestServePiNativeSlowSubscriberClosesWithoutStoppingProcess(t *testing.T) {
	harness := newPiNativeWebSocketHarness(t, `#!/bin/sh
while IFS= read -r line; do
  :
done
`)
	process, err := harness.handler.nativePi.getOrStart(
		harness.item,
		harness.thread,
		"http://127.0.0.1:1/api/projects/"+harness.item.ID+"/threads/"+harness.thread.ID,
		codingAgentLaunchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the production behavior but make the bounded backlog tiny so this
	// characterization test does not depend on kernel socket buffer sizes.
	process.events = broadcast.NewBroker[[]byte](1)

	connection, _, err := websocket.DefaultDialer.Dial(harness.websocketURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	readPiNativeWebSocketEvent(t, connection, "pi_native_ready", "")

	for index := 0; index < 64; index++ {
		process.events.Publish([]byte(fmt.Sprintf(`{"type":"burst","index":%d}`, index)))
	}

	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, readErr := connection.ReadMessage()
		if readErr == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if !errors.As(readErr, &closeErr) {
			t.Fatalf("slow subscriber read error = %v, want WebSocket close", readErr)
		}
		if closeErr.Code != websocket.CloseTryAgainLater {
			t.Fatalf("slow subscriber close code = %d, want %d", closeErr.Code, websocket.CloseTryAgainLater)
		}
		break
	}

	if channelClosed(process.done) {
		t.Fatal("slow WebSocket subscriber stopped the native Pi process")
	}
	current, err := harness.handler.nativePi.getOrStart(
		harness.item,
		harness.thread,
		"http://127.0.0.1:1",
		codingAgentLaunchOptions{},
	)
	if err != nil || current != process {
		t.Fatalf("native Pi process after slow subscriber = %p, %v; want %p", current, err, process)
	}

	fast := process.events.Subscribe()
	defer fast.Close()
	marker := []byte(`{"type":"after_overflow"}`)
	process.events.Publish(marker)
	select {
	case payload, open := <-fast.Events():
		if !open || !bytes.Equal(payload, marker) {
			t.Fatalf("event flow after overflow = %s, open=%t", payload, open)
		}
	case <-time.After(time.Second):
		t.Fatal("native Pi event flow stalled after slow subscriber closed")
	}
}

type piNativeWebSocketHarness struct {
	item    project.Project
	thread  project.Thread
	handler *terminalHandler
	server  *httptest.Server
}

func newPiNativeWebSocketHarness(t *testing.T, script string) piNativeWebSocketHarness {
	t.Helper()
	directory := t.TempDir()
	fakePi := filepath.Join(directory, "fake-pi")
	if err := os.WriteFile(fakePi, []byte(script), 0o700); err != nil {
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
	socketName := fmt.Sprintf("kcv-pi-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("invalid isolated tmux socket %q", socketName)
	}
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	// Native Pi does not require tmux. Make any accidental tmux dependency fail
	// without touching the canonical production server.
	handler.tmuxPath = ""
	handler.nativePi.piPath = fakePi

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/pi/native", handler.servePiNative)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(handler.nativePi.stopAll)
	return piNativeWebSocketHarness{item: item, thread: thread, handler: handler, server: server}
}

func (h piNativeWebSocketHarness) websocketURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") +
		"/api/projects/" + h.item.ID + "/threads/" + h.thread.ID + "/pi/native"
}

func readPiNativeWebSocketEvent(t *testing.T, connection *websocket.Conn, eventType, command string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := connection.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		_, payload, err := connection.ReadMessage()
		if err != nil {
			t.Fatalf("read while waiting for native Pi %s/%s: %v", eventType, command, err)
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode native Pi event while waiting for %s/%s: %v", eventType, command, err)
		}
		if event["type"] == eventType && (command == "" || event["command"] == command) {
			return event
		}
	}
}

func piNativeTestString(value string) *string { return &value }

type piNativeTestWriteCloser struct {
	bytes.Buffer
}

func (*piNativeTestWriteCloser) Close() error { return nil }

func waitForPiNativeEvent(t *testing.T, events <-chan []byte, eventType, command string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case payload, open := <-events:
			if !open {
				t.Fatalf("native Pi event stream closed before %s/%s", eventType, command)
			}
			var event struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode native Pi event: %v", err)
			}
			if event.Type == eventType && (command == "" || event.Command == command) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for native Pi event %s/%s", eventType, command)
		}
	}
}
