package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const (
	maxPathSuggestionQueryBytes = 4 << 10
	maxPathSuggestions          = 50
)

type directorySuggestion struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type directorySuggestionsResponse struct {
	Suggestions []directorySuggestion `json:"suggestions"`
}

func (s *Server) listDirectorySuggestions(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("path")
	if len(value) > maxPathSuggestionQueryBytes {
		writeError(w, http.StatusBadRequest, "Project path is too long.")
		return
	}

	suggestions, err := directorySuggestions(value)
	if err != nil {
		// Missing, inaccessible, and not-a-directory path segments are all normal
		// while someone is in the middle of typing a path.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR) {
			writeJSON(w, http.StatusOK, directorySuggestionsResponse{Suggestions: []directorySuggestion{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not autocomplete the project path.")
		return
	}

	writeJSON(w, http.StatusOK, directorySuggestionsResponse{Suggestions: suggestions})
}

func directorySuggestions(value string) ([]directorySuggestion, error) {
	searchDirectory, prefix, err := directorySuggestionSearch(value)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(searchDirectory)
	if err != nil {
		return nil, err
	}

	foldedPrefix := strings.ToLower(prefix)
	showHidden := strings.HasPrefix(prefix, ".")
	suggestions := make([]directorySuggestion, 0)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !showHidden {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), foldedPrefix) {
			continue
		}

		path := filepath.Join(searchDirectory, name)
		isDirectory := entry.IsDir()
		if !isDirectory && entry.Type()&os.ModeSymlink != 0 {
			info, statErr := os.Stat(path)
			isDirectory = statErr == nil && info.IsDir()
		}
		if !isDirectory {
			continue
		}

		suggestions = append(suggestions, directorySuggestion{Name: name, Path: path})
	}

	sort.Slice(suggestions, func(i, j int) bool {
		leftExact := strings.EqualFold(suggestions[i].Name, prefix)
		rightExact := strings.EqualFold(suggestions[j].Name, prefix)
		if leftExact != rightExact {
			return leftExact
		}
		left := strings.ToLower(suggestions[i].Name)
		right := strings.ToLower(suggestions[j].Name)
		if left == right {
			return suggestions[i].Name < suggestions[j].Name
		}
		return left < right
	})
	if len(suggestions) > maxPathSuggestions {
		suggestions = suggestions[:maxPathSuggestions]
	}
	return suggestions, nil
}

func directorySuggestionSearch(value string) (directory, prefix string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		home, err := os.UserHomeDir()
		return home, "", err
	}

	separator := string(filepath.Separator)
	browseDirectory := strings.HasSuffix(value, separator)
	if value == "~" || strings.HasPrefix(value, "~"+separator) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		if value == "~" {
			value = home
			browseDirectory = true
		} else {
			value = home + separator + strings.TrimPrefix(value, "~"+separator)
		}
	}

	if browseDirectory {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", "", err
		}
		return filepath.Clean(absolute), "", nil
	}

	directoryPart, prefix := filepath.Split(value)
	if directoryPart == "" {
		directoryPart = "."
	}
	absoluteDirectory, err := filepath.Abs(directoryPart)
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(absoluteDirectory), prefix, nil
}
