package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettlementPersistsMigratesAndNeverQueuesDeletion(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	store, err := NewStore(file)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	at := time.Now().UTC().Add(-100 * 24 * time.Hour)
	settled, err := store.setThreadSettledAt(item.ID, thread.ID, true, at)
	if err != nil {
		t.Fatal(err)
	}
	if settled.SettledAt == nil || !settled.SettledAt.Equal(at) {
		t.Fatalf("settled = %#v", settled)
	}
	*settled.SettledAt = time.Time{} // Returned timestamps cannot mutate the store.
	repeated, err := store.SetThreadSettled(item.ID, thread.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.SettledAt.Equal(at) {
		t.Fatal("repeat changed settlement timestamp")
	}
	overview, err := store.CleanupOverview(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Worktrees) != 0 {
		t.Fatal("settlement queued cleanup")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Old archives migrate on every read, including stale backend snapshots.
	data = []byte(strings.ReplaceAll(string(data), "settledAt", "archivedAt"))
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(file)
	if err != nil {
		t.Fatal(err)
	}
	_, migrated, err := reloaded.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SettledAt == nil || !migrated.SettledAt.Equal(at) {
		t.Fatal("archive was not migrated")
	}
	encoded, err := json.Marshal(migrated)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "archivedAt") {
		t.Fatal("emitted legacy archive state")
	}
	active, err := reloaded.SetThreadSettled(item.ID, thread.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if active.SettledAt != nil || active.UnsettledAt == nil {
		t.Fatal("unsettle did not restore active state")
	}
	again, err := reloaded.SetThreadSettled(item.ID, thread.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !again.UnsettledAt.Equal(*active.UnsettledAt) {
		t.Fatal("repeat changed unsettle timestamp")
	}
}

func TestIdleSettlementRechecksPersistedActivity(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	store, err := NewStore(file)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewStore(file)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(4 * 24 * time.Hour)
	if err := store.RecordThreadActivity(item.ID, item.Threads[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.SetThreadSettledIfIdle(item.ID, item.Threads[0].ID, now.Add(-3*24*time.Hour)); !errors.Is(err, ErrThreadRecentlyActive) {
		t.Fatalf("stale idle settle = %v", err)
	}
}
