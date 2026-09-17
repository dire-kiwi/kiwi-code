package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestFileUpload(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Uploads", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	var previous string
	for _, name := range []string{"report notes.pdf", "../../report notes.pdf", `C:\private\report notes.pdf`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/files?name="+url.QueryEscape(name), bytes.NewBufferString("arbitrary file contents"))
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.Path)) })
		if !filepath.IsAbs(result.Path) || filepath.Base(result.Path) != "report notes.pdf" || filepath.Dir(result.Path) == filepath.Dir(previous) {
			t.Fatalf("unsafe or colliding path %q", result.Path)
		}
		contents, err := os.ReadFile(result.Path)
		if err != nil || string(contents) != "arbitrary file contents" {
			t.Fatalf("contents %q, error %v", contents, err)
		}
		info, _ := os.Stat(result.Path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("permissions %v", info.Mode())
		}
		previous = result.Path
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/projects/missing/files?name=x", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing project status %d", response.Code)
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/files?name=huge", nil)
	request.ContentLength = maxPiImageBytes + 1
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status %d", response.Code)
	}
}
