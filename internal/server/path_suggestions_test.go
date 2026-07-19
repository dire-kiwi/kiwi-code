package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectorySuggestions(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Alpha", "alpine", "beta", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "alphabet.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "beta"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "matching prefix", value: filepath.Join(root, "al"), want: []string{"alias", "Alpha", "alpine"}},
		{name: "directory contents", value: root + string(filepath.Separator), want: []string{"alias", "Alpha", "alpine", "beta"}},
		{name: "hidden prefix", value: root + string(filepath.Separator) + ".", want: []string{".hidden"}},
		{name: "exact directory", value: filepath.Join(root, "beta"), want: []string{"beta"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			suggestions, err := directorySuggestions(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if len(suggestions) != len(test.want) {
				t.Fatalf("suggestions = %#v, want names %v", suggestions, test.want)
			}
			for index, want := range test.want {
				if suggestions[index].Name != want || suggestions[index].Path != filepath.Join(root, want) {
					t.Fatalf("suggestion %d = %#v, want name %q in %q", index, suggestions[index], want, root)
				}
			}
		})
	}
}

func TestDirectorySuggestionsUseHomeForEmptyAndTildePaths(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	for _, value := range []string{"", "~", "~/"} {
		suggestions, err := directorySuggestions(value)
		if err != nil {
			t.Fatalf("directorySuggestions(%q): %v", value, err)
		}
		if len(suggestions) != 1 || suggestions[0].Path != filepath.Join(home, "code") {
			t.Fatalf("directorySuggestions(%q) = %#v", value, suggestions)
		}
	}
}

func TestDirectorySuggestionsAreLimited(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < maxPathSuggestions+5; index++ {
		name := filepath.Join(root, strings.Repeat("x", 3)+string(rune('a'+index%26))+strings.Repeat("x", index/26))
		if err := os.Mkdir(name, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	suggestions, err := directorySuggestions(root + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != maxPathSuggestions {
		t.Fatalf("got %d suggestions, want %d", len(suggestions), maxPathSuggestions)
	}
}

func TestDirectorySuggestionsAPIHandlesPartialAndLongPaths(t *testing.T) {
	server := &Server{}

	missingRequest := httptest.NewRequest(http.MethodGet, "/api/filesystem/directories?path="+url.QueryEscape(filepath.Join(t.TempDir(), "missing", "child")), nil)
	missingResponse := httptest.NewRecorder()
	server.listDirectorySuggestions(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusOK {
		t.Fatalf("missing path status = %d, body = %s", missingResponse.Code, missingResponse.Body.String())
	}
	var result directorySuggestionsResponse
	if err := json.NewDecoder(missingResponse.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Suggestions == nil || len(result.Suggestions) != 0 {
		t.Fatalf("missing path suggestions = %#v, want an empty array", result.Suggestions)
	}

	longRequest := httptest.NewRequest(http.MethodGet, "/api/filesystem/directories?path="+strings.Repeat("x", maxPathSuggestionQueryBytes+1), nil)
	longResponse := httptest.NewRecorder()
	server.listDirectorySuggestions(longResponse, longRequest)
	if longResponse.Code != http.StatusBadRequest {
		t.Fatalf("long path status = %d, body = %s", longResponse.Code, longResponse.Body.String())
	}
}
