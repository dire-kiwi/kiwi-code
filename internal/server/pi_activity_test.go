package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestPiActivityAPI(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	activityPath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/pi/activity"
	updatePiActivityForTest(t, handler, activityPath, `{"state":"working"}`, http.StatusOK)

	activities := listPiActivityForTest(t, handler)
	if len(activities) != 1 || activities[0].ProjectID != item.ID || activities[0].ThreadID != thread.ID || activities[0].State != piActivityWorking {
		t.Fatalf("unexpected working activities: %#v", activities)
	}

	// Reading a thread only acknowledges a completed run, never an active one.
	acknowledgeResponse := httptest.NewRecorder()
	handler.ServeHTTP(acknowledgeResponse, httptest.NewRequest(http.MethodDelete, activityPath, nil))
	if acknowledgeResponse.Code != http.StatusNoContent {
		t.Fatalf("acknowledge working status = %d", acknowledgeResponse.Code)
	}
	if activities = listPiActivityForTest(t, handler); len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("working activity was acknowledged: %#v", activities)
	}

	updatePiActivityForTest(t, handler, activityPath, `{"state":"finished"}`, http.StatusOK)
	activities = listPiActivityForTest(t, handler)
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("unexpected finished activities: %#v", activities)
	}

	acknowledgeResponse = httptest.NewRecorder()
	handler.ServeHTTP(acknowledgeResponse, httptest.NewRequest(http.MethodDelete, activityPath, nil))
	if acknowledgeResponse.Code != http.StatusNoContent {
		t.Fatalf("acknowledge finished status = %d", acknowledgeResponse.Code)
	}
	if activities = listPiActivityForTest(t, handler); len(activities) != 0 {
		t.Fatalf("finished activity was not cleared: %#v", activities)
	}

	updatePiActivityForTest(t, handler, activityPath, `{"state":"unknown"}`, http.StatusBadRequest)

	claudeActivityPath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/claude/activity"
	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"working"}`, http.StatusOK)
	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"working","agent":"claude-gpt"}`, http.StatusOK)
	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"finished","agent":"claude"}`, http.StatusOK)
	activities = listPiActivityForTest(t, handler)
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("Claude GPT working activity was overwritten by regular Claude: %#v", activities)
	}
	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"idle","agent":"claude-gpt"}`, http.StatusNoContent)
	activities = listPiActivityForTest(t, handler)
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("regular Claude finished activity was not retained: %#v", activities)
	}
	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"idle","agent":"claude"}`, http.StatusNoContent)

	orderedWorking := `{"state":"working","session":"session-1","token":"prompt-1"}`
	orderedFinished := `{"state":"finished","session":"session-1","token":"prompt-1"}`
	updatePiActivityForTest(t, handler, claudeActivityPath, orderedWorking, http.StatusOK)
	updatePiActivityForTest(t, handler, claudeActivityPath, orderedFinished, http.StatusOK)
	acknowledgeResponse = httptest.NewRecorder()
	handler.ServeHTTP(acknowledgeResponse, httptest.NewRequest(http.MethodDelete, activityPath, nil))
	if acknowledgeResponse.Code != http.StatusNoContent {
		t.Fatalf("acknowledge ordered finished status = %d", acknowledgeResponse.Code)
	}
	updatePiActivityForTest(t, handler, claudeActivityPath, orderedFinished, http.StatusNoContent)
	if activities = listPiActivityForTest(t, handler); len(activities) != 0 {
		t.Fatalf("duplicate terminal update resurrected acknowledged status: %#v", activities)
	}

	updatePiActivityForTest(t, handler, claudeActivityPath, `{"state":"working","agent":"pi"}`, http.StatusBadRequest)
	updatePiActivityForTest(t, handler, activityPath, `{"state":"working","agent":"claude-gpt"}`, http.StatusBadRequest)
}

func TestAgentActivityRecordsPromptRecencyWithoutAdvancingOnHeartbeats(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	activityPath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/pi/activity"
	promptedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	explicitStart := `{"state":"working","promptStartedAt":"` + promptedAt.Format(time.RFC3339Nano) + `"}`

	updatePiActivityForTest(t, handler, activityPath, explicitStart, http.StatusOK)
	_, recorded, err := store.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.LastPromptAt == nil || !recorded.LastPromptAt.Equal(promptedAt) {
		t.Fatalf("recorded prompt time = %v, want %v", recorded.LastPromptAt, promptedAt)
	}

	// The integration repeats the same prompt timestamp on heartbeats. Legacy
	// integrations omit it, and that repeated working transition is also a
	// heartbeat rather than a new user prompt.
	updatePiActivityForTest(t, handler, activityPath, explicitStart, http.StatusOK)
	updatePiActivityForTest(t, handler, activityPath, `{"state":"working"}`, http.StatusOK)
	_, afterHeartbeats, err := store.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterHeartbeats.LastPromptAt == nil || !afterHeartbeats.LastPromptAt.Equal(promptedAt) {
		t.Fatalf("heartbeats advanced prompt time to %v", afterHeartbeats.LastPromptAt)
	}

	updatePiActivityForTest(t, handler, activityPath, `{"state":"finished"}`, http.StatusOK)
	updatePiActivityForTest(t, handler, activityPath, `{"state":"working"}`, http.StatusOK)
	_, legacyNextPrompt, err := store.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if legacyNextPrompt.LastPromptAt == nil || !legacyNextPrompt.LastPromptAt.After(promptedAt) {
		t.Fatalf("legacy next prompt time = %v, want after %v", legacyNextPrompt.LastPromptAt, promptedAt)
	}

	generatedFinished := `{"state":"finished","promptStartedAt":"` + promptedAt.Format(time.RFC3339Nano) + `"}`
	updatePiActivityForTest(t, handler, activityPath, generatedFinished, http.StatusOK)
	_, afterGeneratedFinished, err := store.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterGeneratedFinished.LastPromptAt == nil || !afterGeneratedFinished.LastPromptAt.Equal(*legacyNextPrompt.LastPromptAt) {
		t.Fatalf("terminal generation advanced prompt time to %v", afterGeneratedFinished.LastPromptAt)
	}
}

func TestWorkingActivityReopensACompletedChildThread(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "Completed child", project.AddThreadOptions{ParentThreadID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseChildThread(item.ID, parent.ID, child.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	activityPath := "/api/projects/" + item.ID + "/threads/" + child.ID + "/pi/activity"
	updatePiActivityForTest(t, handler, activityPath, `{"state":"finished"}`, http.StatusOK)
	if _, persisted, err := store.GetThread(item.ID, child.ID); err != nil || persisted.ClosedAt == nil {
		t.Fatalf("finished activity reopened child: child=%#v error=%v", persisted, err)
	}

	updatePiActivityForTest(t, handler, activityPath, `{"state":"working"}`, http.StatusOK)
	if _, reopened, err := store.GetThread(item.ID, child.ID); err != nil || reopened.ClosedAt != nil {
		t.Fatalf("working activity did not reopen child: child=%#v error=%v", reopened, err)
	} else if reopened.LastPromptAt != nil {
		t.Fatalf("managed child prompt changed user-thread recency: %#v", reopened)
	}
}

func TestAgentActivityAggregatesPiAndClaude(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.updateAgent("project", "thread", codingAgentPi, piActivityFinished, now)
	tracker.updateAgent("project", "thread", codingAgentClaude, piActivityWorking, now.Add(time.Second))

	activities := tracker.list(now.Add(2 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("aggregated activity = %#v, want working", activities)
	}

	tracker.acknowledge("project", "thread")
	activities = tracker.list(now.Add(2 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("acknowledging while Claude works removed activity: %#v", activities)
	}

	tracker.updateAgent("project", "thread", codingAgentClaude, piActivityFinished, now.Add(3*time.Second))
	tracker.updateAgent("project", "thread", codingAgentPi, piActivityWorking, now.Add(4*time.Second))
	activities = tracker.list(now.Add(5 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("Pi working did not take priority: %#v", activities)
	}

	tracker.updateAgent("project", "thread", codingAgentPi, piActivityIdle, now.Add(6*time.Second))
	activities = tracker.list(now.Add(7 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("Claude finished activity was not retained: %#v", activities)
	}
	tracker.acknowledge("project", "thread")
	if activities = tracker.list(now.Add(7 * time.Second)); len(activities) != 0 {
		t.Fatalf("finished activities were not acknowledged: %#v", activities)
	}
}

func TestFinishedActivityIgnoresLateHeartbeatForTheSamePrompt(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityWorking, now)
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityFinished, now.Add(time.Second))

	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityWorking, now.Add(2*time.Second)); applied {
		t.Fatal("a heartbeat from the finished prompt was applied")
	}
	activities := tracker.list(now.Add(3 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("late heartbeat resurrected the working indicator: %#v", activities)
	}

	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-2", piActivityWorking, now.Add(4*time.Second)); !applied {
		t.Fatal("the next prompt could not start working")
	}
	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityFinished, now.Add(5*time.Second)); applied {
		t.Fatal("a delayed terminal update from the previous prompt was applied")
	}
	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityWorking, now.Add(6*time.Second)); applied {
		t.Fatal("a delayed heartbeat from the previous prompt was applied")
	}
	activities = tracker.list(now.Add(5 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("the next prompt was replaced by an older update: %#v", activities)
	}

	tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-2", piActivityFinished, now.Add(7*time.Second))
	tracker.acknowledge("project", "thread")
	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-2", piActivityFinished, now.Add(8*time.Second)); applied {
		t.Fatal("a duplicate terminal update was applied after acknowledgement")
	}
	if activities = tracker.list(now.Add(9 * time.Second)); len(activities) != 0 {
		t.Fatalf("a duplicate terminal update resurrected acknowledged activity: %#v", activities)
	}
}

func TestNewPromptSupersedesAnUnsettledPrompt(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityWorking, now)
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-2", piActivityWorking, now.Add(time.Second))

	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityFinished, now.Add(2*time.Second)); applied {
		t.Fatal("the superseded prompt was allowed to finish the current prompt")
	}
	if _, _, applied := tracker.updateAgentToken("project", "thread", codingAgentClaude, "session", "prompt-1", piActivityWorking, now.Add(3*time.Second)); applied {
		t.Fatal("the superseded prompt was allowed to resume")
	}
	activities := tracker.list(now.Add(4 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("superseded prompt changed current activity: %#v", activities)
	}
}

func TestNewerTerminalCanArriveBeforeItsFirstWorkingUpdate(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now().UTC()
	promptAStartedAt := now.Add(-4 * time.Second)
	promptBStartedAt := now.Add(-2 * time.Second)

	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-a",
		&promptAStartedAt, piActivityWorking, now.Add(-3*time.Second),
	); !applied {
		t.Fatal("prompt A did not start")
	}
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-b",
		&promptBStartedAt, piActivityFinished, now.Add(-time.Second),
	); !applied {
		t.Fatal("newer terminal update was rejected before its first working update")
	}

	activities := tracker.list(now)
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("terminal-first prompt did not settle activity: %#v", activities)
	}
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-b",
		&promptBStartedAt, piActivityWorking, now.Add(time.Second),
	); applied {
		t.Fatal("late first heartbeat resurrected terminal-first prompt B")
	}
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-a",
		&promptAStartedAt, piActivityFinished, now.Add(2*time.Second),
	); applied {
		t.Fatal("older terminal update replaced prompt B")
	}
}

func TestOlderGeneratedTerminalCannotFinishANewerWorkingPrompt(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now().UTC()
	promptAStartedAt := now.Add(-2 * time.Second)
	promptBStartedAt := now.Add(-time.Second)

	tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-b",
		&promptBStartedAt, piActivityWorking, now,
	)
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-a",
		&promptAStartedAt, piActivityFinished, now.Add(time.Second),
	); applied {
		t.Fatal("older terminal update finished newer working prompt")
	}
	activities := tracker.list(now.Add(2 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("older terminal changed newer activity: %#v", activities)
	}
}

func TestFuturePromptGenerationIsClampedBeforeOrdering(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now().UTC()
	future := now.Add(24 * time.Hour)
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-a",
		&future, piActivityWorking, now,
	); !applied {
		t.Fatal("future-skewed prompt A did not start")
	}

	promptBStartedAt := now.Add(time.Second)
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-b",
		&promptBStartedAt, piActivityWorking, now.Add(2*time.Second),
	); !applied {
		t.Fatal("future client timestamp pinned prompt ordering")
	}
	activities := tracker.list(now.Add(3 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("newer prompt did not replace future-skewed prompt: %#v", activities)
	}
}

func TestChildSessionFinishingKeepsTheDrivingSessionWorking(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "main", "prompt-1", piActivityWorking, now)
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "child", "prompt-2", piActivityWorking, now.Add(time.Second))
	tracker.updateAgentToken("project", "thread", codingAgentClaude, "child", "prompt-2", piActivityFinished, now.Add(2*time.Second))

	activities := tracker.list(now.Add(3 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("a child session finishing cleared the spinner: %#v", activities)
	}

	tracker.updateAgentToken("project", "thread", codingAgentClaude, "main", "prompt-1", piActivityFinished, now.Add(4*time.Second))
	activities = tracker.list(now.Add(5 * time.Second))
	if len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("the completed indicator did not appear: %#v", activities)
	}
}

func TestPiActivityEventStream(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/pi/activity/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamResponse, err := server.Client().Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResponse.Body.Close()
	if streamResponse.StatusCode != http.StatusOK {
		t.Fatalf("stream Pi activity status = %d", streamResponse.StatusCode)
	}
	if contentType := streamResponse.Header.Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("stream Pi activity content type = %q", contentType)
	}

	reader := bufio.NewReader(streamResponse.Body)
	activities, err := readPiActivityEvent(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 {
		t.Fatalf("unexpected initial activity: %#v", activities)
	}

	activityPath := server.URL + "/api/projects/" + item.ID + "/threads/" + thread.ID + "/pi/activity"
	updateActivity := func(state piActivityState) {
		t.Helper()
		body := `{"state":"` + string(state) + `"}`
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, activityPath, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		wantStatus := http.StatusOK
		if state == piActivityIdle {
			wantStatus = http.StatusNoContent
		}
		if response.StatusCode != wantStatus {
			t.Fatalf("update Pi activity to %s status = %d, want %d", state, response.StatusCode, wantStatus)
		}
	}

	updateActivity(piActivityWorking)
	activities, err = readPiActivityEvent(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].State != piActivityWorking || activities[0].ThreadID != thread.ID {
		t.Fatalf("unexpected streamed working activity: %#v", activities)
	}

	updateActivity(piActivityFinished)
	activities, err = readPiActivityEvent(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].State != piActivityFinished || activities[0].ThreadID != thread.ID {
		t.Fatalf("unexpected streamed finished activity: %#v", activities)
	}

	updateActivity(piActivityIdle)
	activities, err = readPiActivityEvent(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 {
		t.Fatalf("unexpected streamed idle activity: %#v", activities)
	}
}

func TestPiActivitySubscriptionSignalsRapidTransitions(t *testing.T) {
	tracker := newPiActivityTracker()
	updates, unsubscribe := tracker.subscribe()
	defer unsubscribe()

	now := time.Now()
	tracker.update("project", "thread", piActivityWorking, now)
	tracker.update("project", "thread", piActivityFinished, now.Add(time.Millisecond))
	tracker.update("project", "thread", piActivityIdle, now.Add(2*time.Millisecond))

	for index := 0; index < 3; index++ {
		select {
		case <-updates:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for transition invalidation %d", index)
		}
	}
	if activities := tracker.list(now.Add(3 * time.Millisecond)); len(activities) != 0 {
		t.Fatalf("invalidations did not resolve to current idle state: %#v", activities)
	}
}

func TestPiActivitySubscriptionPublishesWorkingHeartbeats(t *testing.T) {
	tracker := newPiActivityTracker()
	updates, unsubscribe := tracker.subscribe()
	defer unsubscribe()

	now := time.Now()
	for index, wantTime := range []time.Time{now, now.Add(time.Second)} {
		tracker.update("project", "thread", piActivityWorking, wantTime)
		select {
		case <-updates:
			activities := tracker.list(wantTime)
			if len(activities) != 1 || activities[0].State != piActivityWorking || !activities[0].UpdatedAt.Equal(wantTime.UTC()) {
				t.Fatalf("heartbeat %d = %#v, want updatedAt %s", index, activities, wantTime.UTC())
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for heartbeat %d", index)
		}
	}
}

func TestPiActivityEventStreamPeriodicallyReconciles(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.update("project", "thread", piActivityWorking, now)
	serverState := &Server{piActivity: tracker}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverState.streamPiActivityWithInterval(w, r, 20*time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	activities, err := readPiActivityEvent(reader)
	if err != nil {
		t.Fatalf("read initial Pi activity event: %v", err)
	}
	if len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("unexpected initial activity: %#v", activities)
	}

	// Simulate a dropped change notification. The periodic snapshot must still
	// reconcile the client with the authoritative tracker state.
	tracker.mu.Lock()
	tracker.activities[piActivityKey{projectID: "project", threadID: "thread", agent: codingAgentPi}] = piThreadActivity{
		ProjectID: "project",
		ThreadID:  "thread",
		State:     piActivityFinished,
		UpdatedAt: now.Add(time.Second),
	}
	tracker.mu.Unlock()

	for {
		activities, err = readPiActivityEvent(reader)
		if err != nil {
			t.Fatalf("read periodic Pi activity event: %v", err)
		}
		if len(activities) == 1 && activities[0].State == piActivityFinished {
			break
		}
	}
}

func TestPiWorkingActivityExpiresWithoutHeartbeat(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now()
	tracker.update("project", "thread", piActivityWorking, now.Add(-piWorkingTimeout-time.Second))
	if activities := tracker.list(now); len(activities) != 0 {
		t.Fatalf("stale working activity was not removed: %#v", activities)
	}
}

func TestExpiredOrderedActivityCanResumeAndFinish(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now().UTC()
	promptStartedAt := now.Add(-piWorkingTimeout - 2*time.Second)
	tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-1",
		&promptStartedAt, piActivityWorking, promptStartedAt,
	)
	if activities := tracker.list(now); len(activities) != 0 {
		t.Fatalf("stale ordered activity was not removed: %#v", activities)
	}
	if _, startedWorking, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-1",
		&promptStartedAt, piActivityWorking, now.Add(time.Second),
	); !applied {
		t.Fatal("heartbeat could not recover after a temporary sleep timeout")
	} else if startedWorking {
		t.Fatal("lease recovery was misclassified as a new user prompt")
	}
	if activities := tracker.list(now.Add(2 * time.Second)); len(activities) != 1 || activities[0].State != piActivityWorking {
		t.Fatalf("recovered ordered activity did not resume working: %#v", activities)
	}
	if _, _, applied := tracker.updateAgentTokenAt(
		"project", "thread", codingAgentClaude, "session", "prompt-1",
		&promptStartedAt, piActivityFinished, now.Add(3*time.Second),
	); !applied {
		t.Fatal("terminal update could not settle a recovered activity")
	}
	if activities := tracker.list(now.Add(4 * time.Second)); len(activities) != 1 || activities[0].State != piActivityFinished {
		t.Fatalf("recovered ordered activity did not finish: %#v", activities)
	}
}

func TestPromptOrderingStateExpiresAfterBoundedRetention(t *testing.T) {
	tracker := newPiActivityTracker()
	now := time.Now().UTC()
	old := now.Add(-piActivityOrderRetention - time.Second)
	for index := 0; index < 100; index++ {
		tracker.updateAgentToken(
			"project",
			"thread",
			codingAgentClaude,
			fmt.Sprintf("session-%d", index),
			fmt.Sprintf("prompt-%d", index),
			piActivityIdle,
			old,
		)
	}
	if got := len(tracker.promptOrder); got != 100 {
		t.Fatalf("prompt ordering entries = %d, want 100 before expiry", got)
	}
	tracker.list(now)
	if got := len(tracker.promptOrder); got != 0 {
		t.Fatalf("expired prompt ordering entries = %d, want 0", got)
	}
}

func readPiActivityEvent(reader *bufio.Reader) ([]piThreadActivity, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var activities []piThreadActivity
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &activities); err != nil {
			return nil, err
		}
		return activities, nil
	}
}

func updatePiActivityForTest(t *testing.T, handler http.Handler, path, body string, wantStatus int) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(body))
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("update Pi activity status = %d, want %d; body = %s", response.Code, wantStatus, response.Body.String())
	}
}

func listPiActivityForTest(t *testing.T, handler http.Handler) []piThreadActivity {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/pi/activity", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list Pi activity status = %d, body = %s", response.Code, response.Body.String())
	}
	var activities []piThreadActivity
	if err := json.NewDecoder(response.Body).Decode(&activities); err != nil {
		t.Fatal(err)
	}
	return activities
}
