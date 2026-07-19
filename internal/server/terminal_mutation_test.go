package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalMutationLockSerializesSameThreadAcrossManagers(t *testing.T) {
	dataDirectory := t.TempDir()
	firstManager := newTerminalMutationManager(dataDirectory)
	secondManager := newTerminalMutationManager(dataDirectory)

	firstLease, err := firstManager.LockThread("project", "thread")
	if err != nil {
		t.Fatalf("lock first mutation lease: %v", err)
	}

	attempted := make(chan struct{})
	acquired := make(chan *terminalMutationLease, 1)
	errs := make(chan error, 1)
	go func() {
		close(attempted)
		lease, lockErr := secondManager.LockThread("project", "thread")
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- lease
	}()
	<-attempted

	select {
	case lease := <-acquired:
		_ = lease.Release()
		t.Fatal("second manager acquired the same thread before the first released it")
	case lockErr := <-errs:
		t.Fatalf("second manager failed while waiting: %v", lockErr)
	case <-time.After(75 * time.Millisecond):
	}

	if err := firstLease.Release(); err != nil {
		t.Fatalf("release first mutation lease: %v", err)
	}

	select {
	case secondLease := <-acquired:
		if err := secondLease.Release(); err != nil {
			t.Fatalf("release second mutation lease: %v", err)
		}
	case lockErr := <-errs:
		t.Fatalf("second manager failed after release: %v", lockErr)
	case <-time.After(2 * time.Second):
		t.Fatal("second manager did not acquire the same thread after release")
	}
}

func TestTerminalMutationLockAllowsDifferentThreads(t *testing.T) {
	dataDirectory := t.TempDir()
	firstManager := newTerminalMutationManager(dataDirectory)
	secondManager := newTerminalMutationManager(dataDirectory)

	firstLease, err := firstManager.LockThread("project", "thread-one")
	if err != nil {
		t.Fatalf("lock first thread: %v", err)
	}
	defer func() {
		if err := firstLease.Release(); err != nil {
			t.Errorf("release first thread: %v", err)
		}
	}()

	type lockResult struct {
		lease *terminalMutationLease
		err   error
	}
	result := make(chan lockResult, 1)
	go func() {
		lease, lockErr := secondManager.LockThread("project", "thread-two")
		result <- lockResult{lease: lease, err: lockErr}
	}()

	select {
	case outcome := <-result:
		if outcome.err != nil {
			t.Fatalf("lock different thread: %v", outcome.err)
		}
		if err := outcome.lease.Release(); err != nil {
			t.Fatalf("release different thread: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a lock for one thread blocked a different thread")
	}
}

func TestTerminalMutationLockUsesSafePersistentPath(t *testing.T) {
	manager := newTerminalMutationManager(t.TempDir())
	projectID := "../../project/with/slashes"
	threadID := "../thread/with/slashes"

	lease, err := manager.LockThread(projectID, threadID)
	if err != nil {
		t.Fatalf("lock mutation lease: %v", err)
	}
	path := manager.threadPath(projectID, threadID)
	if err := lease.Release(); err != nil {
		t.Fatalf("release mutation lease: %v", err)
	}

	relativePath, err := filepath.Rel(manager.root, path)
	if err != nil {
		t.Fatalf("make mutation path relative: %v", err)
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		t.Fatalf("mutation path escaped manager root: %q", relativePath)
	}
	if strings.Contains(path, projectID) || strings.Contains(path, threadID) {
		t.Fatalf("mutation path contains an unsafe raw identity: %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent mutation lock missing after release: %v", err)
	}

	secondLease, err := manager.LockThread(projectID, threadID)
	if err != nil {
		t.Fatalf("reuse persistent mutation lock: %v", err)
	}
	if err := secondLease.Release(); err != nil {
		t.Fatalf("release reused mutation lock: %v", err)
	}
}

func TestTerminalMutationLockValidatesIdentities(t *testing.T) {
	manager := newTerminalMutationManager(t.TempDir())

	if _, err := manager.LockThread("", "thread"); err == nil {
		t.Fatal("expected an empty project ID to be rejected")
	}
	if _, err := manager.LockThread("project", ""); err == nil {
		t.Fatal("expected an empty thread ID to be rejected")
	}
}

func TestTerminalMutationLockPermissionsAndDoubleRelease(t *testing.T) {
	manager := newTerminalMutationManager(t.TempDir())
	path := manager.threadPath("project", "thread")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create permissive mutation directory: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create permissive mutation file: %v", err)
	}

	lease, err := manager.LockThread("project", "thread")
	if err != nil {
		t.Fatalf("lock mutation lease: %v", err)
	}

	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		info, statErr := os.Stat(directory)
		if statErr != nil {
			t.Fatalf("stat mutation directory %q: %v", directory, statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("mutation directory %q permissions = %o, want 700", directory, got)
		}
		if directory == manager.root {
			break
		}
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat mutation lock: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("mutation lock permissions = %o, want 600", got)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("release mutation lease: %v", err)
	}
	if err := lease.Release(); !errors.Is(err, errTerminalMutationLeaseReleased) {
		t.Fatalf("second release error = %v, want %v", err, errTerminalMutationLeaseReleased)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mutation lock was removed on release: %v", err)
	}
}
