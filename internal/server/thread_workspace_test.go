package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestThreadWorkspaceAPI(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Workspace", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + item.ID + "/threads/" + item.Threads[0].ID + "/workspace"
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"codingAgent":"codex","activeTab":"terminal"}`, http.StatusOK},
		{`{"codingAgent":"pi-native","activeTab":"pi","initialize":true}`, http.StatusOK},
		{`{"activeTab":"process"}`, http.StatusOK},
		{`{"activeTab":"bogus"}`, http.StatusBadRequest},
		{`{"codingAgent":"bogus"}`, http.StatusBadRequest},
		{`{}`, http.StatusBadRequest},
		{`{"other":"pi"}`, http.StatusBadRequest},
		{`{"activeTab":"pi"} {}`, http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(test.body)))
		if response.Code != test.status {
			t.Fatalf("%s: status %d: %s", test.body, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, strings.TrimSuffix(path, "/workspace"), nil))
	var thread project.Thread
	if err := json.Unmarshal(response.Body.Bytes(), &thread); err != nil {
		t.Fatal(err)
	}
	if thread.CodingAgent != "codex" || thread.ActiveTab != "process" {
		t.Fatalf("workspace changed unexpectedly: %+v", thread)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, strings.Replace(path, item.Threads[0].ID, "missing", 1), strings.NewReader(`{"activeTab":"pi"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing thread: %d", response.Code)
	}
}
