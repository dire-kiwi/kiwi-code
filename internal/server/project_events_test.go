package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestProjectEventStreamUpdatesEveryClient(t *testing.T) {
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
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	openStream := func() (*http.Response, *bufio.Reader) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/projects/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			t.Fatalf("stream projects status = %d", response.StatusCode)
		}
		if contentType := response.Header.Get("Content-Type"); contentType != "text/event-stream" {
			_ = response.Body.Close()
			t.Fatalf("stream projects content type = %q", contentType)
		}
		return response, bufio.NewReader(response.Body)
	}

	firstResponse, firstReader := openStream()
	defer firstResponse.Body.Close()
	secondResponse, secondReader := openStream()
	defer secondResponse.Body.Close()
	readers := []*bufio.Reader{firstReader, secondReader}

	for _, reader := range readers {
		projects, err := readProjectsEvent(reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || len(projects[0].Threads) != 1 {
			t.Fatalf("unexpected initial projects: %#v", projects)
		}
	}

	threadPath := server.URL + "/api/projects/" + item.ID + "/threads"
	createRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, threadPath, bytes.NewBufferString(`{"title":"Stream this thread"}`))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse, err := server.Client().Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create thread status = %d", createResponse.StatusCode)
	}
	var thread project.Thread
	if err := json.NewDecoder(createResponse.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers {
		projects, err := readProjectsEvent(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !projectSnapshotHasThread(projects, item.ID, thread.ID, "Stream this thread") {
			t.Fatalf("created thread missing from streamed projects: %#v", projects)
		}
	}

	threadPath += "/" + thread.ID
	updateRequest, err := http.NewRequestWithContext(ctx, http.MethodPatch, threadPath, bytes.NewBufferString(`{"title":"Updated everywhere"}`))
	if err != nil {
		t.Fatal(err)
	}
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse, err := server.Client().Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update thread status = %d", updateResponse.StatusCode)
	}
	for _, reader := range readers {
		projects, err := readProjectsEvent(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !projectSnapshotHasThread(projects, item.ID, thread.ID, "Updated everywhere") {
			t.Fatalf("updated thread missing from streamed projects: %#v", projects)
		}
	}

	deleteRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, threadPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResponse, err := server.Client().Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("archive thread status = %d", deleteResponse.StatusCode)
	}
	for _, reader := range readers {
		projects, err := readProjectsEvent(reader)
		if err != nil {
			t.Fatal(err)
		}
		if projectSnapshotHasThread(projects, item.ID, thread.ID, "") {
			t.Fatalf("archived thread remained in streamed projects: %#v", projects)
		}
	}
}

func readProjectsEvent(reader *bufio.Reader) ([]project.Project, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var projects []project.Project
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &projects); err != nil {
			return nil, err
		}
		return projects, nil
	}
}

func projectSnapshotHasThread(projects []project.Project, projectID, threadID, title string) bool {
	for _, item := range projects {
		if item.ID != projectID {
			continue
		}
		for _, thread := range item.Threads {
			if thread.ID == threadID && (title == "" || thread.Title == title) {
				return true
			}
		}
	}
	return false
}
