package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestSettlementClosesOnlyItsSessionsAndAllowsReopeningAfterUnsettle(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	other, err := store.AddThread(item.ID, "Other")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	s := handler.(*Server)
	for _, target := range []project.Thread{thread, other} {
		for _, tool := range []string{"terminal", "process"} {
			if err := s.terminal.tmuxCommand("new-session", "-d", "-s", tmuxSessionName(item.ID, target.ID, tool)).Run(); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now().UTC()
	s.piActivity.update(item.ID, thread.ID, piActivityWorking, now)
	if _, err := s.setThreadSettlement(item.ID, thread.ID, true, now); !errors.Is(err, errThreadWorking) {
		t.Fatalf("working settle = %v", err)
	}
	s.piActivity.update(item.ID, thread.ID, piActivityFinished, now)
	result, err := s.setThreadSettlement(item.ID, thread.ID, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.SettledAt == nil {
		t.Fatal("not settled")
	}
	for _, tool := range []string{"terminal", "process"} {
		if exists, err := s.terminal.tmuxExactSessionExists(tmuxSessionName(item.ID, thread.ID, tool)); err != nil || exists {
			t.Fatalf("settled session exists=%v err=%v", exists, err)
		}
		if exists, err := s.terminal.tmuxExactSessionExists(tmuxSessionName(item.ID, other.ID, tool)); err != nil || !exists {
			t.Fatalf("other session exists=%v err=%v", exists, err)
		}
	}
	if _, _, _, err := s.terminal.ensureTmuxSession(item, thread, "terminal"); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("settled attach = %v", err)
	}
	if _, err := s.setThreadSettlement(item.ID, thread.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.terminal.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatalf("unsettled attach = %v", err)
	}
	// Legacy writes are rejected rather than silently reviving the deletion policy.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/projects/"+item.ID+"/threads/"+thread.ID, strings.NewReader(`{"archived":true}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("archive status = %d", response.Code)
	}
}

func TestIdleSettlementBoundaryAndRetention(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	s := &Server{projects: store, piActivity: newPiActivityTracker()}
	cutoff := thread.CreatedAt.Add(threadSettlementIdleLimit)
	if err := s.settleIdleThreads(cutoff.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	_, current, _ := store.GetThread(item.ID, thread.ID)
	if current.SettledAt != nil {
		t.Fatal("settled too soon")
	}
	if err := s.settleIdleThreads(cutoff); err != nil {
		t.Fatal(err)
	}
	_, current, _ = store.GetThread(item.ID, thread.ID)
	if current.SettledAt == nil {
		t.Fatal("did not settle at three days")
	}
	if err := s.runCleanupCycle(cutoff.Add(365 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetThread(item.ID, thread.ID); err != nil {
		t.Fatal("settled thread was deleted", err)
	}
	active, err := store.SetThreadSettled(item.ID, thread.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.settleIdleThreads(active.UnsettledAt.Add(threadSettlementIdleLimit - time.Second)); err != nil {
		t.Fatal(err)
	}
	_, current, _ = store.GetThread(item.ID, thread.ID)
	if current.SettledAt != nil {
		t.Fatal("unsettle did not reset idle period")
	}
}
