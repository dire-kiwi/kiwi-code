package server

import (
	"errors"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestWithTerminalThreadMutationCleansUpAfterBothLocksRelease(t *testing.T) {
	actionErr := errors.New("action failed")
	dataDirectory := t.TempDir()
	handler := &terminalHandler{
		terminalMutations: newTerminalMutationManager(dataDirectory),
	}
	item := project.Project{ID: "project"}
	thread := project.Thread{ID: "thread"}

	cleanupCalled := false
	result, err := withTerminalThreadMutation(handler, item, thread, func() (string, error) {
		if !handler.sessionMu.TryLock() {
			t.Fatal("action ran while holding the session lock")
		}
		handler.sessionMu.Unlock()
		if lease, ok, tryErr := handler.terminalMutations.TryLockThread(item.ID, thread.ID); tryErr != nil {
			t.Fatalf("probe mutation lease: %v", tryErr)
		} else if ok {
			_ = lease.Release()
			t.Fatal("action ran without the mutation lease")
		}
		return "partial", actionErr
	}, func(partial string) {
		cleanupCalled = true
		if partial != "partial" {
			t.Errorf("cleanup value = %q, want partial", partial)
		}
		if !handler.sessionMu.TryLock() {
			t.Error("cleanup ran before the session lock was released")
		} else {
			handler.sessionMu.Unlock()
		}
		lease, lockErr := newTerminalMutationManager(dataDirectory).LockThread(item.ID, thread.ID)
		if lockErr != nil {
			t.Errorf("cleanup could not reacquire mutation lease: %v", lockErr)
			return
		}
		if releaseErr := lease.Release(); releaseErr != nil {
			t.Errorf("release cleanup mutation lease: %v", releaseErr)
		}
	})
	if !errors.Is(err, actionErr) {
		t.Fatalf("mutation error = %v, want %v", err, actionErr)
	}
	if result != "" {
		t.Fatalf("failed mutation result = %q, want zero value", result)
	}
	if !cleanupCalled {
		t.Fatal("failed mutation did not run cleanup")
	}
}

func TestWithTerminalThreadMutationReturnsSuccessfulResultWithoutCleanup(t *testing.T) {
	handler := &terminalHandler{
		terminalMutations: newTerminalMutationManager(t.TempDir()),
	}
	cleanupCalled := false
	result, err := withTerminalThreadMutation(
		handler,
		project.Project{ID: "project"},
		project.Thread{ID: "thread"},
		func() (int, error) { return 42, nil },
		func(int) { cleanupCalled = true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Fatalf("mutation result = %d, want 42", result)
	}
	if cleanupCalled {
		t.Fatal("successful mutation ran error cleanup")
	}
}
