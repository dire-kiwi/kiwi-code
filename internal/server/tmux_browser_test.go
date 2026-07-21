package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

func TestTmuxBrowserListsAndAttachesWindows(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	if _, _, _, err := handler.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.newShellWindow(item, thread); err != nil {
		t.Fatal(err)
	}

	sessions, err := handler.tmuxBrowserSessions()
	if err != nil {
		t.Fatal(err)
	}
	sessionName := tmuxSessionName(item.ID, thread.ID, "terminal")
	session := findTmuxBrowserSession(t, sessions, sessionName)
	if session.Kind != "shell" || session.ProjectName != item.Name || session.ThreadTitle != thread.Title {
		t.Fatalf("tmux session context = %#v", session)
	}
	if len(session.Windows) != 2 {
		t.Fatalf("tmux browser windows = %#v, want two", session.Windows)
	}

	viewName, err := handler.createTmuxViewSession(item, thread, session.Name, tmuxWindowTarget{
		Index:     session.Windows[0].Index,
		ID:        session.Windows[0].ID,
		ServerPID: session.Windows[0].ServerPID,
		ProcessID: session.Windows[0].ProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err = handler.tmuxBrowserSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range sessions {
		if strings.HasPrefix(listed.Name, tmuxViewSessionPrefix) {
			t.Fatalf("temporary view session was listed: %#v", listed)
		}
	}
	handler.closeTmuxViewSession(viewName)
	handler.sessionMu.Lock()
	viewStillActive := handler.tmuxViewIsActiveLocked(viewName)
	handler.sessionMu.Unlock()
	if viewStillActive {
		t.Fatalf("closed tmux browser view %q remained registered", viewName)
	}

	server := newTmuxBrowserTestServer(handler)
	defer server.Close()
	window := session.Windows[0]
	connection := dialTmuxBrowserTerminal(t, server.URL, session.Name, window.ID)
	defer connection.Close()

	token := fmt.Sprintf("tmux-browser-%d", time.Now().UnixNano())
	writeTerminalInput(t, connection, "printf '\\n%s\\n' '"+token+"'\n")
	readTerminalUntil(t, connection, token)
	if exists, err := handler.tmuxSessionExists(session.Name); err != nil || !exists {
		t.Fatalf("browser attachment changed canonical session: exists=%t err=%v", exists, err)
	}
}

func TestTmuxBrowserOwnerUsesFreshStoreAndDurableStopRecipe(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	writer, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := writer.Add("Browser owner", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler := &terminalHandler{
		projects:      stale,
		terminalStops: newTerminalStopManager(stale.DataDirectory()),
	}

	for _, sessionName := range []string{
		tmuxSessionName(item.ID, thread.ID, "terminal"),
		tmuxSessionName(item.ID, thread.ID, "process"),
	} {
		owner, ownerThread, managed, ownerErr := handler.tmuxBrowserSessionOwner(sessionName)
		if ownerErr != nil || !managed || owner.ID != item.ID || ownerThread.ID != thread.ID {
			t.Fatalf("fresh owner for %q = project %#v thread %#v managed=%t err=%v", sessionName, owner, ownerThread, managed, ownerErr)
		}
	}

	canonicalSession := tmuxSessionName(item.ID, thread.ID, "terminal")
	lease, err := handler.terminalStops.beginThread(item.ID, thread.ID, []string{canonicalSession})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}
	if err := writer.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, managed, ownerErr := handler.tmuxBrowserSessionOwner(canonicalSession); !errors.Is(ownerErr, errTerminalStopping) || managed {
		t.Fatalf("deleted managed owner = managed=%t err=%v, want terminal stopping", managed, ownerErr)
	}
	if _, _, managed, ownerErr := handler.tmuxBrowserSessionOwner("external-session"); ownerErr != nil || managed {
		t.Fatalf("external owner = managed=%t err=%v", managed, ownerErr)
	}
}

func TestTmuxBrowserViewRejectsReusedWindowIncarnation(t *testing.T) {
	handler, _ := newIsolatedTmuxHandler(t)
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("reset tmux server after capability probe: %v: %s", err, output)
	}
	const sessionName = "browser-incarnation-test"
	stale, err := handler.createTmuxSession(sessionName, "/", "old", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("restart tmux server: %v: %s", err, output)
	}
	replacement, err := handler.createTmuxSession(sessionName, "/", "replacement", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID != stale.ID || replacement.ServerPID == stale.ServerPID {
		t.Fatalf("tmux did not reuse a window ID across server incarnations: stale=%#v replacement=%#v", stale, replacement)
	}
	beforeLinks := tmuxWindowLinkCount(t, handler, replacement.ID)
	if viewName, err := handler.createTmuxBrowserViewSession(sessionName, stale); !errors.Is(err, errTmuxWindowIncarnationChanged) {
		if err == nil {
			handler.closeTmuxViewSession(viewName)
		}
		t.Fatalf("stale browser view error = %v, want window incarnation change", err)
	}
	if afterLinks := tmuxWindowLinkCount(t, handler, replacement.ID); afterLinks != beforeLinks {
		t.Fatalf("stale browser view changed replacement link count: before=%d after=%d", beforeLinks, afterLinks)
	}
	if pid, found, err := handler.tmuxTargetServerPID(replacement.ID); err != nil || !found || pid != replacement.ServerPID {
		t.Fatalf("stale browser view changed replacement: pid=%q found=%t err=%v", pid, found, err)
	}
}

func TestTmuxBrowserParsesSessionsAndHidesTemporaryViews(t *testing.T) {
	directory := t.TempDir()
	tmuxPath := filepath.Join(directory, "tmux")
	output := strings.Join([]string{
		"kiwi-code-project-thread-terminal\t1\t@2\t2\tshell two\t1\t1\tzsh\t421\t",
		"kiwi-code-project-thread-terminal\t1\t@1\t1\tshell one\t0\t2\tbash\t421\t",
		"kiwi-code-project-thread-tools\t0\t@3\t1\tpi\t1\t2\tnode\t421\tprocess-3",
		"kiwi-code-view-123\t1\t@3\t1\tpi\t1\t2\tnode\t421\tprocess-3",
	}, "\\n")
	script := "#!/bin/sh\nprintf '%b\\n' " + shellQuote(output) + "\n"
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{tmuxPath: tmuxPath, tmuxSocket: "browser-test"}

	sessions, err := handler.tmuxBrowserSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v, want two persistent sessions", sessions)
	}
	terminal := findTmuxBrowserSession(t, sessions, "kiwi-code-project-thread-terminal")
	if !terminal.Attached || len(terminal.Windows) != 2 || terminal.Windows[0].ID != "@1" || terminal.Windows[1].ID != "@2" {
		t.Fatalf("parsed terminal session = %#v", terminal)
	}
	if terminal.Windows[0].PaneCount != 2 || terminal.Windows[0].CurrentCommand != "bash" {
		t.Fatalf("parsed terminal window = %#v", terminal.Windows[0])
	}
	if terminal.Windows[0].ServerPID != "421" || terminal.Windows[0].ProcessID != "" {
		t.Fatalf("parsed terminal window identity = %#v", terminal.Windows[0])
	}
	tools := findTmuxBrowserSession(t, sessions, "kiwi-code-project-thread-tools")
	if len(tools.Windows) != 1 || tools.Windows[0].ProcessID != "process-3" {
		t.Fatalf("parsed process window identity = %#v", tools.Windows)
	}
}

func TestTmuxBrowserTerminalRejectsPlainHTTPBeforeRunningTmux(t *testing.T) {
	handler := &terminalHandler{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tmux/terminal?session=example&window=@1", nil)
	handler.serveTmuxBrowserTerminal(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("plain HTTP status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestTmuxBrowserListEndpoint(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	if _, _, _, err := handler.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.listTmuxSessions(response, httptest.NewRequest(http.MethodGet, "/api/tmux/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var sessions []tmuxBrowserSession
	if err := json.NewDecoder(response.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	findTmuxBrowserSession(t, sessions, tmuxSessionName(item.ID, thread.ID, "terminal"))
}

func findTmuxBrowserSession(t *testing.T, sessions []tmuxBrowserSession, name string) tmuxBrowserSession {
	t.Helper()
	for _, session := range sessions {
		if session.Name == name {
			return session
		}
	}
	t.Fatalf("tmux sessions %#v do not contain %q", sessions, name)
	return tmuxBrowserSession{}
}

func newTmuxBrowserTestServer(handler *terminalHandler) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tmux/sessions", handler.listTmuxSessions)
	mux.HandleFunc("GET /api/tmux/terminal", handler.serveTmuxBrowserTerminal)
	return httptest.NewServer(mux)
}

func dialTmuxBrowserTerminal(t *testing.T, serverURL, sessionName, windowID string) *websocket.Conn {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/tmux/terminal?session=" + sessionName + "&window=" + windowID + "&cols=80&rows=24"
	connection, response, err := websocket.DefaultDialer.Dial(websocketURL, nil)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("dial tmux browser terminal: %v (status %d, body %s)", err, response.StatusCode, body)
		}
		t.Fatal(err)
	}
	return connection
}
