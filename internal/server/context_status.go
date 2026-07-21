package server

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	contextStatusSourcePiTerminal = "pi-terminal"
	contextStatusSourcePiNative   = "pi-native"
	maxContextStatusBodyBytes     = 4 << 10
	maxContextStatusTokens        = int64(1_000_000_000)
	// Pi can briefly exceed 100% before overflow compaction and retry. Keep
	// legitimate overflow visible while still rejecting unbounded input.
	maxContextStatusPercent = 10_000
)

type agentContextStatus struct {
	Source        string    `json:"source"`
	Tokens        *int64    `json:"tokens"`
	ContextWindow int64     `json:"contextWindow"`
	Percent       *float64  `json:"percent"`
	Model         string    `json:"model,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type agentContextStatusUpdate struct {
	Source        string   `json:"source"`
	Tokens        *int64   `json:"tokens"`
	ContextWindow int64    `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
	Model         string   `json:"model"`
}

type contextStatusKey struct {
	projectID string
	threadID  string
	source    string
}

type contextStatusTracker struct {
	mu       sync.RWMutex
	statuses map[contextStatusKey]agentContextStatus
}

func newContextStatusTracker() *contextStatusTracker {
	return &contextStatusTracker{statuses: make(map[contextStatusKey]agentContextStatus)}
}

func (t *contextStatusTracker) update(projectID, threadID string, status agentContextStatus) (agentContextStatus, bool) {
	if t == nil {
		return status, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.statuses == nil {
		t.statuses = make(map[contextStatusKey]agentContextStatus)
	}
	key := contextStatusKey{projectID: projectID, threadID: threadID, source: status.Source}
	current, exists := t.statuses[key]
	changed := !exists || !equalAgentContextStatus(current, status)
	status = cloneAgentContextStatus(status)
	t.statuses[key] = status
	return cloneAgentContextStatus(status), changed
}

func (t *contextStatusTracker) forThread(projectID, threadID string) map[string]agentContextStatus {
	statuses := make(map[string]agentContextStatus)
	if t == nil {
		return statuses
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for key, status := range t.statuses {
		if key.projectID == projectID && key.threadID == threadID {
			statuses[key.source] = cloneAgentContextStatus(status)
		}
	}
	return statuses
}

func (t *contextStatusTracker) removeThread(projectID, threadID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for key := range t.statuses {
		if key.projectID == projectID && key.threadID == threadID {
			delete(t.statuses, key)
		}
	}
}

func (t *contextStatusTracker) removeProject(projectID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for key := range t.statuses {
		if key.projectID == projectID {
			delete(t.statuses, key)
		}
	}
}

func equalAgentContextStatus(left, right agentContextStatus) bool {
	return left.Source == right.Source &&
		left.ContextWindow == right.ContextWindow &&
		left.Model == right.Model &&
		equalOptionalInt64(left.Tokens, right.Tokens) &&
		equalOptionalFloat64(left.Percent, right.Percent)
}

func equalOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalFloat64(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func cloneAgentContextStatus(status agentContextStatus) agentContextStatus {
	if status.Tokens != nil {
		tokens := *status.Tokens
		status.Tokens = &tokens
	}
	if status.Percent != nil {
		percent := *status.Percent
		status.Percent = &percent
	}
	return status
}

func normalizeAgentContextStatus(input agentContextStatusUpdate, now time.Time) (agentContextStatus, bool) {
	if input.Source != contextStatusSourcePiTerminal && input.Source != contextStatusSourcePiNative {
		return agentContextStatus{}, false
	}
	if input.ContextWindow <= 0 || input.ContextWindow > maxContextStatusTokens {
		return agentContextStatus{}, false
	}
	if (input.Tokens == nil) != (input.Percent == nil) {
		return agentContextStatus{}, false
	}
	if input.Tokens != nil && (*input.Tokens < 0 || *input.Tokens > maxContextStatusTokens) {
		return agentContextStatus{}, false
	}
	if input.Percent != nil && (math.IsNaN(*input.Percent) || math.IsInf(*input.Percent, 0) || *input.Percent < 0 || *input.Percent > maxContextStatusPercent) {
		return agentContextStatus{}, false
	}
	model := strings.TrimSpace(input.Model)
	if !validContextStatusModel(model) {
		return agentContextStatus{}, false
	}
	return agentContextStatus{
		Source:        input.Source,
		Tokens:        input.Tokens,
		ContextWindow: input.ContextWindow,
		Percent:       input.Percent,
		Model:         model,
		UpdatedAt:     now.UTC(),
	}, true
}

func validContextStatusModel(model string) bool {
	if !utf8.ValidString(model) || utf8.RuneCountInString(model) > 256 {
		return false
	}
	for _, character := range model {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (s *Server) updateContextStatus(w http.ResponseWriter, r *http.Request) {
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

	var input agentContextStatusUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxContextStatusBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid context status.")
		return
	}
	status, ok := normalizeAgentContextStatus(input, time.Now())
	if !ok {
		writeError(w, http.StatusBadRequest, "Invalid context status.")
		return
	}
	status, changed := s.contextStatuses.update(projectID, threadID, status)
	if changed {
		s.notifyThreadStatusChanged(projectID, threadID)
	}
	writeJSON(w, http.StatusOK, status)
}
