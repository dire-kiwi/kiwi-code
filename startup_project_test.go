package main

import (
	"path/filepath"
	"testing"

	"github.com/ivan/dire-mux/internal/project"
)

func TestEnsureProjectPathAddsCurrentDirectoryOnce(t *testing.T) {
	dataDirectory := t.TempDir()
	projectDirectory := t.TempDir()
	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}

	created, added, err := ensureProjectPath(store, projectDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("new current directory project was reported as already present")
	}
	if created.Name != filepath.Base(projectDirectory) || created.Path != projectDirectory {
		t.Fatalf("created project = %#v", created)
	}
	if created.ProfileID != project.PersonalProfileID {
		t.Fatalf("created project profile = %q, want %q", created.ProfileID, project.PersonalProfileID)
	}
	if len(created.Threads) != 1 || created.Threads[0].Cwd != projectDirectory {
		t.Fatalf("created project threads = %#v", created.Threads)
	}

	// Reopen the store to model a development backend restart using the same
	// isolated data directory.
	reopened, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	existing, added, err := ensureProjectPath(reopened, filepath.Join(projectDirectory, "."))
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("existing current directory project was added again")
	}
	if existing.ID != created.ID || len(reopened.List()) != 1 {
		t.Fatalf("project changed across restart: created=%#v existing=%#v all=%#v", created, existing, reopened.List())
	}
}

func TestEnsureProjectPathRejectsMissingDirectory(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ensureProjectPath(store, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing current directory project was accepted")
	}
}
