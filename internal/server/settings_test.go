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

func TestSettingsAPIUpdatesTitleModel(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleModel":"anthropic/claude-sonnet-5"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("update title model status = %d, body = %s", response.Code, response.Body.String())
	}
	var settings project.Settings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.TitleModel != "anthropic/claude-sonnet-5" || settings.DefaultTitleModel != project.DefaultTitleModel {
		t.Fatalf("unexpected title model settings: %#v", settings)
	}
	if !settings.UsingDefault {
		t.Fatalf("title model update changed worktree settings: %#v", settings)
	}
	if store.TitleModel() != "anthropic/claude-sonnet-5" {
		t.Fatalf("store title model = %q", store.TitleModel())
	}

	reloaded, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.GetSettings().TitleModel != "anthropic/claude-sonnet-5" {
		t.Fatalf("title model was not persisted: %#v", reloaded.GetSettings())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleModel":"not-a-model"}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid title model status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleModel":""}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("reset title model status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.TitleModel() != project.DefaultTitleModel {
		t.Fatalf("store title model after reset = %q", store.TitleModel())
	}
}

func TestSettingsAPIUpdatesTitleThinking(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleThinking":"high"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("update title thinking status = %d, body = %s", response.Code, response.Body.String())
	}
	var settings project.Settings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.TitleThinking != "high" || settings.DefaultTitleThinking != project.DefaultTitleThinking {
		t.Fatalf("unexpected title thinking settings: %#v", settings)
	}
	if store.TitleThinking() != "high" {
		t.Fatalf("store title thinking = %q", store.TitleThinking())
	}

	reloaded, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.GetSettings().TitleThinking != "high" {
		t.Fatalf("title thinking was not persisted: %#v", reloaded.GetSettings())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleThinking":"ultra"}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid title thinking status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"titleThinking":""}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("reset title thinking status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.TitleThinking() != project.DefaultTitleThinking {
		t.Fatalf("store title thinking after reset = %q", store.TitleThinking())
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
