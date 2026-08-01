package server

import (
	"encoding/json"
	"net/http"

	"github.com/dire-kiwi/kiwi-code/internal/usage"
)

// Aliases while HTTP and agent code migrate to the usage package directly.
type (
	threadUsageTotals   = usage.Totals
	threadUsageSnapshot = usage.Snapshot
	threadUsageTracker  = usage.Tracker
)

func newThreadUsageTracker(dataDirectory string) (*threadUsageTracker, error) {
	return usage.NewTracker(dataDirectory)
}

func validThreadUsageTotals(totals threadUsageTotals) bool {
	return usage.ValidTotals(totals)
}

func (s *Server) listThreadUsage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.threadUsage.Snapshots(clientProjects(s.projects.List())))
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
	if err := s.threadUsage.Report(projectID, threadID, input.SessionID, totals); err != nil {
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
	reached, sourceID := s.threadUsage.BudgetReached(item, thread.ID)
	return reached, sourceID, nil
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
