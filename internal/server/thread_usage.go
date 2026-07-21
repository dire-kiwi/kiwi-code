package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const threadUsageFileName = "thread-usage.json"

type threadUsageTotals struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CostUSD          float64 `json:"costUsd"`
}

type threadUsageSnapshot struct {
	ProjectID     string            `json:"projectId"`
	ThreadID      string            `json:"threadId"`
	Own           threadUsageTotals `json:"own"`
	Children      threadUsageTotals `json:"children"`
	Total         threadUsageTotals `json:"total"`
	TokenLimit    *int64            `json:"tokenLimit,omitempty"`
	CostLimitUSD  *float64          `json:"costLimitUsd,omitempty"`
	LimitReached  bool              `json:"limitReached"`
	LimitThreadID string            `json:"limitThreadId,omitempty"`
	UpdatedAt     *time.Time        `json:"updatedAt,omitempty"`
}

type persistedThreadUsage struct {
	ProjectID string            `json:"projectId"`
	ThreadID  string            `json:"threadId"`
	SessionID string            `json:"sessionId"`
	Totals    threadUsageTotals `json:"totals"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type threadUsageSessionKey struct {
	ProjectID string
	ThreadID  string
	SessionID string
}

type threadUsageTracker struct {
	mu       sync.RWMutex
	path     string
	sessions map[threadUsageSessionKey]persistedThreadUsage
	changes  *broadcast.Broker[struct{}]
}

func newThreadUsageTracker(dataDirectory string) (*threadUsageTracker, error) {
	tracker := &threadUsageTracker{
		path:     filepath.Join(dataDirectory, threadUsageFileName),
		sessions: make(map[threadUsageSessionKey]persistedThreadUsage),
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
		if record.ProjectID == "" || record.ThreadID == "" || record.SessionID == "" || !validThreadUsageTotals(record.Totals) {
			return nil, errors.New("decode thread usage: invalid usage record")
		}
		key := threadUsageSessionKey{record.ProjectID, record.ThreadID, record.SessionID}
		if _, duplicate := tracker.sessions[key]; duplicate {
			return nil, errors.New("decode thread usage: duplicate usage record")
		}
		tracker.sessions[key] = record
	}
	return tracker, nil
}

func validThreadUsageTotals(totals threadUsageTotals) bool {
	if totals.InputTokens < 0 || totals.OutputTokens < 0 || totals.CacheReadTokens < 0 || totals.CacheWriteTokens < 0 || totals.TotalTokens < 0 {
		return false
	}
	if math.IsNaN(totals.CostUSD) || math.IsInf(totals.CostUSD, 0) || totals.CostUSD < 0 || totals.CostUSD > 1_000_000_000 {
		return false
	}
	sum := totals.InputTokens + totals.OutputTokens + totals.CacheReadTokens + totals.CacheWriteTokens
	return sum >= 0 && totals.TotalTokens == sum
}

func (t *threadUsageTracker) report(projectID, threadID, sessionID string, totals threadUsageTotals) error {
	projectID, threadID, sessionID = strings.TrimSpace(projectID), strings.TrimSpace(threadID), strings.TrimSpace(sessionID)
	if projectID == "" || threadID == "" || sessionID == "" || len(sessionID) > 512 || !validThreadUsageTotals(totals) {
		return errors.New("invalid thread usage")
	}
	key := threadUsageSessionKey{projectID, threadID, sessionID}
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
	t.notify()
	return nil
}

func (t *threadUsageTracker) saveLocked() error {
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
	if err := writeFileAtomically(t.path, contents, serverAtomicFileOptions{Mode: 0o600, SyncFile: true}); err != nil {
		return fmt.Errorf("save thread usage: %w", err)
	}
	return nil
}

func addThreadUsage(left, right threadUsageTotals) threadUsageTotals {
	return threadUsageTotals{
		InputTokens:      left.InputTokens + right.InputTokens,
		OutputTokens:     left.OutputTokens + right.OutputTokens,
		CacheReadTokens:  left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens: left.CacheWriteTokens + right.CacheWriteTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
		CostUSD:          left.CostUSD + right.CostUSD,
	}
}

func usageLimitReached(total threadUsageTotals, thread project.Thread) bool {
	return (thread.TokenLimit != nil && total.TotalTokens >= *thread.TokenLimit) ||
		(thread.CostLimitUSD != nil && total.CostUSD >= *thread.CostLimitUSD)
}

func (t *threadUsageTracker) snapshots(projects []project.Project) []threadUsageSnapshot {
	t.mu.RLock()
	own := make(map[threadUsageSessionKey]persistedThreadUsage, len(t.sessions))
	for key, record := range t.sessions {
		own[key] = record
	}
	t.mu.RUnlock()

	result := make([]threadUsageSnapshot, 0)
	for _, item := range projects {
		threadOwn := make(map[string]threadUsageTotals, len(item.Threads))
		updated := make(map[string]time.Time, len(item.Threads))
		for key, record := range own {
			if key.ProjectID != item.ID {
				continue
			}
			threadOwn[key.ThreadID] = addThreadUsage(threadOwn[key.ThreadID], record.Totals)
			if record.UpdatedAt.After(updated[key.ThreadID]) {
				updated[key.ThreadID] = record.UpdatedAt
			}
		}
		children := make(map[string][]string)
		for _, thread := range item.Threads {
			if thread.ParentThreadID != "" {
				children[thread.ParentThreadID] = append(children[thread.ParentThreadID], thread.ID)
			}
		}
		var descendantUsage func(string) (threadUsageTotals, time.Time)
		descendantUsage = func(threadID string) (threadUsageTotals, time.Time) {
			var totals threadUsageTotals
			latest := updated[threadID]
			for _, childID := range children[threadID] {
				childTotal, childUpdated := descendantUsage(childID)
				totals = addThreadUsage(totals, addThreadUsage(threadOwn[childID], childTotal))
				if childUpdated.After(latest) {
					latest = childUpdated
				}
			}
			return totals, latest
		}
		projectStart := len(result)
		for _, thread := range item.Threads {
			childTotals, latest := descendantUsage(thread.ID)
			if updated[thread.ID].After(latest) {
				latest = updated[thread.ID]
			}
			total := addThreadUsage(threadOwn[thread.ID], childTotals)
			reached := usageLimitReached(total, thread)
			snapshot := threadUsageSnapshot{
				ProjectID: item.ID, ThreadID: thread.ID, Own: threadOwn[thread.ID], Children: childTotals, Total: total,
				TokenLimit: thread.TokenLimit, CostLimitUSD: thread.CostLimitUSD, LimitReached: reached,
			}
			if reached {
				snapshot.LimitThreadID = thread.ID
			}
			if !latest.IsZero() {
				value := latest.UTC()
				snapshot.UpdatedAt = &value
			}
			result = append(result, snapshot)
		}
		byID := make(map[string]*threadUsageSnapshot, len(item.Threads))
		for index := projectStart; index < len(result); index++ {
			byID[result[index].ThreadID] = &result[index]
		}
		for _, thread := range item.Threads {
			current := byID[thread.ID]
			if current == nil || current.LimitReached {
				continue
			}
			parentID := thread.ParentThreadID
			for parentID != "" {
				parent := byID[parentID]
				if parent == nil {
					break
				}
				if parent.LimitReached {
					current.LimitReached = true
					current.LimitThreadID = parent.LimitThreadID
					break
				}
				parentThreadID := ""
				for _, candidate := range item.Threads {
					if candidate.ID == parentID {
						parentThreadID = candidate.ParentThreadID
						break
					}
				}
				parentID = parentThreadID
			}
		}
	}
	return result
}

func (t *threadUsageTracker) remove(projectID, threadID string) error {
	t.mu.Lock()
	removed := make(map[threadUsageSessionKey]persistedThreadUsage)
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
	t.notify()
	return nil
}

func (t *threadUsageTracker) subscribe() (*broadcast.Subscription[struct{}], func()) {
	subscription := t.changes.Subscribe()
	return subscription, func() { subscription.Close() }
}

func (t *threadUsageTracker) notify() {
	if t != nil && t.changes != nil {
		t.changes.Publish(struct{}{})
	}
}

func (s *Server) listThreadUsage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.threadUsage.snapshots(clientProjects(s.projects.List())))
}

func (s *Server) updateThreadUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID, threadID := r.PathValue("id"), r.PathValue("threadId")
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	var input struct {
		SessionID        string  `json:"sessionId"`
		InputTokens      int64   `json:"inputTokens"`
		OutputTokens     int64   `json:"outputTokens"`
		CacheReadTokens  int64   `json:"cacheReadTokens"`
		CacheWriteTokens int64   `json:"cacheWriteTokens"`
		TotalTokens      int64   `json:"totalTokens"`
		CostUSD          float64 `json:"costUsd"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid thread usage.")
		return
	}
	totals := threadUsageTotals{
		InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
		CacheReadTokens: input.CacheReadTokens, CacheWriteTokens: input.CacheWriteTokens,
		TotalTokens: input.TotalTokens, CostUSD: input.CostUSD,
	}
	if err := s.threadUsage.report(projectID, threadID, input.SessionID, totals); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) threadBudgetReached(projectID, threadID string) (bool, string, error) {
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if err != nil {
		return false, "", err
	}
	snapshots := s.threadUsage.snapshots([]project.Project{item})
	byID := make(map[string]threadUsageSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byID[snapshot.ThreadID] = snapshot
	}
	snapshot := byID[thread.ID]
	return snapshot.LimitReached, snapshot.LimitThreadID, nil
}

func (s *Server) threadBudget(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	reached, sourceID, err := s.threadBudgetReached(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"limitReached": reached, "limitThreadId": sourceID})
}
