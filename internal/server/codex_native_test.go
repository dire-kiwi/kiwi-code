package server

import (
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"os"
	"os/exec"
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
	fakePi := filepath.Join(directory, "pi")
	if err := os.WriteFile(fakePi, []byte("#!/bin/sh\nprintf 'Fix Native Thread Naming\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIWI_CODE_PI_PATH", fakePi)
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

func TestCodexNativeNamesFirstAcceptedPrompt(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	for _, scenario := range []string{"text", "image", "locked", "already named", "generator failure"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, _, fixtureThread := codexFixtureManager(t)
			if scenario == "generator failure" {
				if err := os.WriteFile(os.Getenv("KIWI_CODE_PI_PATH"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
			if err != nil {
				t.Fatal(err)
			}
			item, err := store.Add("Native naming", fixtureThread.Cwd)
			if err != nil {
				t.Fatal(err)
			}
			thread := item.Threads[0]
			if scenario == "locked" || scenario == "already named" {
				thread, err = store.UpdateThreadTitle(item.ID, thread.ID, "Keep this title", scenario == "already named")
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "locked" {
					thread, err = store.SetThreadTitleLocked(item.ID, thread.ID, true)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			handler, err := newIsolatedServerHandler(t, store)
			if err != nil {
				t.Fatal(err)
			}
			s := handler.(*Server)
			s.terminal.nativeCodex.codexPath = fixture.codexPath
			server := httptest.NewServer(handler)
			defer server.Close()
			endpoint := server.URL + "/api/projects/" + item.ID + "/threads/" + thread.ID
			p, err := s.terminal.startCodexNativeProcess(item, thread, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.terminal.nativeCodex.stopThread(item.ID, thread.ID) })
			named := make(chan struct{}, 4)
			releaseNamer := make(chan struct{})
			p.mu.Lock()
			original := p.nameThread
			p.nameThread = func(sessionID, prompt string) {
				<-releaseNamer
				original(sessionID, prompt)
				named <- struct{}{}
			}
			p.mu.Unlock()
			if err := p.prompt(chatClientMessage{Message: "reject"}); err == nil {
				t.Fatal("expected rejection")
			}
			message := chatClientMessage{Message: "fix native naming"}
			if scenario == "image" {
				message = chatClientMessage{Images: []piNativeClientImage{{Path: "/tmp/fixture.png"}}}
			}
			if err := p.prompt(message); err != nil {
				t.Fatal(err)
			}
			// The response must finish even while naming is blocked.
			awaitCodexState(t, p, func(s chatState) bool { return !s.Working && len(s.Items) == 2 })
			close(releaseNamer)
			select {
			case <-named:
			case <-time.After(5 * time.Second):
				t.Fatal("native title hook did not finish")
			}
			_, updated, err := store.GetThread(item.ID, thread.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "Fix Native Thread Naming"
			if scenario == "locked" || scenario == "already named" {
				want = "Keep this title"
			}
			if scenario == "generator failure" {
				want = thread.Title
			}
			if updated.Title != want {
				t.Fatalf("title = %q, want %q", updated.Title, want)
			}
			if scenario != "locked" && scenario != "generator failure" && !updated.AutoNamed {
				t.Fatal("title was not marked auto-generated")
			}
			awaitCodexState(t, p, func(s chatState) bool { return !s.Working })
			if err := p.prompt(chatClientMessage{Message: "second prompt"}); err != nil {
				t.Fatal(err)
			}
			awaitCodexState(t, p, func(s chatState) bool { return !s.Working })
			if err := p.stop(); err != nil {
				t.Fatal(err)
			}
			resumed, err := s.terminal.startCodexNativeProcess(item, thread, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			resumed.mu.Lock()
			resumed.nameThread = func(string, string) { named <- struct{}{} }
			resumed.mu.Unlock()
			if err := resumed.prompt(chatClientMessage{Message: "resumed prompt"}); err != nil {
				t.Fatal(err)
			}
			awaitCodexState(t, resumed, func(s chatState) bool { return !s.Working })
			select {
			case <-named:
				t.Fatal("namer ran for a rejected, subsequent, or resumed prompt")
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
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
