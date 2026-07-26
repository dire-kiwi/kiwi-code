package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/wire"
	"github.com/gorilla/websocket"
)

type stateTestMessage struct {
	Type       string          `json:"t"`
	Protocol   int             `json:"protocol"`
	InstanceID string          `json:"instanceId"`
	ServerTime string          `json:"serverTime"`
	ID         uint32          `json:"id"`
	Seq        uint64          `json:"seq"`
	Data       json.RawMessage `json:"data"`
	Error      string          `json:"error"`
	Reason     string          `json:"reason"`
	Timestamp  int64           `json:"ts"`
}

func TestStateSocketProjectsMutationResnapshotAndPing(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Add("First", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{
		projects:     store,
		stateChanges: newStateChangeBroker(),
	}
	connection, closeServer := openStateTestSocket(t, application)
	defer closeServer()
	defer connection.Close()

	ready := readStateTestMessage(t, connection)
	if ready.Type != wire.ServerReady || ready.Protocol != wire.ProtocolVersion ||
		ready.InstanceID == "" || ready.ServerTime == "" {
		t.Fatalf("ready = %#v", ready)
	}

	writeStateTestMessage(t, connection, map[string]any{"t": wire.ClientPing, "ts": int64(1234)})
	pong := readStateTestMessage(t, connection)
	if pong.Type != wire.ServerPong || pong.Timestamp != 1234 {
		t.Fatalf("pong = %#v", pong)
	}

	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(1), "topic": map[string]any{"tag": stateTopicProjects},
	})
	initial := readStateTestMessage(t, connection)
	if initial.Type != wire.ServerSnap || initial.ID != 1 || initial.Seq != 1 {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	var projects []project.Project
	if err := json.Unmarshal(initial.Data, &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != first.ID {
		t.Fatalf("initial projects = %#v", projects)
	}

	second, err := store.Add("Second", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changed := readStateTestMessage(t, connection)
	if changed.Type != wire.ServerSnap || changed.ID != 1 || changed.Seq != 2 {
		t.Fatalf("changed snapshot = %#v", changed)
	}
	projects = nil
	if err := json.Unmarshal(changed.Data, &projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || !stateTestHasProject(projects, first.ID) || !stateTestHasProject(projects, second.ID) {
		t.Fatalf("changed projects = %#v", projects)
	}

	writeStateTestMessage(t, connection, map[string]any{"t": wire.ClientResnap, "id": uint32(1)})
	resnapshot := readStateTestMessage(t, connection)
	if resnapshot.Type != wire.ServerSnap || resnapshot.ID != 1 || resnapshot.Seq != 3 ||
		string(resnapshot.Data) != string(changed.Data) {
		t.Fatalf("resnapshot = %#v, previous data = %s", resnapshot, changed.Data)
	}
}

func TestStateSocketRejectsBadSubscriptionWithoutClosingConnection(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{projects: store}
	connection, closeServer := openStateTestSocket(t, application)
	defer closeServer()
	defer connection.Close()
	_ = readStateTestMessage(t, connection)

	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(1), "topic": map[string]any{"tag": "not-a-topic"},
	})
	rejected := readStateTestMessage(t, connection)
	if rejected.Type != wire.ServerSuberr || rejected.ID != 1 || rejected.Error == "" {
		t.Fatalf("subscription rejection = %#v", rejected)
	}

	// Invalid subscription ids are consumed too: an id can never be reused.
	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(1), "topic": map[string]any{"tag": stateTopicProjects},
	})
	reused := readStateTestMessage(t, connection)
	if reused.Type != wire.ServerSuberr || reused.ID != 1 || !strings.Contains(reused.Error, "never reused") {
		t.Fatalf("reused id response = %#v", reused)
	}

	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(2), "topic": map[string]any{"tag": stateTopicProjects},
	})
	snapshot := readStateTestMessage(t, connection)
	if snapshot.Type != wire.ServerSnap || snapshot.ID != 2 || snapshot.Seq != 1 {
		t.Fatalf("snapshot after rejection = %#v", snapshot)
	}
}

func TestStateSocketEnforcesChannelCap(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{projects: store}
	connection, closeServer := openStateTestSocket(t, application)
	defer closeServer()
	defer connection.Close()
	_ = readStateTestMessage(t, connection)

	for id := uint32(1); id <= stateChannelLimit; id++ {
		writeStateTestMessage(t, connection, map[string]any{
			"t": wire.ClientSub, "id": id, "topic": map[string]any{"tag": stateTopicProjects},
		})
	}
	seen := make(map[uint32]struct{}, stateChannelLimit)
	for range stateChannelLimit {
		snapshot := readStateTestMessage(t, connection)
		if snapshot.Type != wire.ServerSnap || snapshot.Seq != 1 {
			t.Fatalf("initial capped snapshot = %#v", snapshot)
		}
		seen[snapshot.ID] = struct{}{}
	}
	if len(seen) != stateChannelLimit {
		t.Fatalf("received %d unique channels, want %d", len(seen), stateChannelLimit)
	}

	rejectedID := uint32(stateChannelLimit + 1)
	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": rejectedID, "topic": map[string]any{"tag": stateTopicProjects},
	})
	rejected := readStateTestMessage(t, connection)
	if rejected.Type != wire.ServerSuberr || rejected.ID != rejectedID ||
		!strings.Contains(rejected.Error, "at most 64 channels") {
		t.Fatalf("channel cap response = %#v", rejected)
	}
}

func TestStateSocketRejectsCrossOriginUpgrade(t *testing.T) {
	application := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", application.serveStateSocket)
	server := httptest.NewServer(mux)
	defer server.Close()

	header := http.Header{"Origin": []string{"https://untrusted.example"}}
	connection, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/api/state",
		header,
	)
	if connection != nil {
		_ = connection.Close()
	}
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("cross-origin state WebSocket upgrade succeeded")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response = %#v, error = %v", response, err)
	}
}

func TestStateSocketUnsubscribeAndConnectionCloseReleaseTmuxWatches(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false is not installed")
	}
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	socketName := fmt.Sprintf("kcv-state-ws-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("unsafe tmux socket name %q", socketName)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	terminal.tmuxPath = falsePath
	application := &Server{
		projects:        store,
		terminal:        terminal,
		contextStatuses: newContextStatusTracker(),
		stateChanges:    newStateChangeBroker(),
	}
	terminal.threadStatusChanged = application.notifyThreadStatusChanged

	connection, closeServer := openStateTestSocket(t, application)
	defer closeServer()
	_ = readStateTestMessage(t, connection)
	topic := map[string]any{
		"tag": stateTopicThreadStatus, "projectId": item.ID, "threadId": item.Threads[0].ID,
	}
	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(1), "topic": topic,
	})
	if snapshot := readStateTestMessage(t, connection); snapshot.Type != wire.ServerSnap || snapshot.ID != 1 {
		t.Fatalf("thread snapshot = %#v", snapshot)
	}
	eventuallyStateTest(t, func() bool { return stateTestTmuxWatchCount(terminal) == 2 })

	writeStateTestMessage(t, connection, map[string]any{"t": wire.ClientUnsub, "id": uint32(1)})
	eventuallyStateTest(t, func() bool { return stateTestTmuxWatchCount(terminal) == 0 })
	writeStateTestMessage(t, connection, map[string]any{"t": wire.ClientPing, "ts": int64(55)})
	if pong := readStateTestMessage(t, connection); pong.Type != wire.ServerPong || pong.Timestamp != 55 {
		t.Fatalf("pong after unsubscribe = %#v", pong)
	}

	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientSub, "id": uint32(2), "topic": topic,
	})
	if snapshot := readStateTestMessage(t, connection); snapshot.Type != wire.ServerSnap || snapshot.ID != 2 {
		t.Fatalf("replacement thread snapshot = %#v", snapshot)
	}
	eventuallyStateTest(t, func() bool { return stateTestTmuxWatchCount(terminal) == 2 })
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	eventuallyStateTest(t, func() bool { return stateTestTmuxWatchCount(terminal) == 0 })
}

func TestStateSnapshotQueueCoalescesByChannel(t *testing.T) {
	connectionContext, cancelConnection := context.WithCancel(context.Background())
	defer cancelConnection()
	channelContext, cancelChannel := context.WithCancel(connectionContext)
	defer cancelChannel()
	connection := &stateConnection{
		ctx:      connectionContext,
		cancel:   cancelConnection,
		channels: make(map[uint32]*stateChannel),
		wake:     make(chan struct{}, 1),
	}
	channel := &stateChannel{
		connection: connection,
		id:         1,
		ctx:        channelContext,
		cancel:     cancelChannel,
		resnap:     make(chan struct{}, 1),
	}
	connection.channels[channel.id] = channel

	for value := 1; value <= 100; value++ {
		if err := channel.Snapshot(map[string]int{"value": value}); err != nil {
			t.Fatal(err)
		}
	}
	connection.mu.Lock()
	if len(connection.queue) != 1 || !channel.queued || channel.pending == nil {
		connection.mu.Unlock()
		t.Fatalf("coalesced queue = %#v, channel = %#v", connection.queue, channel)
	}
	pending := *channel.pending
	connection.mu.Unlock()
	if pending.seq != 100 || string(pending.data) != `{"value":100}` {
		t.Fatalf("pending snapshot = %#v", pending)
	}

	connection.requestResnap(channel.id)
	if err := channel.Snapshot(map[string]int{"value": 100}); err != nil {
		t.Fatal(err)
	}
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if len(connection.queue) != 1 || channel.pending == nil || channel.pending.seq != 101 {
		t.Fatalf("forced resnapshot did not replace the pending slot: queue=%#v channel=%#v", connection.queue, channel)
	}
}

func TestAgentActivityTopicCoalescesPendingSnapshotsToCurrentState(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	tracker := newPiActivityTracker()
	application := &Server{projects: store, piActivity: tracker}

	ctx, cancel := context.WithCancel(context.Background())
	channel := newStateTestChannel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- application.openAgentActivityTopic(ctx, channel)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("agent activity topic did not stop after cancellation")
		}
	})

	pending := func() (uint64, []piThreadActivity, int) {
		channel.connection.mu.Lock()
		defer channel.connection.mu.Unlock()
		if channel.pending == nil {
			return 0, nil, len(channel.connection.queue)
		}
		var activities []piThreadActivity
		if err := json.Unmarshal(channel.pending.data, &activities); err != nil {
			return channel.pending.seq, nil, len(channel.connection.queue)
		}
		return channel.pending.seq, activities, len(channel.connection.queue)
	}

	eventuallyStateTest(t, func() bool {
		seq, activities, queued := pending()
		return seq == 1 && len(activities) == 0 && queued == 1
	})

	now := time.Now().UTC()
	tracker.update(item.ID, thread.ID, piActivityWorking, now)
	var workingSeq uint64
	eventuallyStateTest(t, func() bool {
		seq, activities, queued := pending()
		if len(activities) != 1 || activities[0].State != piActivityWorking || queued != 1 {
			return false
		}
		workingSeq = seq
		return true
	})

	tracker.update(item.ID, thread.ID, piActivityFinished, now.Add(time.Millisecond))
	eventuallyStateTest(t, func() bool {
		seq, activities, queued := pending()
		return seq > workingSeq &&
			len(activities) == 1 &&
			activities[0].State == piActivityFinished &&
			queued == 1
	})
}

func TestStateChannelEnforcesSnapshotBeforeTerminalMessage(t *testing.T) {
	newFixture := func() (*stateConnection, *stateChannel, context.CancelFunc) {
		connectionContext, cancelConnection := context.WithCancel(context.Background())
		channelContext, cancelChannel := context.WithCancel(connectionContext)
		connection := &stateConnection{
			ctx:      connectionContext,
			cancel:   cancelConnection,
			channels: make(map[uint32]*stateChannel),
			wake:     make(chan struct{}, 1),
		}
		channel := &stateChannel{
			connection: connection,
			id:         1,
			ctx:        channelContext,
			cancel:     cancelChannel,
			resnap:     make(chan struct{}, 1),
		}
		connection.channels[channel.id] = channel
		return connection, channel, cancelConnection
	}

	connection, channel, cancel := newFixture()
	defer cancel()
	connection.finishChannel(channel, nil)
	if len(connection.queue) != 1 || connection.queue[0].snapshot {
		t.Fatalf("no-snapshot terminal queue = %#v", connection.queue)
	}
	var rejected stateTestMessage
	if err := json.Unmarshal(connection.queue[0].payload, &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected.Type != wire.ServerSuberr {
		t.Fatalf("handler returning before its first snapshot emitted %#v", rejected)
	}

	connection, channel, cancel = newFixture()
	defer cancel()
	if err := channel.Snapshot(map[string]bool{"ready": true}); err != nil {
		t.Fatal(err)
	}
	connection.finishChannel(channel, nil)
	if len(connection.queue) != 2 || !connection.queue[0].snapshot || !connection.queue[1].terminal {
		t.Fatalf("snapshot terminal queue ordering = %#v", connection.queue)
	}
	var ended stateTestMessage
	if err := json.Unmarshal(connection.queue[1].payload, &ended); err != nil {
		t.Fatal(err)
	}
	if ended.Type != wire.ServerSubend {
		t.Fatalf("post-snapshot terminal = %#v", ended)
	}
}

func TestStateConnectionShutdownRacesSubscribeWithoutWaitGroupMisuse(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &Server{projects: store}
	for iteration := 0; iteration < 250; iteration++ {
		connectionContext, cancelConnection := context.WithCancel(context.Background())
		connection := &stateConnection{
			server:   application,
			ctx:      connectionContext,
			cancel:   cancelConnection,
			channels: make(map[uint32]*stateChannel),
			wake:     make(chan struct{}, 1),
		}
		subscribeDone := make(chan struct{})
		go func() {
			connection.subscribe(1, json.RawMessage(`{"tag":"projects"}`))
			close(subscribeDone)
		}()
		connection.shutdown()
		connection.channelWG.Wait()
		select {
		case <-subscribeDone:
		case <-time.After(time.Second):
			t.Fatalf("subscribe %d did not finish", iteration)
		}
	}
}

func TestThreadStatusTopicReleasesTmuxWatchesOnCancellation(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false is not installed")
	}
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	socketName := fmt.Sprintf("kcv-state-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("unsafe tmux socket name %q", socketName)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	terminal.tmuxPath = falsePath
	application := &Server{
		projects:        store,
		terminal:        terminal,
		contextStatuses: newContextStatusTracker(),
	}
	terminal.threadStatusChanged = application.notifyThreadStatusChanged

	connectionContext, cancelConnection := context.WithCancel(context.Background())
	defer cancelConnection()
	channelContext, cancelChannel := context.WithCancel(connectionContext)
	connection := &stateConnection{
		ctx:      connectionContext,
		cancel:   cancelConnection,
		channels: make(map[uint32]*stateChannel),
		wake:     make(chan struct{}, 1),
	}
	channel := &stateChannel{
		connection: connection,
		id:         1,
		ctx:        channelContext,
		cancel:     cancelChannel,
		resnap:     make(chan struct{}, 1),
	}
	connection.channels[channel.id] = channel
	done := make(chan error, 1)
	go func() {
		done <- application.openThreadStatusTopic(
			channelContext,
			item.ID,
			item.Threads[0].ID,
			channel,
		)
	}()

	var watches []*tmuxSessionWatch
	eventuallyStateTest(t, func() bool {
		terminal.tmuxWatchMu.Lock()
		defer terminal.tmuxWatchMu.Unlock()
		if len(terminal.tmuxWatches) != 2 {
			return false
		}
		watches = watches[:0]
		for _, watch := range terminal.tmuxWatches {
			if watch.refs != 1 {
				return false
			}
			watches = append(watches, watch)
		}
		connection.mu.Lock()
		hasSnapshot := channel.pending != nil
		connection.mu.Unlock()
		return hasSnapshot
	})

	cancelChannel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("thread status topic returned nil after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thread status topic did not stop after cancellation")
	}
	eventuallyStateTest(t, func() bool {
		terminal.tmuxWatchMu.Lock()
		defer terminal.tmuxWatchMu.Unlock()
		return len(terminal.tmuxWatches) == 0
	})
	for _, watch := range watches {
		select {
		case <-watch.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("tmux watch %q did not release", watch.sessionName)
		}
	}
}

func TestThreadStatusTopicEndsAndReleasesTmuxWatchesWhenThreadIsDeleted(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false is not installed")
	}
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	socketName := fmt.Sprintf("kcv-delete-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("unsafe tmux socket name %q", socketName)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	terminal.tmuxPath = falsePath
	application := &Server{
		projects:        store,
		terminal:        terminal,
		contextStatuses: newContextStatusTracker(),
	}
	terminal.threadStatusChanged = application.notifyThreadStatusChanged

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel := newStateTestChannel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- application.openThreadStatusTopic(ctx, item.ID, thread.ID, channel)
	}()
	eventuallyStateTest(t, func() bool {
		return stateTestTmuxWatchCount(terminal) == 2 && stateTestChannelHasSnapshot(channel)
	})

	if err := store.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var topicErr *stateTopicError
		if !errors.As(err, &topicErr) || !strings.Contains(topicErr.Error(), "no longer exists") {
			t.Fatalf("thread deletion result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("thread status topic did not end promptly after thread deletion")
	}
	eventuallyStateTest(t, func() bool { return stateTestTmuxWatchCount(terminal) == 0 })
}

func TestStateTmuxSnapshotReadsHonorCancellation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	tmuxPath, startedPath := writeHangingStateTestTmux(t)
	socketName := fmt.Sprintf("kcv-cancel-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("unsafe tmux socket name %q", socketName)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	terminal.tmuxPath = tmuxPath
	application := &Server{projects: store, terminal: terminal}

	tests := []struct {
		name string
		read func(context.Context)
	}{
		{
			name: "thread processes",
			read: func(ctx context.Context) {
				_, _ = terminal.processWindowsContext(ctx, item, thread)
			},
		},
		{
			name: "thread shells",
			read: func(ctx context.Context) {
				_, _ = terminal.existingShellWindowsContext(ctx, item, thread)
			},
		},
		{
			name: "sidebar processes",
			read: func(ctx context.Context) {
				_ = application.sidebarProcessWebServersContext(ctx)
			},
		},
		{
			name: "tmux sessions",
			read: func(ctx context.Context) {
				_, _ = terminal.tmuxBrowserSessionsContext(ctx)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.Remove(startedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				test.read(ctx)
				close(done)
			}()
			eventuallyStateTest(t, func() bool {
				_, err := os.Stat(startedPath)
				return err == nil
			})
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("tmux snapshot read did not stop after cancellation")
			}
		})
	}
}

func openStateTestSocket(t *testing.T, application *Server) (*websocket.Conn, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", application.serveStateSocket)
	server := httptest.NewServer(mux)
	connection, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/api/state",
		nil,
	)
	if err != nil {
		server.Close()
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatal(err)
	}
	writeStateTestMessage(t, connection, map[string]any{
		"t": wire.ClientOpen, "protocol": wire.ProtocolVersion, "client": "state-test",
	})
	return connection, server.Close
}

func writeStateTestMessage(t *testing.T, connection *websocket.Conn, value any) {
	t.Helper()
	if err := connection.WriteJSON(value); err != nil {
		t.Fatal(err)
	}
}

func readStateTestMessage(t *testing.T, connection *websocket.Conn) stateTestMessage {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message stateTestMessage
	if err := connection.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	return message
}

func eventuallyStateTest(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}

func stateTestHasProject(projects []project.Project, projectID string) bool {
	for _, item := range projects {
		if item.ID == projectID {
			return true
		}
	}
	return false
}

func stateTestTmuxWatchCount(terminal *terminalHandler) int {
	terminal.tmuxWatchMu.Lock()
	defer terminal.tmuxWatchMu.Unlock()
	return len(terminal.tmuxWatches)
}

func stateTestChannelHasSnapshot(channel *stateChannel) bool {
	channel.connection.mu.Lock()
	defer channel.connection.mu.Unlock()
	return channel.pending != nil
}

func newStateTestChannel(ctx context.Context) *stateChannel {
	connection := &stateConnection{
		ctx:      ctx,
		channels: make(map[uint32]*stateChannel),
		wake:     make(chan struct{}, 1),
	}
	channel := &stateChannel{
		connection: connection,
		id:         1,
		ctx:        ctx,
		resnap:     make(chan struct{}, 1),
	}
	connection.channels[channel.id] = channel
	return channel
}

func writeHangingStateTestTmux(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "tmux-hang")
	startedPath := filepath.Join(directory, "started")
	t.Setenv("KIWI_CODE_STATE_TMUX_STARTED", startedPath)
	script := "#!/bin/sh\n: > \"$KIWI_CODE_STATE_TMUX_STARTED\"\nexec sleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, startedPath
}
