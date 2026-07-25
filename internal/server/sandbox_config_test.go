package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestGlobalSandboxConfigAPIRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sandbox/config", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
	var state sandboxConfigState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Scope != "global" || state.Exists || state.ParseError != "" {
		t.Fatalf("unexpected initial state: %#v", state)
	}
	if state.Path != filepath.Join(home, ".config", "kiwi-sandbox", "sandbox.json") {
		t.Fatalf("unexpected config path: %q", state.Path)
	}
	if !state.Effective.Network || state.Effective.Shell != "/bin/zsh" {
		t.Fatalf("unexpected effective defaults: %#v", state.Effective)
	}

	body := []byte(`{
		"network": false,
		"shell": "/bin/bash",
		"defaults": {"read": ["$CWD", " /extra "], "write": ["$CWD", ""]},
		"commands": [{"patterns": ["npm *", "pnpm *"], "network": true}]
	}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/sandbox/config", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Exists || state.ParseError != "" {
		t.Fatalf("unexpected saved state: %#v", state)
	}
	if state.Config.Network == nil || *state.Config.Network || state.Effective.Network {
		t.Fatalf("network was not persisted: %#v", state)
	}
	if state.Config.Defaults == nil ||
		len(state.Config.Defaults.Read) != 2 || state.Config.Defaults.Read[1] != "/extra" ||
		len(state.Config.Defaults.Write) != 1 {
		t.Fatalf("defaults were not trimmed: %#v", state.Config.Defaults)
	}
	if state.Config.Commands == nil || len(*state.Config.Commands) != 1 ||
		len((*state.Config.Commands)[0].Patterns) != 2 {
		t.Fatalf("commands were not persisted: %#v", state.Config.Commands)
	}
	if state.Effective.Shell != "/bin/bash" {
		t.Fatalf("effective shell = %q", state.Effective.Shell)
	}

	// The on-disk file must stay loadable by the sandbox's own schema: pattern
	// arrays use the "pattern" key and unknown keys are rejected.
	data, err := os.ReadFile(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	commands, ok := disk["commands"].([]any)
	if !ok || len(commands) != 1 {
		t.Fatalf("unexpected disk commands: %#v", disk["commands"])
	}
	rule, ok := commands[0].(map[string]any)
	if !ok || rule["patterns"] != nil || rule["pattern"] == nil {
		t.Fatalf("disk rule must use the pattern key: %#v", commands[0])
	}

	// Related projects are rejected at the global scope.
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/sandbox/config",
		bytes.NewReader([]byte(`{"relatedProjects": ["../other"]}`))))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("related projects status = %d, body = %s", response.Code, response.Body.String())
	}

	// Clearing every field removes the file.
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/sandbox/config", bytes.NewReader([]byte(`{}`))))
	if response.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Exists {
		t.Fatalf("config file should be removed: %#v", state)
	}
	if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
		t.Fatalf("config file still present: %v", err)
	}
}

func TestGlobalSandboxConfigAPIRejectsInvalidInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"relative shell": `{"shell": "zsh"}`,
		"empty pattern":  `{"commands": [{"patterns": ["  "]}]}`,
		"unknown field":  `{"defaultz": {}}`,
		"malformed":      `{"network": "yes"}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/sandbox/config",
			bytes.NewReader([]byte(body))))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, body = %s", name, response.Code, response.Body.String())
		}
	}
}

func TestGlobalSandboxConfigAPISurfacesParseErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "kiwi-sandbox", "sandbox.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"network": "broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sandbox/config", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
	var state sandboxConfigState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Exists || state.ParseError == "" {
		t.Fatalf("expected a parse error: %#v", state)
	}
}

func TestThreadSandboxConfigAPIOverlaysGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPath := filepath.Join(home, ".config", "kiwi-sandbox", "sandbox.json")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"network": false, "shell": "/bin/bash"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/sandbox/config"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
	var state sandboxConfigState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Scope != "thread" || state.Exists {
		t.Fatalf("unexpected initial thread state: %#v", state)
	}
	if state.Path != filepath.Join(thread.Cwd, ".config", "kiwi-sandbox.json") {
		t.Fatalf("unexpected thread config path: %q", state.Path)
	}
	if state.Effective.Network || state.Effective.Shell != "/bin/bash" {
		t.Fatalf("global config not reflected in effective policy: %#v", state.Effective)
	}

	body := []byte(`{"network": true, "relatedProjects": ["../shared-library"]}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, url, bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Exists || !state.Effective.Network || state.Effective.Shell != "/bin/bash" {
		t.Fatalf("thread overlay not applied: %#v", state)
	}
	if len(state.Effective.RelatedProjects) != 1 || state.Effective.RelatedProjects[0] != "../shared-library" {
		t.Fatalf("related projects not persisted: %#v", state.Effective.RelatedProjects)
	}
	if _, err := os.Stat(filepath.Join(thread.Cwd, ".config", "kiwi-sandbox.json")); err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/projects/"+item.ID+"/threads/missing/sandbox/config", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing thread status = %d", response.Code)
	}
}
