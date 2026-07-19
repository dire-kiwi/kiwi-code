package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

func usageForTest(input, output, read, write int64, cost float64) threadUsageTotals {
	return threadUsageTotals{
		InputTokens: input, OutputTokens: output, CacheReadTokens: read, CacheWriteTokens: write,
		TotalTokens: input + output + read + write, CostUSD: cost,
	}
}

func assertUsageTotals(t *testing.T, got, want threadUsageTotals) {
	t.Helper()
	gotCost, wantCost := got.CostUSD, want.CostUSD
	got.CostUSD, want.CostUSD = 0, 0
	if got != want || math.Abs(gotCost-wantCost) > 1e-9 {
		t.Fatalf("usage = %#v, want %#v", got, want)
	}
}

func TestThreadUsageTrackerPersistsCumulativeSessionsAndRollsUpChildren(t *testing.T) {
	directory := t.TempDir()
	tracker, err := newThreadUsageTracker(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.report("project", "root", "root-session", usageForTest(100, 20, 30, 0, .25)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.report("project", "child", "child-session", usageForTest(50, 10, 5, 1, .10)); err != nil {
		t.Fatal(err)
	}
	// A duplicate and then a stale lower cumulative report must not double count
	// or erase usage already attributed to the session.
	if err := tracker.report("project", "child", "child-session", usageForTest(50, 10, 5, 1, .10)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.report("project", "child", "child-session", usageForTest(40, 8, 4, 0, .08)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.report("project", "child", "second-session", usageForTest(7, 3, 0, 0, .02)); err != nil {
		t.Fatal(err)
	}

	reloaded, err := newThreadUsageTracker(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item := project.Project{ID: "project", Threads: []project.Thread{
		{ID: "root", TokenLimit: int64Pointer(220), CostLimitUSD: float64Pointer(.36)},
		{ID: "child", ParentThreadID: "root", ClosedAt: &now},
		{ID: "unrelated"},
	}}
	snapshots := reloaded.snapshots([]project.Project{item})
	if len(snapshots) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(snapshots))
	}
	byID := make(map[string]threadUsageSnapshot)
	for _, snapshot := range snapshots {
		byID[snapshot.ThreadID] = snapshot
	}
	assertUsageTotals(t, byID["child"].Total, usageForTest(57, 13, 5, 1, .12))
	if !byID["child"].LimitReached || byID["child"].LimitThreadID != "root" {
		t.Fatalf("child did not inherit root limit: %#v", byID["child"])
	}
	root := byID["root"]
	assertUsageTotals(t, root.Own, usageForTest(100, 20, 30, 0, .25))
	assertUsageTotals(t, root.Children, usageForTest(57, 13, 5, 1, .12))
	assertUsageTotals(t, root.Total, usageForTest(157, 33, 35, 1, .37))
	if !root.LimitReached {
		t.Fatal("root aggregate did not reach its limit")
	}
	if byID["unrelated"].Total.TotalTokens != 0 {
		t.Fatal("unrelated usage was included")
	}
	if _, err := filepath.Abs(filepath.Join(directory, threadUsageFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestThreadUsageAndLimitEndpoints(t *testing.T) {
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
	server := handler.(*Server)

	limitsPath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/limits"
	request := httptest.NewRequest(http.MethodPut, limitsPath, bytes.NewBufferString(`{"tokenLimit":100,"costLimitUsd":0.5}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("limits status = %d: %s", response.Code, response.Body.String())
	}

	usagePath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/usage"
	request = httptest.NewRequest(http.MethodPut, usagePath, bytes.NewBufferString(`{"sessionId":"session","inputTokens":80,"outputTokens":20,"cacheReadTokens":0,"cacheWriteTokens":0,"totalTokens":100,"costUsd":0.25}`))
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("usage status = %d: %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/thread-usage", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d", response.Code)
	}
	var snapshots []threadUsageSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshots); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !snapshots[0].LimitReached || snapshots[0].Total.TotalTokens != 100 {
		t.Fatalf("usage snapshots = %#v", snapshots)
	}

	budgetPath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/budget"
	request = httptest.NewRequest(http.MethodGet, budgetPath, nil)
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"limitReached":true`)) {
		t.Fatalf("budget response = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, limitsPath, bytes.NewBufferString(`{"tokenLimit":null,"costLimitUsd":null}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("clear limits status = %d: %s", response.Code, response.Body.String())
	}
	_, cleared, err := store.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.TokenLimit != nil || cleared.CostLimitUSD != nil {
		t.Fatalf("limits were not cleared: %#v", cleared)
	}
}

func TestPiNativeProcessReportsValidSessionStats(t *testing.T) {
	var sessionID string
	var totals threadUsageTotals
	process := &piNativeProcess{
		key: piNativeProcessKey{ProjectID: "project", ThreadID: "thread"},
		usageReporter: func(_ piNativeProcessKey, reportedSessionID string, reported threadUsageTotals) {
			sessionID, totals = reportedSessionID, reported
		},
	}
	process.reportSessionUsage(json.RawMessage(`{"sessionId":"abc","tokens":{"input":10,"output":2,"cacheRead":3,"cacheWrite":1,"total":16},"cost":0.04}`))
	if sessionID != "abc" {
		t.Fatalf("session ID = %q", sessionID)
	}
	assertUsageTotals(t, totals, usageForTest(10, 2, 3, 1, .04))

	sessionID = ""
	process.reportSessionUsage(json.RawMessage(`{"sessionId":"bad","tokens":{"input":10,"output":2,"cacheRead":3,"cacheWrite":1,"total":99},"cost":0.04}`))
	if sessionID != "" {
		t.Fatal("invalid native stats were reported")
	}
}

func TestThreadUsageTrackerRejectsInconsistentTotals(t *testing.T) {
	tracker, err := newThreadUsageTracker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalid := usageForTest(1, 2, 3, 4, .1)
	invalid.TotalTokens++
	if err := tracker.report("project", "thread", "session", invalid); err == nil {
		t.Fatal("inconsistent total was accepted")
	}
}

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }
