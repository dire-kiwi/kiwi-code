package server

import (
	"path/filepath"
	"reflect"
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
	application.piActivity.UpdateAgent(item.ID, thread.ID, codingAgentPi, piActivityWorking, stampedAt)
	application.piActivity.UpdateAgent(item.ID, thread.ID, codingAgentPi, piActivityWorking, stampedAt.Add(20*time.Hour))
	application.piActivity.UpdateAgent(item.ID, thread.ID, codingAgentPi, piActivityIdle, stampedAt)

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
