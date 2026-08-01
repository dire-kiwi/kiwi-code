package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
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
		nativeProcessCore: &nativeProcessCore{
			key: piNativeProcessKey{ProjectID: "project", ThreadID: "thread"},
		},
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
