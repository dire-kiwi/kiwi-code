package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestParseTmuxSessionActivities(t *testing.T) {
	activities, err := parseTmuxSessionActivities([]byte(
		"kiwi-code-project-thread-terminal\t0\t1700000000\t1700000100\t1700000200\t1700000250\t\n" +
			"kiwi-code-view-1\t1\t1700000300\t1700000400\t1700000500\t0\tkiwi-code-project-thread-tools\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 || activities[0].Name != "kiwi-code-project-thread-terminal" || activities[0].Attached {
		t.Fatalf("parsed activities = %#v", activities)
	}
	if !activities[1].Attached || activities[1].SourceSession != "kiwi-code-project-thread-tools" {
		t.Fatalf("parsed linked view = %#v", activities[1])
	}
	if got := activities[0].LastAttachedAt; !got.Equal(time.Unix(1700000200, 0)) {
		t.Fatalf("last attached = %v", got)
	}

	for _, malformed := range [][]byte{
		[]byte("missing-fields\t0\t1\n"),
		[]byte("name\t2\t1700000000\t0\t0\t0\t\n"),
		[]byte("name\t0\tbad\t0\t0\t0\t\n"),
	} {
		if _, err := parseTmuxSessionActivities(malformed); err == nil {
			t.Fatalf("malformed activity %q was accepted", malformed)
		}
	}
}

func TestInactiveSessionsForThreadIncludesPromptsAndLinkedViews(t *testing.T) {
	createdAt := time.Unix(1700000000, 0).UTC()
	promptedAt := createdAt.Add(3 * time.Hour)
	item := project.Project{ID: "project", Name: "Demo"}
	thread := project.Thread{ID: "thread", Title: "Work", CreatedAt: createdAt, LastPromptAt: &promptedAt}
	terminalName := tmuxSessionName(item.ID, thread.ID, "terminal")
	toolsName := tmuxSessionName(item.ID, thread.ID, "process")
	result := inactiveSessionsForThread(item, thread, []tmuxSessionActivity{
		{Name: terminalName, CreatedAt: createdAt.Add(time.Hour), ActivityAt: createdAt.Add(2 * time.Hour), RecordedUseAt: createdAt.Add(5 * time.Hour)},
		{Name: toolsName, CreatedAt: createdAt.Add(time.Hour)},
		{Name: "kiwi-code-view-1", SourceSession: toolsName, Attached: true, CreatedAt: createdAt.Add(4 * time.Hour)},
		{Name: "unrelated", Attached: true, CreatedAt: createdAt.Add(10 * time.Hour)},
	})
	if !reflect.DeepEqual(result.SessionNames, []string{terminalName, toolsName}) {
		t.Fatalf("session names = %#v", result.SessionNames)
	}
	if !result.Attached {
		t.Fatal("attached linked view did not keep the thread active")
	}
	if want := createdAt.Add(5 * time.Hour); !result.LastActivityAt.Equal(want) {
		t.Fatalf("last activity = %v, want %v", result.LastActivityAt, want)
	}
}

func TestCleanupCycleClosesAndLogsInactiveTmuxSessions(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
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
	application := handler.(*Server)
	if _, _, _, err := application.terminal.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatal(err)
	}
	toolsSession := tmuxSessionName(item.ID, thread.ID, "process")
	if _, err := application.terminal.createTmuxSession(toolsSession, thread.Cwd, "process", "/bin/sleep", []string{"120"}); err != nil {
		t.Fatal(err)
	}

	touchedAt := time.Now().UTC().Add(25 * time.Hour)
	if err := application.terminal.markThreadTmuxSessionsUsed(item, thread, touchedAt); err != nil {
		t.Fatal(err)
	}
	if err := application.runCleanupCycle(touchedAt); err != nil {
		t.Fatal(err)
	}
	for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
		if exists, err := application.terminal.tmuxExactSessionExists(sessionName); err != nil || !exists {
			t.Fatalf("recently used session %q exists=%t err=%v", sessionName, exists, err)
		}
	}

	closedAt := touchedAt.Add(25 * time.Hour)
	if err := application.runCleanupCycle(closedAt); err != nil {
		t.Fatal(err)
	}
	for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
		if exists, err := application.terminal.tmuxExactSessionExists(sessionName); err != nil || exists {
			t.Fatalf("inactive session %q exists=%t err=%v", sessionName, exists, err)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/session-closures", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("session closure log status = %d, body = %s", response.Code, response.Body.String())
	}
	var overview sessionClosureOverview
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.InactivityHours != 24 || len(overview.Events) != 1 {
		t.Fatalf("session closure overview = %#v", overview)
	}
	event := overview.Events[0]
	if event.ProjectName != item.Name || event.ThreadTitle != thread.Title || len(event.SessionNames) != 2 || event.Reason != "inactivity" {
		t.Fatalf("session closure event = %#v", event)
	}
	if !event.ClosedAt.Equal(closedAt) {
		t.Fatalf("closed at = %v, want %v", event.ClosedAt, closedAt)
	}
	if _, _, created, err := application.terminal.ensureTmuxSession(item, thread, "terminal"); err != nil || !created {
		t.Fatalf("recreate session after inactivity cleanup: created=%t err=%v", created, err)
	}
}

func TestSessionClosureLogPersistsEvents(t *testing.T) {
	directory := t.TempDir()
	log, err := newSessionClosureLog(directory)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1700000000, 0).UTC()
	for index, title := range []string{"First", "Second"} {
		event := sessionClosureEvent{
			ID:             title,
			ProjectID:      "project",
			ProjectName:    "Demo",
			ThreadID:       title,
			ThreadTitle:    title,
			SessionNames:   []string{"session-" + title},
			LastActivityAt: base.Add(time.Duration(index) * time.Hour),
			ClosedAt:       base.Add(time.Duration(index+1) * time.Hour),
			Reason:         "inactivity",
		}
		if err := log.append(event); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := newSessionClosureLog(directory)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reloaded.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ThreadTitle != "First" || events[1].ThreadTitle != "Second" {
		t.Fatalf("persisted events = %#v", events)
	}
}
