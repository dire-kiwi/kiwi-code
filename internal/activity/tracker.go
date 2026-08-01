// Package activity tracks per-thread coding-agent working state, fed by
// agent-side heartbeats and consumed by the sidebar activity indicator and
// inactivity cleanup.
package activity

import (
	"sort"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/thread"
)

type State string

const (
	StateWorking  State = "working"
	StateFinished State = "finished"
	StateIdle     State = "idle"
	// Integrations heartbeat every 5s. Allow several consecutive misses so a
	// slow request or a busy host cannot prune a genuinely working thread and
	// make the sidebar indicator flicker mid-turn.
	WorkingTimeout   = 25 * time.Second
	OrderRetention   = 30 * time.Minute
	MaxTokenLength   = 200
	maxRetiredTokens = 64
)

type ThreadActivity struct {
	ProjectID string    `json:"projectId"`
	ThreadID  string    `json:"threadId"`
	State     State     `json:"state"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type activityKey struct {
	projectID string
	threadID  string
	agent     string
	// session separates concurrent agent sessions running against one thread.
	// Claude Code reports activity from child sessions as well as the session
	// the user drives; without this they would share an entry and each child
	// finishing would flip the thread out of working while it is still busy.
	session string
}

type threadActivityKey = thread.Key

type promptOrder struct {
	active          string
	activeStartedAt time.Time
	latestStartedAt time.Time
	retired         []string
	updatedAt       time.Time
}

type Tracker struct {
	mu         sync.Mutex
	activities map[activityKey]ThreadActivity
	// promptOrder keeps terminal updates idempotent and prevents delayed updates
	// from an older prompt from replacing the state of a newer prompt. A bounded
	// retired-token history covers requests that remain in flight across turns
	// without growing for the lifetime of the server.
	promptOrder map[activityKey]*promptOrder
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

// NewTracker builds a tracker. stateChanged (optional) is invoked outside the
// tracker's lock on every transition between distinct agent states; it is
// supplied at construction so no caller ever patches tracker fields later.
func NewTracker(stateChanged func(projectID, threadID string, at time.Time)) *Tracker {
	return &Tracker{
		activities:   make(map[activityKey]ThreadActivity),
		promptOrder:  make(map[activityKey]*promptOrder),
		changes:      broadcast.NewBroker[struct{}](broadcast.DefaultMaxPending),
		stateChanged: stateChanged,
	}
}

func (t *Tracker) Update(projectID, threadID string, state State, now time.Time) *ThreadActivity {
	return t.UpdateAgent(projectID, threadID, "pi", state, now)
}

func (t *Tracker) UpdateAgent(projectID, threadID, agent string, state State, now time.Time) *ThreadActivity {
	activity, _ := t.UpdateAgentTransition(projectID, threadID, agent, state, now)
	return activity
}

func (t *Tracker) UpdateAgentTransition(projectID, threadID, agent string, state State, now time.Time) (*ThreadActivity, bool) {
	activity, startedWorking, _ := t.UpdateAgentToken(projectID, threadID, agent, "", "", state, now)
	return activity, startedWorking
}

// updateAgentToken applies an activity update. session scopes the update to one
// agent session and token identifies the prompt it belongs to; both are optional.
// An empty token uses legacy arrival ordering until the key has received an
// ordered update. The final return value reports whether the update was applied.
func (t *Tracker) UpdateAgentToken(projectID, threadID, agent, session, token string, state State, now time.Time) (*ThreadActivity, bool, bool) {
	return t.UpdateAgentTokenAt(projectID, threadID, agent, session, token, nil, state, now)
}

// updateAgentTokenAt also accepts the prompt's stable start time. Claude's
// heartbeat and terminal hooks send the same value, which gives independently
// running hook processes a comparable generation even when the terminal request
// reaches the server before the first working request.
func (t *Tracker) UpdateAgentTokenAt(
	projectID, threadID, agent, session, token string,
	promptStartedAt *time.Time,
	state State,
	now time.Time,
) (*ThreadActivity, bool, bool) {
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
	key := activityKey{projectID: projectID, threadID: threadID, agent: agent, session: session}
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
			order = &promptOrder{}
			t.promptOrder[key] = order
		}
		if state == StateWorking {
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
	startedWorking := state == StateWorking &&
		(startedPrompt || (token == "" && (!exists || previous.State != StateWorking)))
	// A new prompt on a key that is already working is a real change too: the
	// state repeats, but the thread genuinely started fresh work.
	changedState = startedPrompt || (exists && previous.State != state) ||
		(!exists && state != StateIdle)
	if state == StateIdle {
		if exists {
			delete(t.activities, key)
			t.notifyLocked()
		}
		return nil, false, true
	}
	activity := ThreadActivity{
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

func (t *Tracker) List(now time.Time) []ThreadActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	removedStaleActivity := false
	for key, activity := range t.activities {
		if activity.State == StateWorking && now.Sub(activity.UpdatedAt) > WorkingTimeout {
			delete(t.activities, key)
			removedStaleActivity = true
		}
	}
	for key, order := range t.promptOrder {
		if order.updatedAt.IsZero() || now.Sub(order.updatedAt) <= OrderRetention {
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

func (t *Tracker) Subscribe() (<-chan struct{}, func()) {
	subscription := t.changes.Subscribe()
	return subscription.Events(), subscription.Close
}

func (t *Tracker) SubscribeLatest() (<-chan struct{}, func()) {
	subscription := t.changes.SubscribeLatest()
	return subscription.Events(), subscription.Close
}

func (t *Tracker) notifyLocked() {
	t.changes.Publish(struct{}{})
}

func (o *promptOrder) isRetired(token string) bool {
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

func (o *promptOrder) retire(token string) {
	if token == "" || o.isRetired(token) {
		return
	}
	o.retired = append(o.retired, token)
	if len(o.retired) > maxRetiredTokens {
		copy(o.retired, o.retired[len(o.retired)-maxRetiredTokens:])
		o.retired = o.retired[:maxRetiredTokens]
	}
}

func (o *promptOrder) canSupersede(startedAt time.Time, allowUnordered bool) bool {
	if startedAt.IsZero() {
		return allowUnordered && o.latestStartedAt.IsZero()
	}
	return o.latestStartedAt.IsZero() || startedAt.After(o.latestStartedAt)
}

func (o *promptOrder) observe(startedAt time.Time) {
	if startedAt.After(o.latestStartedAt) {
		o.latestStartedAt = startedAt
	}
}

func (t *Tracker) snapshotLocked() []ThreadActivity {
	aggregated := make(map[threadActivityKey]ThreadActivity)
	for key, activity := range t.activities {
		threadKey := threadActivityKey{ProjectID: key.projectID, ThreadID: key.threadID}
		current, exists := aggregated[threadKey]
		if !exists || activityPriority(activity.State) > activityPriority(current.State) ||
			(activity.State == current.State && activity.UpdatedAt.After(current.UpdatedAt)) {
			aggregated[threadKey] = activity
		}
	}
	activities := make([]ThreadActivity, 0, len(aggregated))
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

func activityPriority(state State) int {
	if state == StateWorking {
		return 2
	}
	if state == StateFinished {
		return 1
	}
	return 0
}

func (t *Tracker) Acknowledge(projectID, threadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	removedActivity := false
	acknowledgedAt := time.Now().UTC()
	for key, activity := range t.activities {
		if key.projectID == projectID && key.threadID == threadID && activity.State == StateFinished {
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

func (t *Tracker) RemoveThread(projectID, threadID string) {
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

func (t *Tracker) RemoveProject(projectID string) {
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
