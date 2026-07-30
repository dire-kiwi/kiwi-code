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

func TestProjectAPI(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	projectPath := filepath.Join(t.TempDir(), "new", "project")
	body, err := json.Marshal(map[string]string{"name": "Demo", "path": projectPath})
	if err != nil {
		t.Fatal(err)
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(body))
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	var created project.Project
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "Demo" || created.Path != projectPath || created.ProfileID != project.PersonalProfileID {
		t.Fatalf("unexpected project: %#v", created)
	}
	if len(created.Threads) != 1 || created.Threads[0].Cwd != projectPath {
		t.Fatalf("unexpected initial thread: %#v", created.Threads)
	}
	if info, err := os.Stat(projectPath); err != nil {
		t.Fatalf("created project directory: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("created project path is not a directory: %v", info.Mode())
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d", listResponse.Code)
	}
	var projects []project.Project
	if err := json.NewDecoder(listResponse.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID {
		t.Fatalf("unexpected project list: %#v", projects)
	}

	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/projects/"+created.ID, nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestProjectEnvironmentAPI(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	environment := project.LocalEnvironment{
		Name:           "Development",
		SetupScripts:   project.PlatformScripts{Default: "npm install"},
		CleanupScripts: project.PlatformScripts{Default: "rm -rf .cache"},
		Variables:      []project.EnvironmentVariable{{Name: "APP_MODE", Value: "test"}},
		Actions: []project.EnvironmentAction{{
			ID: "run-tests", Name: "Run tests", Scripts: project.PlatformScripts{Default: `printf '%s' "$APP_MODE"`},
		}},
	}
	body, err := json.Marshal(map[string]any{"environment": environment})
	if err != nil {
		t.Fatal(err)
	}
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, httptest.NewRequest(http.MethodPatch, "/api/projects/"+item.ID, bytes.NewReader(body)))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update environment status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated project.Project
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Environment.Name != "Development" || len(updated.Environment.Actions) != 1 {
		t.Fatalf("updated environment = %#v", updated.Environment)
	}

	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, command, variables, err := project.ResolveEnvironmentAction(persisted, item.Threads[0], "run-tests")
	if err != nil {
		t.Fatal(err)
	}
	if action.Name != "Run tests" || command == "" || len(variables) < 2 {
		t.Fatalf("resolved environment action = %#v, command = %q, variables = %#v", action, command, variables)
	}
}

func TestProjectAndThreadOrderAPI(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Add("First", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Add("Second", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lastThread, err := store.AddThread(first.ID, "Last")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	projectBody, err := json.Marshal(map[string]any{
		"profileId":  project.PersonalProfileID,
		"projectIds": []string{second.ID, first.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	projectResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectResponse, httptest.NewRequest(http.MethodPut, "/api/projects/order", bytes.NewReader(projectBody)))
	if projectResponse.Code != http.StatusNoContent {
		t.Fatalf("project order status = %d, body = %s", projectResponse.Code, projectResponse.Body.String())
	}
	if projects := store.List(); len(projects) != 2 || projects[0].ID != second.ID || projects[1].ID != first.ID {
		t.Fatalf("project order = %#v", projects)
	}

	threadBody, err := json.Marshal(map[string]any{
		"threadIds": []string{lastThread.ID, first.Threads[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	threadResponse := httptest.NewRecorder()
	threadPath := "/api/projects/" + first.ID + "/threads/order"
	handler.ServeHTTP(threadResponse, httptest.NewRequest(http.MethodPut, threadPath, bytes.NewReader(threadBody)))
	if threadResponse.Code != http.StatusNoContent {
		t.Fatalf("thread order status = %d, body = %s", threadResponse.Code, threadResponse.Body.String())
	}
	item, err := store.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Threads) != 2 || item.Threads[0].ID != lastThread.ID || item.Threads[1].ID != first.Threads[0].ID {
		t.Fatalf("thread order = %#v", item.Threads)
	}

	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, httptest.NewRequest(http.MethodPut, threadPath, bytes.NewBufferString(`{"threadIds":[]}`)))
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid thread order status = %d, body = %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, httptest.NewRequest(http.MethodPut, "/api/projects/missing/threads/order", bytes.NewBufferString(`{"threadIds":[]}`)))
	if unknownResponse.Code != http.StatusNotFound {
		t.Fatalf("missing project order status = %d, body = %s", unknownResponse.Code, unknownResponse.Body.String())
	}
}

func TestProjectAPIRejectsUnknownFields(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{"path":"/tmp","extra":true}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPiImageUpload(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/pi/images", bytes.NewReader(image))
	request.Header.Set("Content-Type", "image/png")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}

	var upload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&upload); err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(upload.Path) != ".png" {
		t.Fatalf("uploaded image path = %q, want a .png file", upload.Path)
	}
	t.Cleanup(func() { _ = os.Remove(upload.Path) })
	stored, err := os.ReadFile(upload.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, image) {
		t.Fatalf("stored image = %v, want %v", stored, image)
	}

	unsupportedRequest := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/pi/images", bytes.NewBufferString("not an image"))
	unsupportedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unsupportedResponse, unsupportedRequest)
	if unsupportedResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported upload status = %d, body = %s", unsupportedResponse.Code, unsupportedResponse.Body.String())
	}

	oversizedRequest := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/pi/images", nil)
	oversizedRequest.ContentLength = maxPiImageBytes + 1
	oversizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, body = %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestPiImageExtension(t *testing.T) {
	tests := []struct {
		name      string
		contents  []byte
		extension string
		supported bool
	}{
		{name: "png", contents: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, extension: "png", supported: true},
		{name: "jpeg", contents: []byte{0xff, 0xd8, 0xff, 0}, extension: "jpg", supported: true},
		{name: "gif", contents: []byte("GIF89a"), extension: "gif", supported: true},
		{name: "webp", contents: []byte("RIFFxxxxWEBPVP8 "), extension: "webp", supported: true},
		{name: "text", contents: []byte("not an image")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extension, supported := piImageExtension(test.contents)
			if extension != test.extension || supported != test.supported {
				t.Fatalf("piImageExtension() = %q, %v; want %q, %v", extension, supported, test.extension, test.supported)
			}
		})
	}
}

func TestRestartAPIRequestsApplicationRestart(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	restarted := make(chan struct{}, 1)
	handler, err := newIsolatedServerHandlerWithOptions(t, store, Options{
		Restart: func() { restarted <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}

	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	var health map[string]string
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" || health["instanceId"] == "" {
		t.Fatalf("health response = %#v", health)
	}

	restartResponse := httptest.NewRecorder()
	handler.ServeHTTP(restartResponse, httptest.NewRequest(http.MethodPost, "/api/restart", nil))
	if restartResponse.Code != http.StatusAccepted {
		t.Fatalf("restart status = %d, body = %s", restartResponse.Code, restartResponse.Body.String())
	}
	var response map[string]string
	if err := json.NewDecoder(restartResponse.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "restarting" || response["instanceId"] != health["instanceId"] {
		t.Fatalf("restart response = %#v, health = %#v", response, health)
	}
	select {
	case <-restarted:
	default:
		t.Fatal("restart callback was not called")
	}
}

func TestRestartAPIReportsUnavailableWithoutCallback(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/restart", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("restart status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHealthAndFrontendFallback(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthResponse.Code)
	}

	frontendResponse := httptest.NewRecorder()
	handler.ServeHTTP(frontendResponse, httptest.NewRequest(http.MethodGet, "/projects/demo", nil))
	if frontendResponse.Code != http.StatusOK {
		t.Fatalf("frontend status = %d", frontendResponse.Code)
	}
	if contentType := frontendResponse.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("frontend content type = %q", contentType)
	}
}
