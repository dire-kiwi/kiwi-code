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

func TestSettingsAPIUpdatesCodingAgents(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	workDirectory := filepath.Join(t.TempDir(), "claude-work")
	body, err := json.Marshal(map[string]any{"codingAgents": []map[string]any{
		{"id": "pi-native", "name": "Pi Native", "kind": "pi-native"},
		{"id": "work", "name": "Work", "kind": "claude", "configDirectory": workDirectory, "isDefault": true},
		{"id": "gpt", "name": "GPT", "kind": "claude-gpt"},
		{"id": "pi", "name": "Pi", "kind": "pi"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("update coding agents status = %d, body = %s", response.Code, response.Body.String())
	}
	var settings project.Settings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.CodingAgents) != 6 || settings.CodingAgents[0].Kind != project.CodingAgentKindPiNative ||
		settings.CodingAgents[1].Name != "Work" || settings.CodingAgents[1].ConfigDirectory != workDirectory || !settings.CodingAgents[1].IsDefault ||
		settings.CodingAgents[2].Kind != project.CodingAgentKindClaudeGPT ||
		settings.CodingAgents[3].Kind != project.CodingAgentKindPi ||
		settings.CodingAgents[4].Kind != project.CodingAgentKindCodex ||
		settings.CodingAgents[5].Kind != project.CodingAgentKindGrok {
		t.Fatalf("coding agents = %#v", settings.CodingAgents)
	}
	if info, err := os.Stat(workDirectory); err != nil || !info.IsDir() {
		t.Fatalf("Claude Code config directory was not created: info=%v err=%v", info, err)
	}
	reloaded, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if agents := reloaded.GetSettings().CodingAgents; len(agents) != len(settings.CodingAgents) {
		t.Fatalf("persisted coding agents = %#v", agents)
	} else {
		for index := range agents {
			if agents[index] != settings.CodingAgents[index] {
				t.Fatalf("persisted coding agents = %#v", agents)
			}
		}
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"codingAgents":[{"id":"one","name":"Work","kind":"claude","configDirectory":"/tmp/one"},{"id":"two","name":"work","kind":"claude-gpt"}]}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate coding agent status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestSettingsAPIUpdatesThemeIndependently(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	theme := project.DefaultTheme()
	theme.FontSize = 18
	theme.Colors.Background = "#101820"
	body, err := json.Marshal(map[string]any{"theme": theme})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("update theme status = %d, body = %s", response.Code, response.Body.String())
	}
	var settings project.Settings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.UsingDefaultTheme || settings.Theme != theme {
		t.Fatalf("unexpected updated theme: %#v", settings)
	}
	if !settings.UsingDefault {
		t.Fatalf("theme update changed worktree settings: %#v", settings)
	}

	invalid := theme
	invalid.Colors.Cyan = "cyan"
	body, err = json.Marshal(map[string]any{"theme": invalid})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid theme status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty settings status = %d, body = %s", response.Code, response.Body.String())
	}
}
