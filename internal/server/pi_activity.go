package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

type piActivityState string

const (
	piActivityWorking          piActivityState = "working"
	piActivityFinished         piActivityState = "finished"
	piActivityIdle             piActivityState = "idle"
	// Integrations heartbeat every 5s. Allow several consecutive misses so a
	// slow request or a busy host cannot prune a genuinely working thread and
	// make the sidebar indicator flicker mid-turn.
	piWorkingTimeout                           = 25 * time.Second
	piActivitySnapshotInterval                 = 5 * time.Second
	maxPiActivityTokenLength                   = 200
)

type piThreadActivity struct {
	ProjectID string          `json:"projectId"`
	ThreadID  string          `json:"threadId"`
	State     piActivityState `json:"state"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type piActivityKey struct {
	projectID string
	threadID  string
	agent     string
	// session separates concurrent agent sessions running against one thread.
	// Claude Code reports activity from child sessions as well as the session
	// the user drives; without this they would share an entry and each child
	// finishing would flip the thread out of working while it is still busy.
	session string
}

type piThreadActivityKey struct {
	projectID string
	threadID  string
}

type piActivityTracker struct {
	mu         sync.Mutex
	activities map[piActivityKey]piThreadActivity
	// settled records the prompt token whose turn has already ended for a key.
	// Integrations that report activity from several processes (Claude Code
	// runs its heartbeat and its stop hook independently) can deliver a working
	// heartbeat that was already in flight when the turn ended. Dropping those
	// keeps a finished turn from flapping back to working.
	settled map[piActivityKey]string
	changes *broadcast.Broker[[]piThreadActivity]
}

func newPiActivityTracker() *piActivityTracker {
	return &piActivityTracker{
		activities: make(map[piActivityKey]piThreadActivity),
		settled:    make(map[piActivityKey]string),
		changes:    broadcast.NewBroker[[]piThreadActivity](broadcast.DefaultMaxPending),
	}
}

func (t *piActivityTracker) update(projectID, threadID string, state piActivityState, now time.Time) *piThreadActivity {
	return t.updateAgent(projectID, threadID, codingAgentPi, state, now)
}

func (t *piActivityTracker) updateAgent(projectID, threadID, agent string, state piActivityState, now time.Time) *piThreadActivity {
	activity, _ := t.updateAgentTransition(projectID, threadID, agent, state, now)
	return activity
}

func (t *piActivityTracker) updateAgentTransition(projectID, threadID, agent string, state piActivityState, now time.Time) (*piThreadActivity, bool) {
	activity, startedWorking, _ := t.updateAgentToken(projectID, threadID, agent, "", "", state, now)
	return activity, startedWorking
}

// updateAgentToken applies an activity update. session scopes the update to one
// agent session and token identifies the prompt it belongs to; both are optional
// and an empty token opts out of ordering protection. The final return value
// reports whether the update was applied.
func (t *piActivityTracker) updateAgentToken(projectID, threadID, agent, session, token string, state piActivityState, now time.Time) (*piThreadActivity, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := piActivityKey{projectID: projectID, threadID: threadID, agent: agent, session: session}
	if token != "" {
		if state == piActivityWorking {
			if t.settled[key] == token {
				return nil, false, false
			}
			delete(t.settled, key)
		} else {
			t.settled[key] = token
		}
	} else if state != piActivityWorking {
		delete(t.settled, key)
	}
	previous, exists := t.activities[key]
	startedWorking := state == piActivityWorking && (!exists || previous.State != piActivityWorking)
	if state == piActivityIdle {
		if exists {
			delete(t.activities, key)
			t.notifyLocked()
		}
		return nil, false, true
	}
	activity := piThreadActivity{
		ProjectID: projectID,
		ThreadID:  threadID,
		State:     state,
		UpdatedAt: now.UTC(),
	}
	t.activities[key] = activity
	// Repeated working updates refresh UpdatedAt and are status events too.
	// Publish every heartbeat so every connected client observes it.
	t.notifyLocked()
	return &activity, startedWorking, true
}

func (t *piActivityTracker) list(now time.Time) []piThreadActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	removedStaleActivity := false
	for key, activity := range t.activities {
		if activity.State == piActivityWorking && now.Sub(activity.UpdatedAt) > piWorkingTimeout {
			delete(t.activities, key)
			delete(t.settled, key)
			removedStaleActivity = true
		}
	}
	activities := t.snapshotLocked()
	if removedStaleActivity {
		t.changes.Publish(activities)
	}
	return activities
}

func (t *piActivityTracker) subscribe() (<-chan []piThreadActivity, func()) {
	subscription := t.changes.Subscribe()
	return subscription.Events(), subscription.Close
}

func (t *piActivityTracker) notifyLocked() {
	t.changes.Publish(t.snapshotLocked())
}

func (t *piActivityTracker) snapshotLocked() []piThreadActivity {
	aggregated := make(map[piThreadActivityKey]piThreadActivity)
	for key, activity := range t.activities {
		threadKey := piThreadActivityKey{projectID: key.projectID, threadID: key.threadID}
		current, exists := aggregated[threadKey]
		if !exists || activityPriority(activity.State) > activityPriority(current.State) ||
			(activity.State == current.State && activity.UpdatedAt.After(current.UpdatedAt)) {
			aggregated[threadKey] = activity
		}
	}
	activities := make([]piThreadActivity, 0, len(aggregated))
	for _, activity := range aggregated {
		activities = append(activities, activity)
	}
	sort.Slice(activities, func(i, j int) bool {
		if activities[i].ProjectID == activities[j].ProjectID {
			return activities[i].ThreadID < activities[j].ThreadID
		}
		return activities[i].ProjectID < activities[j].ProjectID
	})
	return activities
}

func activityPriority(state piActivityState) int {
	if state == piActivityWorking {
		return 2
	}
	if state == piActivityFinished {
		return 1
	}
	return 0
}

func (t *piActivityTracker) acknowledge(projectID, threadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	removedActivity := false
	for key, activity := range t.activities {
		if key.projectID == projectID && key.threadID == threadID && activity.State == piActivityFinished {
			delete(t.activities, key)
			removedActivity = true
		}
	}
	if removedActivity {
		t.notifyLocked()
	}
}

func (t *piActivityTracker) removeThread(projectID, threadID string) {
	t.mu.Lock()
	removedActivity := false
	for key := range t.activities {
		if key.projectID == projectID && key.threadID == threadID {
			delete(t.activities, key)
			delete(t.settled, key)
			removedActivity = true
		}
	}
	for key := range t.settled {
		if key.projectID == projectID && key.threadID == threadID {
			delete(t.settled, key)
		}
	}
	if removedActivity {
		t.notifyLocked()
	}
	t.mu.Unlock()
}

func (t *piActivityTracker) removeProject(projectID string) {
	t.mu.Lock()
	removedActivity := false
	for key := range t.activities {
		if key.projectID == projectID {
			delete(t.activities, key)
			removedActivity = true
		}
	}
	for key := range t.settled {
		if key.projectID == projectID {
			delete(t.settled, key)
		}
	}
	if removedActivity {
		t.notifyLocked()
	}
	t.mu.Unlock()
}

func (s *Server) listPiActivity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.clientPiActivities(s.piActivity.list(time.Now())))
}

func (s *Server) streamPiActivity(w http.ResponseWriter, r *http.Request) {
	s.streamPiActivityWithInterval(w, r, piActivitySnapshotInterval)
}

func (s *Server) streamPiActivityWithInterval(w http.ResponseWriter, r *http.Request, snapshotInterval time.Duration) {
	flusher, ok := prepareEventStream(w, "Pi activity streaming is unavailable.")
	if !ok {
		return
	}

	updates, unsubscribe := s.piActivity.subscribe()
	defer unsubscribe()
	if err := writePiActivityEvent(w, s.clientPiActivities(s.piActivity.list(time.Now()))); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case activities, open := <-updates:
			if !open || writePiActivityEvent(w, s.clientPiActivities(activities)) != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			// Periodic snapshots reconcile clients that missed a transition while
			// disconnected or while optimistically acknowledging an earlier state.
			if err := writePiActivityEvent(w, s.clientPiActivities(s.piActivity.list(time.Now()))); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writePiActivityEvent(w http.ResponseWriter, activities []piThreadActivity) error {
	payload, err := json.Marshal(activities)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}

func (s *Server) updatePiActivity(w http.ResponseWriter, r *http.Request) {
	s.updateAgentActivity(w, r, codingAgentPi, "Pi")
}

func (s *Server) updateClaudeActivity(w http.ResponseWriter, r *http.Request) {
	s.updateAgentActivity(w, r, codingAgentClaude, "Claude")
}

func (s *Server) updateAgentActivity(w http.ResponseWriter, r *http.Request, agent, label string) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	_, thread, err := s.projects.GetThread(projectID, threadID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Thread not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}

	var input struct {
		State           piActivityState `json:"state"`
		Agent           string          `json:"agent,omitempty"`
		Session         string          `json:"session,omitempty"`
		Token           string          `json:"token,omitempty"`
		PromptStartedAt *time.Time      `json:"promptStartedAt,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid "+label+" activity.")
		return
	}
	if input.State != piActivityWorking && input.State != piActivityFinished && input.State != piActivityIdle {
		writeError(w, http.StatusBadRequest, "Unknown "+label+" activity state.")
		return
	}
	if input.PromptStartedAt != nil && input.State != piActivityWorking {
		writeError(w, http.StatusBadRequest, "A prompt start can only be reported with working activity.")
		return
	}
	if len(input.Token) > maxPiActivityTokenLength || len(input.Session) > maxPiActivityTokenLength {
		writeError(w, http.StatusBadRequest, "The "+label+" activity identifiers are too long.")
		return
	}
	activityAgent := agent
	if input.Agent != "" {
		requestedAgent, normalizeErr := normalizeCodingAgent(input.Agent)
		if normalizeErr != nil || agent != codingAgentClaude || !isClaudeCodingAgent(requestedAgent) {
			writeError(w, http.StatusBadRequest, "Unknown "+label+" coding agent.")
			return
		}
		activityAgent = requestedAgent
	}
	now := time.Now().UTC()
	activity, startedWorking, applied := s.piActivity.updateAgentToken(projectID, threadID, activityAgent, input.Session, input.Token, input.State, now)
	if !applied {
		// A heartbeat that was already in flight when the turn ended. Ignoring it
		// keeps the finished indicator from flapping back to working.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if input.State == piActivityWorking && thread.ParentThreadID != "" && thread.ClosedAt != nil {
		if _, err := s.projects.ReopenChildThread(projectID, thread.ParentThreadID, threadID); err != nil {
			if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
				writeError(w, http.StatusNotFound, "Thread not found.")
			} else {
				writeError(w, http.StatusInternalServerError, "Could not reopen the child thread.")
			}
			return
		}
	}

	promptedAt := input.PromptStartedAt
	if promptedAt == nil && startedWorking {
		// Compatibility for already-running integrations that predate explicit
		// prompt timestamps. Repeated working heartbeats do not take this path.
		promptedAt = &now
	}
	if promptedAt != nil && thread.ParentThreadID == "" {
		promptTime := promptedAt.UTC()
		if promptTime.After(now) {
			promptTime = now
		}
		if thread.LastPromptAt == nil || promptTime.After(*thread.LastPromptAt) {
			if _, err := s.projects.RecordThreadPrompt(projectID, threadID, promptTime); err != nil {
				writeError(w, http.StatusInternalServerError, "Could not record thread prompt activity.")
				return
			}
		}
	}
	if activity == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (s *Server) acknowledgePiActivity(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Thread not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}
	s.piActivity.acknowledge(projectID, threadID)
	w.WriteHeader(http.StatusNoContent)
}
