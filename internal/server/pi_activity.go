package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

type piActivityState string

const (
	piActivityWorking  piActivityState = "working"
	piActivityFinished piActivityState = "finished"
	piActivityIdle     piActivityState = "idle"
	// Integrations heartbeat every 5s. Allow several consecutive misses so a
	// slow request or a busy host cannot prune a genuinely working thread and
	// make the sidebar indicator flicker mid-turn.
	piWorkingTimeout           = 25 * time.Second
	piActivityOrderRetention   = 30 * time.Minute
	maxPiActivityTokenLength   = 200
	maxPiActivityRetiredTokens = 64
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

type piActivityPromptOrder struct {
	active          string
	activeStartedAt time.Time
	latestStartedAt time.Time
	retired         []string
	updatedAt       time.Time
}

type piActivityTracker struct {
	mu         sync.Mutex
	activities map[piActivityKey]piThreadActivity
	// promptOrder keeps terminal updates idempotent and prevents delayed updates
	// from an older prompt from replacing the state of a newer prompt. A bounded
	// retired-token history covers requests that remain in flight across turns
	// without growing for the lifetime of the server.
	promptOrder map[piActivityKey]*piActivityPromptOrder
	// Changes are invalidations rather than historical snapshots. State
	// consumers always reread current state, so an authoritative snapshot can
	// never be followed by an older queued payload.
	changes *broadcast.Broker[struct{}]
	// stateChanged reports transitions between distinct agent states, never the
	// repeated heartbeats that keep an unchanged state fresh. Inactivity cleanup
	// treats it as evidence that a thread is still doing something, so a stalled
	// agent repeating one state must not look like progress.
	stateChanged func(projectID, threadID string, at time.Time)
}

func newPiActivityTracker() *piActivityTracker {
	return &piActivityTracker{
		activities:  make(map[piActivityKey]piThreadActivity),
		promptOrder: make(map[piActivityKey]*piActivityPromptOrder),
		changes:     broadcast.NewBroker[struct{}](broadcast.DefaultMaxPending),
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
// agent session and token identifies the prompt it belongs to; both are optional.
// An empty token uses legacy arrival ordering until the key has received an
// ordered update. The final return value reports whether the update was applied.
func (t *piActivityTracker) updateAgentToken(projectID, threadID, agent, session, token string, state piActivityState, now time.Time) (*piThreadActivity, bool, bool) {
	return t.updateAgentTokenAt(projectID, threadID, agent, session, token, nil, state, now)
}

// updateAgentTokenAt also accepts the prompt's stable start time. Claude's
// heartbeat and terminal hooks send the same value, which gives independently
// running hook processes a comparable generation even when the terminal request
// reaches the server before the first working request.
func (t *piActivityTracker) updateAgentTokenAt(
	projectID, threadID, agent, session, token string,
	promptStartedAt *time.Time,
	state piActivityState,
	now time.Time,
) (*piThreadActivity, bool, bool) {
	// Registered before the lock so it runs after the deferred unlock: the hook
	// shells out to tmux and must never hold the tracker's mutex.
	changedState := false
	defer func() {
		if changedState && t.stateChanged != nil {
			t.stateChanged(projectID, threadID, now)
		}
	}()
	t.mu.Lock()
	defer t.mu.Unlock()
	key := piActivityKey{projectID: projectID, threadID: threadID, agent: agent, session: session}
	now = now.UTC()
	var generation time.Time
	if promptStartedAt != nil {
		generation = promptStartedAt.UTC()
		if generation.After(now) {
			// A bad client clock must not pin ordering in the future and prevent
			// subsequent prompts from superseding this one.
			generation = now
		}
	}
	startedPrompt := false
	if token != "" {
		order := t.promptOrder[key]
		if order == nil {
			order = &piActivityPromptOrder{}
			t.promptOrder[key] = order
		}
		if state == piActivityWorking {
			if order.isRetired(token) {
				return nil, false, false
			}
			if order.active != token {
				if !order.canSupersede(generation, true) {
					return nil, false, false
				}
				order.retire(order.active)
				order.active = token
				order.activeStartedAt = generation
				order.observe(generation)
				startedPrompt = true
			} else if order.activeStartedAt.IsZero() && !generation.IsZero() {
				order.activeStartedAt = generation
				order.observe(generation)
			}
		} else {
			if order.isRetired(token) {
				return nil, false, false
			}
			if order.active != token {
				// A terminal hook for another prompt may race ahead of that
				// prompt's first heartbeat. It may supersede an active prompt
				// only when it carries a newer, comparable generation. For
				// legacy token-only clients, a terminal-first update remains
				// valid when no generated ordering has been established.
				allowUnordered := order.active == "" && order.latestStartedAt.IsZero()
				if !(generation.IsZero() && allowUnordered) &&
					!order.canSupersede(generation, false) {
					return nil, false, false
				}
				order.retire(order.active)
				order.active = token
				order.activeStartedAt = generation
				order.observe(generation)
			} else if order.activeStartedAt.IsZero() && !generation.IsZero() {
				order.activeStartedAt = generation
				order.observe(generation)
			}
			if order.active == token {
				order.active = ""
				order.activeStartedAt = time.Time{}
			}
			order.retire(token)
		}
		if now.After(order.updatedAt) {
			order.updatedAt = now
		}
	} else if order := t.promptOrder[key]; order != nil && (order.active != "" || len(order.retired) > 0) {
		// Once an integration has supplied ordered prompt tokens, an unversioned
		// update for the same key cannot safely supersede that state. Tokenless
		// integrations continue to work on keys that have never used ordering.
		return nil, false, false
	}
	previous, exists := t.activities[key]
	startedWorking := state == piActivityWorking &&
		(startedPrompt || (token == "" && (!exists || previous.State != piActivityWorking)))
	// A new prompt on a key that is already working is a real change too: the
	// state repeats, but the thread genuinely started fresh work.
	changedState = startedPrompt || (exists && previous.State != state) ||
		(!exists && state != piActivityIdle)
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
			removedStaleActivity = true
		}
	}
	for key, order := range t.promptOrder {
		if order.updatedAt.IsZero() || now.Sub(order.updatedAt) <= piActivityOrderRetention {
			continue
		}
		delete(t.promptOrder, key)
	}
	activities := t.snapshotLocked()
	if removedStaleActivity {
		t.changes.Publish(struct{}{})
	}
	return activities
}

func (t *piActivityTracker) subscribe() (<-chan struct{}, func()) {
	subscription := t.changes.Subscribe()
	return subscription.Events(), subscription.Close
}

func (t *piActivityTracker) subscribeLatest() (<-chan struct{}, func()) {
	subscription := t.changes.SubscribeLatest()
	return subscription.Events(), subscription.Close
}

func (t *piActivityTracker) notifyLocked() {
	t.changes.Publish(struct{}{})
}

func (o *piActivityPromptOrder) isRetired(token string) bool {
	if token == "" {
		return false
	}
	for _, retired := range o.retired {
		if retired == token {
			return true
		}
	}
	return false
}

func (o *piActivityPromptOrder) retire(token string) {
	if token == "" || o.isRetired(token) {
		return
	}
	o.retired = append(o.retired, token)
	if len(o.retired) > maxPiActivityRetiredTokens {
		copy(o.retired, o.retired[len(o.retired)-maxPiActivityRetiredTokens:])
		o.retired = o.retired[:maxPiActivityRetiredTokens]
	}
}

func (o *piActivityPromptOrder) canSupersede(startedAt time.Time, allowUnordered bool) bool {
	if startedAt.IsZero() {
		return allowUnordered && o.latestStartedAt.IsZero()
	}
	return o.latestStartedAt.IsZero() || startedAt.After(o.latestStartedAt)
}

func (o *piActivityPromptOrder) observe(startedAt time.Time) {
	if startedAt.After(o.latestStartedAt) {
		o.latestStartedAt = startedAt
	}
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
	acknowledgedAt := time.Now().UTC()
	for key, activity := range t.activities {
		if key.projectID == projectID && key.threadID == threadID && activity.State == piActivityFinished {
			delete(t.activities, key)
			if order := t.promptOrder[key]; order != nil && acknowledgedAt.After(order.updatedAt) {
				order.updatedAt = acknowledgedAt
			}
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
			delete(t.promptOrder, key)
			removedActivity = true
		}
	}
	for key := range t.promptOrder {
		if key.projectID == projectID && key.threadID == threadID {
			delete(t.promptOrder, key)
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
	for key := range t.promptOrder {
		if key.projectID == projectID {
			delete(t.promptOrder, key)
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

func (s *Server) updatePiActivity(w http.ResponseWriter, r *http.Request) {
	s.updateAgentActivity(w, r, codingAgentPi, "Pi")
}

func (s *Server) updateCodexActivity(w http.ResponseWriter, r *http.Request) {
	s.updateAgentActivity(w, r, codingAgentCodex, "Codex")
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
	if len(input.Token) > maxPiActivityTokenLength || len(input.Session) > maxPiActivityTokenLength {
		writeError(w, http.StatusBadRequest, "The "+label+" activity identifiers are too long.")
		return
	}
	activityAgent := agent
	if input.Agent != "" {
		requestedAgent, normalizeErr := normalizeCodingAgent(input.Agent)
		validAgent := false
		switch agent {
		case codingAgentCodex:
			validAgent = normalizeErr == nil && requestedAgent == codingAgentCodex
		case codingAgentClaude:
			validAgent = normalizeErr == nil && isClaudeCodingAgent(requestedAgent)
		}
		if !validAgent {
			writeError(w, http.StatusBadRequest, "Unknown "+label+" coding agent.")
			return
		}
		activityAgent = requestedAgent
	}
	now := time.Now().UTC()
	promptStartedAt := input.PromptStartedAt
	if promptStartedAt != nil {
		normalized := promptStartedAt.UTC()
		if normalized.After(now) {
			normalized = now
		}
		promptStartedAt = &normalized
	}
	activity, startedWorking, applied := s.piActivity.updateAgentTokenAt(
		projectID,
		threadID,
		activityAgent,
		input.Session,
		input.Token,
		promptStartedAt,
		input.State,
		now,
	)
	if !applied {
		// A delayed update from a settled or older prompt. Ignoring it keeps the
		// indicator from flapping between prompt states.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var promptedAt *time.Time
	if input.State == piActivityWorking {
		promptedAt = promptStartedAt
		if promptedAt == nil && startedWorking {
			// Compatibility for already-running integrations that predate explicit
			// prompt timestamps. Repeated working heartbeats do not take this path.
			promptedAt = &now
		}
	}
	if promptedAt != nil {
		promptTime := promptedAt.UTC()
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
