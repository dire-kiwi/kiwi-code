package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

type serverSentEvent struct {
	Name string
	Data []byte
}

func TestGlobalEventStreamFansOutNamedStatusesToEveryClient(t *testing.T) {
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
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readers := make([]*bufio.Reader, 0, 3)
	responses := make([]*http.Response, 0, 3)
	for index := 0; index < 3; index++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("client %d stream response = %d %q", index, response.StatusCode, response.Header.Get("Content-Type"))
		}
		reader := bufio.NewReader(response.Body)
		readers = append(readers, reader)
		if event := readServerSentEvent(t, reader); event.Name != projectsEventName {
			t.Fatalf("client %d first event = %q", index, event.Name)
		}
		if event := readServerSentEvent(t, reader); event.Name != piActivityEventName {
			t.Fatalf("client %d second event = %q", index, event.Name)
		}
		if event := readServerSentEvent(t, reader); event.Name != profilesEventName || !strings.Contains(string(event.Data), `"id":"personal"`) {
			t.Fatalf("client %d third event = %q %s", index, event.Name, event.Data)
		}
		if event := readServerSentEvent(t, reader); event.Name != threadUsageEventName || !strings.Contains(string(event.Data), `"threadId":"`+thread.ID+`"`) {
			t.Fatalf("client %d fourth event = %q %s", index, event.Name, event.Data)
		}
	}

	putJSONForEventTest(t, ctx, server.Client(), server.URL+"/api/profiles", `{"name":"Shared client"}`, http.StatusCreated, http.MethodPost)
	for index, reader := range readers {
		event := readServerSentEvent(t, reader)
		if event.Name != profilesEventName || !strings.Contains(string(event.Data), `"name":"Shared client"`) {
			t.Fatalf("client %d profile event = %q %s", index, event.Name, event.Data)
		}
	}

	activityURL := fmt.Sprintf("%s/api/projects/%s/threads/%s/pi/activity", server.URL, item.ID, thread.ID)
	putJSONForEventTest(t, ctx, server.Client(), activityURL, `{"state":"working"}`, http.StatusOK)
	for index, reader := range readers {
		event := readServerSentEvent(t, reader)
		if event.Name != piActivityEventName || !strings.Contains(string(event.Data), `"state":"working"`) {
			t.Fatalf("client %d working event = %q %s", index, event.Name, event.Data)
		}
		event = readServerSentEvent(t, reader)
		if event.Name != projectsEventName || !strings.Contains(string(event.Data), `"lastPromptAt":`) {
			t.Fatalf("client %d prompt recency event = %q %s", index, event.Name, event.Data)
		}
	}

	threadURL := fmt.Sprintf("%s/api/projects/%s/threads", server.URL, item.ID)
	putJSONForEventTest(t, ctx, server.Client(), threadURL, `{"title":"Every client sees this"}`, http.StatusCreated, http.MethodPost)
	for index, reader := range readers {
		event := readServerSentEvent(t, reader)
		if event.Name != projectsEventName || !strings.Contains(string(event.Data), "Every client sees this") {
			t.Fatalf("client %d project event = %q %s", index, event.Name, event.Data)
		}
	}

	bookmarkURL := fmt.Sprintf("%s/api/projects/%s/threads/%s", server.URL, item.ID, thread.ID)
	putJSONForEventTest(t, ctx, server.Client(), bookmarkURL, `{"bookmarked":true}`, http.StatusOK, http.MethodPatch)
	for index, reader := range readers {
		event := readServerSentEvent(t, reader)
		if event.Name != projectsEventName || !strings.Contains(string(event.Data), `"id":"`+thread.ID+`"`) || !strings.Contains(string(event.Data), `"bookmarked":true`) {
			t.Fatalf("client %d bookmark event = %q %s", index, event.Name, event.Data)
		}
	}
}

func TestClientPiActivitiesFiltersRemovedThreads(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{projects: store}
	activities := server.clientPiActivities([]piThreadActivity{
		{ProjectID: item.ID, ThreadID: item.Threads[0].ID, State: piActivityWorking},
		{ProjectID: item.ID, ThreadID: "removed-thread", State: piActivityFinished},
		{ProjectID: "removed-project", ThreadID: "thread", State: piActivityFinished},
	})
	if len(activities) != 1 || activities[0].ThreadID != item.Threads[0].ID {
		t.Fatalf("filtered activities = %#v", activities)
	}
}

func readServerSentEvent(t *testing.T, reader *bufio.Reader) serverSentEvent {
	t.Helper()
	var event serverSentEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if event.Name != "" || len(event.Data) > 0 {
				return event
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			event.Name = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			event.Data = append(event.Data, value...)
		}
	}
}

func putJSONForEventTest(t *testing.T, ctx context.Context, client *http.Client, target, body string, wantStatus int, methods ...string) {
	t.Helper()
	method := http.MethodPut
	if len(methods) > 0 {
		method = methods[0]
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		var detail any
		_ = json.NewDecoder(response.Body).Decode(&detail)
		t.Fatalf("%s %s status = %d, want %d: %#v", method, target, response.StatusCode, wantStatus, detail)
	}
}
