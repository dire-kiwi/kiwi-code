package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var errSimulatedCrash = errors.New("simulated crash")

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func newRecoveryTestStore(t *testing.T, dataFile, repositoryPath string) (*Store, Project) {
	t.Helper()
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	return store, item
}

func persistedRecoveryThread(t *testing.T, dataFile, projectID, threadID string) (Thread, bool) {
	t.Helper()
	projects, err := readProjectsFile(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range projects {
		if item.ID != projectID {
			continue
		}
		for _, thread := range item.Threads {
			if thread.ID == threadID {
				return thread, true
			}
		}
	}
	return Thread{}, false
}

func assertWorktreeArtifacts(t *testing.T, repositoryPath string, thread Thread, wantPresent bool) {
	t.Helper()
	_, statErr := os.Stat(thread.WorktreePath)
	branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", thread.Branch))
	if wantPresent && (statErr != nil || branch == "") {
		t.Fatalf("worktree artifacts missing: stat=%v branch=%q", statErr, branch)
	}
	if !wantPresent && (!errors.Is(statErr, os.ErrNotExist) || branch != "") {
		t.Fatalf("worktree artifacts remain: stat=%v branch=%q", statErr, branch)
	}
}

func moveRepository(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func TestBeginThreadCreationRollbackRestoresThreadWhenMarkerSaveFails(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, item := newRecoveryTestStore(t, dataFile, t.TempDir())
	thread, err := store.AddThreadWithOptions(item.ID, "Pending", AddThreadOptions{CreationPending: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitThreadCreation(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected mark failure")
	store.rollbackSave = func(stage string) error {
		if stage == "mark" {
			return injected
		}
		return nil
	}
	marked, err := store.BeginThreadCreationRollback(item.ID, thread.ID)
	if marked || !errors.Is(err, injected) {
		t.Fatalf("BeginThreadCreationRollback() = %t, %v; want false, injected failure", marked, err)
	}
	if _, current, err := store.GetThread(item.ID, thread.ID); err != nil || current.RollbackPending {
		t.Fatalf("in-memory thread after failed mark = %#v, %v", current, err)
	}
	if persisted, found := persistedRecoveryThread(t, dataFile, item.ID, thread.ID); !found || persisted.RollbackPending {
		t.Fatalf("persisted thread after failed mark = %#v, found %t", persisted, found)
	}
}

func TestFinalizeThreadCreationRollbackKeepsWorktreeUntilReadyIsSaved(t *testing.T) {
	requireGit(t)
	repositoryPath := createGitRepository(t)
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, item := newRecoveryTestStore(t, dataFile, repositoryPath)
	thread, err := store.AddThreadWithOptions(item.ID, "Pending", AddThreadOptions{Worktree: true, CreationPending: true})
	if err != nil {
		t.Fatal(err)
	}
	if marked, err := store.BeginThreadCreationRollback(item.ID, thread.ID); !marked || err != nil {
		t.Fatalf("BeginThreadCreationRollback() = %t, %v", marked, err)
	}

	injected := errors.New("injected ready failure")
	removed := false
	store.rollbackSave = func(stage string) error {
		if stage == "ready" {
			return injected
		}
		return nil
	}
	store.rollbackRemoveWorktree = func(string, Thread) error {
		removed = true
		return nil
	}
	if err := store.FinalizeThreadCreationRollback(item.ID, thread.ID); !errors.Is(err, injected) {
		t.Fatalf("FinalizeThreadCreationRollback() error = %v, want injected failure", err)
	}
	if removed {
		t.Fatal("worktree teardown ran before the ready stage was saved")
	}
	persisted, found := persistedRecoveryThread(t, dataFile, item.ID, thread.ID)
	if !found || !persisted.RollbackPending || persisted.RollbackCleanupReady {
		t.Fatalf("persisted thread after failed ready save = %#v, found %t", persisted, found)
	}
	assertWorktreeArtifacts(t, repositoryPath, thread, true)

	store.rollbackSave = nil
	store.rollbackRemoveWorktree = nil
	if err := store.FinalizeThreadCreationRollback(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, found := persistedRecoveryThread(t, dataFile, item.ID, thread.ID); found {
		t.Fatal("finalized rollback left the thread in projects.json")
	}
	assertWorktreeArtifacts(t, repositoryPath, thread, false)
}

func TestStartupRecoveryKeepsRollbackWhenRepositoryIsMissing(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	dataFile := filepath.Join(root, "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	type pendingRollback struct {
		repositoryPath string
		item           Project
		thread         Thread
	}
	stageRollback := func(name string) pendingRollback {
		repositoryPath := createGitRepository(t)
		item, err := store.Add(name, repositoryPath)
		if err != nil {
			t.Fatal(err)
		}
		thread, err := store.AddThreadWithOptions(item.ID, "Pending", AddThreadOptions{Worktree: true, CreationPending: true})
		if err != nil {
			t.Fatal(err)
		}
		if marked, err := store.BeginThreadCreationRollback(item.ID, thread.ID); !marked || err != nil {
			t.Fatalf("BeginThreadCreationRollback() = %t, %v", marked, err)
		}
		// The process exits after publishing the ready stage but before the
		// worktree teardown completes.
		store.rollbackRemoveWorktree = func(string, Thread) error { return errSimulatedCrash }
		if err := store.FinalizeThreadCreationRollback(item.ID, thread.ID); !errors.Is(err, errSimulatedCrash) {
			t.Fatalf("FinalizeThreadCreationRollback() error = %v, want simulated crash", err)
		}
		store.rollbackRemoveWorktree = nil
		persisted, found := persistedRecoveryThread(t, dataFile, item.ID, thread.ID)
		if !found || !persisted.RollbackCleanupReady {
			t.Fatalf("persisted thread before restart = %#v, found %t", persisted, found)
		}
		return pendingRollback{repositoryPath: repositoryPath, item: item, thread: thread}
	}
	reachable := stageRollback("Reachable")
	missing := stageRollback("Missing")

	movedPath := filepath.Join(root, "moved-repository")
	moveRepository(t, missing.repositoryPath, movedPath)
	if _, err := NewStore(dataFile); err != nil {
		t.Fatalf("NewStore() with a missing rollback repository: %v", err)
	}
	if _, found := persistedRecoveryThread(t, dataFile, reachable.item.ID, reachable.thread.ID); found {
		t.Fatal("recovery of the reachable rollback was blocked by the missing repository")
	}
	assertWorktreeArtifacts(t, reachable.repositoryPath, reachable.thread, false)
	persisted, found := persistedRecoveryThread(t, dataFile, missing.item.ID, missing.thread.ID)
	if !found || !persisted.RollbackPending || !persisted.RollbackCleanupReady {
		t.Fatalf("missing-repository rollback was not kept for retry: %#v, found %t", persisted, found)
	}
	if _, err := os.Stat(missing.thread.WorktreePath); err != nil {
		t.Fatalf("worktree was removed without its repository: %v", err)
	}

	moveRepository(t, movedPath, missing.repositoryPath)
	if _, err := NewStore(dataFile); err != nil {
		t.Fatal(err)
	}
	if _, found := persistedRecoveryThread(t, dataFile, missing.item.ID, missing.thread.ID); found {
		t.Fatal("retried rollback left the thread in projects.json")
	}
	assertWorktreeArtifacts(t, missing.repositoryPath, missing.thread, false)
}

func TestStartupRecoveryKeepsWorktreeCreationIntentWhenRepositoryIsMissing(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repositoryPath := createGitRepository(t)
	dataFile := filepath.Join(root, "data", "projects.json")
	store, item := newRecoveryTestStore(t, dataFile, repositoryPath)

	// Simulate the process dying after the creation intent and Git worktree
	// exist but before the thread is saved.
	var created Thread
	store.worktreeSetup = func(thread Thread) error {
		created = thread
		panic(errSimulatedCrash)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != errSimulatedCrash {
				panic(recovered)
			}
		}()
		_, _ = store.AddThread(item.ID, "Interrupted", true)
	}()
	store.worktreeSetup = nil
	if created.ID == "" {
		t.Fatal("worktree setup was not reached")
	}
	if _, found := persistedRecoveryThread(t, dataFile, item.ID, created.ID); found {
		t.Fatal("interrupted thread was saved")
	}
	assertCreationIntent := func(want bool) {
		t.Helper()
		records, err := readOrphanedWorktreesFile(filepath.Join(filepath.Dir(dataFile), "orphaned-worktrees.json"))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range records {
			if record.ThreadID == created.ID && record.DeleteBranch {
				found = true
			}
		}
		if found != want {
			t.Fatalf("creation intent present = %t, want %t: %#v", found, want, records)
		}
	}
	assertCreationIntent(true)
	assertWorktreeArtifacts(t, repositoryPath, created, true)

	movedPath := filepath.Join(root, "moved-repository")
	moveRepository(t, repositoryPath, movedPath)
	if _, err := NewStore(dataFile); err != nil {
		t.Fatalf("NewStore() with a missing intent repository: %v", err)
	}
	assertCreationIntent(true)
	if _, err := os.Stat(created.WorktreePath); err != nil {
		t.Fatalf("worktree was removed without its repository: %v", err)
	}

	moveRepository(t, movedPath, repositoryPath)
	if _, err := NewStore(dataFile); err != nil {
		t.Fatal(err)
	}
	assertCreationIntent(false)
	assertWorktreeArtifacts(t, repositoryPath, created, false)
}

func TestStoreConfigurationFilesStayFailClosed(t *testing.T) {
	for _, name := range []string{"projects.json", "settings.json", "profiles.json"} {
		t.Run(name, func(t *testing.T) {
			dataDirectory := t.TempDir()
			path := filepath.Join(dataDirectory, name)
			if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(filepath.Join(dataDirectory, "projects.json")); err == nil {
				t.Fatalf("NewStore() accepted a corrupt %s", name)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != "{corrupt" {
				t.Fatalf("corrupt %s was modified: %q, %v", name, contents, err)
			}
			if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
				t.Fatalf("configuration file was quarantined: %v", matches)
			}
		})
	}
}
