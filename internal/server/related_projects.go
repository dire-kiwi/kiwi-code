package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// codingAgentRelatedProjectDirectories expands a project's related-project
// paths into the absolute directories accepted by coding agents' --add-dir
// flags. Relative paths and $CWD are resolved for the thread so worktree
// sessions keep the same behavior as regular workspace sessions.
func codingAgentRelatedProjectDirectories(item project.Project, thread project.Thread) ([]string, error) {
	if len(item.RelatedProjects) == 0 {
		return nil, nil
	}

	root := thread.WorktreePath
	if root == "" {
		root = thread.Cwd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve thread directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	directories := make([]string, 0, len(item.RelatedProjects))
	seen := make(map[string]struct{}, len(item.RelatedProjects))
	for _, configured := range item.RelatedProjects {
		expanded := strings.ReplaceAll(configured, "$CWD", root)
		expanded = strings.ReplaceAll(expanded, "$HOME", home)
		expanded = strings.ReplaceAll(expanded, "$TMPDIR", os.TempDir())
		if expanded == "~" {
			expanded = home
		} else if strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		} else if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(root, expanded)
		}
		expanded = filepath.Clean(expanded)
		if _, exists := seen[expanded]; exists {
			continue
		}
		seen[expanded] = struct{}{}
		directories = append(directories, expanded)
	}
	return directories, nil
}
