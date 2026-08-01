// Package usage tracks per-thread token and cost totals, persists them in
// the data directory, and evaluates thread budget limits.
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/atomicfile"
	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/datadir"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// Totals is a cumulative token/cost account for one agent session.
type Totals struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// Snapshot is the client-facing per-thread usage summary.
type Snapshot struct {
	ProjectID     string     `json:"projectId"`
	ThreadID      string     `json:"threadId"`
	Own           Totals     `json:"own"`
	Total         Totals     `json:"total"`
	TokenLimit    *int64     `json:"tokenLimit,omitempty"`
	CostLimitUSD  *float64   `json:"costLimitUsd,omitempty"`
	LimitReached  bool       `json:"limitReached"`
	LimitThreadID string     `json:"limitThreadId,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

type persistedThreadUsage struct {
	ProjectID string    `json:"projectId"`
	ThreadID  string    `json:"threadId"`
	SessionID string    `json:"sessionId"`
	Totals    Totals    `json:"totals"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type sessionKey struct {
	ProjectID string
	ThreadID  string
	SessionID string
}

// Reporter is the narrow interface agent runtimes use to record usage.
type Reporter interface {
	Report(projectID, threadID, sessionID string, totals Totals) error
}

// Tracker persists session usage records and answers snapshot and budget
// queries. Reports are cumulative per session; totals never decrease.
type Tracker struct {
	mu       sync.RWMutex
	path     string
	sessions map[sessionKey]persistedThreadUsage
	changes  *broadcast.Broker[struct{}]
}

func NewTracker(dataDirectory string) (*Tracker, error) {
	tracker := &Tracker{
		path:     filepath.Join(dataDirectory, datadir.ThreadUsageFileName),
		sessions: make(map[sessionKey]persistedThreadUsage),
		changes:  broadcast.NewBroker[struct{}](broadcast.DefaultMaxPending),
	}
	contents, err := os.ReadFile(tracker.path)
	if errors.Is(err, os.ErrNotExist) {
		return tracker, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read thread usage: %w", err)
	}
	var records []persistedThreadUsage
	if err := json.Unmarshal(contents, &records); err != nil {
		return nil, fmt.Errorf("decode thread usage: %w", err)
	}
	for _, record := range records {
		if record.ProjectID == "" || record.ThreadID == "" || record.SessionID == "" || !validTotals(record.Totals) {
			return nil, errors.New("decode thread usage: invalid usage record")
		}
		key := sessionKey{record.ProjectID, record.ThreadID, record.SessionID}
		if _, duplicate := tracker.sessions[key]; duplicate {
			return nil, errors.New("decode thread usage: duplicate usage record")
		}
		tracker.sessions[key] = record
	}
	return tracker, nil
}

// ValidTotals reports whether an account is internally consistent (no
// negative counts, total equals the component sum, sane cost).
func ValidTotals(totals Totals) bool {
	return validTotals(totals)
}

func validTotals(totals Totals) bool {
	if totals.InputTokens < 0 || totals.OutputTokens < 0 || totals.CacheReadTokens < 0 || totals.CacheWriteTokens < 0 || totals.TotalTokens < 0 {
		return false
	}
	if math.IsNaN(totals.CostUSD) || math.IsInf(totals.CostUSD, 0) || totals.CostUSD < 0 || totals.CostUSD > 1_000_000_000 {
		return false
	}
	sum := totals.InputTokens + totals.OutputTokens + totals.CacheReadTokens + totals.CacheWriteTokens
	return sum >= 0 && totals.TotalTokens == sum
}

func (t *Tracker) Report(projectID, threadID, sessionID string, totals Totals) error {
	projectID, threadID, sessionID = strings.TrimSpace(projectID), strings.TrimSpace(threadID), strings.TrimSpace(sessionID)
	if projectID == "" || threadID == "" || sessionID == "" || len(sessionID) > 512 || !validTotals(totals) {
		return errors.New("invalid thread usage")
	}
	key := sessionKey{projectID, threadID, sessionID}
	t.mu.Lock()
	previous, found := t.sessions[key]
	if found {
		// Reports are cumulative for a stable Pi session. Never let a stale or
		// branch-local report reduce lifetime accounting.
		totals.InputTokens = max(totals.InputTokens, previous.Totals.InputTokens)
		totals.OutputTokens = max(totals.OutputTokens, previous.Totals.OutputTokens)
		totals.CacheReadTokens = max(totals.CacheReadTokens, previous.Totals.CacheReadTokens)
		totals.CacheWriteTokens = max(totals.CacheWriteTokens, previous.Totals.CacheWriteTokens)
		totals.TotalTokens = totals.InputTokens + totals.OutputTokens + totals.CacheReadTokens + totals.CacheWriteTokens
		totals.CostUSD = math.Max(totals.CostUSD, previous.Totals.CostUSD)
		if totals == previous.Totals {
			t.mu.Unlock()
			return nil
		}
	}
	record := persistedThreadUsage{ProjectID: projectID, ThreadID: threadID, SessionID: sessionID, Totals: totals, UpdatedAt: time.Now().UTC()}
	t.sessions[key] = record
	if err := t.saveLocked(); err != nil {
		if found {
			t.sessions[key] = previous
		} else {
			delete(t.sessions, key)
		}
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()
	t.Notify()
	return nil
}

func (t *Tracker) saveLocked() error {
	records := make([]persistedThreadUsage, 0, len(t.sessions))
	for _, record := range t.sessions {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].ProjectID != records[j].ProjectID {
			return records[i].ProjectID < records[j].ProjectID
		}
		if records[i].ThreadID != records[j].ThreadID {
			return records[i].ThreadID < records[j].ThreadID
		}
		return records[i].SessionID < records[j].SessionID
	})
	contents, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if _, err := atomicfile.Write(t.path, contents, atomicfile.Options{
		Mode:        0o600,
		TempPattern: ".kiwi-code-atomic-*",
		SyncFile:    true,
	}); err != nil {
		return fmt.Errorf("save thread usage: %w", err)
	}
	return nil
}

// AddTotals sums two accounts.
func AddTotals(left, right Totals) Totals {
	return Totals{
		InputTokens:      left.InputTokens + right.InputTokens,
		OutputTokens:     left.OutputTokens + right.OutputTokens,
		CacheReadTokens:  left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens: left.CacheWriteTokens + right.CacheWriteTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
		CostUSD:          left.CostUSD + right.CostUSD,
	}
}

func limitReached(total Totals, thread project.Thread) bool {
	return (thread.TokenLimit != nil && total.TotalTokens >= *thread.TokenLimit) ||
		(thread.CostLimitUSD != nil && total.CostUSD >= *thread.CostLimitUSD)
}

func (t *Tracker) Snapshots(projects []project.Project) []Snapshot {
	t.mu.RLock()
	own := make(map[sessionKey]persistedThreadUsage, len(t.sessions))
	for key, record := range t.sessions {
		own[key] = record
	}
	t.mu.RUnlock()

	result := make([]Snapshot, 0)
	for _, item := range projects {
		threadOwn := make(map[string]Totals, len(item.Threads))
		updated := make(map[string]time.Time, len(item.Threads))
		for key, record := range own {
			if key.ProjectID != item.ID {
				continue
			}
			threadOwn[key.ThreadID] = AddTotals(threadOwn[key.ThreadID], record.Totals)
			if record.UpdatedAt.After(updated[key.ThreadID]) {
				updated[key.ThreadID] = record.UpdatedAt
			}
		}
		for _, thread := range item.Threads {
			total := threadOwn[thread.ID]
			reached := limitReached(total, thread)
			snapshot := Snapshot{
				ProjectID: item.ID, ThreadID: thread.ID, Own: total, Total: total,
				TokenLimit: thread.TokenLimit, CostLimitUSD: thread.CostLimitUSD, LimitReached: reached,
			}
			if reached {
				snapshot.LimitThreadID = thread.ID
			}
			if latest := updated[thread.ID]; !latest.IsZero() {
				value := latest.UTC()
				snapshot.UpdatedAt = &value
			}
			result = append(result, snapshot)
		}
	}
	return result
}

// BudgetReached reports whether the thread's configured budget is exhausted,
// returning the thread that supplied the limit.
func (t *Tracker) BudgetReached(item project.Project, threadID string) (bool, string) {
	snapshots := t.Snapshots([]project.Project{item})
	for _, snapshot := range snapshots {
		if snapshot.ThreadID == threadID {
			return snapshot.LimitReached, snapshot.LimitThreadID
		}
	}
	return false, ""
}

func (t *Tracker) Remove(projectID, threadID string) error {
	t.mu.Lock()
	removed := make(map[sessionKey]persistedThreadUsage)
	for key, record := range t.sessions {
		if key.ProjectID == projectID && (threadID == "" || key.ThreadID == threadID) {
			removed[key] = record
			delete(t.sessions, key)
		}
	}
	if len(removed) == 0 {
		t.mu.Unlock()
		return nil
	}
	if err := t.saveLocked(); err != nil {
		for key, record := range removed {
			t.sessions[key] = record
		}
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()
	t.Notify()
	return nil
}

func (t *Tracker) Subscribe() (*broadcast.Subscription[struct{}], func()) {
	subscription := t.changes.Subscribe()
	return subscription, func() { subscription.Close() }
}

func (t *Tracker) SubscribeLatest() (*broadcast.Subscription[struct{}], func()) {
	subscription := t.changes.SubscribeLatest()
	return subscription, func() { subscription.Close() }
}

// Notify wakes subscribers to reread usage state (used when limits change
// without a new usage report).
func (t *Tracker) Notify() {
	if t != nil && t.changes != nil {
		t.changes.Publish(struct{}{})
	}
}
