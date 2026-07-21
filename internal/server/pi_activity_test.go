package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	invalidFinished := `{"state":"finished","promptStartedAt":"` + promptedAt.Format(time.RFC3339Nano) + `"}`
	updatePiActivityForTest(t, handler, activityPath, invalidFinished, http.StatusBadRequest)
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

func TestPiActivitySubscriptionPreservesRapidTransitions(t *testing.T) {
	tracker := newPiActivityTracker()
	updates, unsubscribe := tracker.subscribe()
	defer unsubscribe()

	now := time.Now()
	tracker.update("project", "thread", piActivityWorking, now)
	tracker.update("project", "thread", piActivityFinished, now.Add(time.Millisecond))
	tracker.update("project", "thread", piActivityIdle, now.Add(2*time.Millisecond))

	wants := []struct {
		length int
		state  piActivityState
	}{
		{length: 1, state: piActivityWorking},
		{length: 1, state: piActivityFinished},
		{length: 0},
	}
	for index, want := range wants {
		select {
		case activities := <-updates:
			if len(activities) != want.length {
				t.Fatalf("transition %d = %#v, want length %d", index, activities, want.length)
			}
			if want.length > 0 && activities[0].State != want.state {
				t.Fatalf("transition %d state = %s, want %s", index, activities[0].State, want.state)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for transition %d", index)
		}
	}
}

func TestPiActivitySubscriptionPublishesWorkingHeartbeats(t *testing.T) {
	tracker := newPiActivityTracker()
	updates, unsubscribe := tracker.subscribe()
	defer unsubscribe()

	now := time.Now()
	tracker.update("project", "thread", piActivityWorking, now)
	tracker.update("project", "thread", piActivityWorking, now.Add(time.Second))

	for index, wantTime := range []time.Time{now, now.Add(time.Second)} {
		select {
		case activities := <-updates:
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
