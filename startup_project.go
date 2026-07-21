package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// ensureProjectPath adds directory to the project store unless that exact
// absolute path is already present. The idempotent lookup keeps development
// backend restarts from replacing the initial project or thread.
func ensureProjectPath(store *project.Store, directory string) (project.Project, bool, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return project.Project{}, false, fmt.Errorf("project directory is required")
	}
	absolutePath, err := filepath.Abs(directory)
	if err != nil {
		return project.Project{}, false, fmt.Errorf("resolve project directory: %w", err)
	}
	absolutePath = filepath.Clean(absolutePath)

	if item, found := projectAtPath(store.List(), absolutePath); found {
		return item, false, nil
	}

	item, err := store.Add("", absolutePath)
	if err == nil {
		return item, true, nil
	}

	// Add reloads the persisted project list while holding the mutation lock.
	// Recheck after an error so a concurrent startup, or an error returned after
	// a committed save, is still treated as an already-satisfied request.
	if existing, found := projectAtPath(store.List(), absolutePath); found {
		return existing, false, nil
	}
	return project.Project{}, false, fmt.Errorf("add project %q: %w", absolutePath, err)
}

func projectAtPath(projects []project.Project, path string) (project.Project, bool) {
	for _, item := range projects {
		if filepath.Clean(item.Path) == path {
			return item, true
		}
	}
	return project.Project{}, false
}
