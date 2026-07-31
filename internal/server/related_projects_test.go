package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestCodingAgentRelatedProjectDirectoriesExpandsAndDeduplicatesPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threadRoot := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(threadRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	item := project.Project{RelatedProjects: []string{
		"../shared",
		"$CWD/../shared",
		"~/personal",
		"$HOME/personal",
		"$TMPDIR/cache",
	}}
	got, err := codingAgentRelatedProjectDirectories(item, project.Thread{Cwd: "/ignored", WorktreePath: threadRoot})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Clean(filepath.Join(threadRoot, "../shared")),
		filepath.Join(home, "personal"),
		filepath.Join(os.TempDir(), "cache"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("related directories = %#v, want %#v", got, want)
	}
}
