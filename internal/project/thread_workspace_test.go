package project

import (
	"path/filepath"
	"testing"
	"time"
)

func TestThreadWorkspacePersistsAndBroadcasts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Workspace", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	threadID := item.Threads[0].ID
	first, closeFirst := store.SubscribeChanges()
	defer closeFirst()
	second, closeSecond := store.SubscribeChanges()
	defer closeSecond()
	update := ThreadWorkspaceUpdate{CodingAgent: "codex", ActiveTab: "terminal"}
	if _, err := store.UpdateThreadWorkspace(item.ID, threadID, update); err != nil {
		t.Fatal(err)
	}
	for _, client := range []<-chan []Project{first, second} {
		select {
		case snapshot := <-client:
			thread := snapshot[0].Threads[0]
			if thread.CodingAgent != "codex" || thread.ActiveTab != "terminal" {
				t.Fatalf("snapshot: %+v", thread)
			}
		case <-time.After(time.Second):
			t.Fatal("workspace change was not broadcast")
		}
	}
	// An old browser opening the thread must not reset either server value.
	if _, err := store.UpdateThreadWorkspace(item.ID, threadID, ThreadWorkspaceUpdate{CodingAgent: "pi-native", ActiveTab: "pi", Initialize: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
		t.Fatal("no-op initialization broadcast a change")
	default:
	}
	if _, err := store.UpdateThreadWorkspace(item.ID, threadID, ThreadWorkspaceUpdate{ActiveTab: "process"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, thread, err := reloaded.GetThread(item.ID, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if thread.CodingAgent != "codex" || thread.ActiveTab != "process" {
		t.Fatalf("persisted workspace: %+v", thread)
	}
	// A subsequent agent-only update preserves the tab.
	thread, err = reloaded.UpdateThreadWorkspace(item.ID, threadID, ThreadWorkspaceUpdate{CodingAgent: "claude-native"})
	if err != nil {
		t.Fatal(err)
	}
	if thread.ActiveTab != "process" || thread.CodingAgent != "claude-native" {
		t.Fatalf("agent patch: %+v", thread)
	}
}

func TestThreadWorkspaceValidation(t *testing.T) {
	for _, update := range []ThreadWorkspaceUpdate{
		{}, {ActiveTab: "shell"}, {ActiveTab: "unknown"}, {CodingAgent: "unknown"},
		{CodingAgent: "claude-profile-"}, {CodingAgent: "claude-profile-../../bad"},
	} {
		if update.Validate() == nil {
			t.Fatalf("accepted invalid update: %+v", update)
		}
	}
	for _, agent := range []string{"pi", "pi-native", "codex", "grok", "claude", "claude-native", "claude-gpt", "claude-profile-personal", "claude-gpt-profile-work"} {
		if err := (ThreadWorkspaceUpdate{CodingAgent: agent}).Validate(); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
	}
}
