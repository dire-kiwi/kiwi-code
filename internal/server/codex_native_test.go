package server

import (
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func codexFixtureManager(t *testing.T) (*codexNativeManager, project.Project, project.Thread) {
	t.Helper()
	script, err := os.ReadFile("testdata/codex-app-server.py")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "codex")
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	m := newCodexNativeManager(t.TempDir())
	m.codexPath = path
	t.Cleanup(func() { _ = m.stopMatching(func(nativeProcessKey) bool { return true }) })
	return m, project.Project{ID: "project"}, project.Thread{ID: "thread", Cwd: directory}
}
func awaitCodexState(t *testing.T, p *codexNativeProcess, match func(chatState) bool) chatState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var snapshot struct{ Value chatState }
		if err := json.Unmarshal(p.snapshot(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if match(snapshot.Value) {
			return snapshot.Value
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state: %s", p.snapshot())
	return chatState{}
}
func TestCodexNativeStreamsAndResumesExactConversation(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.getOrStart(item, thread, nil)
	if err != nil || same != p {
		t.Fatal("reconnect did not reuse process", err)
	}
	if err := p.prompt(chatClientMessage{Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	state := awaitCodexState(t, p, func(s chatState) bool { return len(s.Items) == 2 && !s.Working })
	if state.Items[0].Kind != "user" || state.Items[1].Text != "Hello from **Codex**." {
		t.Fatalf("bad transcript: %+v", state)
	}
	if err := m.stopThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed == p || resumed.threadID != p.threadID {
		t.Fatal("did not resume exact saved thread")
	}
	awaitCodexState(t, resumed, func(s chatState) bool { return len(s.Items) == 2 })
	if err := m.removeThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.dataDirectory, "codex-native-sessions", item.ID, thread.ID)); !os.IsNotExist(err) {
		t.Fatal("delete retained mapping", err)
	}
}
func TestCodexNativeApprovalQuestionAndInterrupt(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"approval", "question"} {
		if err := p.prompt(chatClientMessage{Message: prompt}); err != nil {
			t.Fatal(err)
		}
		state := awaitCodexState(t, p, func(s chatState) bool { return len(s.Requests) == 1 })
		if !state.Working {
			t.Fatal("pending approval must keep thread busy")
		}
		response := chatClientMessage{RequestID: state.Requests[0].ID, Decision: "accept"}
		if err := p.respond(response); err != nil {
			t.Fatal(err)
		}
		if err := p.respond(response); err == nil {
			t.Fatal("accepted stale approval")
		}
		awaitCodexState(t, p, func(s chatState) bool { return !s.Working && len(s.Requests) == 0 })
	}
	if err := p.prompt(chatClientMessage{Message: "hold"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	turnID := p.turnID
	p.mu.Unlock()
	if _, err := p.rpc("turn/interrupt", map[string]string{"threadId": p.threadID, "turnId": turnID}); err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool { return !s.Working })
}
func TestCodexNativeErrorsDoNotReplaceSavedConversation(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.prompt(chatClientMessage{Message: "reject"}); err == nil {
		t.Fatal("expected provider error")
	}
	awaitCodexState(t, p, func(s chatState) bool { return !s.Working && strings.Contains(s.Error, "another model") })
	if err := p.prompt(chatClientMessage{Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool { return !s.Working })
	_ = p.stop()
	path := filepath.Join(m.dataDirectory, "codex-native-sessions", item.ID, thread.ID, "thread-id")
	if err := os.WriteFile(path, []byte("missing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.getOrStart(item, thread, nil); err == nil {
		t.Fatal("silently replaced missing saved conversation")
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != "missing" {
		t.Fatal("overwrote resume identity")
	}
}

func TestCodexNativeSettlementStopsAndResumes(t *testing.T) {
	fixture, _, fixtureThread := codexFixtureManager(t)
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Native test", fixtureThread.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	s := handler.(*Server)
	s.terminal.nativeCodex.codexPath = fixture.codexPath
	// Exercise the native process fence independently of sidebar heartbeats.
	s.terminal.nativeCodex.activityReporter = nil
	thread := item.Threads[0]
	p, err := s.terminal.startCodexNativeProcess(item, thread, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.terminal.nativeCodex.stopThread(item.ID, thread.ID) })
	if err := p.prompt(chatClientMessage{Message: "hold"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.setThreadSettlement(item.ID, thread.ID, true, time.Now()); !errors.Is(err, errThreadWorking) {
		t.Fatalf("working settle: %v", err)
	}
	p.mu.Lock()
	turnID := p.turnID
	p.mu.Unlock()
	_, err = p.rpc("turn/interrupt", map[string]string{"threadId": p.threadID, "turnId": turnID})
	if err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool { return !s.Working })
	if _, err := s.setThreadSettlement(item.ID, thread.ID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !channelClosed(p.done) {
		t.Fatal("settlement left Codex running")
	}
	if _, err := s.terminal.startCodexNativeProcess(item, thread, ""); err == nil {
		t.Fatal("settled thread started Codex")
	}
	if _, err := s.setThreadSettlement(item.ID, thread.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.terminal.startCodexNativeProcess(item, thread, "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.threadID != p.threadID {
		t.Fatal("unsettle changed conversation")
	}
	awaitCodexState(t, resumed, func(s chatState) bool { return len(s.Items) == 2 })
}

func TestCodexNativeWebSocketAcknowledgesPromptsAndHeartbeats(t *testing.T) {
	fixture, _, fixtureThread := codexFixtureManager(t)
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Native websocket", fixtureThread.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	s := handler.(*Server)
	s.terminal.nativeCodex.codexPath = fixture.codexPath
	t.Cleanup(func() { _ = s.terminal.nativeCodex.stopThread(item.ID, item.Threads[0].ID) })
	server := httptest.NewServer(handler)
	defer server.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/projects/"+item.ID+"/threads/"+item.Threads[0].ID+"/codex/native", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	pinged := false
	connection.SetPingHandler(func(data string) error {
		pinged = true
		return connection.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	_ = connection.SetReadDeadline(time.Now().Add(terminalPingInterval + 3*time.Second))
	var first struct{ Type string }
	if err := connection.ReadJSON(&first); err != nil || first.Type != "chat_snapshot" {
		t.Fatalf("snapshot: %+v %v", first, err)
	}
	if err := connection.WriteJSON(chatClientMessage{Type: "prompt", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	acknowledged, answered := false, false
	for !acknowledged || !answered {
		var update struct {
			Type  string
			Value struct{ Text string }
		}
		if err := connection.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		acknowledged = acknowledged || update.Type == "chat_sent"
		answered = answered || update.Value.Text == "Hello from **Codex**."
	}
	// Read through the heartbeat; the expected read timeout ends the observation
	// window after ping/pong has already renewed the server's read deadline.
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
			break
		}
	}
	if !pinged {
		t.Fatal("idle native chat did not send a WebSocket heartbeat")
	}
}

func TestCodexNativeEmptyAndRejectedThreadsRestartWithoutResume(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	for _, reject := range []bool{false, true} {
		p, err := m.getOrStart(item, thread, nil)
		if err != nil {
			t.Fatal(err)
		}
		if reject {
			if err := p.prompt(chatClientMessage{Message: "reject"}); err == nil {
				t.Fatal("expected rejection")
			}
		}
		_ = m.stopThread(item.ID, thread.ID)
		path := filepath.Join(m.dataDirectory, "codex-native-sessions", item.ID, thread.ID, "thread-id")
		if err := os.WriteFile(path, []byte("empty-thread-without-rollout"), 0600); err != nil {
			t.Fatal(err)
		}
		// The fixture rejects resume of this ID. Reopening an unused conversation
		// must start fresh because real Codex has no saved rollout yet.
		if _, err := m.getOrStart(item, thread, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexNativeReportsCumulativeUsageWithoutDoubleCountingCache(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	reported := make(chan threadUsageTotals, 4)
	m.usageReporter = func(_ nativeProcessKey, _ string, totals threadUsageTotals) { reported <- totals }
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.prompt(chatClientMessage{Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case totals := <-reported:
		if totals.InputTokens != 8 || totals.CacheReadTokens != 2 || totals.OutputTokens != 5 || totals.TotalTokens != 15 {
			t.Fatalf("bad usage: %+v", totals)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("usage was not reported")
	}
	awaitCodexState(t, p, func(s chatState) bool { return s.Usage != nil && s.Usage.TotalTokens == 15 })
}

func TestCodexNativeQueueSurvivesReconnectAndRunsInOrder(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.prompt(chatClientMessage{Message: "approval"}); err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool { return len(s.Requests) == 1 })
	for _, message := range []string{"first follow-up", "second follow-up"} {
		if err := p.prompt(chatClientMessage{Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	reconnected, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := awaitCodexState(t, reconnected, func(s chatState) bool { return len(s.QueuedMessages) == 2 })
	if state.QueuedMessages[0] != "first follow-up" || state.QueuedMessages[1] != "second follow-up" {
		t.Fatalf("queue order: %+v", state)
	}
	if err := p.respond(chatClientMessage{RequestID: json.RawMessage("900"), Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	state = awaitCodexState(t, p, func(s chatState) bool { return !s.Working && len(s.QueuedMessages) == 0 && len(s.Items) == 7 })
	var users []string
	for _, item := range state.Items {
		if item.Kind == "user" {
			users = append(users, item.Text)
		}
	}
	if strings.Join(users, ",") != "approval,first follow-up,second follow-up" {
		t.Fatalf("transcript order: %v", users)
	}
}

func TestCodexNativeRetainsRejectedQueuedPrompt(t *testing.T) {
	m, item, thread := codexFixtureManager(t)
	p, err := m.getOrStart(item, thread, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.prompt(chatClientMessage{Message: "approval"}); err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool { return len(s.Requests) == 1 })
	if err := p.prompt(chatClientMessage{Message: "reject"}); err != nil {
		t.Fatal(err)
	}
	if err := p.respond(chatClientMessage{RequestID: json.RawMessage("900"), Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	awaitCodexState(t, p, func(s chatState) bool {
		return !s.Working && len(s.QueuedMessages) == 1 && strings.Contains(s.Error, "Queued message failed")
	})
}
