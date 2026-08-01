package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectImportsLegacyRelatedProjects(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".config", "kiwi-sandbox.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"network":false,"relatedProjects":[" ../shared ","$HOME/personal"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"../shared", "$HOME/personal"}
	if !reflect.DeepEqual(item.RelatedProjects, want) {
		t.Fatalf("imported related projects = %#v, want %#v", item.RelatedProjects, want)
	}
}

func TestProjectRelatedProjectsPersistNormalizedPaths(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "projects.json")
	store, err := NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{" ../shared ", "$HOME/personal", "../shared"}
	updated, err := store.UpdateProject(item.ID, ProjectUpdate{RelatedProjects: &paths})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"../shared", "$HOME/personal"}
	if !reflect.DeepEqual(updated.RelatedProjects, want) {
		t.Fatalf("related projects = %#v, want %#v", updated.RelatedProjects, want)
	}

	reloaded, err := NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.RelatedProjects, want) {
		t.Fatalf("persisted related projects = %#v, want %#v", persisted.RelatedProjects, want)
	}

	invalid := []string{""}
	if _, err := reloaded.UpdateProject(item.ID, ProjectUpdate{RelatedProjects: &invalid}); err == nil {
		t.Fatal("empty related project path was accepted")
	}
}
