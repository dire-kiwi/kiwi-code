package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStorePersistsProjects(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	projectPath := t.TempDir()

	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Add("Demo", projectPath)
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.List()
	if len(items) != 1 || items[0].ID != created.ID || items[0].Name != "Demo" {
		t.Fatalf("unexpected persisted projects: %#v", items)
	}
	if items[0].ProfileID != PersonalProfileID {
		t.Fatalf("default profile = %q, want %q", items[0].ProfileID, PersonalProfileID)
	}
	if len(items[0].Threads) != 1 || items[0].Threads[0].Cwd != projectPath {
		t.Fatalf("unexpected initial thread: %#v", items[0].Threads)
	}

	if err := reloaded.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStorePersistsLatestThreadPromptTime(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	promptedAt := time.Date(2026, time.April, 10, 11, 12, 13, 0, time.FixedZone("test", 2*60*60))

	updates, unsubscribe := store.SubscribeChanges()
	defer unsubscribe()
	updated, err := store.RecordThreadPrompt(item.ID, thread.ID, promptedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantPromptedAt := promptedAt.UTC()
	if updated.LastPromptAt == nil || !updated.LastPromptAt.Equal(wantPromptedAt) {
		t.Fatalf("updated last prompt time = %v, want %v", updated.LastPromptAt, wantPromptedAt)
	}
	if snapshot := readProjectSnapshot(t, updates); snapshot[0].Threads[0].LastPromptAt == nil || !snapshot[0].Threads[0].LastPromptAt.Equal(wantPromptedAt) {
		t.Fatalf("published last prompt snapshot = %#v", snapshot)
	}

	older := promptedAt.Add(-time.Hour)
	unchanged, err := store.RecordThreadPrompt(item.ID, thread.ID, older)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.LastPromptAt == nil || !unchanged.LastPromptAt.Equal(wantPromptedAt) {
		t.Fatalf("older report changed last prompt time = %v", unchanged.LastPromptAt)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted, err := reloaded.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LastPromptAt == nil || !persisted.LastPromptAt.Equal(wantPromptedAt) {
		t.Fatalf("persisted last prompt time = %v, want %v", persisted.LastPromptAt, wantPromptedAt)
	}
}

func TestStorePersistsProjectAndThreadOrder(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Add("First", t.TempDir(), PersonalProfileID)
	if err != nil {
		t.Fatal(err)
	}
	work, err := store.Add("Work", t.TempDir(), WorkProfileID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Add("Second", t.TempDir(), PersonalProfileID)
	if err != nil {
		t.Fatal(err)
	}
	middleThread, err := store.AddThread(first.ID, "Middle")
	if err != nil {
		t.Fatal(err)
	}
	lastThread, err := store.AddThread(first.ID, "Last")
	if err != nil {
		t.Fatal(err)
	}

	updates, unsubscribe := store.SubscribeChanges()
	defer unsubscribe()
	if err := store.ReorderProjects(PersonalProfileID, []string{second.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	if snapshot := readProjectSnapshot(t, updates); len(snapshot) != 3 || snapshot[0].ID != second.ID || snapshot[1].ID != work.ID || snapshot[2].ID != first.ID {
		t.Fatalf("reordered project snapshot = %#v", snapshot)
	}

	threadOrder := []string{lastThread.ID, first.Threads[0].ID, middleThread.ID}
	if err := store.ReorderThreads(first.ID, threadOrder); err != nil {
		t.Fatal(err)
	}
	if snapshot := readProjectSnapshot(t, updates); len(snapshot) != 3 || snapshot[2].Threads[0].ID != lastThread.ID {
		t.Fatalf("reordered thread snapshot = %#v", snapshot)
	}

	if err := store.ReorderProjects(PersonalProfileID, []string{first.ID}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("incomplete project order error = %v", err)
	}
	if err := store.ReorderThreads(first.ID, []string{lastThread.ID, lastThread.ID, middleThread.ID}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("duplicate thread order error = %v", err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	projects := reloaded.List()
	if len(projects) != 3 || projects[0].ID != second.ID || projects[1].ID != work.ID || projects[2].ID != first.ID {
		t.Fatalf("persisted project order = %#v", projects)
	}
	for index, threadID := range threadOrder {
		if projects[2].Threads[index].ID != threadID {
			t.Fatalf("persisted thread order = %#v", projects[2].Threads)
		}
	}
}

func TestStorePersistsAndLimitsChildThreadRelationships(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "Child implementation", AddThreadOptions{
		ParentThreadID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Child review", AddThreadOptions{
		ParentThreadID: child.ID,
	}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("nested child error = %v, want ErrChildThreadDepthLimit", err)
	}
	if child.ParentThreadID != parent.ID {
		t.Fatalf("unexpected child relationship: %#v", child)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Missing parent", AddThreadOptions{ParentThreadID: "missing"}); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("missing parent error = %v, want ErrThreadNotFound", err)
	}
	closedAt := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	closed, err := store.CloseChildThread(item.ID, parent.ID, child.ID, closedAt)
	if err != nil {
		t.Fatal(err)
	}
	if closed.ClosedAt == nil || !closed.ClosedAt.Equal(closedAt) {
		t.Fatalf("closed child = %#v", closed)
	}
	reloadedWithChild, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persistedWithChild, err := reloadedWithChild.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedWithChild.Threads) != 2 || persistedWithChild.Threads[1].ClosedAt == nil || !persistedWithChild.Threads[1].ClosedAt.Equal(closedAt) {
		t.Fatalf("persisted closed child = %#v", persistedWithChild.Threads)
	}
	if _, err := store.CloseChildThread(item.ID, "other-parent", child.ID, closedAt); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("mismatched parent close error = %v, want ErrThreadNotFound", err)
	}
	if err := store.DeleteThread(item.ID, parent.ID); !errors.Is(err, ErrThreadHasChildren) {
		t.Fatalf("delete parent error = %v, want ErrThreadHasChildren", err)
	}
	if err := store.DeleteThread(item.ID, child.ID); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 1 || persisted.Threads[0].ID != parent.ID {
		t.Fatalf("unexpected persisted threads: %#v", persisted.Threads)
	}
}

func TestStoreConfiguresSubAgentNestingDepthGloballyAndPerProject(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := item.Threads[0]
	rootContext, err := store.SubAgentNestingContext(item.ID, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rootContext != (SubAgentNestingContext{CurrentDepth: 0, MaxDepth: DefaultSubAgentNestingDepth}) {
		t.Fatalf("root nesting context = %#v", rootContext)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Child", AddThreadOptions{ParentThreadID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	childContext, err := store.SubAgentNestingContext(item.ID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if childContext != (SubAgentNestingContext{CurrentDepth: 1, MaxDepth: DefaultSubAgentNestingDepth}) {
		t.Fatalf("child nesting context = %#v", childContext)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked grandchild", AddThreadOptions{ParentThreadID: child.ID}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("default nested child error = %v, want ErrChildThreadDepthLimit", err)
	}

	globalDepth := 2
	if _, err := store.UpdateSettingsValues(SettingsUpdate{SubAgentNestingDepth: &globalDepth}); err != nil {
		t.Fatal(err)
	}
	grandchild, err := store.AddThreadWithOptions(item.ID, "Grandchild", AddThreadOptions{ParentThreadID: child.ID})
	if err != nil {
		t.Fatal(err)
	}
	grandchildContext, err := store.SubAgentNestingContext(item.ID, grandchild.ID)
	if err != nil {
		t.Fatal(err)
	}
	if grandchildContext != (SubAgentNestingContext{CurrentDepth: 2, MaxDepth: globalDepth}) {
		t.Fatalf("grandchild nesting context = %#v", grandchildContext)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked great-grandchild", AddThreadOptions{ParentThreadID: grandchild.ID}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("global depth boundary error = %v, want ErrChildThreadDepthLimit", err)
	}

	override := 3
	updated, err := store.UpdateProject(item.ID, ProjectUpdate{
		SubAgentNestingDepthOverride:       &override,
		UpdateSubAgentNestingDepthOverride: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.SubAgentNestingDepthOverride == nil || *updated.SubAgentNestingDepthOverride != override {
		t.Fatalf("project override = %#v", updated.SubAgentNestingDepthOverride)
	}
	greatGrandchild, err := store.AddThreadWithOptions(item.ID, "Great-grandchild", AddThreadOptions{ParentThreadID: grandchild.ID})
	if err != nil {
		t.Fatal(err)
	}
	greatGrandchildContext, err := store.SubAgentNestingContext(item.ID, greatGrandchild.ID)
	if err != nil {
		t.Fatal(err)
	}
	if greatGrandchildContext != (SubAgentNestingContext{CurrentDepth: 3, MaxDepth: override}) {
		t.Fatalf("great-grandchild nesting context = %#v", greatGrandchildContext)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Too deep", AddThreadOptions{ParentThreadID: greatGrandchild.ID}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("project depth boundary error = %v, want ErrChildThreadDepthLimit", err)
	}

	closedAt := time.Now().UTC()
	if _, err := store.CloseChildThread(item.ID, grandchild.ID, greatGrandchild.ID, closedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseChildThread(item.ID, child.ID, grandchild.ID, closedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseChildThread(item.ID, root.ID, child.ID, closedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Child under closed parent", AddThreadOptions{ParentThreadID: child.ID}); !errors.Is(err, ErrThreadClosed) {
		t.Fatalf("closed parent error = %v, want ErrThreadClosed", err)
	}
	if _, err := store.ReopenChildThread(item.ID, grandchild.ID, greatGrandchild.ID); err != nil {
		t.Fatal(err)
	}
	for _, threadID := range []string{child.ID, grandchild.ID, greatGrandchild.ID} {
		_, reopened, err := store.GetThread(item.ID, threadID)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.ClosedAt != nil {
			t.Fatalf("ancestor chain was not reopened: %#v", reopened)
		}
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if settings := reloaded.GetSettings(); settings.SubAgentNestingDepth != globalDepth || settings.MaxSubAgentNestingDepth != MaxSubAgentNestingDepth {
		t.Fatalf("persisted nesting settings = %#v", settings)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.SubAgentNestingDepthOverride == nil || *persisted.SubAgentNestingDepthOverride != override {
		t.Fatalf("persisted project override = %#v", persisted.SubAgentNestingDepthOverride)
	}

	zero := 0
	if _, err := reloaded.UpdateProject(item.ID, ProjectUpdate{
		SubAgentNestingDepthOverride:       &zero,
		UpdateSubAgentNestingDepthOverride: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.AddThreadWithOptions(item.ID, "Disabled child", AddThreadOptions{ParentThreadID: root.ID}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("zero override error = %v, want ErrChildThreadDepthLimit", err)
	}
	if _, err := reloaded.UpdateProject(item.ID, ProjectUpdate{UpdateSubAgentNestingDepthOverride: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.AddThreadWithOptions(item.ID, "Inherited child", AddThreadOptions{ParentThreadID: root.ID}); err != nil {
		t.Fatalf("cleared override did not inherit global setting: %v", err)
	}

	invalid := MaxSubAgentNestingDepth + 1
	if _, err := reloaded.UpdateSettingsValues(SettingsUpdate{SubAgentNestingDepth: &invalid}); err == nil {
		t.Fatal("expected invalid global nesting depth to be rejected")
	}
	if _, err := reloaded.UpdateProject(item.ID, ProjectUpdate{
		SubAgentNestingDepthOverride:       &invalid,
		UpdateSubAgentNestingDepthOverride: true,
	}); err == nil {
		t.Fatal("expected invalid project nesting depth to be rejected")
	}
}

func TestStoreEnforcesAndPersistsPerThreadRelativeDescendantLimits(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectDepth := MaxSubAgentNestingDepth
	if _, err := store.UpdateProject(item.ID, ProjectUpdate{
		SubAgentNestingDepthOverride:       &projectDepth,
		UpdateSubAgentNestingDepthOverride: true,
	}); err != nil {
		t.Fatal(err)
	}

	tooManyRemainingLevels := projectDepth
	if _, err := store.AddThreadWithOptions(item.ID, "Impossible relative depth", AddThreadOptions{
		ParentThreadID: item.Threads[0].ID,
		NestedDepth:    &tooManyRemainingLevels,
	}); err == nil || !strings.Contains(err.Error(), "remaining sub-agent nesting depth") {
		t.Fatalf("impossible relative depth error = %v", err)
	}

	const wantRelativeDepth = 1
	relativeDepth := wantRelativeDepth
	limited, err := store.AddThreadWithOptions(item.ID, "Limited child", AddThreadOptions{
		ParentThreadID: item.Threads[0].ID,
		NestedDepth:    &relativeDepth,
	})
	if err != nil {
		t.Fatal(err)
	}
	relativeDepth = 0
	*limited.NestedDepth = 0
	_, isolated, err := store.GetThread(item.ID, limited.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isolated.NestedDepth == nil || *isolated.NestedDepth != wantRelativeDepth {
		t.Fatalf("stored nested depth aliased caller or result: %#v", isolated.NestedDepth)
	}
	helper, err := store.AddThreadWithOptions(item.ID, "One allowed level", AddThreadOptions{
		ParentThreadID: limited.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked by ancestor", AddThreadOptions{
		ParentThreadID: helper.ID,
	}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("ancestor relative limit error = %v, want ErrChildThreadDepthLimit", err)
	}
	context, err := store.SubAgentNestingContext(item.ID, helper.ID)
	if err != nil {
		t.Fatal(err)
	}
	if context != (SubAgentNestingContext{CurrentDepth: 2, MaxDepth: 2}) {
		t.Fatalf("effective helper nesting context = %#v", context)
	}

	zero := 0
	leaf, err := store.AddThreadWithOptions(item.ID, "Leaf child", AddThreadOptions{
		ParentThreadID: item.Threads[0].ID,
		NestedDepth:    &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked direct child", AddThreadOptions{
		ParentThreadID: leaf.ID,
	}); !errors.Is(err, ErrChildThreadDepthLimit) {
		t.Fatalf("zero relative limit error = %v, want ErrChildThreadDepthLimit", err)
	}
	contents, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"nestedDepth": 0`) {
		t.Fatalf("projects JSON did not preserve explicit zero nested depth: %s", contents)
	}
	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted, err := reloaded.GetThread(item.ID, limited.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.NestedDepth == nil || *persisted.NestedDepth != wantRelativeDepth {
		t.Fatalf("persisted nested depth = %#v", persisted.NestedDepth)
	}
	_, persistedLeaf, err := reloaded.GetThread(item.ID, leaf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedLeaf.NestedDepth == nil || *persistedLeaf.NestedDepth != 0 {
		t.Fatalf("persisted zero nested depth = %#v", persistedLeaf.NestedDepth)
	}

	invalid := MaxSubAgentNestingDepth + 1
	if _, err := reloaded.AddThreadWithOptions(item.ID, "Invalid depth", AddThreadOptions{NestedDepth: &invalid}); err == nil {
		t.Fatal("expected an invalid per-thread nesting depth to be rejected")
	}
}

func TestStoreDeletesAnExpectedThreadTreeAtomically(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	firstChild, err := store.AddThreadWithOptions(item.ID, "First child", AddThreadOptions{ParentThreadID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	secondChild, err := store.AddThreadWithOptions(item.ID, "Second child", AddThreadOptions{ParentThreadID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.AddThread(item.ID, "Unrelated")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteThreadTree(item.ID, parent.ID, []string{parent.ID, firstChild.ID}); !errors.Is(err, ErrThreadTreeChanged) {
		t.Fatalf("stale tree deletion error = %v, want ErrThreadTreeChanged", err)
	}
	unchanged, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Threads) != 4 {
		t.Fatalf("stale tree deletion changed threads: %#v", unchanged.Threads)
	}

	if err := store.DeleteThreadTree(item.ID, parent.ID, []string{secondChild.ID, parent.ID, firstChild.ID}); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 1 || persisted.Threads[0].ID != unrelated.ID {
		t.Fatalf("threads after tree deletion = %#v", persisted.Threads)
	}
}

func TestStoreRejectsInvalidPersistedChildRelationships(t *testing.T) {
	projectPath := t.TempDir()
	createdAt := time.Now().UTC()
	tests := []struct {
		name    string
		threads []Thread
	}{
		{
			name: "missing parent",
			threads: []Thread{
				{ID: "root", Title: "Root", Cwd: projectPath, CreatedAt: createdAt},
				{ID: "child", Title: "Child", Cwd: projectPath, CreatedAt: createdAt, ParentThreadID: "missing"},
			},
		},
		{
			name: "cycle",
			threads: []Thread{
				{ID: "first", Title: "First", Cwd: projectPath, CreatedAt: createdAt, ParentThreadID: "second"},
				{ID: "second", Title: "Second", Cwd: projectPath, CreatedAt: createdAt, ParentThreadID: "first"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataFile := filepath.Join(t.TempDir(), "projects.json")
			contents, err := json.Marshal([]Project{{
				ID: "project", Name: "Project", Path: projectPath, CreatedAt: createdAt, Threads: test.threads,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dataFile, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(dataFile); err == nil {
				t.Fatal("NewStore accepted invalid child relationships")
			}
		})
	}
}

func TestReorderRebasesOnLatestPersistedProjects(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	writer, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	first, err := writer.Add("First", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Add("Second", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := writer.AddThread(first.ID, "Second thread")
	if err != nil {
		t.Fatal(err)
	}

	stale, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.UpdateThreadTitle(first.ID, first.Threads[0].ID, "Updated elsewhere", false); err != nil {
		t.Fatal(err)
	}
	if err := stale.ReorderThreads(first.ID, []string{secondThread.ID, first.Threads[0].ID}); err != nil {
		t.Fatal(err)
	}
	persisted, err := writer.GetPersisted(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 2 || persisted.Threads[0].ID != secondThread.ID || persisted.Threads[1].Title != "Updated elsewhere" {
		t.Fatalf("thread reorder lost a concurrent title update: %#v", persisted.Threads)
	}

	third, err := writer.Add("Third", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.ReorderProjects(PersonalProfileID, []string{second.ID, first.ID}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("stale project order error = %v, want ErrInvalidOrder", err)
	}
	projects, err := writer.ListPersisted()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 || projects[2].ID != third.ID {
		t.Fatalf("stale reorder lost a concurrently added project: %#v", projects)
	}
}

func TestPersistedReadsBypassStaleStoreSnapshot(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	writer, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := writer.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initialThread := item.Threads[0]
	stale, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	addedThread, err := writer.AddThread(item.ID, "Added by another Store", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stale.GetThread(item.ID, addedThread.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("stale Store unexpectedly saw added thread: %v", err)
	}
	persistedProjects, err := stale.ListPersisted()
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedProjects) != 1 || len(persistedProjects[0].Threads) != 2 || persistedProjects[0].Threads[0].ID != addedThread.ID {
		t.Fatalf("fresh persisted project list did not include added thread: %#v", persistedProjects)
	}
	persisted, err := stale.GetPersisted(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 2 || persisted.Threads[0].ID != addedThread.ID {
		t.Fatalf("fresh persisted project did not include added thread: %#v", persisted.Threads)
	}
	if exists, err := stale.PersistedResourceExists(item.ID, addedThread.ID); err != nil || !exists {
		t.Fatalf("persisted added thread: exists=%t err=%v", exists, err)
	}
	persistedItem, persistedThread, err := stale.GetThreadPersisted(item.ID, addedThread.ID)
	if err != nil || persistedItem.ID != item.ID || persistedThread.ID != addedThread.ID {
		t.Fatalf("fresh persisted thread = item %#v thread %#v err=%v", persistedItem, persistedThread, err)
	}

	if err := writer.DeleteThread(item.ID, initialThread.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stale.GetThread(item.ID, initialThread.ID); err != nil {
		t.Fatalf("stale Store no longer demonstrates cached deleted thread: %v", err)
	}
	if exists, err := stale.PersistedResourceExists(item.ID, initialThread.ID); err != nil || exists {
		t.Fatalf("persisted deleted thread: exists=%t err=%v", exists, err)
	}
	if exists, err := stale.PersistedResourceExists(item.ID, "missing-thread"); err != nil || exists {
		t.Fatalf("persisted missing thread: exists=%t err=%v", exists, err)
	}
	if exists, err := stale.PersistedResourceExists(item.ID, ""); err != nil || !exists {
		t.Fatalf("persisted project scope: exists=%t err=%v", exists, err)
	}

	if err := writer.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Get(item.ID); err != nil {
		t.Fatalf("stale Store no longer demonstrates cached deleted project: %v", err)
	}
	if exists, err := stale.PersistedResourceExists(item.ID, ""); err != nil || exists {
		t.Fatalf("persisted deleted project: exists=%t err=%v", exists, err)
	}
	if _, err := stale.GetPersisted(item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPersisted deleted project error = %v, want ErrNotFound", err)
	}
}

func TestSeparateStoresRebaseProjectMutationsOnLatestPersistence(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	first, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	alpha, err := first.Add("Alpha", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	beta, err := second.Add("Beta", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertPersistedProjectIDs(t, dataFile, alpha.ID, beta.ID)

	firstThread, err := first.AddThread(alpha.ID, "First Store")
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := second.AddThread(alpha.ID, "Second Store")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := second.GetPersisted(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !projectHasThread(persisted, firstThread.ID) || !projectHasThread(persisted, secondThread.ID) {
		t.Fatalf("separate Store thread additions were not merged: %#v", persisted.Threads)
	}

	initialThreadID := alpha.Threads[0].ID
	if err := first.DeleteThread(alpha.ID, initialThreadID); err != nil {
		t.Fatal(err)
	}
	if _, err := second.UpdateThreadTitle(alpha.ID, initialThreadID, "Resurrected", false); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("stale Store updated a deleted thread: %v", err)
	}
	persisted, err = second.GetPersisted(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projectHasThread(persisted, initialThreadID) {
		t.Fatalf("stale Store resurrected deleted thread %q", initialThreadID)
	}

	if err := first.Delete(alpha.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := second.AddThread(alpha.ID, "Resurrected project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale Store mutated a deleted project: %v", err)
	}
	assertPersistedProjectIDs(t, dataFile, beta.ID)
}

func TestStaleStoreRevalidatesCachedNotFoundBeforeDelete(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	writer, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	staleProjectStore, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := writer.Add("Added later", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleProjectStore.Get(item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Store did not begin with a cached project NotFound: %v", err)
	}
	if err := staleProjectStore.Delete(item.ID); err != nil {
		t.Fatalf("cached project NotFound was not revalidated from persistence: %v", err)
	}
	if exists, err := writer.PersistedResourceExists(item.ID, ""); err != nil || exists {
		t.Fatalf("freshly persisted project was not deleted: exists=%t err=%v", exists, err)
	}

	item, err = writer.Add("Thread target", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	staleThreadStore, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := writer.AddThread(item.ID, "Added later")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := staleThreadStore.GetThread(item.ID, thread.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("Store did not begin with a cached thread NotFound: %v", err)
	}
	if err := staleThreadStore.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatalf("cached thread NotFound was not revalidated from persistence: %v", err)
	}
	if exists, err := writer.PersistedResourceExists(item.ID, thread.ID); err != nil || exists {
		t.Fatalf("freshly persisted thread was not deleted: exists=%t err=%v", exists, err)
	}
}

func TestSeparateStoresPreserveConcurrentThreadMutations(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	first, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := first.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	const additions = 24
	stores := []*Store{first, second}
	start := make(chan struct{})
	errorsByMutation := make(chan error, additions)
	var wait sync.WaitGroup
	for index := 0; index < additions; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := stores[index%len(stores)].AddThread(item.ID, fmt.Sprintf("Concurrent %02d", index))
			errorsByMutation <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByMutation)
	for err := range errorsByMutation {
		if err != nil {
			t.Fatal(err)
		}
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(persisted.Threads), additions+1; got != want {
		t.Fatalf("persisted thread count = %d, want %d: %#v", got, want, persisted.Threads)
	}
	titles := make(map[string]bool, len(persisted.Threads))
	for _, thread := range persisted.Threads {
		titles[thread.Title] = true
	}
	for index := 0; index < additions; index++ {
		title := fmt.Sprintf("Concurrent %02d", index)
		if !titles[title] {
			t.Fatalf("persisted threads are missing %q", title)
		}
	}
}

func TestConcurrentStoresShareOneSafeProjectMigration(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	projectPath := t.TempDir()
	createdAt := time.Now().UTC()
	legacy := []Project{{ID: "legacy", Name: "Legacy", Path: projectPath, CreatedAt: createdAt}}
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	const loaders = 16
	start := make(chan struct{})
	threadIDs := make(chan string, loaders)
	loadErrors := make(chan error, loaders)
	var wait sync.WaitGroup
	for index := 0; index < loaders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store, err := NewStore(dataFile)
			if err != nil {
				loadErrors <- err
				return
			}
			items := store.List()
			if len(items) != 1 || len(items[0].Threads) != 1 {
				loadErrors <- fmt.Errorf("unexpected migrated projects: %#v", items)
				return
			}
			threadIDs <- items[0].Threads[0].ID
		}()
	}
	close(start)
	wait.Wait()
	close(threadIDs)
	close(loadErrors)
	for err := range loadErrors {
		t.Fatal(err)
	}
	var expected string
	for threadID := range threadIDs {
		if expected == "" {
			expected = threadID
		}
		if threadID != expected {
			t.Fatalf("concurrent migration produced thread IDs %q and %q", expected, threadID)
		}
	}
	if expected == "" {
		t.Fatal("concurrent migration returned no thread ID")
	}
}

func TestProjectMutationLockIsPersistentAndPrivate(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "data")
	dataFile := filepath.Join(dataDirectory, "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := dataFile + ".lock"
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Mode().Perm(); got != 0o600 {
		t.Fatalf("project mutation lock mode = %#o, want 0600", got)
	}
	if directory, err := os.Stat(dataDirectory); err != nil {
		t.Fatal(err)
	} else if got := directory.Mode().Perm(); got != 0o700 {
		t.Fatalf("project mutation directory mode = %#o, want 0700", got)
	}
	if _, err := store.Add("Demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("project mutation replaced its sidecar lock inode")
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(dataDirectory, ".projects.json-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("project mutation left temporary files behind: %v", temporaryFiles)
	}
}

func TestProjectMutationsAreSerializedAcrossProcesses(t *testing.T) {
	dataDirectory := t.TempDir()
	dataFile := filepath.Join(dataDirectory, "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	startPath := filepath.Join(dataDirectory, "start")
	titles := []string{"Process one", "Process two"}
	commands := make([]*exec.Cmd, len(titles))
	readyPaths := make([]string, len(titles))
	for index, title := range titles {
		readyPaths[index] = filepath.Join(dataDirectory, fmt.Sprintf("ready-%d", index))
		command := exec.Command(os.Args[0], "-test.run=^TestProjectMutationHelperProcess$")
		command.Env = append(os.Environ(),
			"DIRE_MUX_PROJECT_STORE_HELPER=1",
			"DIRE_MUX_PROJECT_STORE_FILE="+dataFile,
			"DIRE_MUX_PROJECT_STORE_ID="+item.ID,
			"DIRE_MUX_PROJECT_STORE_TITLE="+title,
			"DIRE_MUX_PROJECT_STORE_READY="+readyPaths[index],
			"DIRE_MUX_PROJECT_STORE_START="+startPath,
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands[index] = command
	}
	for _, readyPath := range readyPaths {
		waitForProjectTestFile(t, readyPath)
	}
	if err := os.WriteFile(startPath, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("project mutation helper failed: %v", err)
		}
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range titles {
		found := false
		for _, thread := range persisted.Threads {
			found = found || thread.Title == title
		}
		if !found {
			t.Fatalf("cross-process mutations lost %q: %#v", title, persisted.Threads)
		}
	}
}

func TestProjectMutationHelperProcess(t *testing.T) {
	if os.Getenv("DIRE_MUX_PROJECT_STORE_HELPER") != "1" {
		return
	}
	store, err := NewStore(os.Getenv("DIRE_MUX_PROJECT_STORE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("DIRE_MUX_PROJECT_STORE_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	startPath := os.Getenv("DIRE_MUX_PROJECT_STORE_START")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(startPath); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for project mutation start signal")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := store.AddThread(
		os.Getenv("DIRE_MUX_PROJECT_STORE_ID"),
		os.Getenv("DIRE_MUX_PROJECT_STORE_TITLE"),
	); err != nil {
		t.Fatal(err)
	}
}

func waitForProjectTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertPersistedProjectIDs(t *testing.T, dataFile string, expected ...string) {
	t.Helper()
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	projects := store.List()
	if len(projects) != len(expected) {
		t.Fatalf("persisted project count = %d, want %d: %#v", len(projects), len(expected), projects)
	}
	found := make(map[string]bool, len(projects))
	for _, item := range projects {
		found[item.ID] = true
	}
	for _, projectID := range expected {
		if !found[projectID] {
			t.Fatalf("persisted projects are missing %q: %#v", projectID, projects)
		}
	}
}

func projectHasThread(item Project, threadID string) bool {
	for _, thread := range item.Threads {
		if thread.ID == threadID {
			return true
		}
	}
	return false
}

func TestPersistedReadsHandleMissingAndInvalidStorage(t *testing.T) {
	t.Run("missing is absent", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
		if err != nil {
			t.Fatal(err)
		}
		if exists, err := store.PersistedResourceExists("missing-project", ""); err != nil || exists {
			t.Fatalf("missing persisted project: exists=%t err=%v", exists, err)
		}
		if _, err := store.GetPersisted("missing-project"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing GetPersisted error = %v, want ErrNotFound", err)
		}
	})

	t.Run("malformed fails closed", func(t *testing.T) {
		dataDirectory := t.TempDir()
		dataFile := filepath.Join(dataDirectory, "projects.json")
		store, err := NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dataFile, []byte("{malformed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PersistedResourceExists("project", ""); err == nil {
			t.Fatal("malformed persisted projects were treated as an absent project")
		}
	})

	t.Run("read error fails closed", func(t *testing.T) {
		dataDirectory := t.TempDir()
		dataFile := filepath.Join(dataDirectory, "projects.json")
		store, err := NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dataFile, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PersistedResourceExists("project", ""); err == nil {
			t.Fatal("persisted projects read error was treated as an absent project")
		}
	})

	t.Run("invalid identity fails closed", func(t *testing.T) {
		dataDirectory := t.TempDir()
		dataFile := filepath.Join(dataDirectory, "projects.json")
		store, err := NewStore(dataFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dataFile, []byte(`[{"id":"","threads":[]}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PersistedResourceExists("project", ""); err == nil {
			t.Fatal("invalid persisted project identity was treated as an absent project")
		}
	})
}

func TestStorePersistsProfilesAndProjectAssignments(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.ListProfiles()
	if len(profiles) != 2 || profiles[0].ID != PersonalProfileID || profiles[1].ID != WorkProfileID {
		t.Fatalf("default profiles = %#v", profiles)
	}

	profileUpdates, unsubscribeProfiles := store.SubscribeProfileChanges()
	defer unsubscribeProfiles()
	client, err := store.AddProfile("Client work")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-profileUpdates:
		if len(snapshot) != 3 || snapshot[2] != client {
			t.Fatalf("profile snapshot = %#v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile snapshot")
	}

	item, err := store.Add("Demo", t.TempDir(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.ProfileID != client.ID {
		t.Fatalf("created project profile = %q, want %q", item.ProfileID, client.ID)
	}
	projectUpdates, unsubscribeProjects := store.SubscribeChanges()
	defer unsubscribeProjects()
	updated, err := store.UpdateProjectProfile(item.ID, WorkProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProfileID != WorkProfileID {
		t.Fatalf("updated project profile = %q, want %q", updated.ProfileID, WorkProfileID)
	}
	if snapshot := readProjectSnapshot(t, projectUpdates); len(snapshot) != 1 || snapshot[0].ProfileID != WorkProfileID {
		t.Fatalf("project assignment snapshot = %#v", snapshot)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if profiles := reloaded.ListProfiles(); len(profiles) != 3 || profiles[2] != client {
		t.Fatalf("persisted profiles = %#v", profiles)
	}
	if projects := reloaded.List(); len(projects) != 1 || projects[0].ProfileID != WorkProfileID {
		t.Fatalf("persisted project assignment = %#v", projects)
	}
	if _, err := reloaded.AddProfile("client WORK"); err == nil {
		t.Fatal("expected duplicate profile name to be rejected case-insensitively")
	}
	if _, err := reloaded.UpdateProjectProfile(item.ID, "missing"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("missing profile error = %v", err)
	}
}

func TestStoreSubscriptionPreservesEveryCommittedSnapshot(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	updates, unsubscribe := store.SubscribeChanges()
	defer unsubscribe()

	thread, err := store.AddThread(item.ID, "First title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateThreadTitle(item.ID, thread.ID, "Second title", false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}

	first := readProjectSnapshot(t, updates)
	if !snapshotHasThread(first, thread.ID, "First title") {
		t.Fatalf("first committed snapshot = %#v", first)
	}
	second := readProjectSnapshot(t, updates)
	if !snapshotHasThread(second, thread.ID, "Second title") {
		t.Fatalf("second committed snapshot = %#v", second)
	}
	third := readProjectSnapshot(t, updates)
	if snapshotHasThread(third, thread.ID, "") {
		t.Fatalf("deleted thread remained in third committed snapshot: %#v", third)
	}
}

func TestResolveSnapshotRefreshesExternalGitStatus(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	projectPath := t.TempDir()
	item, err := store.Add("Demo", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if item.IsGitRepo {
		t.Fatal("new temporary directory unexpectedly reported as a Git repository")
	}

	updates, unsubscribe := store.SubscribeChanges()
	defer unsubscribe()
	if _, err := store.UpdateThreadTitle(item.ID, item.Threads[0].ID, "Before git init", false); err != nil {
		t.Fatal(err)
	}
	snapshot := readProjectSnapshot(t, updates)
	if snapshot[0].IsGitRepo {
		t.Fatal("committed snapshot unexpectedly contained externally resolved Git status")
	}
	runGit(t, projectPath, "init")
	resolved := store.ResolveSnapshot(snapshot)
	if len(resolved) != 1 || !resolved[0].IsGitRepo {
		t.Fatalf("resolved snapshot did not refresh Git status: %#v", resolved)
	}
	if snapshot[0].IsGitRepo {
		t.Fatal("ResolveSnapshot mutated the committed event snapshot")
	}
}

func readProjectSnapshot(t *testing.T, updates <-chan []Project) []Project {
	t.Helper()
	select {
	case snapshot := <-updates:
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for project snapshot")
		return nil
	}
}

func snapshotHasThread(projects []Project, threadID, title string) bool {
	for _, item := range projects {
		for _, thread := range item.Threads {
			if thread.ID == threadID && (title == "" || thread.Title == title) {
				return true
			}
		}
	}
	return false
}

func TestStoreCreatesAndDeletesThreads(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	projectPath := t.TempDir()
	item, err := store.Add("Demo", projectPath)
	if err != nil {
		t.Fatal(err)
	}

	thread, err := store.AddThread(item.ID, "Add thread support", false)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Cwd != projectPath || thread.Title != "Add thread support" {
		t.Fatalf("unexpected thread: %#v", thread)
	}
	if _, found, err := store.GetThread(item.ID, thread.ID); err != nil || found.ID != thread.ID {
		t.Fatalf("GetThread() = %#v, %v", found, err)
	}
	if err := store.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetThread(item.ID, thread.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestStoreAddsNewRootThreadsAtTop(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.AddThread(item.ID, "Third")
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{third.ID, second.ID, item.Threads[0].ID}
	if len(persisted.Threads) != len(want) {
		t.Fatalf("persisted threads = %#v, want %d", persisted.Threads, len(want))
	}
	for index, threadID := range want {
		if persisted.Threads[index].ID != threadID {
			t.Fatalf("persisted thread order = %#v, want newest roots first", persisted.Threads)
		}
	}
}

func TestStoreBookmarksThreadsWithoutReordering(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.AddThread(item.ID, "Third")
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{third.ID, second.ID, item.Threads[0].ID}

	updates, unsubscribe := store.SubscribeChanges()
	defer unsubscribe()
	bookmarked, err := store.SetThreadBookmarked(item.ID, second.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bookmarked.Bookmarked {
		t.Fatalf("bookmarked thread = %#v", bookmarked)
	}
	if snapshot := readProjectSnapshot(t, updates); !snapshot[0].Threads[1].Bookmarked {
		t.Fatalf("bookmark snapshot = %#v", snapshot)
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, threadID := range wantOrder {
		if persisted.Threads[index].ID != threadID {
			t.Fatalf("bookmark changed thread order: %#v", persisted.Threads)
		}
	}

	if _, err := store.SetThreadBookmarked(item.ID, second.ID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-updates:
		t.Fatalf("idempotent bookmark published a snapshot: %#v", snapshot)
	case <-time.After(50 * time.Millisecond):
	}

	archived, err := store.SetThreadArchived(item.ID, second.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Bookmarked {
		t.Fatalf("archive cleared bookmark: %#v", archived)
	}
	restored, err := store.SetThreadArchived(item.ID, second.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Bookmarked {
		t.Fatalf("restore cleared bookmark: %#v", restored)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, reloadedThread, err := reloaded.GetThread(item.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloadedThread.Bookmarked {
		t.Fatalf("bookmark was not persisted: %#v", reloadedThread)
	}
	if _, err := reloaded.SetThreadBookmarked(item.ID, second.ID, false); err != nil {
		t.Fatal(err)
	}
	cleared, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, clearedThread, err := cleared.GetThread(item.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if clearedThread.Bookmarked {
		t.Fatalf("cleared bookmark was persisted as true: %#v", clearedThread)
	}
}

func TestStoreArchivesRestoresAndExpiresThreads(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.AddThread(item.ID, "Third")
	if err != nil {
		t.Fatal(err)
	}

	archivedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	archived, err := store.setThreadArchivedAt(item.ID, second.ID, true, archivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("archived thread = %#v", archived)
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{persisted.Threads[0].ID, persisted.Threads[1].ID, persisted.Threads[2].ID}; got[0] != third.ID || got[1] != item.Threads[0].ID || got[2] != second.ID {
		t.Fatalf("archived thread order = %v", got)
	}
	fourth, err := store.AddThread(item.ID, "Fourth")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 4 || persisted.Threads[0].ID != fourth.ID || persisted.Threads[3].ID != second.ID {
		t.Fatalf("new active thread was not inserted first and before archived threads: %#v", persisted.Threads)
	}

	retentionDays := 7
	if _, err := store.UpdateSettingsValues(SettingsUpdate{ArchivedThreadRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ArchivedThreadsDue(archivedAt.Add(8 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ProjectID != item.ID || due[0].ThreadID != second.ID {
		t.Fatalf("expired archived threads = %#v", due)
	}
	if err := store.DeleteArchivedThread(item.ID, second.ID, archivedAt.Add(-time.Second)); !errors.Is(err, ErrThreadNotArchived) {
		t.Fatalf("early archived deletion error = %v, want ErrThreadNotArchived", err)
	}

	restored, err := store.SetThreadArchived(item.ID, second.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ArchivedAt != nil {
		t.Fatalf("restored thread remained archived: %#v", restored)
	}
	persisted, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Threads[3].ID != second.ID {
		t.Fatalf("restored thread was not placed after active threads: %#v", persisted.Threads)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	settings := reloaded.GetSettings()
	if settings.ArchivedThreadRetentionDays != retentionDays || settings.OrphanedWorktreeRetentionDays != defaultOrphanedWorktreeRetentionDays {
		t.Fatalf("reloaded cleanup settings = %#v", settings)
	}
}

func TestStoreCleansOnlyUnattachedWorktreesWithoutChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := createGitRepository(t)
	store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	cleanThread, err := store.AddThread(item.ID, "Clean orphan", true)
	if err != nil {
		t.Fatal(err)
	}
	dirtyThread, err := store.AddThread(item.ID, "Dirty orphan", true)
	if err != nil {
		t.Fatal(err)
	}
	projectThread, err := store.AddThread(item.ID, "Project orphan", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirtyThread.WorktreePath, "UNTRACKED.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	retentionDays := 0
	if _, err := store.UpdateSettingsValues(SettingsUpdate{OrphanedWorktreeRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteThread(item.ID, cleanThread.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteThread(item.ID, dirtyThread.ID); err != nil {
		t.Fatal(err)
	}

	cleanupAt := time.Now().UTC().Add(48 * time.Hour)
	disabledResult, err := store.CleanupOrphanedWorktrees(cleanupAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledResult.Deleted) != 0 {
		t.Fatalf("disabled cleanup deleted worktrees: %#v", disabledResult.Deleted)
	}
	if _, err := os.Stat(cleanThread.WorktreePath); err != nil {
		t.Fatalf("disabled cleanup removed clean worktree: %v", err)
	}

	retentionDays = 1
	if _, err := store.UpdateSettingsValues(SettingsUpdate{OrphanedWorktreeRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}
	result, err := store.CleanupOrphanedWorktrees(cleanupAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != cleanThread.WorktreePath {
		t.Fatalf("deleted clean worktrees = %#v", result.Deleted)
	}
	if len(result.RetainedWithChanges) != 1 || result.RetainedWithChanges[0] != dirtyThread.WorktreePath {
		t.Fatalf("retained dirty worktrees = %#v", result.RetainedWithChanges)
	}
	if _, err := os.Stat(cleanThread.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clean worktree still exists: %v", err)
	}
	if _, err := os.Stat(dirtyThread.WorktreePath); err != nil {
		t.Fatalf("dirty worktree was removed: %v", err)
	}
	if _, err := os.Stat(projectThread.WorktreePath); err != nil {
		t.Fatalf("attached worktree was removed: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", cleanThread.Branch)); branch == "" {
		t.Fatalf("worktree cleanup deleted branch %q", cleanThread.Branch)
	}

	if err := os.Remove(filepath.Join(dirtyThread.WorktreePath, "UNTRACKED.txt")); err != nil {
		t.Fatal(err)
	}
	result, err = store.CleanupOrphanedWorktrees(cleanupAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != dirtyThread.WorktreePath {
		t.Fatalf("deleted newly clean worktrees = %#v", result.Deleted)
	}
	if _, err := os.Stat(dirtyThread.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("newly clean worktree still exists: %v", err)
	}

	if err := store.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	result, err = store.CleanupOrphanedWorktrees(cleanupAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != projectThread.WorktreePath {
		t.Fatalf("deleted project worktrees = %#v", result.Deleted)
	}
	if _, err := os.Stat(projectThread.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unattached project worktree still exists: %v", err)
	}
}

func TestStoreDoesNotDiscoverAnActiveWorktreeThroughAPathAlias(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	realBase := filepath.Join(root, "real-worktrees")
	if err := os.Mkdir(realBase, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasBase := filepath.Join(root, "worktree-alias")
	if err := os.Symlink(realBase, aliasBase); err != nil {
		t.Skipf("cannot create a worktree path alias: %v", err)
	}

	store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSettings(aliasBase); err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", createGitRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThread(item.ID, "Active alias", true)
	if err != nil {
		t.Fatal(err)
	}
	retentionDays := 1
	if _, err := store.UpdateSettingsValues(SettingsUpdate{OrphanedWorktreeRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}

	if result, err := store.CleanupOrphanedWorktrees(time.Now().UTC().Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	} else if len(result.Deleted) != 0 {
		t.Fatalf("cleanup deleted an attached path alias: %#v", result.Deleted)
	}
	if _, err := os.Stat(thread.WorktreePath); err != nil {
		t.Fatalf("attached path-alias worktree is missing: %v", err)
	}
	if len(store.orphanedWorktrees) != 0 {
		t.Fatalf("attached path-alias worktree was recorded as unattached: %#v", store.orphanedWorktrees)
	}
}

func TestStoreDiscoversPreviouslyUntrackedManagedWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThread(item.ID, "Legacy orphan", true)
	if err != nil {
		t.Fatal(err)
	}

	projects, err := readProjectsFile(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	projects[0].Threads = []Thread{item.Threads[0]}
	contents, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	retentionDays := 1
	if _, err := reloaded.UpdateSettingsValues(SettingsUpdate{OrphanedWorktreeRetentionDays: &retentionDays}); err != nil {
		t.Fatal(err)
	}
	discoveredAt := time.Now().UTC()
	result, err := reloaded.CleanupOrphanedWorktrees(discoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("newly discovered worktree was deleted immediately: %#v", result.Deleted)
	}
	if _, err := os.Stat(thread.WorktreePath); err != nil {
		t.Fatalf("newly discovered worktree is missing: %v", err)
	}

	result, err = reloaded.CleanupOrphanedWorktrees(discoveredAt.Add(48 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != thread.WorktreePath {
		t.Fatalf("deleted discovered worktrees = %#v", result.Deleted)
	}
}

func TestStorePersistsWorktreeBaseLocation(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.GetSettings()
	wantDefault := filepath.Join(filepath.Dir(dataFile), "worktrees")
	if settings.WorktreeBasePath != wantDefault || settings.DefaultWorktreeBasePath != wantDefault || !settings.UsingDefault {
		t.Fatalf("unexpected default settings: %#v", settings)
	}

	customBase := filepath.Join(t.TempDir(), "custom-worktrees")
	settings, err = store.UpdateSettings(customBase)
	if err != nil {
		t.Fatal(err)
	}
	if settings.WorktreeBasePath != customBase || settings.UsingDefault {
		t.Fatalf("unexpected custom settings: %#v", settings)
	}
	if info, err := os.Stat(customBase); err != nil || !info.IsDir() {
		t.Fatalf("custom worktree directory was not created: %v", err)
	}
	archivedDays := 14
	orphanedDays := 0
	if _, err := store.UpdateSettingsValues(SettingsUpdate{
		ArchivedThreadRetentionDays:   &archivedDays,
		OrphanedWorktreeRetentionDays: &orphanedDays,
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if settings := reloaded.GetSettings(); settings.WorktreeBasePath != customBase || settings.UsingDefault ||
		settings.ArchivedThreadRetentionDays != archivedDays || settings.OrphanedWorktreeRetentionDays != orphanedDays {
		t.Fatalf("custom settings were not persisted: %#v", settings)
	}
	settings, err = reloaded.UpdateSettings("")
	if err != nil {
		t.Fatal(err)
	}
	if settings.WorktreeBasePath != wantDefault || !settings.UsingDefault ||
		settings.ArchivedThreadRetentionDays != archivedDays || settings.OrphanedWorktreeRetentionDays != orphanedDays {
		t.Fatalf("settings were not reset to defaults: %#v", settings)
	}
}

func TestStoreRejectsRelativeWorktreeBaseLocation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSettings("relative/worktrees"); err == nil {
		t.Fatal("expected a relative worktree base path to be rejected")
	}
}

func TestStorePersistsTheme(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	defaults := DefaultTheme()
	settings := store.GetSettings()
	if !settings.UsingDefaultTheme || settings.Theme != defaults || settings.DefaultTheme != defaults {
		t.Fatalf("unexpected default theme settings: %#v", settings)
	}
	if defaults.FontSize != 13 || defaults.Colors.Background != "#282c34" || defaults.Colors.Foreground != "#ffffff" {
		t.Fatalf("default theme no longer matches the original terminal appearance: %#v", defaults)
	}

	custom := defaults
	custom.FontFamily = `"Iosevka", monospace`
	custom.FontSize = 17
	custom.Colors.Canvas = "#ABCDEF"
	custom.Colors.Green = "#123456"
	settings, err = store.UpdateTheme(custom)
	if err != nil {
		t.Fatal(err)
	}
	custom.Colors.Canvas = "#abcdef"
	if settings.UsingDefaultTheme || settings.Theme != custom {
		t.Fatalf("unexpected custom theme settings: %#v", settings)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	settings = reloaded.GetSettings()
	if settings.UsingDefaultTheme || settings.Theme != custom {
		t.Fatalf("custom theme was not persisted: %#v", settings)
	}

	settings, err = reloaded.UpdateTheme(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.UsingDefaultTheme || settings.Theme != defaults {
		t.Fatalf("theme was not reset to defaults: %#v", settings)
	}

	contents, err := os.ReadFile(filepath.Join(filepath.Dir(dataFile), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), `"theme"`) {
		t.Fatalf("default theme should not be persisted as an override: %s", contents)
	}
}

func TestStoreRejectsInvalidTheme(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		change func(*Theme)
	}{
		{name: "empty font family", change: func(theme *Theme) { theme.FontFamily = " " }},
		{name: "small font", change: func(theme *Theme) { theme.FontSize = 5 }},
		{name: "large font", change: func(theme *Theme) { theme.FontSize = 73 }},
		{name: "short color", change: func(theme *Theme) { theme.Colors.Red = "#fff" }},
		{name: "invalid color", change: func(theme *Theme) { theme.Colors.Red = "#gggggg" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			theme := DefaultTheme()
			test.change(&theme)
			if _, err := store.UpdateTheme(theme); err == nil {
				t.Fatal("expected invalid theme to be rejected")
			}
		})
	}
	if settings := store.GetSettings(); !settings.UsingDefaultTheme || settings.Theme != DefaultTheme() {
		t.Fatalf("invalid updates changed the theme: %#v", settings)
	}
}

func TestStoreCreatesGitWorktreeThreads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := createGitRepository(t)

	store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	customWorktreeBase := filepath.Join(t.TempDir(), "managed-worktrees")
	if _, err := store.UpdateSettings(customWorktreeBase); err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !item.IsGitRepo {
		t.Fatal("Git repository was not detected")
	}

	thread, err := store.AddThread(item.ID, "Ship worktrees", true)
	if err != nil {
		t.Fatal(err)
	}
	wantWorktreePath := filepath.Join(customWorktreeBase, item.ID, thread.ID)
	if !thread.Worktree || thread.WorktreePath != wantWorktreePath || thread.Cwd != thread.WorktreePath {
		t.Fatalf("unexpected worktree thread: %#v", thread)
	}
	if !strings.HasPrefix(thread.Branch, "dire-mux/ship-worktrees-") {
		t.Fatalf("worktree branch = %q", thread.Branch)
	}
	if _, err := os.Stat(filepath.Join(thread.Cwd, "README.md")); err != nil {
		t.Fatalf("worktree does not contain repository files: %v", err)
	}
	branch := strings.TrimSpace(runGit(t, thread.Cwd, "branch", "--show-current"))
	if branch != thread.Branch {
		t.Fatalf("checked-out branch = %q, want %q", branch, thread.Branch)
	}
}

func TestStoreCreatesGitWorktreeFromSelectedBaseBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := createGitRepository(t)
	initialBranch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--show-current"))
	runGit(t, repositoryPath, "switch", "-c", "feature/base")
	if err := os.WriteFile(filepath.Join(repositoryPath, "BASE_BRANCH.md"), []byte("selected base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repositoryPath, "add", "BASE_BRANCH.md")
	runGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Add base branch file")
	baseRevision := strings.TrimSpace(runGit(t, repositoryPath, "rev-parse", "HEAD"))
	runGit(t, repositoryPath, "switch", initialBranch)

	store, err := NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThreadWithOptions(item.ID, "From selected base", AddThreadOptions{
		Worktree:   true,
		BaseBranch: "feature/base",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(thread.Cwd, "BASE_BRANCH.md")); err != nil {
		t.Fatalf("worktree does not contain the selected base branch: %v", err)
	}
	if revision := strings.TrimSpace(runGit(t, thread.Cwd, "rev-parse", "HEAD")); revision != baseRevision {
		t.Fatalf("worktree revision = %q, want selected base revision %q", revision, baseRevision)
	}

	if _, err := store.AddThreadWithOptions(item.ID, "Missing base", AddThreadOptions{
		Worktree:   true,
		BaseBranch: "missing/base",
	}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing base branch error = %v", err)
	}
}

func TestStoreRecoversWorktreeCreationIntentWhenPrimaryCleanupFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, test := range []struct {
		name      string
		configure func(*Store)
		wantError string
	}{
		{
			name: "metadata save",
			configure: func(store *Store) {
				store.addThreadSave = func() error { return errors.New("injected thread metadata save failure") }
			},
			wantError: "injected thread metadata save failure",
		},
		{
			name: "post-add setup",
			configure: func(store *Store) {
				store.worktreeSetup = func(Thread) error { return errors.New("injected post-add setup failure") }
			},
			wantError: "injected post-add setup failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
			repositoryPath := createGitRepository(t)
			store, err := NewStore(dataFile)
			if err != nil {
				t.Fatal(err)
			}
			item, err := store.Add("Demo", repositoryPath)
			if err != nil {
				t.Fatal(err)
			}
			test.configure(store)
			store.rollbackRemoveWorktree = func(string, Thread) error {
				return errors.New("injected immediate worktree cleanup failure")
			}
			if _, err := store.AddThreadWithOptions(item.ID, "Interrupted worktree", AddThreadOptions{
				Worktree: true, ParentThreadID: item.Threads[0].ID,
			}); err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), "injected immediate worktree cleanup failure") {
				t.Fatalf("interrupted worktree creation error = %v", err)
			}

			persisted, err := readProjectsFile(dataFile)
			if err != nil {
				t.Fatal(err)
			}
			if len(persisted) != 1 || len(persisted[0].Threads) != 1 || persisted[0].Threads[0].ID != item.Threads[0].ID {
				t.Fatalf("failed creation published thread metadata: %#v", persisted)
			}
			records, err := readOrphanedWorktreesFile(filepath.Join(filepath.Dir(dataFile), "orphaned-worktrees.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || !records[0].DeleteBranch || records[0].WorktreePath == "" || records[0].Branch == "" {
				t.Fatalf("worktree creation intent = %#v", records)
			}
			if _, err := os.Stat(records[0].WorktreePath); err != nil {
				t.Fatalf("injected cleanup failure did not leave its worktree for recovery: %v", err)
			}
			if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", records[0].Branch)); branch == "" {
				t.Fatal("injected cleanup failure did not leave its branch for recovery")
			}

			recovered, err := NewStore(dataFile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(records[0].WorktreePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("startup recovery left worktree path: %v", err)
			}
			if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", records[0].Branch)); branch != "" {
				t.Fatalf("startup recovery left transient branch: %q", branch)
			}
			recoveredRecords, err := readOrphanedWorktreesFile(filepath.Join(filepath.Dir(dataFile), "orphaned-worktrees.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(recoveredRecords) != 0 {
				t.Fatalf("startup recovery retained creation intents: %#v", recoveredRecords)
			}
			if project, err := recovered.Get(item.ID); err != nil || len(project.Threads) != 1 {
				t.Fatalf("project after creation-intent recovery = %#v, %v", project, err)
			}
		})
	}
}

func TestStorePendingThreadCreationBlocksMutationUntilCommit(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	depth := 2
	if _, err := store.UpdateSettingsValues(SettingsUpdate{SubAgentNestingDepth: &depth}); err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Pending child", AddThreadOptions{
		ParentThreadID:  item.Threads[0].ID,
		CreationPending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !child.RollbackPending || child.RollbackCleanupReady {
		t.Fatalf("pending child state = %#v", child)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, pending, err := reloaded.GetThread(item.ID, child.ID)
	if err != nil || !pending.RollbackPending || pending.RollbackCleanupReady {
		t.Fatalf("persisted pending child = %#v, %v", pending, err)
	}
	if _, err := reloaded.AddThreadWithOptions(item.ID, "Blocked descendant", AddThreadOptions{ParentThreadID: child.ID}); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending child accepted a descendant: %v", err)
	}
	if _, err := reloaded.UpdateThreadTitle(item.ID, child.ID, "Blocked rename", false); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending child accepted a rename: %v", err)
	}

	committed, err := reloaded.CommitThreadCreation(item.ID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if committed.RollbackPending || committed.RollbackCleanupReady {
		t.Fatalf("committed child state = %#v", committed)
	}
	committedAgain, err := reloaded.CommitThreadCreation(item.ID, child.ID)
	if err != nil || committedAgain.ID != child.ID || committedAgain.RollbackPending {
		t.Fatalf("idempotent commit = %#v, %v", committedAgain, err)
	}
	if _, err := reloaded.UpdateThreadTitle(item.ID, child.ID, "Committed rename", false); err != nil {
		t.Fatalf("committed child rejected a rename: %v", err)
	}
	if _, err := reloaded.AddThreadWithOptions(item.ID, "Allowed descendant", AddThreadOptions{ParentThreadID: child.ID}); err != nil {
		t.Fatalf("committed child rejected a descendant: %v", err)
	}

	finalStore, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted, err := finalStore.GetThread(item.ID, child.ID)
	if err != nil || persisted.RollbackPending || persisted.Title != "Committed rename" {
		t.Fatalf("persisted committed child = %#v, %v", persisted, err)
	}
}

func TestStoreRollbackThreadCreationRemovesTransientGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
		Worktree:       true,
		ParentThreadID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child.WorktreePath); err != nil {
		t.Fatalf("created worktree path: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch == "" {
		t.Fatalf("created worktree branch %q is missing", child.Branch)
	}

	if err := store.RollbackThreadCreation(item.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetThread(item.ID, child.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("rolled back thread error = %v, want ErrThreadNotFound", err)
	}
	if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled back worktree path still exists: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch != "" {
		t.Fatalf("rolled back worktree branch still exists: %q", branch)
	}
	worktrees := runGit(t, repositoryPath, "worktree", "list", "--porcelain")
	if strings.Contains(worktrees, child.WorktreePath) {
		t.Fatalf("rolled back worktree remains registered:\n%s", worktrees)
	}
	orphaned, err := readOrphanedWorktreesFile(filepath.Join(filepath.Dir(dataFile), "orphaned-worktrees.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != 0 {
		t.Fatalf("rollback recorded orphaned worktrees: %#v", orphaned)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 1 || persisted.Threads[0].ID != parent.ID {
		t.Fatalf("persisted threads after rollback = %#v", persisted.Threads)
	}
}

func TestStoreRollbackThreadCreationRecoversAfterPartialArtifactCleanup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
		Worktree: true, ParentThreadID: item.Threads[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.rollbackRemoveWorktree = func(projectPath string, thread Thread) error {
		if _, err := gitOutput(projectPath, "worktree", "remove", "--force", thread.WorktreePath); err != nil {
			return err
		}
		if err := os.RemoveAll(thread.WorktreePath); err != nil {
			return err
		}
		return errors.New("injected prune failure after worktree removal")
	}
	if err := store.RollbackThreadCreation(item.ID, child.ID); err == nil || !strings.Contains(err.Error(), "injected prune failure") {
		t.Fatalf("partial rollback error = %v", err)
	}
	_, pending, err := store.GetThread(item.ID, child.ID)
	if err != nil || !pending.RollbackPending {
		t.Fatalf("pending rollback thread = %#v, %v", pending, err)
	}
	if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partially removed worktree path still exists: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch == "" {
		t.Fatal("partial cleanup unexpectedly removed branch before injected prune failure")
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked descendant", AddThreadOptions{ParentThreadID: child.ID}); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted a child: %v", err)
	}
	if _, err := store.UpdateThreadTitle(item.ID, child.ID, "Blocked rename", false); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted a rename: %v", err)
	}
	if _, err := store.SetThreadArchived(item.ID, child.ID, true); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted archive mutation: %v", err)
	}
	if _, err := store.SetThreadBookmarked(item.ID, child.ID, true); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted bookmark mutation: %v", err)
	}
	if _, err := store.UpdateThreadLimits(item.ID, child.ID, nil, nil); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted limit mutation: %v", err)
	}
	if err := store.DeleteThread(item.ID, child.ID); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted normal deletion: %v", err)
	}
	if err := store.ReorderThreads(item.ID, []string{item.Threads[0].ID, child.ID}); !errors.Is(err, ErrThreadRollbackPending) {
		t.Fatalf("pending rollback accepted reordering: %v", err)
	}

	recovered, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := recovered.GetThread(item.ID, child.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("recovered rollback thread error = %v, want ErrThreadNotFound", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch != "" {
		t.Fatalf("recovery left transient branch: %q", branch)
	}
}

func TestStoreRollbackThreadCreationRecoversAfterInjectedArtifactStageFailures(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, test := range []struct {
		name           string
		worktreeExists bool
		cleanup        func(string, Thread) error
	}{
		{
			name:           "remove",
			worktreeExists: true,
			cleanup: func(string, Thread) error {
				return errors.New("injected worktree remove failure")
			},
		},
		{
			name: "branch",
			cleanup: func(projectPath string, thread Thread) error {
				if _, err := gitOutput(projectPath, "worktree", "remove", "--force", thread.WorktreePath); err != nil {
					return err
				}
				if err := os.RemoveAll(thread.WorktreePath); err != nil {
					return err
				}
				if _, err := gitOutput(projectPath, "worktree", "prune"); err != nil {
					return err
				}
				return errors.New("injected branch delete failure")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
			repositoryPath := createGitRepository(t)
			store, err := NewStore(dataFile)
			if err != nil {
				t.Fatal(err)
			}
			item, err := store.Add("Demo", repositoryPath)
			if err != nil {
				t.Fatal(err)
			}
			child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
				Worktree: true, ParentThreadID: item.Threads[0].ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			store.rollbackRemoveWorktree = test.cleanup
			rollbackErr := store.RollbackThreadCreation(item.ID, child.ID)
			if rollbackErr == nil || !strings.Contains(rollbackErr.Error(), "injected") {
				t.Fatalf("rollback error = %v", rollbackErr)
			}
			_, pending, err := store.GetThread(item.ID, child.ID)
			if err != nil || !pending.RollbackPending || !pending.RollbackCleanupReady {
				t.Fatalf("cleanup-ready rollback thread = %#v, %v", pending, err)
			}
			_, statErr := os.Stat(child.WorktreePath)
			if test.worktreeExists && statErr != nil {
				t.Fatalf("worktree was removed after injected %s failure: %v", test.name, statErr)
			}
			if !test.worktreeExists && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("worktree remained after injected %s failure: %v", test.name, statErr)
			}
			if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch == "" {
				t.Fatalf("branch was removed before injected %s failure", test.name)
			}

			recovered, err := NewStore(dataFile)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := recovered.GetThread(item.ID, child.ID); !errors.Is(err, ErrThreadNotFound) {
				t.Fatalf("recovered rollback thread error = %v, want ErrThreadNotFound", err)
			}
			if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery left worktree path: %v", err)
			}
			if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch != "" {
				t.Fatalf("recovery left branch: %q", branch)
			}
		})
	}
}

func TestStoreRollbackThreadCreationRetriesAfterReadySaveFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
		Worktree: true, ParentThreadID: item.Threads[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.rollbackSave = func(stage string) error {
		if stage == "ready" {
			return errors.New("injected ready save failure")
		}
		return nil
	}
	if err := store.RollbackThreadCreation(item.ID, child.ID); err == nil || !strings.Contains(err.Error(), "injected ready save failure") {
		t.Fatalf("ready rollback error = %v", err)
	}
	_, pending, err := store.GetThread(item.ID, child.ID)
	if err != nil || !pending.RollbackPending || pending.RollbackCleanupReady {
		t.Fatalf("rollback thread after ready save failure = %#v, %v", pending, err)
	}
	if _, err := os.Stat(child.WorktreePath); err != nil {
		t.Fatalf("ready save failure removed worktree: %v", err)
	}

	recovered, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.FinalizeThreadCreationRollback(item.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := recovered.GetThread(item.ID, child.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("retried rollback thread error = %v, want ErrThreadNotFound", err)
	}
	if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retried rollback left worktree path: %v", err)
	}
}

func TestStoreRollbackThreadCreationRecoversAfterFinalizeSaveFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
		Worktree: true, ParentThreadID: item.Threads[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.rollbackSave = func(stage string) error {
		if stage == "finalize" {
			return errors.New("injected finalize save failure")
		}
		return nil
	}
	if err := store.RollbackThreadCreation(item.ID, child.ID); err == nil || !strings.Contains(err.Error(), "injected finalize save failure") {
		t.Fatalf("finalize rollback error = %v", err)
	}
	_, pending, err := store.GetThread(item.ID, child.ID)
	if err != nil || !pending.RollbackPending {
		t.Fatalf("pending rollback thread = %#v, %v", pending, err)
	}
	if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree path remained after cleanup: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch != "" {
		t.Fatalf("branch remained after cleanup: %q", branch)
	}

	recovered, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := recovered.GetThread(item.ID, child.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("recovered rollback thread error = %v, want ErrThreadNotFound", err)
	}
}

func TestStoreRollbackThreadCreationDoesNotCleanBeforeMarkerSave(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	repositoryPath := createGitRepository(t)
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Transient child", AddThreadOptions{
		Worktree: true, ParentThreadID: item.Threads[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.rollbackSave = func(stage string) error {
		if stage == "mark" {
			return errors.New("injected marker save failure")
		}
		return nil
	}
	if err := store.RollbackThreadCreation(item.ID, child.ID); err == nil || !strings.Contains(err.Error(), "injected marker save failure") {
		t.Fatalf("marker rollback error = %v", err)
	}
	_, current, err := store.GetThread(item.ID, child.ID)
	if err != nil || current.RollbackPending {
		t.Fatalf("thread after marker failure = %#v, %v", current, err)
	}
	if _, err := os.Stat(child.WorktreePath); err != nil {
		t.Fatalf("marker failure removed worktree before durable marker: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--list", child.Branch)); branch == "" {
		t.Fatal("marker failure removed branch before durable marker")
	}
}

func TestStorePinsGitWorktreeToFullBaseRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := createGitRepository(t)
	baseRevision := strings.TrimSpace(runGit(t, repositoryPath, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repositoryPath, "LATER.md"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repositoryPath, "add", "LATER.md")
	runGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Advance branch")
	baseBranch := strings.TrimSpace(runGit(t, repositoryPath, "branch", "--show-current"))

	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThreadWithOptions(item.ID, "Pinned child", AddThreadOptions{
		Worktree:     true,
		BaseBranch:   baseBranch,
		BaseRevision: baseRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision := strings.TrimSpace(runGit(t, thread.Cwd, "rev-parse", "HEAD")); revision != baseRevision {
		t.Fatalf("pinned worktree revision = %q, want %q", revision, baseRevision)
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Short revision", AddThreadOptions{Worktree: true, BaseRevision: baseRevision[:12]}); err == nil {
		t.Fatal("expected a short base revision to be rejected")
	}
	missingRevision := strings.Repeat("f", len(baseRevision))
	if _, err := store.AddThreadWithOptions(item.ID, "Missing revision", AddThreadOptions{Worktree: true, BaseRevision: missingRevision}); err == nil {
		t.Fatal("expected a missing base revision to be rejected")
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Revision without worktree", AddThreadOptions{BaseRevision: baseRevision}); err == nil {
		t.Fatal("expected a base revision without a worktree to be rejected")
	}
}

func TestStoreLazilyNamesGitWorktreeThreads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := createGitRepository(t)
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}

	thread, err := store.AddThread(item.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	initialBranch := "dire-mux/thread-" + thread.ID[:8]
	if thread.Title != defaultThreadTitle || thread.Branch != initialBranch || thread.AutoNamed {
		t.Fatalf("unexpected provisional worktree thread: %#v", thread)
	}

	thread, err = store.UpdateThreadTitle(item.ID, thread.ID, "Name worktree from prompt", true)
	if err != nil {
		t.Fatal(err)
	}
	wantBranch := "dire-mux/name-worktree-from-prompt-" + thread.ID[:8]
	if thread.Title != "Name worktree from prompt" || thread.Branch != wantBranch || !thread.AutoNamed {
		t.Fatalf("unexpected named worktree thread: %#v", thread)
	}
	if current := strings.TrimSpace(runGit(t, thread.Cwd, "branch", "--show-current")); current != wantBranch {
		t.Fatalf("checked-out branch = %q, want %q", current, wantBranch)
	}
	if old := strings.TrimSpace(runGit(t, thread.Cwd, "branch", "--list", initialBranch)); old != "" {
		t.Fatalf("provisional branch still exists: %q", old)
	}

	unchanged, err := store.UpdateThreadTitle(item.ID, thread.ID, "Do not rename twice", true)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != thread.Title || unchanged.Branch != thread.Branch {
		t.Fatalf("second generated name changed the thread: %#v", unchanged)
	}

	reloaded, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted, err := reloaded.GetThread(item.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Title != thread.Title || persisted.Branch != thread.Branch || !persisted.AutoNamed {
		t.Fatalf("named worktree thread was not persisted: %#v", persisted)
	}
}

func TestStoreRejectsWorktreesForNonGitProjects(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.AddThread(item.ID, "Not a worktree", true); err == nil {
		t.Fatal("expected a non-Git project to reject worktree creation")
	}
	projects := store.List()
	if len(projects) != 1 || len(projects[0].Threads) != 1 {
		t.Fatalf("failed worktree creation changed the project: %#v", projects)
	}
}

func createGitRepository(t *testing.T) string {
	t.Helper()
	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repositoryPath, "init")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repositoryPath, "add", "README.md")
	runGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Initial commit")
	return repositoryPath
}

func runGit(t *testing.T, path string, arguments ...string) string {
	t.Helper()
	commandArguments := append([]string{"-C", path}, arguments...)
	output, err := exec.Command("git", commandArguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func TestStoreMigratesProjectsWithoutThreads(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "projects.json")
	projectPath := t.TempDir()
	legacy := []Project{{ID: "legacy", Name: "Legacy", Path: projectPath, CreatedAt: time.Now().UTC()}}
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	items := store.List()
	if len(items) != 1 || items[0].ProfileID != PersonalProfileID || len(items[0].Threads) != 1 || items[0].Threads[0].Cwd != projectPath {
		t.Fatalf("legacy project was not migrated: %#v", items)
	}
}

func TestEmptyStoreReturnsAnEmptyList(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}

	items := store.List()
	if items == nil {
		t.Fatal("List returned nil; API clients require an empty JSON array")
	}
	if len(items) != 0 {
		t.Fatalf("expected an empty list, got %#v", items)
	}
}
