package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestParseTmuxSessionActivities(t *testing.T) {
	activities, err := parseTmuxSessionActivities([]byte(
		"kiwi-code-project-thread-terminal\t1700000000\t1700000250\t1700000300\n" +
			"kiwi-code-project-thread-tools\t1700000100\t\t0\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 || activities[0].Name != "kiwi-code-project-thread-terminal" {
		t.Fatalf("parsed activities = %#v", activities)
	}
	if got := activities[0].RecordedUseAt; !got.Equal(time.Unix(1700000250, 0)) {
		t.Fatalf("recorded use = %v", got)
	}
	if got := activities[0].StatusChangedAt; !got.Equal(time.Unix(1700000300, 0)) {
		t.Fatalf("status changed = %v", got)
	}
	// An unset option is normal for a session that has seen neither signal yet.
	if !activities[1].RecordedUseAt.IsZero() || !activities[1].StatusChangedAt.IsZero() {
		t.Fatalf("unset options = %#v", activities[1])
	}

	for _, malformed := range [][]byte{
		[]byte("missing-fields\t1\n"),
		[]byte("name\tbad\t0\t0\n"),
		[]byte("name\t1700000000\tbad\t0\n"),
		[]byte("\t1700000000\t0\t0\n"),
	} {
		if _, err := parseTmuxSessionActivities(malformed); err == nil {
			t.Fatalf("malformed activity %q was accepted", malformed)
		}
	}
}

func TestInactiveSessionsForThreadUsesVisitsPromptsAndStatusChanges(t *testing.T) {
	createdAt := time.Unix(1700000000, 0).UTC()
	promptedAt := createdAt.Add(3 * time.Hour)
	item := project.Project{ID: "project", Name: "Demo"}
	thread := project.Thread{ID: "thread", Title: "Work", CreatedAt: createdAt, LastPromptAt: &promptedAt}
	terminalName := tmuxSessionName(item.ID, thread.ID, "terminal")
	toolsName := tmuxSessionName(item.ID, thread.ID, "process")
	result := inactiveSessionsForThread(item, thread, []tmuxSessionActivity{
		{Name: terminalName, CreatedAt: createdAt.Add(time.Hour), RecordedUseAt: createdAt.Add(5 * time.Hour)},
		{Name: toolsName, CreatedAt: createdAt.Add(time.Hour), StatusChangedAt: createdAt.Add(7 * time.Hour)},
		{Name: "unrelated", CreatedAt: createdAt.Add(10 * time.Hour)},
	})
	if !reflect.DeepEqual(result.SessionNames, []string{terminalName, toolsName}) {
		t.Fatalf("session names = %#v", result.SessionNames)
	}
	if want := createdAt.Add(7 * time.Hour); !result.LastActivityAt.Equal(want) {
		t.Fatalf("last activity = %v, want %v", result.LastActivityAt, want)
	}
}

// The backend keeps a control-mode client attached to every thread's tools
// session for its whole lifetime, which pins tmux's own session_attached,
// session_activity, and session_last_attached. None of them may keep an
// otherwise untouched thread alive.
func TestCleanupClosesSessionsWithAttachedControlClient(t *testing.T) {
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
	stopWatching := application.terminal.watchThreadTmux(item.ID, thread.ID)
	defer stopWatching()

	deadline := time.Now().Add(10 * time.Second)
	for {
		attached, attachErr := application.terminal.tmuxCommand(
			"list-sessions", "-F", "#{session_name}\t#{?session_attached,1,0}",
		).CombinedOutput()
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		if strings.Contains(string(attached), toolsSession+"\t1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control client never attached: %s", attached)
		}
		time.Sleep(50 * time.Millisecond)
	}

	closedAt := time.Now().UTC().Add(25 * time.Hour)
	if err := application.runCleanupCycle(closedAt); err != nil {
		t.Fatal(err)
	}
	for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
		if exists, err := application.terminal.tmuxExactSessionExists(sessionName); err != nil || exists {
			t.Fatalf("attached-but-inactive session %q exists=%t err=%v", sessionName, exists, err)
		}
	}
}

func TestAgentStateTransitionKeepsSessionsAlive(t *testing.T) {
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

	// A transition stamps the sessions; repeating the same state must not, so a
	// stalled agent cannot hold its sessions open with heartbeats alone.
	stampedAt := time.Now().UTC().Add(30 * time.Hour)
	application.piActivity.updateAgent(item.ID, thread.ID, codingAgentPi, piActivityWorking, stampedAt)
	application.piActivity.updateAgent(item.ID, thread.ID, codingAgentPi, piActivityWorking, stampedAt.Add(20*time.Hour))
	application.piActivity.updateAgent(item.ID, thread.ID, codingAgentPi, piActivityIdle, stampedAt)

	activities, err := application.terminal.tmuxSessionActivities()
	if err != nil {
		t.Fatal(err)
	}
	sessions := inactiveSessionsForThread(item, thread, activities)
	if !sessions.LastActivityAt.Equal(stampedAt.Truncate(time.Second)) {
		t.Fatalf("last activity = %v, want the transition at %v", sessions.LastActivityAt, stampedAt)
	}

	if err := application.runCleanupCycle(stampedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	terminalSession := tmuxSessionName(item.ID, thread.ID, "terminal")
	if exists, err := application.terminal.tmuxExactSessionExists(terminalSession); err != nil || !exists {
		t.Fatalf("session closed despite a recent state change: exists=%t err=%v", exists, err)
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
