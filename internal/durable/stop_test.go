package durable

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestTerminalStopMarkerIsDurableExactAndPathSafe(t *testing.T) {
	dataDirectory := t.TempDir()
	first := NewStopManager(dataDirectory)
	projectID := "../project / ☃"
	threadID := "thread/../../two"
	wantSessions := []string{"kiwi-code-exact-terminal", "kiwi-code-exact-tools"}

	lease, err := first.BeginThread(projectID, threadID, []string{
		wantSessions[1],
		wantSessions[0],
		wantSessions[1],
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Adopted() {
		t.Fatal("new marker was reported as adopted")
	}
	marker := lease.Marker()
	if !reflect.DeepEqual(marker.SessionNames, wantSessions) {
		t.Fatalf("session names = %#v, want exact sorted list %#v", marker.SessionNames, wantSessions)
	}
	if relative, err := filepath.Rel(first.root, lease.path); err != nil || relative == ".." || filepath.IsAbs(relative) {
		t.Fatalf("marker escaped root: relative=%q err=%v", relative, err)
	}
	if filepath.Base(lease.path) == threadID+".json" {
		t.Fatalf("raw unsafe thread ID was used as marker path: %q", lease.path)
	}

	second := NewStopManager(dataDirectory)
	stopped, err := second.ThreadStopped(projectID, threadID)
	if err != nil || !stopped {
		t.Fatalf("independent manager stop state: stopped=%t err=%v", stopped, err)
	}
	stored, found, err := second.ReadThread(projectID, threadID)
	if err != nil || !found {
		t.Fatalf("read persisted marker: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(stored.SessionNames, wantSessions) {
		t.Fatalf("persisted session names = %#v, want %#v", stored.SessionNames, wantSessions)
	}

	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}
	third := NewStopManager(dataDirectory)
	recovery, err := third.BeginThread(projectID, threadID, []string{"ignored-on-adoption"})
	if err != nil {
		t.Fatalf("adopt retained marker: %v", err)
	}
	if !recovery.Adopted() {
		t.Fatal("retained marker was not adopted")
	}
	if !reflect.DeepEqual(recovery.Marker().SessionNames, wantSessions) {
		t.Fatalf("adoption replaced exact cleanup recipe: %#v", recovery.Marker().SessionNames)
	}
	if err := recovery.Retain(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopActiveLeaseCannotBeAdoptedAndRollbackReopens(t *testing.T) {
	dataDirectory := t.TempDir()
	first := NewStopManager(dataDirectory)
	second := NewStopManager(dataDirectory)

	lease, err := first.BeginThread("project", "thread", []string{"exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	lockInfo, err := os.Stat(lease.lockPath)
	if err != nil {
		t.Fatalf("stat persistent sidecar lock: %v", err)
	}
	if got := lockInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("sidecar lock permissions = %o, want 600", got)
	}
	if _, err := second.BeginThread("project", "thread", []string{"other-session"}); !errors.Is(err, ErrStopping) {
		t.Fatalf("active marker begin error = %v, want terminal stopping", err)
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
	retainedLockInfo, err := os.Stat(lease.lockPath)
	if err != nil || !os.SameFile(lockInfo, retainedLockInfo) {
		t.Fatalf("rollback replaced or removed persistent sidecar: same=%t err=%v", err == nil && os.SameFile(lockInfo, retainedLockInfo), err)
	}
	stopped, err := second.ThreadStopped("project", "thread")
	if err != nil || stopped {
		t.Fatalf("rollback stop state: stopped=%t err=%v", stopped, err)
	}
	replacement, err := second.BeginThread("project", "thread", []string{"replacement-session"})
	if err != nil {
		t.Fatalf("begin after rollback: %v", err)
	}
	if replacement.Adopted() {
		t.Fatal("marker created after rollback was reported as adopted")
	}
	reusedLockInfo, err := os.Stat(replacement.lockPath)
	if err != nil || !os.SameFile(lockInfo, reusedLockInfo) {
		t.Fatalf("replacement did not reuse persistent sidecar: same=%t err=%v", err == nil && os.SameFile(lockInfo, reusedLockInfo), err)
	}
	if err := replacement.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopRollbackComparesOwnershipToken(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	lease, err := manager.BeginProject("project", nil, []string{"exact-session"})
	if err != nil {
		t.Fatal(err)
	}

	changed := lease.Marker()
	changed.Token = "00000000000000000000000000000000"
	contents, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(lease.path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Rollback(); !errors.Is(err, errStopLeaseOwnership) {
		t.Fatalf("rollback error = %v, want ownership error", err)
	}
	if _, err := os.Stat(lease.path); err != nil {
		t.Fatalf("mismatched marker was removed: %v", err)
	}
	if err := lease.Retain(); !errors.Is(err, errStopLeaseClosed) {
		t.Fatalf("ownership failure did not release lease: %v", err)
	}
	recovery, err := manager.BeginProject("project", nil, []string{"ignored"})
	if err != nil {
		t.Fatalf("adopt marker after ownership failure: %v", err)
	}
	if !recovery.Adopted() {
		t.Fatal("ownership failure leaked the active marker flock")
	}
	if err := recovery.Retain(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopRollbackComparesSidecarInode(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	lease, err := manager.BeginThread("project", "thread", []string{"exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lease.lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Rollback(); !errors.Is(err, errStopLeaseOwnership) {
		t.Fatalf("rollback with replaced sidecar error = %v, want ownership error", err)
	}
	if _, err := os.Stat(lease.path); err != nil {
		t.Fatalf("sidecar mismatch removed marker: %v", err)
	}
	recovered, found, err := manager.AcquireExisting(StopMarkerRef{
		Scope:     StopScopeThread,
		ProjectID: "project",
		ThreadID:  "thread",
	})
	if err != nil || !found || recovered == nil {
		t.Fatalf("acquire after sidecar ownership failure: found=%t err=%v", found, err)
	}
	if err := recovered.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopLeaseUpdatesExactCleanupRecipe(t *testing.T) {
	dataDirectory := t.TempDir()
	first := NewStopManager(dataDirectory)
	lease, err := first.BeginProject("project", nil, []string{"old-exact"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a-exact", "z-exact"}
	if err := lease.UpdateSessionNames([]string{want[1], want[0], want[1]}); err != nil {
		t.Fatal(err)
	}

	second := NewStopManager(dataDirectory)
	stored, found, err := second.ReadProject("project")
	if err != nil || !found {
		t.Fatalf("read updated marker: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(stored.SessionNames, want) {
		t.Fatalf("updated session names = %#v, want %#v", stored.SessionNames, want)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	recovery, err := second.BeginProject("project", nil, []string{"ignored-on-adoption"})
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.Adopted() {
		t.Fatal("updated marker was not adopted")
	}
	finalWant := []string{"a-exact", "replacement-exact"}
	if err := recovery.UpdateSessionNames([]string{finalWant[1], finalWant[0]}); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Retain(); err != nil {
		t.Fatal(err)
	}
	final, found, err := first.ReadProject("project")
	if err != nil || !found {
		t.Fatalf("read adopted update: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(final.SessionNames, finalWant) {
		t.Fatalf("adopted update session names = %#v, want %#v", final.SessionNames, finalWant)
	}
}

func TestTerminalStopCommittedMarkerIsDurableAndImmutable(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	lease, err := manager.BeginProject("project", []string{"thread"}, []string{"a-exact", "z-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := lease.UpdateSessionNames([]string{"replacement"}); err == nil {
		t.Fatal("committed cleanup recipe was mutable")
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	recovered, found, err := manager.AcquireExisting(StopMarkerRef{
		Scope: StopScopeProject, ProjectID: "project",
	})
	if err != nil || !found || recovered == nil {
		t.Fatalf("recover committed marker: found=%t err=%v", found, err)
	}
	marker := recovered.Marker()
	if !marker.Committed || !reflect.DeepEqual(marker.SessionNames, []string{"a-exact", "z-exact"}) {
		t.Fatalf("recovered committed marker = %#v", marker)
	}
	if err := recovered.Rollback(); err == nil {
		t.Fatal("committed marker was rolled back")
	}
	stored, found, err := manager.ReadProject("project")
	if err != nil || !found || !stored.Committed {
		t.Fatalf("committed marker after rejected rollback: found=%t marker=%#v err=%v", found, stored, err)
	}
}

func TestTerminalStopProjectRechecksThreadsAfterStoreRefresh(t *testing.T) {
	dataDirectory := t.TempDir()
	threadManager := NewStopManager(dataDirectory)
	projectManager := NewStopManager(dataDirectory)

	// Simulate a project DELETE whose original Store snapshot did not contain a
	// concurrently-added thread that had already started its own DELETE.
	threadLease, err := threadManager.BeginThread("project", "late-thread", []string{"thread-exact"})
	if err != nil {
		t.Fatal(err)
	}
	projectLease, err := projectManager.BeginProject("project", nil, []string{"stale-project-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := projectLease.RecheckProjectThreads([]string{"late-thread"}); !errors.Is(err, ErrStopping) {
		t.Fatalf("refreshed project recheck error = %v, want terminal stopping", err)
	}
	if _, found, err := projectManager.ReadProject("project"); err != nil || found {
		t.Fatalf("project marker survived refreshed thread conflict: found=%t err=%v", found, err)
	}
	if stopped, err := projectManager.ThreadStopped("project", "late-thread"); err != nil || !stopped {
		t.Fatalf("winning late thread marker was lost: stopped=%t err=%v", stopped, err)
	}
	if err := threadLease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopProjectAndThreadOrdering(t *testing.T) {
	t.Run("project blocks thread", func(t *testing.T) {
		dataDirectory := t.TempDir()
		projectManager := NewStopManager(dataDirectory)
		threadManager := NewStopManager(dataDirectory)
		projectLease, err := projectManager.BeginProject(
			"project",
			[]string{"thread"},
			[]string{"project-exact"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := threadManager.BeginThread("project", "thread", []string{"thread-exact"}); !errors.Is(err, ErrStopping) {
			t.Fatalf("thread begin error = %v, want terminal stopping", err)
		}
		if _, found, err := threadManager.ReadThread("project", "thread"); err != nil || found {
			t.Fatalf("thread marker survived project conflict: found=%t err=%v", found, err)
		}
		if err := projectLease.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("thread blocks project", func(t *testing.T) {
		dataDirectory := t.TempDir()
		threadManager := NewStopManager(dataDirectory)
		projectManager := NewStopManager(dataDirectory)
		threadLease, err := threadManager.BeginThread("project", "thread", []string{"thread-exact"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projectManager.BeginProject(
			"project",
			[]string{"thread"},
			[]string{"project-exact"},
		); !errors.Is(err, ErrStopping) {
			t.Fatalf("project begin error = %v, want terminal stopping", err)
		}
		if _, found, err := projectManager.ReadProject("project"); err != nil || found {
			t.Fatalf("project marker survived thread conflict: found=%t err=%v", found, err)
		}
		if stopped, err := projectManager.ThreadStopped("project", "thread"); err != nil || !stopped {
			t.Fatalf("winning thread marker was lost: stopped=%t err=%v", stopped, err)
		}
		if err := threadLease.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTerminalStopConcurrentProjectThreadBeginNeverBothWin(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		dataDirectory := t.TempDir()
		projectManager := NewStopManager(dataDirectory)
		threadManager := NewStopManager(dataDirectory)
		start := make(chan struct{})
		type result struct {
			lease *StopLease
			err   error
		}
		projectResult := make(chan result, 1)
		threadResult := make(chan result, 1)
		var ready sync.WaitGroup
		ready.Add(2)
		go func() {
			ready.Done()
			<-start
			lease, err := projectManager.BeginProject("project", []string{"thread"}, []string{"project-exact"})
			projectResult <- result{lease: lease, err: err}
		}()
		go func() {
			ready.Done()
			<-start
			lease, err := threadManager.BeginThread("project", "thread", []string{"thread-exact"})
			threadResult <- result{lease: lease, err: err}
		}()
		ready.Wait()
		close(start)
		project := <-projectResult
		thread := <-threadResult
		if project.lease != nil && thread.lease != nil {
			t.Fatalf("attempt %d: project and thread markers both won", attempt)
		}
		if project.lease == nil && project.err == nil {
			t.Fatalf("attempt %d: project returned neither lease nor error", attempt)
		}
		if thread.lease == nil && thread.err == nil {
			t.Fatalf("attempt %d: thread returned neither lease nor error", attempt)
		}
		if project.lease != nil {
			if err := project.lease.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		if thread.lease != nil {
			if err := thread.lease.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestTerminalStopMalformedAndIOStateFailClosed(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		manager := NewStopManager(t.TempDir())
		path := manager.threadPath("project", "thread")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		stopped, err := manager.ThreadStopped("project", "thread")
		if !stopped || err == nil {
			t.Fatalf("malformed marker state: stopped=%t err=%v", stopped, err)
		}
		if _, err := manager.BeginThread("project", "thread", []string{"exact"}); err == nil {
			t.Fatal("begin adopted a malformed marker")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("malformed marker was removed: %v", statErr)
		}
	})

	t.Run("io", func(t *testing.T) {
		manager := NewStopManager(t.TempDir())
		if err := os.MkdirAll(manager.root, 0o700); err != nil {
			t.Fatal(err)
		}
		// Replacing the expected projects directory with a regular file makes
		// every exact marker lookup fail deterministically with ENOTDIR.
		if err := os.WriteFile(filepath.Join(manager.root, "projects"), []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		stopped, err := manager.ProjectStopped("project")
		if !stopped || err == nil {
			t.Fatalf("I/O marker state: stopped=%t err=%v", stopped, err)
		}
		if _, err := manager.BeginThread("project", "thread", []string{"exact"}); err == nil {
			t.Fatal("begin ignored marker I/O failure")
		}
	})
}

func TestTerminalStopAtomicUpdatesNeverExposePartialJSON(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	lease, err := manager.BeginThread("project", "thread", []string{"initial"})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	readerErrors := make(chan error, 4)
	var readers sync.WaitGroup
	for reader := 0; reader < cap(readerErrors); reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				marker, found, readErr := manager.ReadThread("project", "thread")
				if readErr != nil || !found {
					readerErrors <- fmt.Errorf("read atomic marker: found=%t err=%w", found, readErr)
					return
				}
				if len(marker.SessionNames) == 1 && marker.SessionNames[0] == "initial" {
					continue
				}
				if len(marker.SessionNames) != 2 || marker.SessionNames[0][2:] != marker.SessionNames[1][2:] {
					readerErrors <- fmt.Errorf("observed mixed cleanup recipe: %#v", marker.SessionNames)
					return
				}
			}
		}()
	}

	for update := 0; update < 40; update++ {
		suffix := fmt.Sprintf("%03d", update)
		if err := lease.UpdateSessionNames([]string{"z-" + suffix, "a-" + suffix}); err != nil {
			close(done)
			readers.Wait()
			t.Fatal(err)
		}
	}
	close(done)
	readers.Wait()
	close(readerErrors)
	for readErr := range readerErrors {
		t.Error(readErr)
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopAtomicWriteFailurePreservesPriorRecipe(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	lease, err := manager.BeginProject("project", nil, []string{"old-exact"})
	if err != nil {
		t.Fatal(err)
	}
	updated := lease.Marker()
	updated.SessionNames = []string{"new-exact"}
	injected := errors.New("injected rename failure")
	err = writeStopMarkerAtomicWithRename(lease.path, updated, func(string, string) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("atomic write error = %v, want injected failure", err)
	}
	stored, found, err := manager.ReadProject("project")
	if err != nil || !found {
		t.Fatalf("read prior recipe: found=%t err=%v", found, err)
	}
	if want := []string{"old-exact"}; !reflect.DeepEqual(stored.SessionNames, want) {
		t.Fatalf("recipe after failed replacement = %#v, want %#v", stored.SessionNames, want)
	}
	entries, err := os.ReadDir(filepath.Dir(lease.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isStopTemporaryName(entry.Name()) {
			t.Fatalf("failed atomic write leaked temporary marker %q", entry.Name())
		}
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopListMarkersUsesExactSafeLayout(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	projectRef := StopMarkerRef{Scope: StopScopeProject, ProjectID: "../project / ☃"}
	threadRef := StopMarkerRef{Scope: StopScopeThread, ProjectID: "thread-project", ThreadID: "../thread/../../two"}
	writeTestTerminalStopMarker(t, manager, projectRef, []string{"project-exact"})
	writeTestTerminalStopMarker(t, manager, threadRef, []string{"thread-exact"})

	malformedDirectory := filepath.Join(manager.root, "projects", "not!base64")
	if err := os.MkdirAll(malformedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedProjectID := "linked-project"
	linkedMarker, err := newStopMarker(StopScopeProject, linkedProjectID, "", []string{"must-not-follow"})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := writeStopMarkerAtomic(filepath.Join(outside, "project.json"), linkedMarker); err != nil {
		t.Fatal(err)
	}
	linkedDirectory := filepath.Join(manager.root, "projects", stopPathComponent(linkedProjectID))
	if err := os.Symlink(outside, linkedDirectory); err != nil {
		t.Fatal(err)
	}
	refs, err := manager.ListMarkers()
	if !errors.Is(err, errStopMarkerMalformed) {
		t.Fatalf("list malformed layout error = %v, want malformed marker", err)
	}
	want := []StopMarkerRef{projectRef, threadRef}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("listed marker refs = %#v, want %#v", refs, want)
	}
	for _, ref := range refs {
		path, pathErr := manager.MarkerPath(ref)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		relative, pathErr := filepath.Rel(manager.root, path)
		if pathErr != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("listed marker escaped root: ref=%#v relative=%q err=%v", ref, relative, pathErr)
		}
	}
}

func TestTerminalStopAcquireExistingStates(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	ref := StopMarkerRef{Scope: StopScopeThread, ProjectID: "project", ThreadID: "thread"}

	if lease, found, err := manager.AcquireExisting(ref); err != nil || found || lease != nil {
		t.Fatalf("absent marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	active, err := manager.BeginThread(ref.ProjectID, ref.ThreadID, []string{"active-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if lease, found, err := manager.AcquireExisting(ref); lease != nil || !found || !errors.Is(err, ErrStopping) {
		t.Fatalf("active marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	if err := active.Retain(); err != nil {
		t.Fatal(err)
	}

	recovered, found, err := manager.AcquireExisting(ref)
	if err != nil || !found || recovered == nil {
		t.Fatalf("retained marker acquisition: lease=%v found=%t err=%v", recovered, found, err)
	}
	if !recovered.Adopted() || !reflect.DeepEqual(recovered.Marker().SessionNames, []string{"active-exact"}) {
		t.Fatalf("recovered marker = %#v adopted=%t", recovered.Marker(), recovered.Adopted())
	}
	if err := recovered.Rollback(); err != nil {
		t.Fatal(err)
	}

	malformedPath := manager.threadPath("project", "malformed")
	if err := secureStopDirectory(filepath.Dir(malformedPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformedPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	malformedRef := StopMarkerRef{Scope: StopScopeThread, ProjectID: "project", ThreadID: "malformed"}
	if lease, found, err := manager.AcquireExisting(malformedRef); lease != nil || !found || !errors.Is(err, errStopMarkerMalformed) {
		t.Fatalf("malformed marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	refs, err := manager.ListMarkers()
	if !errors.Is(err, errStopMarkerMalformed) {
		t.Fatalf("list malformed marker error = %v, want malformed marker", err)
	}
	if !reflect.DeepEqual(refs, []StopMarkerRef{malformedRef}) {
		t.Fatalf("malformed marker path refs = %#v, want %#v", refs, []StopMarkerRef{malformedRef})
	}
}

func TestTerminalStopAcquireExistingResolvesDualMarkerCrashState(t *testing.T) {
	manager := NewStopManager(t.TempDir())
	projectRef := StopMarkerRef{Scope: StopScopeProject, ProjectID: "project"}
	threadRef := StopMarkerRef{Scope: StopScopeThread, ProjectID: "project", ThreadID: "thread"}
	writeTestTerminalStopMarker(t, manager, projectRef, []string{"project-exact"})
	writeTestTerminalStopMarker(t, manager, threadRef, []string{"thread-exact"})

	projectLease, found, err := manager.AcquireExisting(projectRef)
	if err != nil || !found || projectLease == nil {
		t.Fatalf("acquire project crash marker: found=%t err=%v", found, err)
	}
	threadLease, found, err := manager.AcquireExisting(threadRef)
	if err != nil || !found || threadLease == nil {
		_ = projectLease.Retain()
		t.Fatalf("acquire thread crash marker independently: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(projectLease.Marker().SessionNames, []string{"project-exact"}) {
		t.Fatalf("project crash recipe = %#v", projectLease.Marker().SessionNames)
	}
	if !reflect.DeepEqual(threadLease.Marker().SessionNames, []string{"thread-exact"}) {
		t.Fatalf("thread crash recipe = %#v", threadLease.Marker().SessionNames)
	}
	if err := projectLease.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := threadLease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func writeTestTerminalStopMarker(
	t *testing.T,
	manager *StopManager,
	ref StopMarkerRef,
	sessionNames []string,
) StopMarker {
	t.Helper()
	marker, err := newStopMarker(ref.Scope, ref.ProjectID, ref.ThreadID, sessionNames)
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.MarkerPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureStopDirectory(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := writeStopMarkerAtomic(path, marker); err != nil {
		t.Fatal(err)
	}
	return marker
}
