package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestSettingsAPIUpdatesWorktreeBaseLocation(t *testing.T) {
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get settings status = %d, body = %s", response.Code, response.Body.String())
	}
	var settings project.Settings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !settings.UsingDefault || settings.WorktreeBasePath != filepath.Join(filepath.Dir(dataFile), "worktrees") {
		t.Fatalf("unexpected default settings: %#v", settings)
	}
	if settings.ArchivedThreadRetentionDays != 30 || settings.OrphanedWorktreeRetentionDays != 30 {
		t.Fatalf("unexpected default cleanup settings: %#v", settings)
	}
	if settings.SubAgentNestingDepth != project.DefaultSubAgentNestingDepth || settings.MaxSubAgentNestingDepth != project.MaxSubAgentNestingDepth {
		t.Fatalf("unexpected default sub-agent settings: %#v", settings)
	}
	if settings.DisableWorkflows || !settings.WorkflowKeywordTrigger || settings.WorkflowSizeGuideline != project.DefaultWorkflowSizeGuideline {
		t.Fatalf("unexpected default workflow settings: %#v", settings)
	}
	if !settings.UsingDefaultTheme || settings.Theme != project.DefaultTheme() || settings.DefaultTheme != project.DefaultTheme() {
		t.Fatalf("unexpected default theme: %#v", settings)
	}

	customBase := filepath.Join(t.TempDir(), "worktrees")
	body, err := json.Marshal(map[string]string{"worktreeBasePath": customBase})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("update settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.UsingDefault || settings.WorktreeBasePath != customBase {
		t.Fatalf("unexpected updated settings: %#v", settings)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"archivedThreadRetentionDays":14,"orphanedWorktreeRetentionDays":0}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("cleanup settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.ArchivedThreadRetentionDays != 14 || settings.OrphanedWorktreeRetentionDays != 0 {
		t.Fatalf("unexpected cleanup settings: %#v", settings)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"subAgentNestingDepth":3}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("sub-agent settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.SubAgentNestingDepth != 3 {
		t.Fatalf("unexpected sub-agent settings: %#v", settings)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPut,
		"/api/settings",
		bytes.NewBufferString(`{"disableWorkflows":true,"workflowKeywordTriggerEnabled":false,"workflowSizeGuideline":"medium"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("workflow settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !settings.DisableWorkflows || settings.WorkflowKeywordTrigger || settings.WorkflowSizeGuideline != "medium" {
		t.Fatalf("unexpected workflow settings: %#v", settings)
	}
	reloaded, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSettings := reloaded.GetSettings()
	if !reloadedSettings.DisableWorkflows || reloadedSettings.WorkflowKeywordTrigger || reloadedSettings.WorkflowSizeGuideline != "medium" {
		t.Fatalf("workflow settings were not persisted: %#v", reloadedSettings)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"workflowSizeGuideline":"huge"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid workflow size status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"subAgentNestingDepth":5}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid nesting status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"archivedThreadRetentionDays":-1}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid retention status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"worktreeBasePath":"relative/path"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("relative path status = %d, body = %s", response.Code, response.Body.String())
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
