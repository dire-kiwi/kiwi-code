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

func TestProfileAPIAndProjectAssignment(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list profiles status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var profiles []project.Profile
	if err := json.NewDecoder(listResponse.Body).Decode(&profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].ID != project.PersonalProfileID || profiles[1].ID != project.WorkProfileID {
		t.Fatalf("default profiles = %#v", profiles)
	}

	createProfileResponse := httptest.NewRecorder()
	handler.ServeHTTP(createProfileResponse, httptest.NewRequest(http.MethodPost, "/api/profiles", bytes.NewBufferString(`{"name":"Client"}`)))
	if createProfileResponse.Code != http.StatusCreated {
		t.Fatalf("create profile status = %d, body = %s", createProfileResponse.Code, createProfileResponse.Body.String())
	}
	var profile project.Profile
	if err := json.NewDecoder(createProfileResponse.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}

	projectPath := t.TempDir()
	createProjectBody, err := json.Marshal(map[string]string{"name": "Client app", "path": projectPath, "profileId": profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	createProjectResponse := httptest.NewRecorder()
	handler.ServeHTTP(createProjectResponse, httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(createProjectBody)))
	if createProjectResponse.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body = %s", createProjectResponse.Code, createProjectResponse.Body.String())
	}
	var item project.Project
	if err := json.NewDecoder(createProjectResponse.Body).Decode(&item); err != nil {
		t.Fatal(err)
	}
	if item.ProfileID != profile.ID {
		t.Fatalf("created project profile = %q, want %q", item.ProfileID, profile.ID)
	}

	updateResponse := httptest.NewRecorder()
	updatePath := "/api/projects/" + item.ID
	handler.ServeHTTP(updateResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"profileId":"work"}`)))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update project profile status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated project.Project
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.ProfileID != project.WorkProfileID {
		t.Fatalf("updated project profile = %q, want %q", updated.ProfileID, project.WorkProfileID)
	}

	depthResponse := httptest.NewRecorder()
	handler.ServeHTTP(depthResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"subAgentNestingDepthOverride":3}`)))
	if depthResponse.Code != http.StatusOK {
		t.Fatalf("update project nesting status = %d, body = %s", depthResponse.Code, depthResponse.Body.String())
	}
	if err := json.NewDecoder(depthResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.ProfileID != project.WorkProfileID || updated.SubAgentNestingDepthOverride == nil || *updated.SubAgentNestingDepthOverride != 3 {
		t.Fatalf("updated project nesting = %#v", updated)
	}

	combinedResponse := httptest.NewRecorder()
	handler.ServeHTTP(combinedResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"profileId":"personal","subAgentNestingDepthOverride":null}`)))
	if combinedResponse.Code != http.StatusOK {
		t.Fatalf("clear project nesting status = %d, body = %s", combinedResponse.Code, combinedResponse.Body.String())
	}
	updated = project.Project{}
	if err := json.NewDecoder(combinedResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.ProfileID != project.PersonalProfileID || updated.SubAgentNestingDepthOverride != nil {
		t.Fatalf("cleared project nesting = %#v", updated)
	}

	prefixResponse := httptest.NewRecorder()
	handler.ServeHTTP(prefixResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"worktreeBranchPrefix":"ivan/"}`)))
	if prefixResponse.Code != http.StatusOK {
		t.Fatalf("update project branch prefix status = %d, body = %s", prefixResponse.Code, prefixResponse.Body.String())
	}
	updated = project.Project{}
	if err := json.NewDecoder(prefixResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.WorktreeBranchPrefix != "ivan/" {
		t.Fatalf("updated project branch prefix = %q", updated.WorktreeBranchPrefix)
	}

	invalidPrefixResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidPrefixResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"worktreeBranchPrefix":"bad prefix/"}`)))
	if invalidPrefixResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid project branch prefix status = %d, body = %s", invalidPrefixResponse.Code, invalidPrefixResponse.Body.String())
	}

	invalidDepthResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidDepthResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"subAgentNestingDepthOverride":5}`)))
	if invalidDepthResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid project nesting status = %d, body = %s", invalidDepthResponse.Code, invalidDepthResponse.Body.String())
	}

	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodPatch, updatePath, bytes.NewBufferString(`{"profileId":"missing"}`)))
	if missingResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing profile status = %d, body = %s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestThreadAPI(t *testing.T) {
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

	createResponse := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/threads", bytes.NewBufferString(`{"nestedDepth":0}`))
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create thread status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var thread project.Thread
	if err := json.NewDecoder(createResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if thread.Title != "New thread" || thread.Cwd != item.Path || thread.NestedDepth == nil || *thread.NestedDepth != 0 {
		t.Fatalf("unexpected thread: %#v", thread)
	}

	invalidAgentResponse := httptest.NewRecorder()
	invalidAgentRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+thread.ID+"/coding-agent",
		bytes.NewBufferString(`{"agent":"unknown","model":"example/model","prompt":"Start this"}`),
	)
	handler.ServeHTTP(invalidAgentResponse, invalidAgentRequest)
	if invalidAgentResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid coding agent status = %d, body = %s", invalidAgentResponse.Code, invalidAgentResponse.Body.String())
	}

	invalidDepthResponse := httptest.NewRecorder()
	invalidDepthRequest := httptest.NewRequest(http.MethodPost, "/api/projects/"+item.ID+"/threads", bytes.NewBufferString(`{"nestedDepth":2}`))
	handler.ServeHTTP(invalidDepthResponse, invalidDepthRequest)
	if invalidDepthResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid thread depth status = %d, body = %s", invalidDepthResponse.Code, invalidDepthResponse.Body.String())
	}

	threadPath := "/api/projects/" + item.ID + "/threads/" + thread.ID
	updateResponse := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"title":"Generated thread title","autoGenerated":true}`))
	handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update thread status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	if err := json.NewDecoder(updateResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if thread.Title != "Generated thread title" || !thread.AutoNamed {
		t.Fatalf("unexpected updated thread: %#v", thread)
	}

	secondUpdateResponse := httptest.NewRecorder()
	secondUpdateRequest := httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"title":"Do not replace","autoGenerated":true}`))
	handler.ServeHTTP(secondUpdateResponse, secondUpdateRequest)
	if secondUpdateResponse.Code != http.StatusOK {
		t.Fatalf("second update status = %d, body = %s", secondUpdateResponse.Code, secondUpdateResponse.Body.String())
	}
	if err := json.NewDecoder(secondUpdateResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if thread.Title != "Generated thread title" {
		t.Fatalf("generated title was replaced: %#v", thread)
	}

	manualUpdateResponse := httptest.NewRecorder()
	manualUpdateRequest := httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"title":"Manually renamed thread","autoGenerated":false}`))
	handler.ServeHTTP(manualUpdateResponse, manualUpdateRequest)
	if manualUpdateResponse.Code != http.StatusOK {
		t.Fatalf("manual update status = %d, body = %s", manualUpdateResponse.Code, manualUpdateResponse.Body.String())
	}
	thread = project.Thread{}
	if err := json.NewDecoder(manualUpdateResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if thread.Title != "Manually renamed thread" || thread.AutoNamed {
		t.Fatalf("unexpected manually updated thread: %#v", thread)
	}

	bookmarkResponse := httptest.NewRecorder()
	handler.ServeHTTP(bookmarkResponse, httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"bookmarked":true}`)))
	if bookmarkResponse.Code != http.StatusOK {
		t.Fatalf("bookmark thread status = %d, body = %s", bookmarkResponse.Code, bookmarkResponse.Body.String())
	}
	thread = project.Thread{}
	if err := json.NewDecoder(bookmarkResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if !thread.Bookmarked {
		t.Fatalf("thread was not bookmarked: %#v", thread)
	}

	for name, body := range map[string]string{
		"empty":         `{}`,
		"multiple":      `{"archived":true,"bookmarked":true}`,
		"unknown field": `{"favorite":true}`,
	} {
		t.Run("rejects "+name+" update", func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, threadPath, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get thread status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	var persistedThread project.Thread
	if err := json.NewDecoder(getResponse.Body).Decode(&persistedThread); err != nil {
		t.Fatal(err)
	}
	if persistedThread.Title != "Manually renamed thread" || !persistedThread.Bookmarked {
		t.Fatalf("thread updates were not persisted: %#v", persistedThread)
	}

	archiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(archiveResponse, httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"archived":true}`)))
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive thread status = %d, body = %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	if err := json.NewDecoder(archiveResponse.Body).Decode(&persistedThread); err != nil {
		t.Fatal(err)
	}
	if persistedThread.ArchivedAt == nil || !persistedThread.Bookmarked {
		t.Fatalf("archived thread lost state: %#v", persistedThread)
	}

	restoreResponse := httptest.NewRecorder()
	handler.ServeHTTP(restoreResponse, httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"archived":false}`)))
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore thread status = %d, body = %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	persistedThread = project.Thread{}
	if err := json.NewDecoder(restoreResponse.Body).Decode(&persistedThread); err != nil {
		t.Fatal(err)
	}
	if persistedThread.ArchivedAt != nil || !persistedThread.Bookmarked {
		t.Fatalf("restored thread lost state: %#v", persistedThread)
	}

	clearBookmarkResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearBookmarkResponse, httptest.NewRequest(http.MethodPatch, threadPath, bytes.NewBufferString(`{"bookmarked":false}`)))
	if clearBookmarkResponse.Code != http.StatusOK {
		t.Fatalf("clear bookmark status = %d, body = %s", clearBookmarkResponse.Code, clearBookmarkResponse.Body.String())
	}
	persistedThread = project.Thread{}
	if err := json.NewDecoder(clearBookmarkResponse.Body).Decode(&persistedThread); err != nil {
		t.Fatal(err)
	}
	if persistedThread.Bookmarked {
		t.Fatalf("thread remained bookmarked: %#v", persistedThread)
	}

	deleteResponse := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+thread.ID, nil)
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete thread status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
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
