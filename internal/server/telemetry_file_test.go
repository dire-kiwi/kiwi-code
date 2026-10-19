package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func assertQuarantinedTelemetryFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt telemetry file %s was not moved aside: %v", filepath.Base(path), err)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantined copies of %s = %v, want one", filepath.Base(path), matches)
	}
	quarantined, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(quarantined) != string(contents) {
		t.Fatalf("quarantined contents = %q, want %q", quarantined, contents)
	}
}

func TestCorruptTelemetryFilesAreQuarantinedAtStartup(t *testing.T) {
	dataDirectory := t.TempDir()
	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	usagePath := filepath.Join(dataDirectory, threadUsageFileName)
	closuresPath := filepath.Join(dataDirectory, sessionClosureLogFileName)
	corruptUsage := []byte(`[{"projectId":"project","threadId":"thread","sessionId":"session","totals":{"totalTokens":-1}}]`)
	corruptClosures := []byte("{truncated")
	if err := os.WriteFile(usagePath, corruptUsage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(closuresPath, corruptClosures, 0o600); err != nil {
		t.Fatal(err)
	}

	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatalf("server did not start with corrupt telemetry files: %v", err)
	}
	server := handler.(*Server)
	assertQuarantinedTelemetryFile(t, usagePath, corruptUsage)
	assertQuarantinedTelemetryFile(t, closuresPath, corruptClosures)

	if err := server.threadUsage.report("project", "thread", "session", usageForTest(1, 2, 0, 0, .01)); err != nil {
		t.Fatalf("report usage after quarantine: %v", err)
	}
	if _, err := newThreadUsageTracker(dataDirectory); err != nil {
		t.Fatalf("fresh usage file is not readable: %v", err)
	}
	events, err := server.sessionClosures.list()
	if err != nil || len(events) != 0 {
		t.Fatalf("session closures after quarantine = %#v, %v", events, err)
	}
}

func TestTelemetryReadErrorsStillFailStartup(t *testing.T) {
	dataDirectory := t.TempDir()
	usagePath := filepath.Join(dataDirectory, threadUsageFileName)
	if err := os.Mkdir(usagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := newThreadUsageTracker(dataDirectory); err == nil {
		t.Fatal("unreadable thread usage was accepted")
	}
	if info, err := os.Stat(usagePath); err != nil || !info.IsDir() {
		t.Fatalf("unreadable thread usage path was moved: %v", err)
	}
}

func TestServerStartsWhenPendingRollbackRepositoryIsMissing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	repositoryPath := filepath.Join(root, "repository")
	if err := os.Mkdir(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "init")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "add", "README.md")
	serverGit(t, repositoryPath, "-c", "user.name=Kiwi Code", "-c", "user.email=kiwi-code@example.invalid", "commit", "-m", "Initial commit")

	dataFile := filepath.Join(root, "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	// A crash leaves the thread creation pending; the user then moves the
	// repository before Kiwi Code starts again.
	thread, err := store.AddThreadWithOptions(item.ID, "Pending", project.AddThreadOptions{Worktree: true, CreationPending: true})
	if err != nil {
		t.Fatal(err)
	}
	movedPath := filepath.Join(root, "moved-repository")
	if err := os.Rename(repositoryPath, movedPath); err != nil {
		t.Fatal(err)
	}

	if _, err := newIsolatedServerHandler(t, store); err != nil {
		t.Fatalf("server did not start with a missing rollback repository: %v", err)
	}
	_, persisted, err := store.GetThreadPersisted(item.ID, thread.ID)
	if err != nil || !persisted.RollbackPending || !persisted.RollbackCleanupReady {
		t.Fatalf("rollback was not kept for retry: %#v, %v", persisted, err)
	}

	if err := os.Rename(movedPath, repositoryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := project.NewStore(dataFile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetThreadPersisted(item.ID, thread.ID); err != project.ErrThreadNotFound {
		t.Fatalf("retried rollback lookup error = %v, want ErrThreadNotFound", err)
	}
	if _, err := os.Stat(thread.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("retried rollback left the worktree: %v", err)
	}
}
