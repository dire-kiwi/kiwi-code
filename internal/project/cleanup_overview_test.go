package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOverviewListsScheduledAndDirtyResourcesWithoutDeletingThem(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", createGitRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanThread, err := store.AddThread(item.ID, "Clean worktree", true)
	if err != nil {
		t.Fatal(err)
	}
	dirtyThread, err := store.AddThread(item.ID, "Dirty worktree", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirtyThread.WorktreePath, "UNTRACKED.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteThread(item.ID, cleanThread.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteThread(item.ID, dirtyThread.ID); err != nil {
		t.Fatal(err)
	}

	worktreeDays := 2
	if _, err := store.UpdateSettingsValues(SettingsUpdate{
		OrphanedWorktreeRetentionDays: &worktreeDays,
	}); err != nil {
		t.Fatal(err)
	}
	overviewAt := time.Now().UTC().Add(72 * time.Hour)
	overview, err := store.CleanupOverview(overviewAt)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.GeneratedAt.Equal(overviewAt) || overview.OrphanedWorktreeRetentionDays != worktreeDays {
		t.Fatalf("unexpected cleanup overview metadata: %#v", overview)
	}
	if len(overview.Worktrees) != 2 {
		t.Fatalf("scheduled worktrees = %#v", overview.Worktrees)
	}

	entriesByPath := make(map[string]WorktreeCleanupOverview, len(overview.Worktrees))
	for _, entry := range overview.Worktrees {
		entriesByPath[entry.WorktreePath] = entry
		if entry.ProjectName != item.Name || entry.ScheduledDeletionAt == nil || !entry.ScheduledDeletionAt.Equal(entry.DetachedAt.Add(48*time.Hour)) {
			t.Fatalf("unexpected worktree entry: %#v", entry)
		}
	}
	cleanEntry := entriesByPath[cleanThread.WorktreePath]
	if cleanEntry.ThreadTitle != cleanThread.Title || cleanEntry.HasUncommittedChanges || cleanEntry.InspectionError != "" {
		t.Fatalf("clean worktree entry = %#v", cleanEntry)
	}
	dirtyEntry := entriesByPath[dirtyThread.WorktreePath]
	if dirtyEntry.ThreadTitle != dirtyThread.Title || !dirtyEntry.HasUncommittedChanges || dirtyEntry.InspectionError != "" {
		t.Fatalf("dirty worktree entry = %#v", dirtyEntry)
	}
	for _, path := range []string{cleanThread.WorktreePath, dirtyThread.WorktreePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup overview removed %q: %v", path, err)
		}
	}

	disabled := 0
	if _, err := store.UpdateSettingsValues(SettingsUpdate{
		OrphanedWorktreeRetentionDays: &disabled,
	}); err != nil {
		t.Fatal(err)
	}
	disabledOverview, err := store.CleanupOverview(overviewAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range disabledOverview.Worktrees {
		if entry.ScheduledDeletionAt != nil {
			t.Fatalf("disabled worktree deletion time = %v", entry.ScheduledDeletionAt)
		}
	}

	if _, err := os.Stat(cleanThread.WorktreePath); err != nil {
		t.Fatalf("cleanup overview removed a clean worktree after cleanup was disabled: %v", err)
	}
}
