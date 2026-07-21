package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestForkedPlanningChildPublishesDownloadableParentPlan(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "data")
	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "kiwi-code-planner · Plan", project.AddThreadOptions{
		ParentThreadID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)
	childPlansPath := "/api/projects/" + item.ID + "/threads/" + child.ID + "/plans"
	content := "# Implementation plan\n\n1. Update `internal/server/thread_plans.go`.\n2. Run the tests.\n"
	body, err := json.Marshal(map[string]string{
		"title":   "Add retained thread plans",
		"content": content,
	})
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, childPlansPath, bytes.NewReader(body)))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized upload status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	uploadRequest := httptest.NewRequest(http.MethodPost, childPlansPath, bytes.NewReader(body))
	uploadRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var uploaded threadPlanSnapshot
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == "" || uploaded.ProjectID != item.ID || uploaded.ThreadID != parent.ID ||
		uploaded.SourceThreadID != child.ID || uploaded.Title != "Add retained thread plans" ||
		uploaded.SizeBytes != len([]byte(content)) {
		t.Fatalf("uploaded plan = %#v", uploaded)
	}

	for name, path := range map[string]string{
		"parent": "/api/projects/" + item.ID + "/threads/" + parent.ID + "/plans",
		"child":  childPlansPath,
	} {
		t.Run("list from "+name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
			}
			var plans []threadPlanSnapshot
			if err := json.NewDecoder(response.Body).Decode(&plans); err != nil {
				t.Fatal(err)
			}
			if len(plans) != 1 || plans[0] != uploaded {
				t.Fatalf("listed plans = %#v, want %#v", plans, uploaded)
			}
		})
	}

	downloadPath := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/plans/" + uploaded.ID
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, httptest.NewRequest(http.MethodGet, downloadPath, nil))
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if downloadResponse.Body.String() != content {
		t.Fatalf("downloaded plan = %q, want %q", downloadResponse.Body.String(), content)
	}
	if contentType := downloadResponse.Header().Get("Content-Type"); contentType != "text/markdown; charset=utf-8" {
		t.Fatalf("download content type = %q", contentType)
	}
	if disposition := downloadResponse.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, ".md") {
		t.Fatalf("download disposition = %q", disposition)
	}

	reloaded, err := newThreadPlanManager(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := reloaded.list(item.ID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0] != uploaded {
		t.Fatalf("persisted plans = %#v, want %#v", plans, uploaded)
	}

	currentItem, currentParent, err := store.GetThread(item.ID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := application.readThreadStatusSnapshot(context.Background(), currentItem, currentParent)
	if len(snapshot.Plans) != 1 || snapshot.Plans[0] != uploaded || snapshot.Errors.Plans != "" {
		t.Fatalf("thread status plans = %#v, error = %q", snapshot.Plans, snapshot.Errors.Plans)
	}
}

func TestThreadPlanUploadRequiresForkedChildAndValidMarkdown(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
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
	application := handler.(*Server)
	parent := item.Threads[0]
	parentPath := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/plans"

	parentRequest := httptest.NewRequest(http.MethodPost, parentPath, strings.NewReader(`{"title":"No child","content":"# Plan"}`))
	parentRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	parentResponse := httptest.NewRecorder()
	handler.ServeHTTP(parentResponse, parentRequest)
	if parentResponse.Code != http.StatusConflict || !strings.Contains(parentResponse.Body.String(), "context: fork") {
		t.Fatalf("parent upload status = %d, body = %s", parentResponse.Code, parentResponse.Body.String())
	}

	workflowChild, err := store.AddThreadWithOptions(item.ID, "Workflow child", project.AddThreadOptions{
		ParentThreadID:  parent.ID,
		WorkflowRunID:   "run-1",
		WorkflowAgentID: "agent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	workflowPath := "/api/projects/" + item.ID + "/threads/" + workflowChild.ID + "/plans"
	workflowRequest := httptest.NewRequest(http.MethodPost, workflowPath, strings.NewReader(`{"title":"Workflow","content":"# Plan"}`))
	workflowRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	workflowResponse := httptest.NewRecorder()
	handler.ServeHTTP(workflowResponse, workflowRequest)
	if workflowResponse.Code != http.StatusConflict || !strings.Contains(workflowResponse.Body.String(), "context: fork") {
		t.Fatalf("workflow upload status = %d, body = %s", workflowResponse.Code, workflowResponse.Body.String())
	}

	child, err := store.AddThreadWithOptions(item.ID, "Planner", project.AddThreadOptions{ParentThreadID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	childPath := "/api/projects/" + item.ID + "/threads/" + child.ID + "/plans"
	request := httptest.NewRequest(http.MethodPost, childPath, strings.NewReader(`{"title":"Empty","content":"   "}`))
	request.Header.Set(agentTokenHeader, application.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty plan status = %d, body = %s", response.Code, response.Body.String())
	}

	oversizedContent := strings.Repeat("x", maxThreadPlanContentBytes+1)
	oversizedBody, err := json.Marshal(map[string]string{"title": "Too large", "content": oversizedContent})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, childPath, bytes.NewReader(oversizedBody))
	request.Header.Set(agentTokenHeader, application.terminal.agentToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized plan status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestThreadPlanRetentionKeepsNewestPlans(t *testing.T) {
	plans, err := newThreadPlanManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxRetainedThreadPlans+2; index++ {
		title := "Plan " + strings.Repeat("x", index)
		if _, err := plans.create("project", "parent", "child", title, "# Plan\n"); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := plans.list("project", "parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != maxRetainedThreadPlans {
		t.Fatalf("retained plans = %d, want %d", len(listed), maxRetainedThreadPlans)
	}
	if listed[0].Title != "Plan "+strings.Repeat("x", maxRetainedThreadPlans+1) {
		t.Fatalf("newest retained plan = %q", listed[0].Title)
	}
	for _, plan := range listed {
		if plan.Title == "Plan" || plan.Title == "Plan x" {
			t.Fatalf("old plan was not pruned: %#v", plan)
		}
	}
}

func TestDeletedProjectRemovesRetainedPlans(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "data")
	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
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
	application := handler.(*Server)
	parent := item.Threads[0]
	if _, err := application.plans.create(item.ID, parent.ID, "child", "Cleanup plan", "# Plan\n"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete project status = %d, body = %s", response.Code, response.Body.String())
	}
	listed, err := application.plans.list(item.ID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("plans survived project deletion: %#v", listed)
	}
}

func TestDeletedThreadRuntimeRemovesRetainedPlans(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "data")
	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
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
	application := handler.(*Server)
	parent := item.Threads[0]
	created, err := application.plans.create(item.ID, parent.ID, "child", "Cleanup plan", "# Plan\n")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created plan has no ID")
	}
	application.finishDeletedThreadRuntime(item.ID, parent.ID, "test")
	listed, err := application.plans.list(item.ID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("plans survived thread deletion: %#v", listed)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, threadPlanDirectoryName, item.ID, parent.ID)); !os.IsNotExist(err) {
		t.Fatalf("thread plan directory still exists: %v", err)
	}
}
