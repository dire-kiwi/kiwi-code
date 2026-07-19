package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ivan/dire-mux/internal/project"
)

func TestTerminalStopMarkerIsDurableExactAndPathSafe(t *testing.T) {
	dataDirectory := t.TempDir()
	first := newTerminalStopManager(dataDirectory)
	projectID := "../project / ☃"
	threadID := "thread/../../two"
	wantSessions := []string{"dire-mux-exact-terminal", "dire-mux-exact-tools"}

	lease, err := first.beginThread(projectID, threadID, []string{
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

	second := newTerminalStopManager(dataDirectory)
	stopped, err := second.threadStopped(projectID, threadID)
	if err != nil || !stopped {
		t.Fatalf("independent manager stop state: stopped=%t err=%v", stopped, err)
	}
	stored, found, err := second.readThread(projectID, threadID)
	if err != nil || !found {
		t.Fatalf("read persisted marker: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(stored.SessionNames, wantSessions) {
		t.Fatalf("persisted session names = %#v, want %#v", stored.SessionNames, wantSessions)
	}

	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}
	third := newTerminalStopManager(dataDirectory)
	recovery, err := third.beginThread(projectID, threadID, []string{"ignored-on-adoption"})
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
	first := newTerminalStopManager(dataDirectory)
	second := newTerminalStopManager(dataDirectory)

	lease, err := first.beginThread("project", "thread", []string{"exact-session"})
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
	if _, err := second.beginThread("project", "thread", []string{"other-session"}); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("active marker begin error = %v, want terminal stopping", err)
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
	retainedLockInfo, err := os.Stat(lease.lockPath)
	if err != nil || !os.SameFile(lockInfo, retainedLockInfo) {
		t.Fatalf("rollback replaced or removed persistent sidecar: same=%t err=%v", err == nil && os.SameFile(lockInfo, retainedLockInfo), err)
	}
	stopped, err := second.threadStopped("project", "thread")
	if err != nil || stopped {
		t.Fatalf("rollback stop state: stopped=%t err=%v", stopped, err)
	}
	replacement, err := second.beginThread("project", "thread", []string{"replacement-session"})
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
	manager := newTerminalStopManager(t.TempDir())
	lease, err := manager.beginProject("project", nil, []string{"exact-session"})
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
	if err := lease.Rollback(); !errors.Is(err, errTerminalStopLeaseOwnership) {
		t.Fatalf("rollback error = %v, want ownership error", err)
	}
	if _, err := os.Stat(lease.path); err != nil {
		t.Fatalf("mismatched marker was removed: %v", err)
	}
	if err := lease.Retain(); !errors.Is(err, errTerminalStopLeaseClosed) {
		t.Fatalf("ownership failure did not release lease: %v", err)
	}
	recovery, err := manager.beginProject("project", nil, []string{"ignored"})
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
	manager := newTerminalStopManager(t.TempDir())
	lease, err := manager.beginThread("project", "thread", []string{"exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lease.lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Rollback(); !errors.Is(err, errTerminalStopLeaseOwnership) {
		t.Fatalf("rollback with replaced sidecar error = %v, want ownership error", err)
	}
	if _, err := os.Stat(lease.path); err != nil {
		t.Fatalf("sidecar mismatch removed marker: %v", err)
	}
	recovered, found, err := manager.acquireExisting(terminalStopMarkerRef{
		Scope:     terminalStopScopeThread,
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
	first := newTerminalStopManager(dataDirectory)
	lease, err := first.beginProject("project", nil, []string{"old-exact"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a-exact", "z-exact"}
	if err := lease.UpdateSessionNames([]string{want[1], want[0], want[1]}); err != nil {
		t.Fatal(err)
	}

	second := newTerminalStopManager(dataDirectory)
	stored, found, err := second.readProject("project")
	if err != nil || !found {
		t.Fatalf("read updated marker: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(stored.SessionNames, want) {
		t.Fatalf("updated session names = %#v, want %#v", stored.SessionNames, want)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	recovery, err := second.beginProject("project", nil, []string{"ignored-on-adoption"})
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
	final, found, err := first.readProject("project")
	if err != nil || !found {
		t.Fatalf("read adopted update: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(final.SessionNames, finalWant) {
		t.Fatalf("adopted update session names = %#v, want %#v", final.SessionNames, finalWant)
	}
}

func TestTerminalStopCommittedMarkerIsDurableAndImmutable(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	lease, err := manager.beginProject("project", []string{"thread"}, []string{"a-exact", "z-exact"})
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

	recovered, found, err := manager.acquireExisting(terminalStopMarkerRef{
		Scope: terminalStopScopeProject, ProjectID: "project",
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
	stored, found, err := manager.readProject("project")
	if err != nil || !found || !stored.Committed {
		t.Fatalf("committed marker after rejected rollback: found=%t marker=%#v err=%v", found, stored, err)
	}
}

func TestTerminalStopProjectRechecksThreadsAfterStoreRefresh(t *testing.T) {
	dataDirectory := t.TempDir()
	threadManager := newTerminalStopManager(dataDirectory)
	projectManager := newTerminalStopManager(dataDirectory)

	// Simulate a project DELETE whose original Store snapshot did not contain a
	// concurrently-added thread that had already started its own DELETE.
	threadLease, err := threadManager.beginThread("project", "late-thread", []string{"thread-exact"})
	if err != nil {
		t.Fatal(err)
	}
	projectLease, err := projectManager.beginProject("project", nil, []string{"stale-project-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := projectLease.RecheckProjectThreads([]string{"late-thread"}); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("refreshed project recheck error = %v, want terminal stopping", err)
	}
	if _, found, err := projectManager.readProject("project"); err != nil || found {
		t.Fatalf("project marker survived refreshed thread conflict: found=%t err=%v", found, err)
	}
	if stopped, err := projectManager.threadStopped("project", "late-thread"); err != nil || !stopped {
		t.Fatalf("winning late thread marker was lost: stopped=%t err=%v", stopped, err)
	}
	if err := threadLease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopProjectAndThreadOrdering(t *testing.T) {
	t.Run("project blocks thread", func(t *testing.T) {
		dataDirectory := t.TempDir()
		projectManager := newTerminalStopManager(dataDirectory)
		threadManager := newTerminalStopManager(dataDirectory)
		projectLease, err := projectManager.beginProject(
			"project",
			[]string{"thread"},
			[]string{"project-exact"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := threadManager.beginThread("project", "thread", []string{"thread-exact"}); !errors.Is(err, errTerminalStopping) {
			t.Fatalf("thread begin error = %v, want terminal stopping", err)
		}
		if _, found, err := threadManager.readThread("project", "thread"); err != nil || found {
			t.Fatalf("thread marker survived project conflict: found=%t err=%v", found, err)
		}
		if err := projectLease.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("thread blocks project", func(t *testing.T) {
		dataDirectory := t.TempDir()
		threadManager := newTerminalStopManager(dataDirectory)
		projectManager := newTerminalStopManager(dataDirectory)
		threadLease, err := threadManager.beginThread("project", "thread", []string{"thread-exact"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projectManager.beginProject(
			"project",
			[]string{"thread"},
			[]string{"project-exact"},
		); !errors.Is(err, errTerminalStopping) {
			t.Fatalf("project begin error = %v, want terminal stopping", err)
		}
		if _, found, err := projectManager.readProject("project"); err != nil || found {
			t.Fatalf("project marker survived thread conflict: found=%t err=%v", found, err)
		}
		if stopped, err := projectManager.threadStopped("project", "thread"); err != nil || !stopped {
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
		projectManager := newTerminalStopManager(dataDirectory)
		threadManager := newTerminalStopManager(dataDirectory)
		start := make(chan struct{})
		type result struct {
			lease *terminalStopLease
			err   error
		}
		projectResult := make(chan result, 1)
		threadResult := make(chan result, 1)
		var ready sync.WaitGroup
		ready.Add(2)
		go func() {
			ready.Done()
			<-start
			lease, err := projectManager.beginProject("project", []string{"thread"}, []string{"project-exact"})
			projectResult <- result{lease: lease, err: err}
		}()
		go func() {
			ready.Done()
			<-start
			lease, err := threadManager.beginThread("project", "thread", []string{"thread-exact"})
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
		manager := newTerminalStopManager(t.TempDir())
		path := manager.threadPath("project", "thread")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		stopped, err := manager.threadStopped("project", "thread")
		if !stopped || err == nil {
			t.Fatalf("malformed marker state: stopped=%t err=%v", stopped, err)
		}
		if _, err := manager.beginThread("project", "thread", []string{"exact"}); err == nil {
			t.Fatal("begin adopted a malformed marker")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("malformed marker was removed: %v", statErr)
		}
	})

	t.Run("io", func(t *testing.T) {
		manager := newTerminalStopManager(t.TempDir())
		if err := os.MkdirAll(manager.root, 0o700); err != nil {
			t.Fatal(err)
		}
		// Replacing the expected projects directory with a regular file makes
		// every exact marker lookup fail deterministically with ENOTDIR.
		if err := os.WriteFile(filepath.Join(manager.root, "projects"), []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		stopped, err := manager.projectStopped("project")
		if !stopped || err == nil {
			t.Fatalf("I/O marker state: stopped=%t err=%v", stopped, err)
		}
		if _, err := manager.beginThread("project", "thread", []string{"exact"}); err == nil {
			t.Fatal("begin ignored marker I/O failure")
		}
	})
}

func TestTerminalStopAtomicUpdatesNeverExposePartialJSON(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	lease, err := manager.beginThread("project", "thread", []string{"initial"})
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
				marker, found, readErr := manager.readThread("project", "thread")
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
	manager := newTerminalStopManager(t.TempDir())
	lease, err := manager.beginProject("project", nil, []string{"old-exact"})
	if err != nil {
		t.Fatal(err)
	}
	updated := lease.Marker()
	updated.SessionNames = []string{"new-exact"}
	injected := errors.New("injected rename failure")
	err = writeTerminalStopMarkerAtomicWithRename(lease.path, updated, func(string, string) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("atomic write error = %v, want injected failure", err)
	}
	stored, found, err := manager.readProject("project")
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
		if isTerminalStopTemporaryName(entry.Name()) {
			t.Fatalf("failed atomic write leaked temporary marker %q", entry.Name())
		}
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStopListMarkersUsesExactSafeLayout(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	projectRef := terminalStopMarkerRef{Scope: terminalStopScopeProject, ProjectID: "../project / ☃"}
	threadRef := terminalStopMarkerRef{Scope: terminalStopScopeThread, ProjectID: "thread-project", ThreadID: "../thread/../../two"}
	writeTestTerminalStopMarker(t, manager, projectRef, []string{"project-exact"})
	writeTestTerminalStopMarker(t, manager, threadRef, []string{"thread-exact"})

	malformedDirectory := filepath.Join(manager.root, "projects", "not!base64")
	if err := os.MkdirAll(malformedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedProjectID := "linked-project"
	linkedMarker, err := newTerminalStopMarker(terminalStopScopeProject, linkedProjectID, "", []string{"must-not-follow"})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := writeTerminalStopMarkerAtomic(filepath.Join(outside, "project.json"), linkedMarker); err != nil {
		t.Fatal(err)
	}
	linkedDirectory := filepath.Join(manager.root, "projects", terminalStopPathComponent(linkedProjectID))
	if err := os.Symlink(outside, linkedDirectory); err != nil {
		t.Fatal(err)
	}
	refs, err := manager.listMarkers()
	if !errors.Is(err, errTerminalStopMarkerMalformed) {
		t.Fatalf("list malformed layout error = %v, want malformed marker", err)
	}
	want := []terminalStopMarkerRef{projectRef, threadRef}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("listed marker refs = %#v, want %#v", refs, want)
	}
	for _, ref := range refs {
		path, pathErr := manager.markerPath(ref)
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
	manager := newTerminalStopManager(t.TempDir())
	ref := terminalStopMarkerRef{Scope: terminalStopScopeThread, ProjectID: "project", ThreadID: "thread"}

	if lease, found, err := manager.acquireExisting(ref); err != nil || found || lease != nil {
		t.Fatalf("absent marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	active, err := manager.beginThread(ref.ProjectID, ref.ThreadID, []string{"active-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if lease, found, err := manager.acquireExisting(ref); lease != nil || !found || !errors.Is(err, errTerminalStopping) {
		t.Fatalf("active marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	if err := active.Retain(); err != nil {
		t.Fatal(err)
	}

	recovered, found, err := manager.acquireExisting(ref)
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
	if err := secureTerminalStopDirectory(filepath.Dir(malformedPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformedPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	malformedRef := terminalStopMarkerRef{Scope: terminalStopScopeThread, ProjectID: "project", ThreadID: "malformed"}
	if lease, found, err := manager.acquireExisting(malformedRef); lease != nil || !found || !errors.Is(err, errTerminalStopMarkerMalformed) {
		t.Fatalf("malformed marker acquisition: lease=%v found=%t err=%v", lease, found, err)
	}
	refs, err := manager.listMarkers()
	if !errors.Is(err, errTerminalStopMarkerMalformed) {
		t.Fatalf("list malformed marker error = %v, want malformed marker", err)
	}
	if !reflect.DeepEqual(refs, []terminalStopMarkerRef{malformedRef}) {
		t.Fatalf("malformed marker path refs = %#v, want %#v", refs, []terminalStopMarkerRef{malformedRef})
	}
}

func TestTerminalStopAcquireExistingResolvesDualMarkerCrashState(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	projectRef := terminalStopMarkerRef{Scope: terminalStopScopeProject, ProjectID: "project"}
	threadRef := terminalStopMarkerRef{Scope: terminalStopScopeThread, ProjectID: "project", ThreadID: "thread"}
	writeTestTerminalStopMarker(t, manager, projectRef, []string{"project-exact"})
	writeTestTerminalStopMarker(t, manager, threadRef, []string{"thread-exact"})

	projectLease, found, err := manager.acquireExisting(projectRef)
	if err != nil || !found || projectLease == nil {
		t.Fatalf("acquire project crash marker: found=%t err=%v", found, err)
	}
	threadLease, found, err := manager.acquireExisting(threadRef)
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
	manager *terminalStopManager,
	ref terminalStopMarkerRef,
	sessionNames []string,
) terminalStopMarker {
	t.Helper()
	marker, err := newTerminalStopMarker(ref.Scope, ref.ProjectID, ref.ThreadID, sessionNames)
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.markerPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureTerminalStopDirectory(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := writeTerminalStopMarkerAtomic(path, marker); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestReconcileTerminalStopsUsesStoreAsCommitOracle(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	manager := newTerminalStopManager(store.DataDirectory())
	handler := &terminalHandler{projects: store, terminalStops: manager}

	t.Run("ambiguous precommit remains fenced", func(t *testing.T) {
		lease, beginErr := manager.beginThread(item.ID, thread.ID, []string{"exact-session"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := lease.Retain(); err != nil {
			t.Fatal(err)
		}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatal(err)
		}
		if marker, found, err := manager.readThread(item.ID, thread.ID); err != nil || !found || marker.Committed {
			t.Fatalf("precommit marker after recovery: found=%t err=%v", found, err)
		}
		recovery, found, err := manager.acquireExisting(terminalStopMarkerRef{
			Scope: terminalStopScopeThread, ProjectID: item.ID, ThreadID: thread.ID,
		})
		if err != nil || !found || recovery == nil {
			t.Fatalf("acquire retained precommit marker: found=%t err=%v", found, err)
		}
		if err := recovery.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("active is skipped", func(t *testing.T) {
		lease, beginErr := manager.beginThread(item.ID, thread.ID, []string{"exact-session"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatalf("active marker should be skipped: %v", err)
		}
		if _, found, err := manager.readThread(item.ID, thread.ID); err != nil || !found {
			t.Fatalf("active marker after recovery: found=%t err=%v", found, err)
		}
		if err := lease.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("committed without tmux defers cleanup", func(t *testing.T) {
		lease, beginErr := manager.beginThread(item.ID, thread.ID, []string{"exact-session"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := store.DeleteThread(item.ID, thread.ID); err != nil {
			t.Fatal(err)
		}
		if err := lease.Retain(); err != nil {
			t.Fatal(err)
		}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatalf("committed recovery without tmux: %v", err)
		}
		if marker, found, err := manager.readThread(item.ID, thread.ID); err != nil || !found || !marker.Committed {
			t.Fatalf("committed marker after unavailable cleanup: found=%t err=%v", found, err)
		}
	})
}

func TestTerminalStopRecoveryUsesPersistedStateAcrossStoreInstances(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "projects.json")
	writer, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := writer.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	stale, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	manager := newTerminalStopManager(writer.DataDirectory())

	t.Run("crash gap is upgraded from durable Store", func(t *testing.T) {
		lease, err := manager.beginThread(item.ID, thread.ID, []string{"thread-exact"})
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.DeleteThread(item.ID, thread.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := stale.GetThread(item.ID, thread.ID); err != nil {
			t.Fatalf("stale Store unexpectedly refreshed: %v", err)
		}
		if err := lease.Retain(); err != nil {
			t.Fatal(err)
		}

		handler := &terminalHandler{projects: stale, terminalStops: manager}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatal(err)
		}
		marker, found, err := manager.readThread(item.ID, thread.ID)
		if err != nil || !found || !marker.Committed {
			t.Fatalf("recovered crash-gap marker: found=%t marker=%#v err=%v", found, marker, err)
		}
	})

	t.Run("stale mutation cannot resurrect committed deletion", func(t *testing.T) {
		if _, err := stale.UpdateThreadTitle(item.ID, thread.ID, "resurrected stale thread", false); !errors.Is(err, project.ErrThreadNotFound) {
			t.Fatalf("stale mutation error = %v, want thread not found", err)
		}
		exists, err := stale.PersistedResourceExists(item.ID, thread.ID)
		if err != nil || exists {
			t.Fatalf("stale mutation resurrected deletion: exists=%t err=%v", exists, err)
		}

		handler := &terminalHandler{projects: stale, terminalStops: manager}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatal(err)
		}
		marker, found, err := manager.readThread(item.ID, thread.ID)
		if err != nil || !found || !marker.Committed {
			t.Fatalf("committed marker after stale resurrection: found=%t marker=%#v err=%v", found, marker, err)
		}
	})
}

func TestStopProjectUsesPersistedCrossStoreThreadSnapshot(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "projects.json")
	writer, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	staleItem, err := writer.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	lateThread, err := writer.AddThread(staleItem.ID, "Late thread", false)
	if err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{
		projects:      stale,
		terminalStops: newTerminalStopManager(stale.DataDirectory()),
	}

	current, lease, err := handler.stopProjectSessions(staleItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Threads) != 2 {
		t.Fatalf("persisted project refresh has %d threads, want 2", len(current.Threads))
	}
	marker := lease.Marker()
	for _, want := range []string{
		tmuxSessionName(staleItem.ID, lateThread.ID, "terminal"),
		tmuxSessionName(staleItem.ID, lateThread.ID, "process"),
	} {
		if !slices.Contains(marker.SessionNames, want) {
			t.Fatalf("persisted project recipe %#v omitted %q", marker.SessionNames, want)
		}
	}
	if err := handler.cancelStopProject(current, lease); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptedCommittedProjectStopPreservesExactRecipe(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := newTerminalStopManager(store.DataDirectory())
	want := []string{"original-a", "original-z"}
	original, err := manager.beginProject(item.ID, projectThreadIDs(item), want)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := original.Retain(); err != nil {
		t.Fatal(err)
	}

	handler := &terminalHandler{projects: store, terminalStops: manager}
	_, adopted, err := handler.stopProjectSessions(item)
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.Adopted() || !adopted.Marker().Committed {
		t.Fatalf("adopted marker state = %#v adopted=%t", adopted.Marker(), adopted.Adopted())
	}
	if got := adopted.Marker().SessionNames; !reflect.DeepEqual(got, want) {
		t.Fatalf("adopted committed recipe = %#v, want %#v", got, want)
	}
	if err := adopted.Retain(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveStopStoreErrorUsesPersistedCommitState(t *testing.T) {
	newFixture := func(t *testing.T) (*project.Store, project.Project, *terminalHandler) {
		t.Helper()
		store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
		if err != nil {
			t.Fatal(err)
		}
		item, err := store.Add("Demo", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return store, item, &terminalHandler{
			projects:      store,
			terminalStops: newTerminalStopManager(store.DataDirectory()),
		}
	}

	t.Run("thread rollback while present", func(t *testing.T) {
		_, item, handler := newFixture(t)
		thread := item.Threads[0]
		lease, err := handler.stopThreadSessions(item, thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		published, err := handler.resolveStopThreadStoreError(item, thread.ID, lease)
		if err != nil || published {
			t.Fatalf("resolve present thread: published=%t err=%v", published, err)
		}
		if _, found, err := handler.terminalStops.readThread(item.ID, thread.ID); err != nil || found {
			t.Fatalf("present thread marker after rollback: found=%t err=%v", found, err)
		}
	})

	t.Run("thread commit after publish", func(t *testing.T) {
		store, item, handler := newFixture(t)
		thread := item.Threads[0]
		lease, err := handler.stopThreadSessions(item, thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteThread(item.ID, thread.ID); err != nil {
			t.Fatal(err)
		}
		published, err := handler.resolveStopThreadStoreError(item, thread.ID, lease)
		if err != nil || !published {
			t.Fatalf("resolve published thread: published=%t err=%v", published, err)
		}
		marker, found, err := handler.terminalStops.readThread(item.ID, thread.ID)
		if err != nil || !found || !marker.Committed {
			t.Fatalf("published thread marker: found=%t marker=%#v err=%v", found, marker, err)
		}
	})

	t.Run("project commit after publish", func(t *testing.T) {
		store, item, handler := newFixture(t)
		stopped, lease, err := handler.stopProjectSessions(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(item.ID); err != nil {
			t.Fatal(err)
		}
		published, err := handler.resolveStopProjectStoreError(stopped, lease)
		if err != nil || !published {
			t.Fatalf("resolve published project: published=%t err=%v", published, err)
		}
		marker, found, err := handler.terminalStops.readProject(item.ID)
		if err != nil || !found || !marker.Committed {
			t.Fatalf("published project marker: found=%t marker=%#v err=%v", found, marker, err)
		}
	})
}

func TestDeleteRetriesBypassCachedNotFoundStoreState(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		dataFile := filepath.Join(t.TempDir(), "projects.json")
		writer, err := project.NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		stale, err := project.NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		item, err := writer.Add("Created by another backend", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stale.Get(item.ID); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("fixture Store is not stale: %v", err)
		}
		handler := &terminalHandler{projects: stale, terminalStops: newTerminalStopManager(stale.DataDirectory())}
		server := &Server{projects: stale, terminal: handler, piActivity: newPiActivityTracker()}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil)
		request.SetPathValue("id", item.ID)
		server.deleteProject(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete stale-NotFound project status=%d body=%s", response.Code, response.Body.String())
		}
		if _, err := writer.GetPersisted(item.ID); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("persisted project after retry delete: %v", err)
		}
	})

	t.Run("thread", func(t *testing.T) {
		dataFile := filepath.Join(t.TempDir(), "projects.json")
		writer, err := project.NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		item, err := writer.Add("Demo", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		stale, err := project.NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		thread, err := writer.AddThread(item.ID, "Created by another backend", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := stale.GetThread(item.ID, thread.ID); !errors.Is(err, project.ErrThreadNotFound) {
			t.Fatalf("fixture Store is not stale: %v", err)
		}
		handler := &terminalHandler{projects: stale, terminalStops: newTerminalStopManager(stale.DataDirectory())}
		server := &Server{projects: stale, terminal: handler, piActivity: newPiActivityTracker()}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+thread.ID, nil)
		request.SetPathValue("id", item.ID)
		request.SetPathValue("threadId", thread.ID)
		server.deleteThread(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete stale-NotFound thread status=%d body=%s", response.Code, response.Body.String())
		}
		if _, _, err := writer.GetThreadPersisted(item.ID, thread.ID); !errors.Is(err, project.ErrThreadNotFound) {
			t.Fatalf("persisted thread after retry delete: %v", err)
		}
	})
}

func TestTerminalStopFenceDecidesCommitPerMarkerScope(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	missingThread := project.Thread{ID: "deleted-thread"}
	manager := newTerminalStopManager(store.DataDirectory())
	writeTestTerminalStopMarker(t, manager, terminalStopMarkerRef{
		Scope:     terminalStopScopeProject,
		ProjectID: item.ID,
	}, []string{"project-pending-exact"})
	writeTestTerminalStopMarker(t, manager, terminalStopMarkerRef{
		Scope:     terminalStopScopeThread,
		ProjectID: item.ID,
		ThreadID:  missingThread.ID,
	}, []string{"thread-committed-exact"})

	callLog := filepath.Join(t.TempDir(), "tmux-calls")
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(callLog) + "\nexit 1\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{
		projects:      store,
		terminalStops: manager,
		tmuxPath:      fakeTmux,
		tmuxSocket:    "scope-test",
	}
	err = handler.finishTerminalThreadMutationLocked(item, missingThread)
	if !errors.Is(err, errTerminalStopping) {
		t.Fatalf("scope-specific fence error = %v, want terminal stopping", err)
	}
	contents, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(contents)
	if !strings.Contains(calls, "=thread-committed-exact") {
		t.Fatalf("committed thread recipe was not inspected: %q", calls)
	}
	if strings.Contains(calls, "project-pending-exact") {
		t.Fatalf("pending project recipe was destructively inspected: %q", calls)
	}
}

func TestFinishTerminalStopsWithoutTmuxDefersCleanup(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	handler := &terminalHandler{terminalStops: manager}
	item := project.Project{ID: "project", Threads: []project.Thread{{ID: "thread"}}}

	threadLease, err := manager.beginThread(item.ID, item.Threads[0].ID, []string{"thread-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.finishStopThread(item, item.Threads[0].ID, threadLease); err != nil {
		t.Fatalf("finish thread without tmux: %v", err)
	}
	threadRecovery, found, err := manager.acquireExisting(terminalStopMarkerRef{
		Scope:     terminalStopScopeThread,
		ProjectID: item.ID,
		ThreadID:  item.Threads[0].ID,
	})
	if err != nil || !found || threadRecovery == nil {
		t.Fatalf("recover retained thread marker: found=%t err=%v", found, err)
	}
	if marker := threadRecovery.Marker(); !marker.Committed {
		t.Fatal("finish thread did not durably commit its retained marker")
	}
	if err := threadRecovery.Retain(); err != nil {
		t.Fatal(err)
	}

	projectItem := project.Project{ID: "project-two", Threads: []project.Thread{{ID: "thread-two"}}}
	projectLease, err := manager.beginProject(projectItem.ID, projectThreadIDs(projectItem), []string{"project-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.finishStopProject(projectItem, projectLease); err != nil {
		t.Fatalf("finish project without tmux: %v", err)
	}
	if marker, found, err := manager.readProject(projectItem.ID); err != nil || !found || !marker.Committed {
		t.Fatalf("finish project marker state: found=%t err=%v", found, err)
	}
}

func TestTerminalMutationFencePreservesSessionsOnMalformedStopStorage(t *testing.T) {
	manager := newTerminalStopManager(t.TempDir())
	path := manager.threadPath("project", "thread")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "tmux-called")
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nprintf called > " + callLog + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{
		terminalStops: manager,
		tmuxPath:      fakeTmux,
		tmuxSocket:    "must-not-run",
	}
	item := project.Project{ID: "project"}
	thread := project.Thread{ID: "thread"}
	err := handler.finishTerminalThreadMutationLocked(item, thread)
	if !errors.Is(err, errTerminalStopping) {
		t.Fatalf("malformed storage fence error = %v, want terminal stopping", err)
	}
	if _, err := os.Stat(callLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed marker triggered destructive tmux cleanup: %v", err)
	}
}
