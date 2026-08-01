package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/durable"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// writeTestTerminalStopMarker writes a stop marker file directly, bypassing
// the manager's mutual-exclusion checks, to simulate arbitrary crash states
// (including deliberately conflicting dual markers). The serialized layout is
// the durable package's pinned on-disk contract.
func writeTestTerminalStopMarker(
	t *testing.T,
	manager *durable.StopManager,
	ref durable.StopMarkerRef,
	sessionNames []string,
) durable.StopMarker {
	t.Helper()
	path, err := manager.MarkerPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	marker := durable.StopMarker{
		Version:      1,
		Scope:        ref.Scope,
		ProjectID:    ref.ProjectID,
		ThreadID:     ref.ThreadID,
		Token:        hex.EncodeToString(token[:]),
		SessionNames: sessionNames,
		CreatedAt:    time.Now().UTC(),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
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
	manager := durable.NewStopManager(store.DataDirectory())
	handler := &terminalHandler{projects: store, terminalStops: manager}

	t.Run("ambiguous precommit remains fenced", func(t *testing.T) {
		lease, beginErr := manager.BeginThread(item.ID, thread.ID, []string{"exact-session"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := lease.Retain(); err != nil {
			t.Fatal(err)
		}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatal(err)
		}
		if marker, found, err := manager.ReadThread(item.ID, thread.ID); err != nil || !found || marker.Committed {
			t.Fatalf("precommit marker after recovery: found=%t err=%v", found, err)
		}
		recovery, found, err := manager.AcquireExisting(durable.StopMarkerRef{
			Scope: durable.StopScopeThread, ProjectID: item.ID, ThreadID: thread.ID,
		})
		if err != nil || !found || recovery == nil {
			t.Fatalf("acquire retained precommit marker: found=%t err=%v", found, err)
		}
		if err := recovery.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("active is skipped", func(t *testing.T) {
		lease, beginErr := manager.BeginThread(item.ID, thread.ID, []string{"exact-session"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err := handler.reconcileTerminalStops(); err != nil {
			t.Fatalf("active marker should be skipped: %v", err)
		}
		if _, found, err := manager.ReadThread(item.ID, thread.ID); err != nil || !found {
			t.Fatalf("active marker after recovery: found=%t err=%v", found, err)
		}
		if err := lease.Rollback(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("committed without tmux defers cleanup", func(t *testing.T) {
		lease, beginErr := manager.BeginThread(item.ID, thread.ID, []string{"exact-session"})
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
		if marker, found, err := manager.ReadThread(item.ID, thread.ID); err != nil || !found || !marker.Committed {
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
	manager := durable.NewStopManager(writer.DataDirectory())

	t.Run("crash gap is upgraded from durable Store", func(t *testing.T) {
		lease, err := manager.BeginThread(item.ID, thread.ID, []string{"thread-exact"})
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
		marker, found, err := manager.ReadThread(item.ID, thread.ID)
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
		marker, found, err := manager.ReadThread(item.ID, thread.ID)
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
		terminalStops: durable.NewStopManager(stale.DataDirectory()),
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
	manager := durable.NewStopManager(store.DataDirectory())
	want := []string{"original-a", "original-z"}
	original, err := manager.BeginProject(item.ID, projectThreadIDs(item), want)
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
			terminalStops: durable.NewStopManager(store.DataDirectory()),
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
		if _, found, err := handler.terminalStops.ReadThread(item.ID, thread.ID); err != nil || found {
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
		marker, found, err := handler.terminalStops.ReadThread(item.ID, thread.ID)
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
		marker, found, err := handler.terminalStops.ReadProject(item.ID)
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
		handler := &terminalHandler{projects: stale, terminalStops: durable.NewStopManager(stale.DataDirectory())}
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
		handler := &terminalHandler{projects: stale, terminalStops: durable.NewStopManager(stale.DataDirectory())}
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
	manager := durable.NewStopManager(store.DataDirectory())
	writeTestTerminalStopMarker(t, manager, durable.StopMarkerRef{
		Scope:     durable.StopScopeProject,
		ProjectID: item.ID,
	}, []string{"project-pending-exact"})
	writeTestTerminalStopMarker(t, manager, durable.StopMarkerRef{
		Scope:     durable.StopScopeThread,
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
	if !errors.Is(err, durable.ErrStopping) {
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
	manager := durable.NewStopManager(t.TempDir())
	handler := &terminalHandler{terminalStops: manager}
	item := project.Project{ID: "project", Threads: []project.Thread{{ID: "thread"}}}

	threadLease, err := manager.BeginThread(item.ID, item.Threads[0].ID, []string{"thread-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.finishStopThread(item, item.Threads[0].ID, threadLease); err != nil {
		t.Fatalf("finish thread without tmux: %v", err)
	}
	threadRecovery, found, err := manager.AcquireExisting(durable.StopMarkerRef{
		Scope:     durable.StopScopeThread,
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
	projectLease, err := manager.BeginProject(projectItem.ID, projectThreadIDs(projectItem), []string{"project-exact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.finishStopProject(projectItem, projectLease); err != nil {
		t.Fatalf("finish project without tmux: %v", err)
	}
	if marker, found, err := manager.ReadProject(projectItem.ID); err != nil || !found || !marker.Committed {
		t.Fatalf("finish project marker state: found=%t err=%v", found, err)
	}
}

func TestTerminalMutationFencePreservesSessionsOnMalformedStopStorage(t *testing.T) {
	manager := durable.NewStopManager(t.TempDir())
	path, err := manager.MarkerPath(durable.StopMarkerRef{
		Scope: durable.StopScopeThread, ProjectID: "project", ThreadID: "thread",
	})
	if err != nil {
		t.Fatal(err)
	}
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
	err = handler.finishTerminalThreadMutationLocked(item, thread)
	if !errors.Is(err, durable.ErrStopping) {
		t.Fatalf("malformed storage fence error = %v, want terminal stopping", err)
	}
	if _, err := os.Stat(callLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed marker triggered destructive tmux cleanup: %v", err)
	}
}
