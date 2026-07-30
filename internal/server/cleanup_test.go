package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestCleanupCycleDeletesExpiredArchivedThreads(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThread(item.ID, "Archive me")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := store.SetThreadArchived(item.ID, thread.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	retentionDays := 1
	if _, err := store.UpdateSettingsValues(project.SettingsUpdate{ArchivedThreadRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}

	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application, ok := handler.(*Server)
	if !ok {
		t.Fatalf("handler type = %T, want *Server", handler)
	}
	if err := application.runCleanupCycle(archived.ArchivedAt.Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetThread(item.ID, thread.ID); !errors.Is(err, project.ErrThreadNotFound) {
		t.Fatalf("expired archived thread lookup error = %v, want ErrThreadNotFound", err)
	}
}

func TestCleanupOverviewAPIListsArchivedThreads(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThread(item.ID, "Archive me")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := store.SetThreadArchived(item.ID, thread.ID, true)
	if err != nil {
		t.Fatal(err)
	}

	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/cleanup", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("cleanup overview status = %d, body = %s", response.Code, response.Body.String())
	}
	var overview project.CleanupOverview
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Threads) != 1 || overview.Threads[0].ProjectName != item.Name || overview.Threads[0].ThreadTitle != thread.Title {
		t.Fatalf("cleanup overview threads = %#v", overview.Threads)
	}
	wantDeletion := archived.ArchivedAt.Add(time.Duration(overview.ArchivedThreadRetentionDays) * 24 * time.Hour)
	if overview.Threads[0].ScheduledDeletionAt == nil || !overview.Threads[0].ScheduledDeletionAt.Equal(wantDeletion) {
		t.Fatalf("scheduled deletion = %v, want %v", overview.Threads[0].ScheduledDeletionAt, wantDeletion)
	}
	if overview.Worktrees == nil || len(overview.Worktrees) != 0 {
		t.Fatalf("cleanup overview worktrees = %#v, want an empty array", overview.Worktrees)
	}
}
