package server

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ivan/dire-mux/internal/project"
)

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

type paneObservingLogWriter struct {
	mu       sync.Mutex
	handler  *terminalHandler
	paneID   string
	buffer   bytes.Buffer
	sawExit  bool
	observed tmuxPaneExitState
	err      error
}

func (w *paneObservingLogWriter) Write(contents []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.Contains(string(contents), "coding agent exited:") {
		w.sawExit = true
		w.observed, w.err = w.handler.tmuxPaneExitState(w.paneID)
	}
	return w.buffer.Write(contents)
}

func (w *paneObservingLogWriter) snapshot() (string, bool, tmuxPaneExitState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String(), w.sawExit, w.observed, w.err
}

func (b *synchronizedBuffer) Write(contents []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(contents)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestTmuxSessionPersistsAcrossHandlerRestart(t *testing.T) {
	tmuxPath, socketName := isolatedTmuxServer(t)

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]

	firstHandler := newTerminalHandlerWithOptions(store, originPolicy{}, socketName)
	firstHandler.tmuxPath = tmuxPath
	firstServer := newTerminalTestServer(firstHandler)

	firstConnection := dialTerminal(t, firstServer.URL, item.ID, thread.ID)
	readTerminalMessage(t, firstConnection)
	token := fmt.Sprintf("dire-mux-persistence-%d", time.Now().UnixNano())
	sessionName := tmuxSessionName(item.ID, thread.ID, "terminal")
	output, err := firstHandler.tmuxCommand(
		"send-keys",
		"-t", sessionName,
		fmt.Sprintf("printf '%%s\\n' '%s'", token),
		"Enter",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("send input to tmux session: %v: %s", err, output)
	}
	readTerminalUntil(t, firstConnection, token)
	if err := firstConnection.Close(); err != nil {
		t.Fatal(err)
	}
	firstServer.Close()

	secondHandler := newTerminalHandlerWithOptions(store, originPolicy{}, socketName)
	secondHandler.tmuxPath = tmuxPath
	waitForTmuxSession(t, secondHandler, tmuxSessionName(item.ID, thread.ID, "terminal"))
	secondServer := newTerminalTestServer(secondHandler)
	defer secondServer.Close()

	secondConnection := dialTerminal(t, secondServer.URL, item.ID, thread.ID)
	defer secondConnection.Close()
	readTerminalUntil(t, secondConnection, token)
}

func TestTerminalAttachEnablesTmuxClipboardForwarding(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	if _, _, _, err := handler.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatal(err)
	}
	if output, err := handler.tmuxCommand("set-option", "-s", "set-clipboard", "off").CombinedOutput(); err != nil {
		t.Fatalf("disable tmux clipboard forwarding: %v: %s", err, output)
	}

	server := newTerminalTestServer(handler)
	defer server.Close()
	connection := dialTerminal(t, server.URL, item.ID, thread.ID)
	defer connection.Close()
	readTerminalMessage(t, connection)

	output, err := handler.tmuxCommand("show-options", "-s", "-v", "set-clipboard").CombinedOutput()
	if err != nil {
		t.Fatalf("read tmux clipboard option: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "external" {
		t.Fatalf("set-clipboard = %q, want external", got)
	}
}

func TestTerminalOutputFansOutOnlyToClientsAttachedToThatSession(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	firstThread := item.Threads[0]
	secondThread, err := handler.projects.AddThread(item.ID, "Isolated thread")
	if err != nil {
		t.Fatal(err)
	}
	server := newTerminalTestServer(handler)
	defer server.Close()

	firstConnections := []*websocket.Conn{
		dialTerminal(t, server.URL, item.ID, firstThread.ID),
		dialTerminal(t, server.URL, item.ID, firstThread.ID),
	}
	secondConnection := dialTerminal(t, server.URL, item.ID, secondThread.ID)
	connections := append(append([]*websocket.Conn{}, firstConnections...), secondConnection)
	for _, connection := range connections {
		t.Cleanup(func() { _ = connection.Close() })
	}

	firstCaptures := []*terminalCapture{
		newTerminalCapture(firstConnections[0]),
		newTerminalCapture(firstConnections[1]),
	}
	secondCapture := newTerminalCapture(secondConnection)

	firstToken := fmt.Sprintf("first-session-%d", time.Now().UnixNano())
	writeTerminalInput(t, firstConnections[0], "printf '\\n%s\\n' '"+firstToken+"'\n")
	for index, capture := range firstCaptures {
		if !capture.waitFor(firstToken, 5*time.Second) {
			t.Fatalf("first-session client %d did not receive %q; output: %q", index, firstToken, capture.text())
		}
	}
	if secondCapture.waitFor(firstToken, 250*time.Millisecond) {
		t.Fatalf("unrelated session received %q; output: %q", firstToken, secondCapture.text())
	}

	secondToken := fmt.Sprintf("second-session-%d", time.Now().UnixNano())
	writeTerminalInput(t, secondConnection, "printf '\\n%s\\n' '"+secondToken+"'\n")
	if !secondCapture.waitFor(secondToken, 5*time.Second) {
		t.Fatalf("second-session client did not receive %q; output: %q", secondToken, secondCapture.text())
	}
	for index, capture := range firstCaptures {
		if capture.waitFor(secondToken, 250*time.Millisecond) {
			t.Fatalf("first-session client %d received unrelated %q; output: %q", index, secondToken, capture.text())
		}
	}
}

type terminalCapture struct {
	mu      sync.Mutex
	output  strings.Builder
	updates chan struct{}
}

func newTerminalCapture(connection *websocket.Conn) *terminalCapture {
	capture := &terminalCapture{updates: make(chan struct{}, 1)}
	go func() {
		for {
			_, message, err := connection.ReadMessage()
			if err != nil {
				return
			}
			capture.mu.Lock()
			capture.output.Write(message)
			capture.mu.Unlock()
			select {
			case capture.updates <- struct{}{}:
			default:
			}
		}
	}()
	return capture
}

func (c *terminalCapture) waitFor(expected string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if strings.Contains(c.text(), expected) {
			return true
		}
		select {
		case <-c.updates:
		case <-deadline.C:
			return strings.Contains(c.text(), expected)
		}
	}
}

func (c *terminalCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func writeTerminalInput(t *testing.T, connection *websocket.Conn, data string) {
	t.Helper()
	payload, err := json.Marshal(clientMessage{Type: "input", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatal(err)
	}
}

func TestShellWindowsCanBeCreatedAndSelected(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]

	initialWindows, err := handler.shellWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(initialWindows) != 1 {
		t.Fatalf("initial shell windows = %#v, want one window", initialWindows)
	}
	if !initialWindows[0].Active {
		t.Fatalf("initial shell window is not active: %#v", initialWindows[0])
	}

	windows, err := handler.newShellWindow(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("shell windows after create = %#v, want two windows", windows)
	}
	var active tmuxWindow
	for _, window := range windows {
		if window.Active {
			active = window
			break
		}
	}
	if active.Index == initialWindows[0].Index {
		t.Fatalf("new shell window was not selected: %#v", windows)
	}

	windows, err = handler.activateShellWindow(item, thread, initialWindows[0].Index)
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, window := range windows {
		if window.Index == initialWindows[0].Index {
			selected = window.Active
			break
		}
	}
	if !selected {
		t.Fatalf("selected shell window is not active: %#v", windows)
	}
}

func TestPiStartsWithoutProcessWindows(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")

	sessionName, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("Pi window was not created")
	}
	if processSession := tmuxSessionName(item.ID, thread.ID, "process"); processSession != sessionName {
		t.Fatalf("Process session = %q, Pi session = %q", processSession, sessionName)
	}
	windows, err := handler.tmuxWindows(sessionName)
	if err != nil {
		t.Fatal(err)
	}
	assertTmuxWindowNames(t, windows, "pi")
	if processes, err := handler.processWindows(item, thread); err != nil {
		t.Fatal(err)
	} else if len(processes) != 0 {
		t.Fatalf("process windows = %#v, want none", processes)
	}

	if _, _, created, err := handler.ensureTmuxSession(item, thread, "pi"); err != nil {
		t.Fatal(err)
	} else if created {
		t.Fatal("an existing Pi window was created again")
	}
}

func TestCodingAgentLaunchDoesNotExposeSleepPlaceholder(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nprintf 'pi-ready\\n'\nwhile :; do /bin/sleep 1; done\n")

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%t err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, handler, target, "pi-ready")

	output, err := handler.tmuxCommand(
		"display-message", "-p",
		"-t", target.ID,
		"#{pane_start_command}\t#{pane_current_command}\t#{@dire-mux-agent}",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Pi launch: %v: %s", err, output)
	}
	launch := strings.TrimSpace(string(output))
	if strings.Contains(launch, "/bin/sleep") || strings.Contains(launch, "86400") {
		t.Fatalf("Pi pane still exposes an adoptable sleep placeholder: %q", launch)
	}
	if !strings.HasSuffix(launch, "\t"+codingAgentPi) {
		t.Fatalf("Pi pane launch metadata = %q, want agent %q", launch, codingAgentPi)
	}
}

func TestAbsentPiWindowBootstrapsRequestedClaude(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockCodingAgents(t)
	server := newTerminalTestServer(handler)
	defer server.Close()

	connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, "tool=pi&agent=claude&cols=80&rows=24")
	defer connection.Close()
	readTerminalUntil(t, connection, "claude-ready")

	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	panes, err := handler.tmuxAgentPanes(windowsTargetID(t, handler, sessionName, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].Agent != codingAgentClaude {
		t.Fatalf("first Claude connection launched panes %#v, want only Claude", panes)
	}
}

func TestCodingAgentLaunchAppliesSelectedModelAndThinkingLevel(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	argumentsPath := filepath.Join(t.TempDir(), "pi-arguments")
	setMockPi(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(argumentsPath)+"\nprintf 'pi-ready\\n'\nwhile :; do /bin/sleep 1; done\n")
	server := newTerminalTestServer(handler)
	defer server.Close()

	connection := dialTerminalWithQuery(
		t,
		server.URL,
		item.ID,
		thread.ID,
		"tool=pi&agent=pi&model=openai-codex%2Fgpt-5.6-sol&thinking=max&cols=80&rows=24",
	)
	defer connection.Close()
	readTerminalUntil(t, connection, "pi-ready")

	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	argumentsWithBoundaries := "\n" + strings.TrimSpace(string(arguments)) + "\n"
	for _, expected := range []string{"--model", "openai-codex/gpt-5.6-sol", "--thinking", "max"} {
		if !strings.Contains(argumentsWithBoundaries, "\n"+expected+"\n") {
			t.Fatalf("Pi arguments = %q, missing %q", arguments, expected)
		}
	}
}

func TestClaudeLaunchSubmitsInitialPromptAsArgument(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	directory := t.TempDir()
	promptPath := filepath.Join(directory, "claude-prompt")
	claudeScript := `#!/bin/sh
last=''
for argument do
  last=$argument
done
printf '%s' "$last" > ` + shellQuote(promptPath) + `
printf 'claude-ready\n'
while :; do /bin/sleep 1; done
`
	if err := os.WriteFile(filepath.Join(directory, codingAgentClaude), []byte(claudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, codingAgentPi), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	server := newTerminalTestServer(handler)
	defer server.Close()

	prompt := "Review the user's repository; then report back."
	query := url.Values{
		"tool":   {"pi"},
		"agent":  {codingAgentClaude},
		"prompt": {prompt},
		"cols":   {"80"},
		"rows":   {"24"},
	}.Encode()
	connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, query)
	defer connection.Close()
	readTerminalUntil(t, connection, "claude-ready")

	got, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != prompt {
		t.Fatalf("Claude initial prompt = %q, want %q", got, prompt)
	}
}

func TestCodingAgentExitTombstoneRequiresExplicitRestart(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	launchesPath := filepath.Join(t.TempDir(), "launches")
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nexit 7\n")
	server := newTerminalTestServer(handler)
	defer server.Close()

	connect := func(query string) (int, string) {
		connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, query)
		defer connection.Close()
		return readTerminalClose(t, connection)
	}
	assertEnded := func(code int, reason string) {
		t.Helper()
		if code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
			t.Fatalf("coding-agent close = (%d, %q), want (%d, %q)", code, reason, websocket.CloseNormalClosure, "Coding agent ended")
		}
	}

	code, reason := connect("tool=pi&agent=pi&cols=80&rows=24")
	assertEnded(code, reason)
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("initial connection launched Pi %d times, want 1", launches)
	}

	code, reason = connect("tool=pi&agent=pi&cols=80&rows=24")
	assertEnded(code, reason)
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("implicit retry relaunched Pi %d times, want 1 total", launches)
	}

	code, reason = connect("tool=pi&agent=pi&restart=1&cols=80&rows=24")
	assertEnded(code, reason)
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 2 {
		t.Fatalf("explicit restart launched Pi %d times, want 2 total", launches)
	}
}

func TestCodingAgentExitTombstoneSurvivesHandlerRestart(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	launchesPath := filepath.Join(t.TempDir(), "launches")
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nexit 7\n")

	firstServer := newTerminalTestServer(handler)
	connection := dialTerminalWithQuery(t, firstServer.URL, item.ID, thread.ID, "tool=pi&agent=pi&cols=80&rows=24")
	code, reason := readTerminalClose(t, connection)
	_ = connection.Close()
	firstServer.Close()
	if code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("initial coding-agent close = (%d, %q)", code, reason)
	}
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("initial connection launched Pi %d times, want 1", launches)
	}
	markerPath := handler.codingAgentExitMarkerPath(item.ID, thread.ID, codingAgentPi)
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("durable exit marker: %v", err)
	}
	if permissions := markerInfo.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("durable exit marker permissions = %o, want 600", permissions)
	}
	waitForTmuxSessionGone(t, handler, tmuxSessionName(item.ID, thread.ID, "pi"))

	restartedHandler := newTerminalHandlerWithOptions(handler.projects, originPolicy{}, handler.tmuxSocket)
	restartedHandler.tmuxPath = handler.tmuxPath
	restartedHandler.envPath = handler.envPath
	restartedServer := newTerminalTestServer(restartedHandler)
	defer restartedServer.Close()

	connection = dialTerminalWithQuery(t, restartedServer.URL, item.ID, thread.ID, "tool=pi&agent=pi&cols=80&rows=24")
	code, reason = readTerminalClose(t, connection)
	_ = connection.Close()
	if code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("implicit post-restart close = (%d, %q)", code, reason)
	}
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("handler restart implicitly relaunched Pi %d times, want 1 total", launches)
	}

	connection = dialTerminalWithQuery(t, restartedServer.URL, item.ID, thread.ID, "tool=pi&agent=pi&restart=1&cols=80&rows=24")
	code, reason = readTerminalClose(t, connection)
	_ = connection.Close()
	if code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("explicit post-restart close = (%d, %q)", code, reason)
	}
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 2 {
		t.Fatalf("explicit handler-restart launch count = %d, want 2", launches)
	}
}

func TestCodingAgentRestartFenceSurvivesFailedReplacement(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	launchesPath := filepath.Join(t.TempDir(), "launches")
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nexit 7\n")
	server := newTerminalTestServer(handler)
	defer server.Close()

	connectAndClose := func(query string) (int, string) {
		connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, query)
		defer connection.Close()
		return readTerminalClose(t, connection)
	}
	if code, reason := connectAndClose("tool=pi&agent=pi&cols=80&rows=24"); code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("initial exit close = (%d, %q)", code, reason)
	}
	markerPath := handler.codingAgentExitMarkerPath(item.ID, thread.ID, codingAgentPi)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("initial restart fence: %v", err)
	}

	originalEnvPath := handler.envPath
	handler.envPath = filepath.Join(t.TempDir(), "missing-env")
	if code, reason := connectAndClose("tool=pi&agent=pi&restart=1&cols=80&rows=24"); code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("failed replacement close = (%d, %q)", code, reason)
	}
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("failed replacement executed Pi %d times, want 1", launches)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("failed replacement removed restart fence: %v", err)
	}
	if code, reason := connectAndClose("tool=pi&agent=pi&cols=80&rows=24"); code != websocket.CloseNormalClosure || reason != "Coding agent ended" {
		t.Fatalf("implicit retry after failed replacement = (%d, %q)", code, reason)
	}
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("implicit retry after failure launched Pi %d times, want 1", launches)
	}

	handler.envPath = originalEnvPath
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nprintf 'pi-restart-live\\n'\nwhile :; do /bin/sleep 1; done\n")
	connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, "tool=pi&agent=pi&restart=1&cols=80&rows=24")
	defer connection.Close()
	readTerminalUntil(t, connection, "pi-restart-live")
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 2 {
		t.Fatalf("successful replacement launched Pi %d times, want 2", launches)
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful replacement left restart fence: %v", err)
	}
	if handler.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("successful live replacement left logical exit state")
	}
}

func TestUnconfirmedLiveRestartPaneCannotBeAttachedImplicitly(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nprintf 'pi-live\\n'\nwhile :; do /bin/sleep 1; done\n")

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	window, found, err := handler.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%t err=%v", found, err)
	}
	panes, err := handler.tmuxAgentPanes(window.ID)
	if err != nil || len(panes) != 1 || panes[0].Agent != codingAgentPi {
		t.Fatalf("find live Pi pane: panes=%#v err=%v", panes, err)
	}
	pane := panes[0]
	state, err := handler.tmuxPaneExitState(pane.ID)
	if err != nil || !state.Found || state.Dead || state.ServerPID == "" {
		t.Fatalf("inspect live Pi pane: state=%#v err=%v", state, err)
	}

	// Model a backend crash after it created a live replacement but before it
	// committed that restart by removing the old durable exit marker.
	err = handler.withCodingAgentExitMarkerLock(item.ID, thread.ID, codingAgentPi, func(path string) error {
		marker := codingAgentExitMarker{
			ProjectID: item.ID,
			ThreadID:  thread.ID,
			Agent:     codingAgentPi,
			PaneID:    "%old-pane",
			ServerPID: "old-server-pid",
			Status:    "7",
			ExitedAt:  "1700000000",
		}
		if err := writeCodingAgentExitMarker(path, marker); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := handler.ensureCodingAgentPaneWithRestart(
		item,
		thread,
		codingAgentPi,
		"",
		sessionName,
		false,
		nil,
	); !errors.Is(err, errCodingAgentEnded) {
		t.Fatalf("implicit attach error = %v, want coding agent ended", err)
	}
	afterImplicit, err := handler.tmuxPaneExitState(pane.ID)
	if err != nil || !afterImplicit.Found || afterImplicit.Dead || afterImplicit.ServerPID != state.ServerPID {
		t.Fatalf("implicit attach changed unconfirmed pane: state=%#v err=%v", afterImplicit, err)
	}
	if !handler.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("implicit attach removed the restart fence")
	}

	expectedServerPID := ""
	gotPane, _, created, err := handler.ensureCodingAgentPaneWithRestart(
		item,
		thread,
		codingAgentPi,
		"",
		sessionName,
		true,
		&expectedServerPID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPane != pane.ID || created || expectedServerPID != state.ServerPID {
		t.Fatalf(
			"explicit confirmation = pane %q created=%t server=%q, want pane %q created=false server=%q",
			gotPane,
			created,
			expectedServerPID,
			pane.ID,
			state.ServerPID,
		)
	}
	if handler.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("explicit restart returned before removing the restart fence")
	}
}

func TestCodingAgentExitPersistenceFailurePreservesDeadPane(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	launchCommand, launchArgs := handler.codingAgentLaunchCommand(codingAgentPi, "/bin/sh", []string{"-c", "exit 7"})
	window, err := handler.createTmuxSession(sessionName, thread.Cwd, "pi", launchCommand, launchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureSharedToolWindow(sessionName, window, "pi"); err != nil {
		t.Fatal(err)
	}
	panes, err := handler.tmuxAgentPanes(window.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("find dead evidence pane: panes=%#v err=%v", panes, err)
	}
	waitForTmuxPaneDead(t, handler, panes[0].ID)
	state, err := handler.tmuxPaneExitState(panes[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler.agentExitDirectory = filepath.Join(blockedParent, "coding-agent-exits")
	var logs synchronizedBuffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()
	handler.handleCodingAgentExit(item.ID, thread.ID, sessionName, window.ID, panes[0].ID, codingAgentPi, state)
	if err := handler.prepareCodingAgentRestartLocked(item.ID, thread.ID, sessionName, codingAgentPi); err == nil {
		t.Fatal("restart reaped retained evidence despite an unwritable marker path")
	}
	if exists, err := handler.tmuxSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("persistence failure removed evidence session: exists=%t err=%v", exists, err)
	}
	panes, err = handler.tmuxAgentPanes(window.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("retained evidence panes = %#v err=%v", panes, err)
	}
	state, err = handler.tmuxPaneExitState(panes[0].ID)
	if err != nil || !state.Found || !state.Dead || state.Status != "7" {
		t.Fatalf("retained evidence state = %#v err=%v", state, err)
	}
	for _, expected := range []string{"coding agent exited:", "status=7", "signal=none"} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("persistence-failure exit log %q does not contain %q", logs.String(), expected)
		}
	}
}

func TestDeadReplacementDuringRestartConfirmationLogsExit(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, pane, state := newRetainedDeadCodingAgentPane(t, handler, item, thread, "exit 13")
	err := handler.withCodingAgentExitMarkerLock(item.ID, thread.ID, codingAgentPi, func(path string) error {
		marker := codingAgentExitMarker{
			ProjectID: item.ID,
			ThreadID:  thread.ID,
			Agent:     codingAgentPi,
			PaneID:    "%old-pane",
			ServerPID: "old-server-pid",
			Status:    "7",
		}
		if err := writeCodingAgentExitMarker(path, marker); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	})
	if err != nil {
		t.Fatal(err)
	}

	var logs synchronizedBuffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()

	err = handler.confirmCodingAgentRestart(
		item.ID,
		thread.ID,
		codingAgentPi,
		pane.ID,
		state.ServerPID,
		true,
	)
	if !errors.Is(err, errCodingAgentEnded) {
		t.Fatalf("confirm dead replacement error = %v, want coding agent ended", err)
	}
	for _, expected := range []string{
		"coding agent exited:",
		"session=\"" + sessionName + "\"",
		"pane=\"" + pane.ID + "\"",
		"status=13",
		"signal=none",
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("restart-confirmation exit log %q does not contain %q", logs.String(), expected)
		}
	}
	marker, found, err := handler.readCodingAgentExitMarker(item.ID, thread.ID, codingAgentPi)
	if err != nil || !found || marker.PaneID != pane.ID || marker.ServerPID != state.ServerPID || marker.Status != "13" {
		t.Fatalf("dead replacement marker: found=%t marker=%#v err=%v", found, marker, err)
	}
}

func TestRecordCodingAgentExitPersistsWithoutReaping(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	_, _, pane, state := newRetainedDeadCodingAgentPane(t, handler, item, thread, "exit 10")
	current, err := handler.recordCodingAgentExit(item.ID, thread.ID, codingAgentPi, pane.ID, state)
	if err != nil || !current {
		t.Fatalf("record exit: current=%t err=%v", current, err)
	}
	retained, err := handler.tmuxPaneExitState(pane.ID)
	if err != nil || !retained.Found || !retained.Dead || retained.ServerPID != state.ServerPID {
		t.Fatalf("recordCodingAgentExit reaped evidence: state=%#v err=%v", retained, err)
	}
	marker, found, err := handler.readCodingAgentExitMarker(item.ID, thread.ID, codingAgentPi)
	if err != nil || !found || marker.PaneID != pane.ID || marker.ServerPID != state.ServerPID || marker.Status != "10" {
		t.Fatalf("durable marker = %#v found=%t err=%v", marker, found, err)
	}
}

func TestCodingAgentRestartPreservesPaneWhenMarkerPathIsDirectory(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, window, pane, state := newRetainedDeadCodingAgentPane(t, handler, item, thread, "exit 11")
	markerPath := handler.codingAgentExitMarkerPath(item.ID, thread.ID, codingAgentPi)
	if err := os.MkdirAll(markerPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := handler.prepareCodingAgentRestartLocked(item.ID, thread.ID, sessionName, codingAgentPi); err == nil {
		t.Fatal("restart accepted a directory in place of its durable marker")
	}
	retained, err := handler.tmuxPaneExitState(pane.ID)
	if err != nil || !retained.Found || !retained.Dead || retained.ServerPID != state.ServerPID {
		t.Fatalf("restart lost retained pane evidence: state=%#v err=%v", retained, err)
	}
	if exists, err := handler.tmuxSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("restart removed evidence session %q (window %q): exists=%t err=%v", sessionName, window.ID, exists, err)
	}
}

func TestCodingAgentRestartRepairsCorruptRegularMarkerBeforeReaping(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, pane, state := newRetainedDeadCodingAgentPane(t, handler, item, thread, "exit 12")
	markerPath := handler.codingAgentExitMarkerPath(item.ID, thread.ID, codingAgentPi)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := handler.prepareCodingAgentRestartLocked(item.ID, thread.ID, sessionName, codingAgentPi); err != nil {
		t.Fatal(err)
	}
	marker, found, err := handler.readCodingAgentExitMarker(item.ID, thread.ID, codingAgentPi)
	if err != nil || !found {
		t.Fatalf("read repaired restart fence: found=%t marker=%#v err=%v", found, marker, err)
	}
	if marker.PaneID != pane.ID || marker.ServerPID != state.ServerPID || marker.Status != "12" {
		t.Fatalf("repaired restart fence = %#v, want pane=%q server=%q status=12", marker, pane.ID, state.ServerPID)
	}
	if retained, err := handler.tmuxPaneExitState(pane.ID); err != nil || retained.Found {
		t.Fatalf("fenced retained pane was not reaped: state=%#v err=%v", retained, err)
	}
}

func TestExplicitRestartReplacesRetainedDeadPane(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	launchCommand, launchArgs := handler.codingAgentLaunchCommand(
		codingAgentPi,
		"/bin/sh",
		[]string{"-c", "exit 7"},
	)
	target, err := handler.createTmuxSession(sessionName, thread.Cwd, "pi", launchCommand, launchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureSharedToolWindow(sessionName, target, "pi"); err != nil {
		t.Fatal(err)
	}
	panes, err := handler.tmuxAgentPanes(target.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("find retained dead Pi pane: panes=%#v err=%v", panes, err)
	}
	deadPaneID := panes[0].ID
	waitForTmuxPaneDead(t, handler, deadPaneID)

	setMockPi(t, "#!/bin/sh\nprintf 'pi-restarted\\n'\nwhile :; do /bin/sleep 1; done\n")
	gotSession, _, created, err := handler.ensureTmuxSessionWithCodingAgent(item, thread, "pi", "", codingAgentPi, true)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession != sessionName || !created {
		t.Fatalf("explicit restart = (%q, created=%t), want recreated %q", gotSession, created, sessionName)
	}
	paneID, _, paneCreated, err := handler.ensureCodingAgentPane(item, thread, codingAgentPi, "", sessionName)
	if err != nil {
		t.Fatal(err)
	}
	if paneCreated || paneID == deadPaneID {
		t.Fatalf("explicit restart pane = %q created=%t, old pane %q", paneID, paneCreated, deadPaneID)
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: paneID}, "pi-restarted")
	if handler.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("explicit restart left the old Pi exit tombstone active")
	}
}

func TestExternalTmuxLossIsGenericAndRecoverable(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nprintf 'pi-ready\\n'\nwhile :; do /bin/sleep 1; done\n")
	server := newTerminalTestServer(handler)
	defer server.Close()

	query := "tool=pi&agent=pi&cols=80&rows=24"
	connection := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, query)
	readTerminalUntil(t, connection, "pi-ready")
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("kill external tmux server: %v: %s", err, output)
	}
	code, reason := readTerminalClose(t, connection)
	_ = connection.Close()
	if code != websocket.CloseNormalClosure || reason != "Terminal session ended" {
		t.Fatalf("external tmux loss close = (%d, %q), want generic recoverable close", code, reason)
	}

	reconnected := dialTerminalWithQuery(t, server.URL, item.ID, thread.ID, query)
	defer reconnected.Close()
	readTerminalUntil(t, reconnected, "pi-ready")
}

func TestCodingAgentWatcherHandlesPaneIDReuseAfterTmuxRestart(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	// isolatedTmuxServer leaves its capability-probe pane running. Reset it so
	// the first application pane and the first pane after restart are both %0.
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("reset tmux server after capability probe: %v: %s", err, output)
	}
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")
	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find original Pi window: found=%t err=%v", found, err)
	}
	panes, err := handler.tmuxAgentPanes(target.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("find original Pi pane: panes=%#v err=%v", panes, err)
	}
	oldPaneID := panes[0].ID
	oldState, err := handler.tmuxPaneExitState(oldPaneID)
	if err != nil || !oldState.Found || oldState.ServerPID == "" {
		t.Fatalf("inspect original Pi pane: state=%#v err=%v", oldState, err)
	}
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("restart tmux server: %v: %s", err, output)
	}

	setMockPi(t, "#!/bin/sh\nexit 9\n")
	_, _, _, err = handler.ensureTmuxSession(item, thread, "pi")
	if err != nil && !errors.Is(err, errCodingAgentEnded) {
		t.Fatal(err)
	}

	var reusedKey codingAgentExitKey
	var reusedState tmuxPaneExitState
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		handler.agentWatchMu.Lock()
		for key, state := range handler.agentExits {
			if key.ProjectID == item.ID && key.ThreadID == thread.ID && key.Agent == codingAgentPi && state.Status == "9" {
				reusedKey = key
				reusedState = state
				break
			}
		}
		handler.agentWatchMu.Unlock()
		if reusedKey.PaneID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reusedKey.PaneID == "" {
		t.Fatal("reused Pi pane exit was not captured")
	}
	if reusedKey.PaneID != oldPaneID {
		t.Fatalf("tmux did not reuse pane ID: old=%q new=%q", oldPaneID, reusedKey.PaneID)
	}
	if reusedState.ServerPID == oldState.ServerPID {
		t.Fatalf("tmux server PID did not change: %q", reusedState.ServerPID)
	}
	if reusedState.Status != "9" {
		t.Fatalf("reused Pi pane status = %q, want 9", reusedState.Status)
	}
}

func TestIncarnationCleanupPreservesReusedTmuxTargets(t *testing.T) {
	handler, _ := newIsolatedTmuxHandler(t)
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("reset tmux server after capability probe: %v: %s", err, output)
	}
	sessionName := "incarnation-cleanup"
	oldTarget, err := handler.createTmuxSession(sessionName, "/", "old", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	oldPaneOutput, err := handler.tmuxCommand("display-message", "-p", "-t", oldTarget.ID, "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("find old pane: %v: %s", err, oldPaneOutput)
	}
	oldPaneID := strings.TrimSpace(string(oldPaneOutput))
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("restart tmux server: %v: %s", err, output)
	}

	replacement, err := handler.createTmuxSession(sessionName, "/", "replacement", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	replacementPaneOutput, err := handler.tmuxCommand("display-message", "-p", "-t", replacement.ID, "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("find replacement pane: %v: %s", err, replacementPaneOutput)
	}
	replacementPaneID := strings.TrimSpace(string(replacementPaneOutput))
	if replacement.ServerPID == oldTarget.ServerPID {
		t.Fatalf("tmux server pid was reused: %q", replacement.ServerPID)
	}
	if replacement.ID != oldTarget.ID || replacementPaneID != oldPaneID {
		t.Fatalf("tmux did not reuse target IDs: old=(%q,%q) replacement=(%q,%q)", oldTarget.ID, oldPaneID, replacement.ID, replacementPaneID)
	}

	if err := handler.killTmuxPaneIncarnation(oldPaneID, oldTarget.ServerPID, false); err != nil {
		t.Fatal(err)
	}
	if state, err := handler.tmuxPaneExitState(replacementPaneID); err != nil || !state.Found || state.ServerPID != replacement.ServerPID {
		t.Fatalf("stale pane cleanup removed replacement: state=%#v err=%v", state, err)
	}
	if err := handler.killTmuxWindowIncarnation(oldTarget.ID, oldTarget.ServerPID); err != nil {
		t.Fatal(err)
	}
	if pid, found, err := handler.tmuxTargetServerPID(replacement.ID); err != nil || !found || pid != replacement.ServerPID {
		t.Fatalf("stale window cleanup removed replacement: pid=%q found=%t err=%v", pid, found, err)
	}
	if err := handler.killTmuxSessionIncarnation(sessionName, oldTarget.ServerPID); err != nil {
		t.Fatal(err)
	}
	if exists, err := handler.tmuxExactSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("stale session cleanup removed replacement: exists=%t err=%v", exists, err)
	}
	if err := handler.killTmuxSessionIncarnation(sessionName, replacement.ServerPID); err != nil {
		t.Fatal(err)
	}
	if exists, err := handler.tmuxExactSessionExists(sessionName); err != nil || exists {
		t.Fatalf("current session cleanup did not remove its incarnation: exists=%t err=%v", exists, err)
	}
}

func TestProcessActionsRejectReusedTmuxIncarnations(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("reset tmux server after capability probe: %v: %s", err, output)
	}
	thread := item.Threads[0]
	sessionName := tmuxSessionName(item.ID, thread.ID, "process")
	const processID = "captured-process"
	oldReady := fmt.Sprintf("old-process-ready-%d", time.Now().UnixNano())
	oldTarget, err := handler.createTmuxSession(
		sessionName,
		thread.Cwd,
		"old-process",
		"/bin/sh",
		[]string{"-c", "printf '" + oldReady + "\\n'; exec /bin/sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureProcessWindow(sessionName, oldTarget, processID, "old-process"); err != nil {
		t.Fatal(err)
	}
	_, captured, found, err := handler.processForRequest(item, thread, processID)
	if err != nil || !found {
		t.Fatalf("capture process incarnation: target=%#v found=%t err=%v", captured, found, err)
	}
	waitForTmuxPaneOutput(t, handler, captured, oldReady)
	guardedOutput, err := handler.tmuxProcessCommand(captured, "capture-pane", "-p", "-J", "-S", "-50", "-t", captured.ID)
	if err != nil || !strings.Contains(string(guardedOutput), oldReady) {
		t.Fatalf("capture current process: err=%v output=%q", err, guardedOutput)
	}
	currentInput := "current-input-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := handler.sendTmuxInput(captured, currentInput, false); err != nil {
		t.Fatalf("send current process input: %v", err)
	}
	waitForTmuxPaneOutput(t, handler, captured, currentInput)
	viewName, err := handler.createTmuxViewSession(item, thread, sessionName, captured)
	if err != nil {
		t.Fatalf("link current process view: %v", err)
	}
	handler.closeTmuxViewSession(viewName)

	if output, err := handler.tmuxCommand("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("restart tmux server: %v: %s", err, output)
	}
	replacementReady := fmt.Sprintf("replacement-process-ready-%d", time.Now().UnixNano())
	replacement, err := handler.createTmuxSession(
		sessionName,
		thread.Cwd,
		"replacement-process",
		"/bin/sh",
		[]string{"-c", "printf '" + replacementReady + "\\n'; exec /bin/sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureProcessWindow(sessionName, replacement, processID, "replacement-process"); err != nil {
		t.Fatal(err)
	}
	if replacement.ID != captured.ID {
		t.Fatalf("tmux did not reuse process window id: old=%q replacement=%q", captured.ID, replacement.ID)
	}
	if replacement.ServerPID == captured.ServerPID {
		t.Fatalf("tmux server PID did not change: %q", replacement.ServerPID)
	}
	waitForTmuxPaneOutput(t, handler, replacement, replacementReady)
	assertStaleProcessActionsPreserveReplacement(t, handler, item, thread, sessionName, captured, replacement, replacementReady, "server-restart")

	retagged := replacement
	retagged.ProcessID = processID
	if err := handler.setTmuxWindowOption(replacement.ID, "@dire-mux-process-id", "replacement-process"); err != nil {
		t.Fatal(err)
	}
	assertStaleProcessActionsPreserveReplacement(t, handler, item, thread, sessionName, retagged, replacement, replacementReady, "process-retag")
}

func TestGuardedProcessDeleteRemovesCurrentIncarnation(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	window, err := handler.newProcessWindow(item, thread, "delete-me", "exec /bin/sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	_, target, found, err := handler.processForRequest(item, thread, window.ID)
	if err != nil || !found {
		t.Fatalf("find current process: found=%t err=%v", found, err)
	}
	output, err := handler.tmuxProcessCommand(target, "kill-window", "-t", target.ID)
	if err != nil {
		t.Fatalf("guarded process delete: %v: %s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("guarded process delete output = %q, want empty after confirmation", output)
	}
	if _, _, found, err := handler.processForRequest(item, thread, window.ID); err != nil || found {
		t.Fatalf("deleted process lookup: found=%t err=%v", found, err)
	}
}

func assertStaleProcessActionsPreserveReplacement(
	t *testing.T,
	handler *terminalHandler,
	item project.Project,
	thread project.Thread,
	sessionName string,
	stale tmuxWindowTarget,
	replacement tmuxWindowTarget,
	replacementReady string,
	phase string,
) {
	t.Helper()
	output, err := handler.tmuxProcessCommand(stale, "capture-pane", "-p", "-J", "-S", "-50", "-t", stale.ID)
	if !errors.Is(err, errTmuxProcessIncarnationChanged) {
		t.Fatalf("%s stale capture error = %v, output=%q", phase, err, output)
	}
	if strings.Contains(string(output), replacementReady) {
		t.Fatalf("%s stale capture leaked replacement output %q", phase, output)
	}

	input := "stale-input-" + phase + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := handler.sendTmuxInput(stale, input, true); !errors.Is(err, errTmuxProcessIncarnationChanged) {
		t.Fatalf("%s stale input error = %v", phase, err)
	}
	time.Sleep(50 * time.Millisecond)
	directOutput, directErr := handler.tmuxCommand("capture-pane", "-p", "-J", "-S", "-50", "-t", replacement.ID).CombinedOutput()
	if directErr != nil {
		t.Fatalf("%s inspect replacement after stale input: %v: %s", phase, directErr, directOutput)
	}
	if strings.Contains(string(directOutput), input) {
		t.Fatalf("%s stale input reached replacement pane: %q", phase, directOutput)
	}

	if _, err := handler.tmuxProcessCommand(stale, "send-keys", "-t", stale.ID, "C-c"); !errors.Is(err, errTmuxProcessIncarnationChanged) {
		t.Fatalf("%s stale interrupt error = %v", phase, err)
	}
	if pid, found, err := handler.tmuxTargetServerPID(replacement.ID); err != nil || !found || pid != replacement.ServerPID {
		t.Fatalf("%s stale interrupt stopped replacement: pid=%q found=%t err=%v", phase, pid, found, err)
	}

	if _, err := handler.tmuxProcessCommand(stale, "kill-window", "-t", stale.ID); !errors.Is(err, errTmuxProcessIncarnationChanged) {
		t.Fatalf("%s stale delete error = %v", phase, err)
	}
	if pid, found, err := handler.tmuxTargetServerPID(replacement.ID); err != nil || !found || pid != replacement.ServerPID {
		t.Fatalf("%s stale delete stopped replacement: pid=%q found=%t err=%v", phase, pid, found, err)
	}

	beforeLinks := tmuxWindowLinkCount(t, handler, replacement.ID)
	if viewName, err := handler.createTmuxViewSession(item, thread, sessionName, stale); !errors.Is(err, errTmuxProcessIncarnationChanged) {
		if err == nil {
			handler.closeTmuxViewSession(viewName)
		}
		t.Fatalf("%s stale view link error = %v", phase, err)
	}
	if afterLinks := tmuxWindowLinkCount(t, handler, replacement.ID); afterLinks != beforeLinks {
		t.Fatalf("%s stale view changed replacement link count: before=%d after=%d", phase, beforeLinks, afterLinks)
	}
}

func tmuxWindowLinkCount(t *testing.T, handler *terminalHandler, windowID string) int {
	t.Helper()
	output, err := handler.tmuxCommand("list-windows", "-a", "-F", "#{window_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list tmux window links: %v: %s", err, output)
	}
	count := 0
	for _, line := range strings.FieldsFunc(string(output), func(character rune) bool { return character == '\n' || character == '\r' }) {
		if line == windowID {
			count++
		}
	}
	return count
}

func TestCanonicalTmuxSessionEnsureIgnoresSuffixDecoy(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "terminal")
	decoySession := canonicalSession + "-suffix-decoy"

	first, err := handler.createTmuxSession(decoySession, thread.Cwd, "decoy-one", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := handler.createTmuxWindow(thread.Cwd, decoySession, "decoy-two", "/bin/sleep", []string{"30"}, false)
	if err != nil {
		t.Fatal(err)
	}
	decoyWindows, err := handler.tmuxDetailedWindows(decoySession)
	if err != nil {
		t.Fatal(err)
	}
	decoyPIDs := map[string]string{
		first.ID:  tmuxPanePID(t, handler, first.ID),
		second.ID: tmuxPanePID(t, handler, second.ID),
	}

	if exists, err := handler.tmuxSessionExists(canonicalSession); err != nil || exists {
		t.Fatalf("canonical lookup matched suffix decoy: exists=%t err=%v", exists, err)
	}
	gotSession, _, created, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if gotSession != canonicalSession || !created {
		t.Fatalf("canonical ensure = (%q, created=%t), want newly created %q", gotSession, created, canonicalSession)
	}
	if exists, err := handler.tmuxExactSessionExists(canonicalSession); err != nil || !exists {
		t.Fatalf("canonical session was not separately created: exists=%t err=%v", exists, err)
	}
	if exists, err := handler.tmuxExactSessionExists(decoySession); err != nil || !exists {
		t.Fatalf("suffix decoy was removed: exists=%t err=%v", exists, err)
	}

	decoyWindowsAfter, err := handler.tmuxDetailedWindows(decoySession)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoyWindowsAfter) != len(decoyWindows) {
		t.Fatalf("suffix decoy windows changed from %#v to %#v", decoyWindows, decoyWindowsAfter)
	}
	for index, before := range decoyWindows {
		if after := decoyWindowsAfter[index]; after != before {
			t.Fatalf("suffix decoy window %d changed from %#v to %#v", index, before, after)
		}
		if gotPID := tmuxPanePID(t, handler, before.Target.ID); gotPID != decoyPIDs[before.Target.ID] {
			t.Fatalf("suffix decoy window %q pid changed from %q to %q", before.Target.ID, decoyPIDs[before.Target.ID], gotPID)
		}
	}

	canonicalWindows, err := handler.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonicalWindows) != 1 || canonicalWindows[0].Name != "shell" {
		t.Fatalf("canonical windows = %#v, want a separate shell window", canonicalWindows)
	}
	if _, decoy := decoyPIDs[canonicalWindows[0].Target.ID]; decoy {
		t.Fatalf("canonical session reused decoy window %q", canonicalWindows[0].Target.ID)
	}
}

func TestPiAndClaudeShareTheFixedPiWindow(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockCodingAgents(t)

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	piPaneID, _, created, err := handler.ensureCodingAgentPane(item, thread, codingAgentPi, "", sessionName)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("the existing Pi process was replaced instead of adopted")
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: piPaneID}, "pi-ready")

	claudePaneID, _, created, err := handler.ensureCodingAgentPane(item, thread, codingAgentClaude, "", sessionName)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("Claude pane was not created")
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: claudePaneID}, "claude-ready")

	windows, err := handler.tmuxWindows(sessionName)
	if err != nil {
		t.Fatal(err)
	}
	assertTmuxWindowNames(t, windows, "pi")
	panes, err := handler.tmuxAgentPanes(windowsTargetID(t, handler, sessionName, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 2 {
		t.Fatalf("coding agent panes = %#v, want Pi and Claude", panes)
	}
	assertCodingAgentPane(t, panes, codingAgentPi, piPaneID, false)
	assertCodingAgentPane(t, panes, codingAgentClaude, claudePaneID, true)

	if _, _, created, err := handler.ensureCodingAgentPane(item, thread, codingAgentClaude, "", sessionName); err != nil {
		t.Fatal(err)
	} else if created {
		t.Fatal("an existing Claude pane was created again")
	}
	if _, _, created, err := handler.ensureCodingAgentPane(item, thread, codingAgentPi, "", sessionName); err != nil {
		t.Fatal(err)
	} else if created {
		t.Fatal("an existing Pi pane was created again")
	}
	panes, err = handler.tmuxAgentPanes(windowsTargetID(t, handler, sessionName, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	assertCodingAgentPane(t, panes, codingAgentPi, piPaneID, true)
	assertCodingAgentPane(t, panes, codingAgentClaude, claudePaneID, false)
}

func TestClaudeGPTUsesADistinctPaneInTheFixedPiWindow(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockCodingAgents(t)
	handler.claudeSandboxPluginPath = t.TempDir()
	handler.claudeSandboxPluginErr = nil

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	gptPaneID, _, created, err := handler.ensureCodingAgentPaneWithOptions(
		item,
		thread,
		codingAgentClaudeGPT,
		"",
		sessionName,
		codingAgentLaunchOptions{Model: "gpt-5.4"},
		false,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("Claude GPT pane was not created")
	}
	waitForTmuxPaneOutput(t, handler, tmuxWindowTarget{ID: gptPaneID}, "claude-ready")

	panes, err := handler.tmuxAgentPanes(windowsTargetID(t, handler, sessionName, "pi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 2 {
		t.Fatalf("coding agent panes = %#v, want Pi and Claude GPT", panes)
	}
	piPaneID := ""
	for _, pane := range panes {
		if pane.Agent == codingAgentPi {
			piPaneID = pane.ID
			break
		}
	}
	if piPaneID == "" {
		t.Fatalf("coding agent panes = %#v, missing Pi", panes)
	}
	assertCodingAgentPane(t, panes, codingAgentPi, piPaneID, false)
	assertCodingAgentPane(t, panes, codingAgentClaudeGPT, gptPaneID, true)
	if repeatedPaneID, _, repeatedCreated, err := handler.ensureCodingAgentPaneWithOptions(
		item,
		thread,
		codingAgentClaudeGPT,
		"",
		sessionName,
		codingAgentLaunchOptions{Model: "gpt-5.3-codex"},
		false,
		nil,
	); err != nil || repeatedCreated || repeatedPaneID != gptPaneID {
		t.Fatalf("repeat Claude GPT ensure = pane %q created=%t err=%v, want %q", repeatedPaneID, repeatedCreated, err, gptPaneID)
	}
}

func TestPiSessionEndsWhenAgentExitsWithoutProcesses(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\n/bin/sleep 0.15\n")

	if _, _, _, err := handler.ensureTmuxSession(item, thread, "terminal"); err != nil {
		t.Fatal(err)
	}
	output, err := handler.tmuxCommand("set-option", "-g", "-w", "remain-on-exit", "on").CombinedOutput()
	if err != nil {
		t.Fatalf("enable remain-on-exit: %v: %s", err, output)
	}

	sessionName, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("Pi window was not created")
	}

	waitForTmuxSessionGone(t, handler, sessionName)
}

func TestCodingAgentExitIsLoggedBeforePaneRemoval(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	launchCommand, launchArgs := handler.codingAgentLaunchCommand(codingAgentPi, "/bin/sh", []string{"-c", "exit 7"})
	window, err := handler.createTmuxSession(sessionName, thread.Cwd, "pi", launchCommand, launchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureSharedToolWindow(sessionName, window, "pi"); err != nil {
		t.Fatal(err)
	}
	panes, err := handler.tmuxAgentPanes(window.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("find retained evidence pane: panes=%#v err=%v", panes, err)
	}
	waitForTmuxPaneDead(t, handler, panes[0].ID)
	state, err := handler.tmuxPaneExitState(panes[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	logs := &paneObservingLogWriter{handler: handler, paneID: panes[0].ID}
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()

	handler.handleCodingAgentExit(item.ID, thread.ID, sessionName, window.ID, panes[0].ID, codingAgentPi, state)
	waitForTmuxSessionGone(t, handler, sessionName)

	output, sawExit, observed, observeErr := logs.snapshot()
	if !sawExit || observeErr != nil || !observed.Found || !observed.Dead || observed.ServerPID != state.ServerPID {
		t.Fatalf("pane at exit-log write: saw=%t state=%#v err=%v", sawExit, observed, observeErr)
	}
	for _, expected := range []string{
		"coding agent exited:",
		"project=\"" + item.ID + "\"",
		"thread=\"" + thread.ID + "\"",
		"session=\"" + sessionName + "\"",
		"window=\"@",
		"pane=\"" + panes[0].ID + "\"",
		"status=7",
		"signal=none",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("coding-agent exit log %q does not contain %q", output, expected)
		}
	}
}

func TestCodingAgentSignalExitIsLoggedBeforePaneRemoval(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nkill -TERM $$\n")

	var logs synchronizedBuffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil && !errors.Is(err, errCodingAgentEnded) {
		t.Fatal(err)
	}
	waitForTmuxSessionGone(t, handler, sessionName)

	output := logs.String()
	for _, expected := range []string{
		"coding agent exited:",
		"project=\"" + item.ID + "\"",
		"thread=\"" + thread.ID + "\"",
		"session=\"" + sessionName + "\"",
		"status=unavailable",
		"signal=term",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("coding-agent signal exit log %q does not contain %q", output, expected)
		}
	}
}

func TestProjectLegacyTerminalSessionIsAdoptedWithAllWindows(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	legacySession := legacyProjectTmuxSessionName(item.ID, "terminal")
	first, err := handler.createTmuxSession(legacySession, thread.Cwd, "shell", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := handler.createTmuxWindow(thread.Cwd, legacySession, "shell", "/bin/sleep", []string{"30"}, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPIDs := map[string]string{
		first.ID:  tmuxPanePID(t, handler, first.ID),
		second.ID: tmuxPanePID(t, handler, second.ID),
	}

	canonicalSession, _, created, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("legacy terminal session was replaced instead of adopted")
	}
	if canonicalSession != tmuxSessionName(item.ID, thread.ID, "terminal") {
		t.Fatalf("canonical terminal session = %q", canonicalSession)
	}
	if exists, err := handler.tmuxSessionExists(legacySession); err != nil || exists {
		t.Fatalf("legacy terminal session still exists: exists=%t err=%v", exists, err)
	}
	windows, err := handler.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		t.Fatal(err)
	}
	for windowID, wantPID := range wantPIDs {
		found := false
		for _, window := range windows {
			if window.Target.ID == windowID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("adopted terminal windows %#v do not contain %q", windows, windowID)
		}
		if gotPID := tmuxPanePID(t, handler, windowID); gotPID != wantPID {
			t.Fatalf("adopted terminal window %q pid = %q, want %q", windowID, gotPID, wantPID)
		}
	}
}

func TestProjectLegacyTerminalConflictPreservesCanonicalAndLegacyProcesses(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "terminal")
	canonical, err := handler.createTmuxSession(canonicalSession, thread.Cwd, "shell", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	legacySession := legacyProjectTmuxSessionName(item.ID, "terminal")
	legacy, err := handler.createTmuxSession(legacySession, thread.Cwd, "shell", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	wantPIDs := map[string]string{
		canonical.ID: tmuxPanePID(t, handler, canonical.ID),
		legacy.ID:    tmuxPanePID(t, handler, legacy.ID),
	}

	gotSession, _, created, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if gotSession != canonicalSession || created {
		t.Fatalf("terminal ensure = (%q, created=%t), want existing %q", gotSession, created, canonicalSession)
	}
	windows, err := handler.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		t.Fatal(err)
	}
	for windowID, wantPID := range wantPIDs {
		found := false
		for _, window := range windows {
			if window.Target.ID == windowID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("merged terminal windows %#v do not contain %q", windows, windowID)
		}
		if gotPID := tmuxPanePID(t, handler, windowID); gotPID != wantPID {
			t.Fatalf("terminal conflict changed pid for %q from %q to %q", windowID, wantPID, gotPID)
		}
	}
}

func TestProjectLegacyTerminalIsNotAdoptedByNonDefaultThread(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	secondThread, err := handler.projects.AddThread(item.ID, "Second thread", false)
	if err != nil {
		t.Fatal(err)
	}
	item, err = handler.projects.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacySession := legacyProjectTmuxSessionName(item.ID, "terminal")
	legacy, err := handler.createTmuxSession(legacySession, item.Threads[0].Cwd, "shell", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	legacyPID := tmuxPanePID(t, handler, legacy.ID)

	secondSession, _, created, err := handler.ensureTmuxSession(item, secondThread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if !created || secondSession != tmuxSessionName(item.ID, secondThread.ID, "terminal") {
		t.Fatalf("non-default terminal ensure = (%q, created=%t)", secondSession, created)
	}
	if exists, err := handler.tmuxSessionExists(legacySession); err != nil || !exists {
		t.Fatalf("non-default thread adopted project legacy terminal: exists=%t err=%v", exists, err)
	}
	if gotPID := tmuxPanePID(t, handler, legacy.ID); gotPID != legacyPID {
		t.Fatalf("non-default terminal changed legacy pid from %q to %q", legacyPID, gotPID)
	}
}

func TestLegacyPiSessionIsAdoptedWithoutRestartingProcess(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	legacySession := legacyThreadTmuxSessionName(item.ID, thread.ID, "pi")
	target, err := handler.createTmuxSession(
		legacySession,
		thread.Cwd,
		"pi",
		"/bin/sh",
		[]string{"-c", "exec /bin/sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)

	canonicalSession, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("legacy Pi process was replaced instead of adopted")
	}
	if canonicalSession != tmuxSessionName(item.ID, thread.ID, "pi") {
		t.Fatalf("canonical session = %q", canonicalSession)
	}
	if exists, err := handler.tmuxSessionExists(legacySession); err != nil || exists {
		t.Fatalf("legacy session still exists: exists=%t err=%v", exists, err)
	}
	adopted, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find adopted Pi window: found=%t err=%v", found, err)
	}
	if adopted.ID != target.ID {
		t.Fatalf("adopted window = %q, want original %q", adopted.ID, target.ID)
	}
	if afterPID := tmuxPanePID(t, handler, adopted.ID); afterPID != beforePID {
		t.Fatalf("adopted Pi pid = %q, want original %q", afterPID, beforePID)
	}
}

func TestProcessReconciliationWatchesMigratedLegacyPiWithoutTerminal(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	legacySession := legacyThreadTmuxSessionName(item.ID, thread.ID, "pi")
	target, err := handler.createTmuxSession(
		legacySession,
		thread.Cwd,
		"pi",
		"/bin/sleep",
		[]string{"30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)

	var logs synchronizedBuffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	processes, err := handler.processWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 0 {
		t.Fatalf("process reconciliation exposed migrated Pi as a process: %#v", processes)
	}
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "pi")
	adopted, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find status-reconciled Pi window: found=%t err=%v", found, err)
	}
	if adopted.ID != target.ID {
		t.Fatalf("status reconciliation changed Pi window %q to %q", target.ID, adopted.ID)
	}
	if afterPID := tmuxPanePID(t, handler, adopted.ID); afterPID != beforePID {
		t.Fatalf("status reconciliation restarted Pi pid %q as %q", beforePID, afterPID)
	}
	panes, err := handler.tmuxAgentPanes(adopted.ID)
	if err != nil || len(panes) != 1 || panes[0].Agent != codingAgentPi {
		t.Fatalf("reconciled Pi panes = %#v err=%v", panes, err)
	}
	paneID := panes[0].ID
	output, err := handler.tmuxCommand(
		"display-message", "-p", "-t", adopted.ID,
		"#{@dire-mux-agent}\t#{remain-on-exit}",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect reconciled Pi pane: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != codingAgentPi+"\ton" {
		t.Fatalf("reconciled Pi pane options = %q, want agent and retained exit", got)
	}

	pid, err := strconv.Atoi(beforePID)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitForTmuxSessionGone(t, handler, canonicalSession)
	exitLog := logs.String()
	for _, expected := range []string{
		"coding agent exited:",
		"agent=\"pi\"",
		"pane=\"" + paneID + "\"",
		"signal=term",
	} {
		if !strings.Contains(exitLog, expected) {
			t.Fatalf("migrated Pi exit log %q does not contain %q", exitLog, expected)
		}
	}
}

func TestProjectLegacyPiSessionIsAdoptedByDefaultThreadWithoutRestart(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	legacySession := legacyProjectTmuxSessionName(item.ID, "pi")
	target, err := handler.createTmuxSession(
		legacySession,
		thread.Cwd,
		"pi",
		"/bin/sh",
		[]string{"-c", "exec /bin/sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)

	canonicalSession, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("project-level legacy Pi process was replaced instead of adopted")
	}
	if exists, err := handler.tmuxSessionExists(legacySession); err != nil || exists {
		t.Fatalf("project-level legacy session still exists: exists=%t err=%v", exists, err)
	}
	adopted, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find adopted Pi window: found=%t err=%v", found, err)
	}
	if adopted.ID != target.ID || tmuxPanePID(t, handler, adopted.ID) != beforePID {
		t.Fatalf("project-level legacy Pi was restarted: target=%#v original=%#v", adopted, target)
	}
}

func TestAdoptedLegacyPiViewRecoversAfterCanonicalSessionLoss(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	legacySession := legacyProjectTmuxSessionName(item.ID, "pi")
	target, err := handler.createTmuxSession(legacySession, thread.Cwd, "pi", "/bin/sleep", []string{"30"})
	if err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)
	viewName, err := handler.createTmuxViewSession(item, thread, legacySession, target)
	if err != nil {
		t.Fatal(err)
	}

	canonicalSession, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("legacy Pi was replaced during initial adoption")
	}
	views, err := handler.tmuxViewSessions()
	if err != nil {
		t.Fatal(err)
	}
	rewritten := false
	for _, view := range views {
		if view.Name == viewName {
			rewritten = view.SourceSession == canonicalSession
		}
	}
	if !rewritten {
		t.Fatalf("legacy Pi view source was not rewritten to %q: %#v", canonicalSession, views)
	}

	staleView := fmt.Sprintf(
		"dire-mux-view-%d-%x-legacy",
		os.Getpid(),
		time.Now().Add(-2*terminalViewCreationGrace).UnixNano(),
	)
	if output, err := handler.tmuxCommand("rename-session", "-t", viewName, staleView).CombinedOutput(); err != nil {
		t.Fatalf("make adopted legacy view stale: %v: %s", err, output)
	}
	if output, err := handler.tmuxCommand("kill-session", "-t", canonicalSession).CombinedOutput(); err != nil {
		t.Fatalf("remove adopted canonical session: %v: %s", err, output)
	}

	gotSession, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if gotSession != canonicalSession || created {
		t.Fatalf("recovered legacy Pi = (%q, created=%t), want adopted %q", gotSession, created, canonicalSession)
	}
	if exists, err := handler.tmuxSessionExists(staleView); err != nil || exists {
		t.Fatalf("stale adopted view survived recovery: exists=%t err=%v", exists, err)
	}
	adopted, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find twice-adopted Pi: found=%t err=%v", found, err)
	}
	if adopted.ID != target.ID || tmuxPanePID(t, handler, adopted.ID) != beforePID {
		t.Fatalf("legacy-view recovery restarted Pi: adopted=%#v original=%#v", adopted, target)
	}
}

func TestStaleViewOnlyPiIsRelinkedWithoutRestartingProcess(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")

	canonicalSession, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%t err=%v", found, err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)
	viewName, err := handler.createTmuxViewSession(item, thread, canonicalSession, target)
	if err != nil {
		t.Fatal(err)
	}
	staleView := fmt.Sprintf(
		"dire-mux-view-%d-%x-1",
		os.Getpid(),
		time.Now().Add(-2*terminalViewCreationGrace).UnixNano(),
	)
	if output, err := handler.tmuxCommand("rename-session", "-t", viewName, staleView).CombinedOutput(); err != nil {
		t.Fatalf("make view stale: %v: %s", err, output)
	}
	if output, err := handler.tmuxCommand("kill-session", "-t", canonicalSession).CombinedOutput(); err != nil {
		t.Fatalf("remove canonical link: %v: %s", err, output)
	}
	if exists, err := handler.tmuxSessionExists(canonicalSession); err != nil || exists {
		t.Fatalf("canonical session survived simulated crash: exists=%t err=%v", exists, err)
	}

	gotSession, _, created, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("stale-view-only Pi was replaced instead of relinked")
	}
	if gotSession != canonicalSession {
		t.Fatalf("reconciled session = %q, want %q", gotSession, canonicalSession)
	}
	if exists, err := handler.tmuxSessionExists(staleView); err != nil || exists {
		t.Fatalf("stale view survived adoption: exists=%t err=%v", exists, err)
	}
	adopted, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find reconciled Pi window: found=%t err=%v", found, err)
	}
	if adopted.ID != target.ID {
		t.Fatalf("reconciled window = %q, want original %q", adopted.ID, target.ID)
	}
	if afterPID := tmuxPanePID(t, handler, adopted.ID); afterPID != beforePID {
		t.Fatalf("reconciled Pi pid = %q, want original %q", afterPID, beforePID)
	}
}

func TestLocallyActiveDetachedViewIsPreservedDuringReconciliation(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")

	canonicalSession, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%t err=%v", found, err)
	}
	viewName, err := handler.createTmuxViewSession(item, thread, canonicalSession, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handler.closeTmuxViewSession(viewName) })

	if err := handler.reconcileThreadTmuxState(item, thread); err != nil {
		t.Fatal(err)
	}
	if exists, err := handler.tmuxSessionExists(viewName); err != nil || !exists {
		t.Fatalf("locally active detached view was reconciled: exists=%t err=%v", exists, err)
	}
	handler.sessionMu.Lock()
	active := handler.tmuxViewIsActiveLocked(viewName)
	handler.sessionMu.Unlock()
	if !active {
		t.Fatalf("view %q was not registered as locally active", viewName)
	}
}

func TestTmuxViewCreationGraceOnlyAppliesToFreshOtherProcess(t *testing.T) {
	now := time.Now()
	otherPID := os.Getppid()
	if otherPID <= 0 || otherPID == os.Getpid() {
		t.Skip("no distinct live parent process")
	}
	freshOtherView := fmt.Sprintf("dire-mux-view-%d-%x-1", otherPID, now.UnixNano())
	if !tmuxViewHasLiveCreationGrace(freshOtherView, now) {
		t.Fatalf("fresh view from live process %d did not receive creation grace", otherPID)
	}
	oldOtherView := fmt.Sprintf("dire-mux-view-%d-%x-2", otherPID, now.Add(-2*terminalViewCreationGrace).UnixNano())
	if tmuxViewHasLiveCreationGrace(oldOtherView, now) {
		t.Fatal("old detached view received indefinite owner-liveness grace")
	}
	freshLocalView := fmt.Sprintf("dire-mux-view-%d-%x-3", os.Getpid(), now.UnixNano())
	if tmuxViewHasLiveCreationGrace(freshLocalView, now) {
		t.Fatal("unregistered same-process view received creation grace")
	}
}

func TestStaleViewConflictPreservesBothPiProcesses(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")

	canonicalSession, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	original, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find original Pi: found=%t err=%v", found, err)
	}
	originalPID := tmuxPanePID(t, handler, original.ID)
	viewName, err := handler.createTmuxViewSession(item, thread, canonicalSession, original)
	if err != nil {
		t.Fatal(err)
	}
	staleView := fmt.Sprintf(
		"dire-mux-view-%d-%x-2",
		os.Getpid(),
		time.Now().Add(-2*terminalViewCreationGrace).UnixNano(),
	)
	if output, err := handler.tmuxCommand("rename-session", "-t", viewName, staleView).CombinedOutput(); err != nil {
		t.Fatalf("make view stale: %v: %s", err, output)
	}
	if output, err := handler.tmuxCommand("kill-session", "-t", canonicalSession).CombinedOutput(); err != nil {
		t.Fatalf("remove original canonical link: %v: %s", err, output)
	}

	replacement, err := handler.createTmuxSession(
		canonicalSession,
		thread.Cwd,
		"pi",
		"/bin/sh",
		[]string{"-c", "exec /bin/sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureSharedToolWindow(canonicalSession, replacement, "pi"); err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := handler.ensureTmuxSession(item, thread, "pi"); err != nil {
		t.Fatal(err)
	} else if created {
		t.Fatal("conflicting canonical Pi should have been reused")
	}

	if exists, err := handler.tmuxSessionExists(staleView); err != nil || !exists {
		t.Fatalf("conflicting stale view was removed: exists=%t err=%v", exists, err)
	}
	if got := tmuxPanePID(t, handler, original.ID); got != originalPID {
		t.Fatalf("orphan Pi pid = %q, want preserved %q", got, originalPID)
	}
	if got := tmuxPanePID(t, handler, replacement.ID); got == originalPID {
		t.Fatalf("replacement and orphan unexpectedly share pid %q", got)
	}
}

func TestTmuxToolsAndAgentCreatedProcessesShareOneSession(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	// Use shell fallbacks so the test validates the layout without requiring
	// Neovim, Lazygit, or Pi to be installed.
	t.Setenv("PATH", "")

	sessions := make(map[string]string)
	for _, tool := range []string{"terminal", "nvim", "lazygit", "pi"} {
		sessionName, _, _, err := handler.ensureTmuxSession(item, thread, tool)
		if err != nil {
			t.Fatalf("ensure %s session: %v", tool, err)
		}
		sessions[tool] = sessionName
		exists, err := handler.tmuxSessionExists(sessionName)
		if err != nil || !exists {
			t.Fatalf("%s session %q does not exist: %v", tool, sessionName, err)
		}
	}

	sharedSession := sessions["pi"]
	for _, tool := range []string{"nvim", "lazygit"} {
		if sessions[tool] != sharedSession {
			t.Fatalf("%s session = %q, shared session = %q", tool, sessions[tool], sharedSession)
		}
	}
	if sessions["terminal"] == sharedSession {
		t.Fatalf("Shell must have its own session: %#v", sessions)
	}
	windows, err := handler.tmuxWindows(sharedSession)
	if err != nil {
		t.Fatal(err)
	}
	assertTmuxWindowNames(t, windows, "nvim", "lazygit", "pi")

	firstProcess, err := handler.newProcessWindow(item, thread, "web", "printf 'web-ready\\n'")
	if err != nil {
		t.Fatal(err)
	}
	secondProcess, err := handler.newProcessWindow(item, thread, "tests-watch", "printf 'tests-ready\\n'")
	if err != nil {
		t.Fatal(err)
	}
	if firstProcess.ID == secondProcess.ID {
		t.Fatalf("process IDs must be unique: %q", firstProcess.ID)
	}
	processes, err := handler.processWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 2 {
		t.Fatalf("process windows = %#v, want two", processes)
	}
	windows, err = handler.tmuxWindows(sharedSession)
	if err != nil {
		t.Fatal(err)
	}
	assertTmuxWindowNames(t, windows, "nvim", "lazygit", "pi", "web", "tests-watch")

	secondThread, err := handler.projects.AddThread(item.ID, "Second thread", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.newProcessWindow(item, secondThread, "worker", "printf 'worker-ready\\n'"); err != nil {
		t.Fatal(err)
	}
	secondSession := tmuxSessionName(item.ID, secondThread.ID, "process")
	if secondSession == sharedSession {
		t.Fatalf("threads must have distinct tools sessions: %q", secondSession)
	}
}

func TestAgentCreatedProcessRunsCommandAndRetainsLogs(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	token := fmt.Sprintf("process-log-%d", time.Now().UnixNano())

	window, err := handler.newProcessWindow(item, thread, "server", "printf '"+token+"\\n'")
	if err != nil {
		t.Fatal(err)
	}
	sessionName := tmuxSessionName(item.ID, thread.ID, "process")
	_, target, found, err := handler.tmuxProcessWindow(sessionName, window.ID)
	if err != nil || !found {
		t.Fatalf("find process window: found=%v err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, handler, target, token)

	if _, err := handler.newProcessWindow(item, thread, "server", "printf duplicate"); err == nil {
		t.Fatal("expected a duplicate process name to fail")
	}
	if _, _, _, err := handler.ensureTmuxSession(item, thread, "process"); err == nil {
		t.Fatal("fixed process window should not be created")
	}
	if err := handler.tmuxCommand("kill-window", "-t", target.ID).Run(); err != nil {
		t.Fatal(err)
	}
	if processes, err := handler.processWindows(item, thread); err != nil {
		t.Fatal(err)
	} else if len(processes) != 0 {
		t.Fatalf("process windows after removal = %#v", processes)
	}
}

func TestIncompleteProcessWindowIsPreservedUntilMetadataCompletes(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName := tmuxSessionName(item.ID, thread.ID, "process")
	target, err := handler.createTmuxSession(
		sessionName,
		thread.Cwd,
		"starting-process",
		"/bin/sh",
		[]string{"-c", "while :; do /bin/sleep 1; done"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.setTmuxWindowOption(target.ID, "@dire-mux-tool", "process"); err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, target.ID)

	processes, err := handler.processWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 0 {
		t.Fatalf("incomplete process windows = %#v, want hidden", processes)
	}
	if afterPID := tmuxPanePID(t, handler, target.ID); afterPID != beforePID {
		t.Fatalf("incomplete process pid = %q, want preserved %q", afterPID, beforePID)
	}

	const processID = "finishing-process"
	if err := handler.setTmuxWindowOption(target.ID, "@dire-mux-process-id", processID); err != nil {
		t.Fatal(err)
	}
	processes, err = handler.processWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].ID != processID || processes[0].TmuxID != target.ID {
		t.Fatalf("completed process windows = %#v", processes)
	}
	if afterPID := tmuxPanePID(t, handler, target.ID); afterPID != beforePID {
		t.Fatalf("completed process pid = %q, want original %q", afterPID, beforePID)
	}
}

func TestTmuxToolViewLinksOnlyRequestedWindow(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	t.Setenv("PATH", "")

	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%v err=%v", found, err)
	}
	viewName, err := handler.createTmuxViewSession(item, thread, sessionName, target)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := handler.tmuxWindows(viewName)
	if err != nil {
		t.Fatal(err)
	}
	assertTmuxWindowNames(t, windows, "pi")
	if err := handler.tmuxCommand("kill-session", "-t", viewName).Run(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := handler.tmuxToolWindow(sessionName, "pi"); err != nil || !found {
		t.Fatalf("closing view removed canonical Pi window: found=%v err=%v", found, err)
	}
}

func TestDeleteThreadStopsTmuxSessions(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	// Use shell fallbacks so every tool session stays alive without requiring
	// the optional programs to be installed.
	t.Setenv("PATH", "")

	sessions := make(map[string]string, len(threadSessionTools))
	for _, tool := range threadSessionTools {
		sessionName, _, _, err := handler.ensureTmuxSession(item, thread, tool)
		if err != nil {
			t.Fatalf("ensure %s session: %v", tool, err)
		}
		sessions[tool] = sessionName
	}

	secondThread, err := handler.projects.AddThread(item.ID, "Second thread", false)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, _, _, err := handler.ensureTmuxSession(item, secondThread, "terminal")
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{projects: handler.projects, terminal: handler, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", server.deleteThread)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+thread.ID, nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete thread status = %d, body = %s", response.Code, response.Body.String())
	}

	for tool, sessionName := range sessions {
		exists, err := handler.tmuxSessionExists(sessionName)
		if err != nil {
			t.Fatalf("check %s session: %v", tool, err)
		}
		if exists {
			t.Fatalf("%s session %q survived thread deletion", tool, sessionName)
		}
	}
	if _, _, err := handler.projects.GetThread(item.ID, thread.ID); !errors.Is(err, project.ErrThreadNotFound) {
		t.Fatalf("deleted thread still exists: %v", err)
	}
	if exists, err := handler.tmuxSessionExists(secondSession); err != nil || !exists {
		t.Fatalf("another thread's session was stopped: %v", err)
	}
}

func TestDeleteThreadCleansProjectLegacySessionsOnlyForDefaultThread(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		deleteDefault bool
		wantLegacy    bool
	}{
		{name: "default", deleteDefault: true, wantLegacy: false},
		{name: "non-default", deleteDefault: false, wantLegacy: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, item := newIsolatedTmuxHandler(t)
			defaultThread := item.Threads[0]
			secondThread, err := handler.projects.AddThread(item.ID, "Second thread", false)
			if err != nil {
				t.Fatal(err)
			}
			item, err = handler.projects.Get(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			legacySessions := make(map[string]string)
			legacyViews := make(map[string]string)
			for _, tool := range []string{"terminal", "pi"} {
				legacySession := legacyProjectTmuxSessionName(item.ID, tool)
				target, createErr := handler.createTmuxSession(
					legacySession,
					defaultThread.Cwd,
					tool,
					"/bin/sleep",
					[]string{"30"},
				)
				if createErr != nil {
					t.Fatal(createErr)
				}
				viewName, viewErr := handler.createTmuxViewSession(item, defaultThread, legacySession, target)
				if viewErr != nil {
					t.Fatal(viewErr)
				}
				legacySessions[tool] = legacySession
				legacyViews[tool] = viewName
			}

			threadID := secondThread.ID
			if testCase.deleteDefault {
				threadID = defaultThread.ID
			}
			server := &Server{projects: handler.projects, terminal: handler, piActivity: newPiActivityTracker()}
			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", server.deleteThread)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+threadID, nil)
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("delete thread status = %d, body = %s", response.Code, response.Body.String())
			}

			for tool, legacySession := range legacySessions {
				if exists, err := handler.tmuxSessionExists(legacySession); err != nil || exists != testCase.wantLegacy {
					t.Fatalf("project legacy %s after deletion: exists=%t err=%v want=%t", tool, exists, err, testCase.wantLegacy)
				}
				viewName := legacyViews[tool]
				if exists, err := handler.tmuxSessionExists(viewName); err != nil || exists != testCase.wantLegacy {
					t.Fatalf("project legacy %s view after deletion: exists=%t err=%v want=%t", tool, exists, err, testCase.wantLegacy)
				}
				if testCase.wantLegacy {
					handler.closeTmuxViewSession(viewName)
				}
			}
		})
	}
}

func TestStoppingThreadRejectsPausedProcessAndViewCreation(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")
	canonicalSession, _, _, err := handler.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := handler.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi window: found=%t err=%v", found, err)
	}

	processStarted := make(chan struct{})
	processResult := make(chan error, 1)
	viewStarted := make(chan struct{})
	viewResult := make(chan error, 1)
	handler.sessionMu.Lock()
	go func() {
		close(processStarted)
		_, createErr := handler.newProcessWindow(item, thread, "late-process", "printf late")
		processResult <- createErr
	}()
	go func() {
		close(viewStarted)
		_, createErr := handler.createTmuxViewSession(item, thread, canonicalSession, target)
		viewResult <- createErr
	}()
	<-processStarted
	<-viewStarted
	if err := handler.markThreadStoppingLocked(item.ID, thread.ID); err != nil {
		handler.sessionMu.Unlock()
		t.Fatal(err)
	}
	if err := handler.stopThreadSessionsLocked(item, thread.ID); err != nil {
		handler.sessionMu.Unlock()
		t.Fatal(err)
	}
	handler.sessionMu.Unlock()

	if err := <-processResult; !errors.Is(err, errTerminalStopping) {
		t.Fatalf("paused process creation error = %v, want terminal stopping", err)
	}
	if err := <-viewResult; !errors.Is(err, errTerminalStopping) {
		t.Fatalf("paused view creation error = %v, want terminal stopping", err)
	}
	if _, _, _, err := handler.ensureTmuxSession(item, thread, "terminal"); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("stale terminal request error = %v, want terminal stopping", err)
	}
	if exists, err := handler.tmuxSessionExists(canonicalSession); err != nil || exists {
		t.Fatalf("canonical session was recreated after stop: exists=%t err=%v", exists, err)
	}
	views, err := handler.tmuxViewSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("terminal views were created after stop: %#v", views)
	}
}

func TestDeleteProjectStopsAllTmuxSessions(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	// Use shell fallbacks so every tool session remains alive without optional
	// programs being installed.
	t.Setenv("PATH", "")
	secondThread, err := handler.projects.AddThread(item.ID, "Second thread", false)
	if err != nil {
		t.Fatal(err)
	}

	var sessions []string
	for _, thread := range []project.Thread{item.Threads[0], secondThread} {
		for _, tool := range threadSessionTools {
			sessionName, _, _, err := handler.ensureTmuxSession(item, thread, tool)
			if err != nil {
				t.Fatalf("ensure %s session for %s: %v", tool, thread.ID, err)
			}
			sessions = append(sessions, sessionName)
		}
	}

	server := &Server{projects: handler.projects, terminal: handler, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}", server.deleteProject)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete project status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, sessionName := range sessions {
		if exists, err := handler.tmuxSessionExists(sessionName); err != nil || exists {
			t.Fatalf("session %q survived project deletion: exists=%t err=%v", sessionName, exists, err)
		}
	}
	if _, err := handler.projects.Get(item.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleted project still exists: %v", err)
	}
}

func TestStopProjectSessionsRefreshesStaleThreadSnapshot(t *testing.T) {
	handler, staleItem := newIsolatedTmuxHandler(t)
	t.Setenv("PATH", "")
	addedThread, err := handler.projects.AddThread(staleItem.ID, "Added after load", false)
	if err != nil {
		t.Fatal(err)
	}
	currentItem, err := handler.projects.Get(staleItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionName, _, _, err := handler.ensureTmuxSession(currentItem, addedThread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, handler, sessionName)

	stoppedItem, stopLease, err := handler.stopProjectSessions(staleItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(stoppedItem.Threads) != len(currentItem.Threads) {
		t.Fatalf("stopped project has %d threads, want refreshed %d", len(stoppedItem.Threads), len(currentItem.Threads))
	}
	if exists, err := handler.tmuxSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("pending project stop removed late-added thread session: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, handler, sessionName); afterPID != beforePID {
		t.Fatalf("pending project stop changed pane pid: before=%s after=%s", beforePID, afterPID)
	}

	// The refreshed value is also the rollback token used when Store.Delete
	// fails, so cancellation must reactivate every thread that was marked.
	if err := handler.cancelStopProject(stoppedItem, stopLease); err != nil {
		t.Fatal(err)
	}
	handler.sessionMu.Lock()
	defer handler.sessionMu.Unlock()
	for _, thread := range currentItem.Threads {
		if err := handler.ensureTerminalThreadActiveLocked(currentItem.ID, thread.ID); err != nil {
			t.Fatalf("thread %s remained stopped after rollback: %v", thread.ID, err)
		}
	}
	if afterPID := tmuxPanePID(t, handler, sessionName); afterPID != beforePID {
		t.Fatalf("project stop rollback changed pane pid: before=%s after=%s", beforePID, afterPID)
	}
}

func TestDurableThreadStopBlocksIndependentHandlersAndSurvivesRestart(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]
	setMockPi(t, "#!/bin/sh\nwhile :; do /bin/sleep 1; done\n")
	canonicalSession, _, _, err := first.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	target, found, err := first.tmuxToolWindow(canonicalSession, "pi")
	if err != nil || !found {
		t.Fatalf("find Pi target: found=%t err=%v", found, err)
	}

	stopLease, err := first.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := second.ensureTmuxSession(item, thread, "terminal"); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("stale terminal creation error = %v, want terminal stopping", err)
	}
	if _, err := second.newProcessWindow(item, thread, "late-process", "printf late"); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("stale process creation error = %v, want terminal stopping", err)
	}
	if _, err := second.createTmuxViewSession(item, thread, canonicalSession, target); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("stale view creation error = %v, want terminal stopping", err)
	}
	if err := first.projects.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.finishStopThread(item, thread.ID, stopLease); err != nil {
		t.Fatal(err)
	}

	third := overlappingTerminalHandler(first)
	if _, _, _, err := third.ensureTmuxSession(item, thread, "terminal"); !errors.Is(err, errTerminalStopping) {
		t.Fatalf("new handler ignored retained marker: %v", err)
	}
	stopped, err := third.terminalStops.threadStopped(item.ID, thread.ID)
	if err != nil || !stopped {
		t.Fatalf("retained durable stop: stopped=%t err=%v", stopped, err)
	}
}

func TestCrossHandlerPostMutationFencePreservesPendingAndRemovesCommittedSession(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "terminal")

	// This models an old handler that passed its precheck immediately before a
	// new handler durably marked the thread.
	second.sessionMu.Lock()
	if err := second.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		second.sessionMu.Unlock()
		t.Fatal(err)
	}
	stopLease, err := first.stopThreadSessions(item, thread.ID)
	if err != nil {
		second.sessionMu.Unlock()
		t.Fatal(err)
	}
	target, err := second.createTmuxSession(canonicalSession, thread.Cwd, "shell", "/bin/sleep", []string{"30"})
	if err != nil {
		second.sessionMu.Unlock()
		t.Fatal(err)
	}
	beforePID := tmuxPanePID(t, second, target.ID)
	fenceErr := second.finishTerminalThreadMutationLocked(item, thread)
	second.sessionMu.Unlock()
	if !errors.Is(fenceErr, errTerminalStopping) {
		t.Fatalf("post-mutation fence error = %v, want terminal stopping", fenceErr)
	}
	if exists, err := first.tmuxSessionExists(canonicalSession); err != nil || !exists {
		t.Fatalf("pending fence removed late canonical session: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, second, target.ID); afterPID != beforePID {
		t.Fatalf("pending fence changed late pane pid: before=%s after=%s", beforePID, afterPID)
	}

	if err := first.projects.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	second.sessionMu.Lock()
	fenceErr = second.finishTerminalThreadMutationLocked(item, thread)
	second.sessionMu.Unlock()
	if !errors.Is(fenceErr, errTerminalStopping) {
		t.Fatalf("committed post-mutation fence error = %v, want terminal stopping", fenceErr)
	}
	if exists, err := first.tmuxSessionExists(canonicalSession); err != nil || exists {
		t.Fatalf("committed fence left late canonical session: exists=%t err=%v", exists, err)
	}
	if err := first.finishStopThread(item, thread.ID, stopLease); err != nil {
		t.Fatal(err)
	}
}

func TestThreadStopFinalSweepUsesExactPersistedNames(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "terminal")
	decoySession := canonicalSession + "-decoy"
	if _, err := handler.createTmuxSession(decoySession, thread.Cwd, "decoy", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	decoyView := "dire-mux-view-late-decoy"
	if _, err := handler.createTmuxSession(decoyView, "/", "view", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	if output, err := handler.tmuxCommand("set-option", "-t", decoyView, "@dire-mux-source-session", decoySession).CombinedOutput(); err != nil {
		t.Fatalf("mark decoy view source: %v: %s", err, output)
	}

	stopLease, err := handler.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{decoySession, decoyView} {
		if exists, err := handler.tmuxExactSessionExists(preserved); err != nil || !exists {
			t.Fatalf("initial exact sweep removed mismatch %q: exists=%t err=%v", preserved, exists, err)
		}
	}
	if _, err := handler.createTmuxSession(canonicalSession, thread.Cwd, "shell", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	lateView := "dire-mux-view-late-final-sweep"
	if _, err := handler.createTmuxSession(lateView, "/", "view", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	if output, err := handler.tmuxCommand("set-option", "-t", lateView, "@dire-mux-source-session", canonicalSession).CombinedOutput(); err != nil {
		t.Fatalf("mark late view source: %v: %s", err, output)
	}

	if err := handler.projects.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := handler.finishStopThread(item, thread.ID, stopLease); err != nil {
		t.Fatal(err)
	}

	for _, removed := range []string{canonicalSession, lateView} {
		if exists, err := handler.tmuxExactSessionExists(removed); err != nil || exists {
			t.Fatalf("exact final-sweep target %q survived: exists=%t err=%v", removed, exists, err)
		}
	}
	for _, preserved := range []string{decoySession, decoyView} {
		if exists, err := handler.tmuxExactSessionExists(preserved); err != nil || !exists {
			t.Fatalf("exact mismatch %q was removed: exists=%t err=%v", preserved, exists, err)
		}
	}
	_ = handler.tmuxCommand("kill-session", "-t", decoyView).Run()
	_ = handler.tmuxCommand("kill-session", "-t", decoySession).Run()
}

func TestDeleteThreadStoreFailureRollsBackDurableStop(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]
	sessionName, _, _, err := first.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := first.tmuxDetailedWindows(sessionName)
	if err != nil || len(windows) != 1 {
		t.Fatalf("inspect terminal before failed delete: windows=%#v err=%v", windows, err)
	}
	windowID := windows[0].Target.ID
	beforePID := tmuxPanePID(t, first, windowID)
	// Replacing the persistent project-mutation lock with a directory makes
	// the Store fail before publishing any deletion.
	lockPath := filepath.Join(first.projects.DataDirectory(), "projects.json.lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	server := &Server{projects: first.projects, terminal: first, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", server.deleteThread)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+thread.ID, nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
	}
	if stopped, err := second.terminalStops.threadStopped(item.ID, thread.ID); err != nil || stopped {
		t.Fatalf("failed Store delete retained stop marker: stopped=%t err=%v", stopped, err)
	}
	if exists, err := second.tmuxExactSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("failed Store delete removed terminal session: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, second, windowID); afterPID != beforePID {
		t.Fatalf("failed Store delete changed pane pid: before=%s after=%s", beforePID, afterPID)
	}
	if _, _, created, err := second.ensureTmuxSession(item, thread, "terminal"); err != nil || created {
		t.Fatalf("terminal remained stopped after Store rollback: %v", err)
	}
}

func TestDeleteProjectStoreFailureRollsBackWithoutChangingPID(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]
	sessionName, _, _, err := first.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := first.tmuxDetailedWindows(sessionName)
	if err != nil || len(windows) != 1 {
		t.Fatalf("inspect project terminal before failed delete: windows=%#v err=%v", windows, err)
	}
	windowID := windows[0].Target.ID
	beforePID := tmuxPanePID(t, first, windowID)

	lockPath := filepath.Join(first.projects.DataDirectory(), "projects.json.lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	server := &Server{projects: first.projects, terminal: first, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}", server.deleteProject)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("delete project status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := first.projects.Get(item.ID); err != nil {
		t.Fatalf("failed Store delete removed project: %v", err)
	}
	if stopped, err := second.terminalStops.projectStopped(item.ID); err != nil || stopped {
		t.Fatalf("failed Store project delete retained stop marker: stopped=%t err=%v", stopped, err)
	}
	if exists, err := second.tmuxExactSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("failed Store project delete removed session: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, second, windowID); afterPID != beforePID {
		t.Fatalf("failed Store project delete changed pane pid: before=%s after=%s", beforePID, afterPID)
	}
}

func TestReconcileTerminalStopsRetainsAmbiguousPrecommitWithoutChangingPID(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, _, err := first.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := first.tmuxDetailedWindows(sessionName)
	if err != nil || len(windows) != 1 {
		t.Fatalf("inspect precommit terminal: windows=%#v err=%v", windows, err)
	}
	windowID := windows[0].Target.ID
	beforePID := tmuxPanePID(t, first, windowID)

	lease, err := first.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	restarted := overlappingTerminalHandler(first)
	if err := restarted.reconcileTerminalStops(); err != nil {
		t.Fatal(err)
	}
	if marker, found, err := restarted.terminalStops.readThread(item.ID, thread.ID); err != nil || !found || marker.Committed {
		t.Fatalf("precommit recovery marker state: found=%t err=%v", found, err)
	}
	if exists, err := restarted.tmuxExactSessionExists(sessionName); err != nil || !exists {
		t.Fatalf("precommit recovery removed session: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, restarted, windowID); afterPID != beforePID {
		t.Fatalf("precommit recovery changed pane pid: before=%s after=%s", beforePID, afterPID)
	}
	if _, _, created, err := restarted.ensureTmuxSession(item, thread, "terminal"); !errors.Is(err, errTerminalStopping) || created {
		t.Fatalf("ambiguous precommit attach = created=%t err=%v, want terminal stopping", created, err)
	}
}

func TestReconcileTerminalStopsSkipsActiveMarker(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, _, err := first.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := first.tmuxDetailedWindows(sessionName)
	if err != nil || len(windows) != 1 {
		t.Fatalf("inspect active-stop terminal: windows=%#v err=%v", windows, err)
	}
	beforePID := tmuxPanePID(t, first, windows[0].Target.ID)
	lease, err := first.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}

	restarted := overlappingTerminalHandler(first)
	if err := restarted.reconcileTerminalStops(); err != nil {
		t.Fatalf("active stop should be skipped: %v", err)
	}
	if stopped, err := restarted.terminalStops.threadStopped(item.ID, thread.ID); err != nil || !stopped {
		t.Fatalf("active marker changed during reconciliation: stopped=%t err=%v", stopped, err)
	}
	if afterPID := tmuxPanePID(t, restarted, windows[0].Target.ID); afterPID != beforePID {
		t.Fatalf("active marker reconciliation changed pane pid: before=%s after=%s", beforePID, afterPID)
	}
	if err := lease.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileTerminalStopsCleansCommittedExactRecipeAndPreservesDecoys(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	canonicalSession, _, _, err := first.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	canonicalWindows, err := first.tmuxDetailedWindows(canonicalSession)
	if err != nil || len(canonicalWindows) != 1 {
		t.Fatalf("inspect canonical terminal: windows=%#v err=%v", canonicalWindows, err)
	}
	canonicalView, err := first.createTmuxViewSession(item, thread, canonicalSession, canonicalWindows[0].Target)
	if err != nil {
		t.Fatal(err)
	}

	decoySession := canonicalSession + "-decoy"
	if _, err := first.createTmuxSession(decoySession, thread.Cwd, "decoy", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	decoyView := "dire-mux-view-recovery-decoy"
	if _, err := first.createTmuxSession(decoyView, "/", "view", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	if output, err := first.tmuxCommand("set-option", "-t", decoyView, "@dire-mux-source-session", decoySession).CombinedOutput(); err != nil {
		t.Fatalf("mark recovery decoy view: %v: %s", err, output)
	}

	lease, err := first.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.projects.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	restarted := overlappingTerminalHandler(first)
	if err := restarted.reconcileTerminalStops(); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{canonicalSession, canonicalView} {
		if exists, err := restarted.tmuxExactSessionExists(removed); err != nil || exists {
			t.Fatalf("committed recovery target %q survived: exists=%t err=%v", removed, exists, err)
		}
	}
	for _, preserved := range []string{decoySession, decoyView} {
		if exists, err := restarted.tmuxExactSessionExists(preserved); err != nil || !exists {
			t.Fatalf("committed recovery removed decoy %q: exists=%t err=%v", preserved, exists, err)
		}
	}
	if stopped, err := restarted.terminalStops.threadStopped(item.ID, thread.ID); err != nil || !stopped {
		t.Fatalf("committed recovery did not retain marker: stopped=%t err=%v", stopped, err)
	}
	_ = restarted.tmuxCommand("kill-session", "-t", "="+decoyView).Run()
	_ = restarted.tmuxCommand("kill-session", "-t", "="+decoySession).Run()
}

func TestTerminalStopFenceAndRecoveryResolveDualMarkersPerScope(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	liveThread := item.Threads[0]
	liveSession, _, _, err := handler.ensureTmuxSession(item, liveThread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	liveWindows, err := handler.tmuxDetailedWindows(liveSession)
	if err != nil || len(liveWindows) != 1 {
		t.Fatalf("inspect live project terminal: windows=%#v err=%v", liveWindows, err)
	}
	livePID := tmuxPanePID(t, handler, liveWindows[0].Target.ID)

	missingThread := project.Thread{ID: "deleted-thread", Cwd: liveThread.Cwd}
	missingSession := tmuxSessionName(item.ID, missingThread.ID, "terminal")
	if _, err := handler.createTmuxSession(missingSession, missingThread.Cwd, "shell", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	writeTestTerminalStopMarker(t, handler.terminalStops, terminalStopMarkerRef{
		Scope:     terminalStopScopeProject,
		ProjectID: item.ID,
	}, []string{liveSession})
	writeTestTerminalStopMarker(t, handler.terminalStops, terminalStopMarkerRef{
		Scope:     terminalStopScopeThread,
		ProjectID: item.ID,
		ThreadID:  missingThread.ID,
	}, []string{missingSession})

	handler.sessionMu.Lock()
	fenceErr := handler.finishTerminalThreadMutationLocked(item, missingThread)
	handler.sessionMu.Unlock()
	if !errors.Is(fenceErr, errTerminalStopping) {
		t.Fatalf("dual-marker fence error = %v, want terminal stopping", fenceErr)
	}
	if exists, err := handler.tmuxExactSessionExists(missingSession); err != nil || exists {
		t.Fatalf("committed thread recipe survived dual-marker fence: exists=%t err=%v", exists, err)
	}
	if exists, err := handler.tmuxExactSessionExists(liveSession); err != nil || !exists {
		t.Fatalf("pending project recipe killed live thread: exists=%t err=%v", exists, err)
	}
	if afterPID := tmuxPanePID(t, handler, liveWindows[0].Target.ID); afterPID != livePID {
		t.Fatalf("pending project marker changed live pid: before=%s after=%s", livePID, afterPID)
	}

	if err := handler.reconcileTerminalStops(); err != nil {
		t.Fatal(err)
	}
	if marker, found, err := handler.terminalStops.readProject(item.ID); err != nil || !found || marker.Committed {
		t.Fatalf("ambiguous project marker was not retained pending: found=%t marker=%#v err=%v", found, marker, err)
	}
	if _, found, err := handler.terminalStops.readThread(item.ID, missingThread.ID); err != nil || !found {
		t.Fatalf("committed thread marker was not retained: found=%t err=%v", found, err)
	}
	if afterPID := tmuxPanePID(t, handler, liveWindows[0].Target.ID); afterPID != livePID {
		t.Fatalf("dual-marker recovery changed live pid: before=%s after=%s", livePID, afterPID)
	}
}

func TestDeleteThreadRetryFinishesRetainedCommittedMarker(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := handler.stopThreadSessions(item, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.projects.DeleteThread(item.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	server := &Server{projects: handler.projects, terminal: handler, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", server.deleteThread)
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID+"/threads/"+thread.ID, nil)
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete retry %d status = %d, body = %s", attempt+1, response.Code, response.Body.String())
		}
	}
	if exists, err := handler.tmuxExactSessionExists(sessionName); err != nil || exists {
		t.Fatalf("delete retry left committed session: exists=%t err=%v", exists, err)
	}
	if stopped, err := handler.terminalStops.threadStopped(item.ID, thread.ID); err != nil || !stopped {
		t.Fatalf("delete retry did not retain committed marker: stopped=%t err=%v", stopped, err)
	}
}

func TestDeleteProjectRetryFinishesRetainedCommittedMarker(t *testing.T) {
	handler, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, _, _, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	stoppedItem, lease, err := handler.stopProjectSessions(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.projects.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := lease.Retain(); err != nil {
		t.Fatal(err)
	}

	server := &Server{projects: handler.projects, terminal: handler, piActivity: newPiActivityTracker()}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/projects/{id}", server.deleteProject)
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/projects/"+item.ID, nil)
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete project retry %d status = %d, body = %s", attempt+1, response.Code, response.Body.String())
		}
	}
	if exists, err := handler.tmuxExactSessionExists(sessionName); err != nil || exists {
		t.Fatalf("delete project retry left committed session: exists=%t err=%v", exists, err)
	}
	if stopped, err := handler.terminalStops.projectStopped(stoppedItem.ID); err != nil || !stopped {
		t.Fatalf("delete project retry did not retain committed marker: stopped=%t err=%v", stopped, err)
	}
}

func TestCrossHandlerMutationLockCreatesOneFixedPiWindow(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]

	sharedSession, _, _, err := first.ensureTmuxSession(item, thread, "nvim")
	if err != nil {
		t.Fatal(err)
	}
	nvimBefore, found, err := first.tmuxToolWindow(sharedSession, "nvim")
	if err != nil || !found {
		t.Fatalf("find pre-existing nvim window: found=%t err=%v", found, err)
	}
	nvimPIDBefore := tmuxPanePID(t, first, nvimBefore.ID)

	launchesPath := filepath.Join(t.TempDir(), "pi-launches")
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nprintf 'pi-mutation-ready\\n'\nwhile :; do /bin/sleep 1; done\n")
	type ensureResult struct {
		session string
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan ensureResult, 2)
	for _, handler := range []*terminalHandler{first, second} {
		go func(handler *terminalHandler) {
			<-start
			session, _, created, ensureErr := handler.ensureTmuxSession(item, thread, "pi")
			results <- ensureResult{session: session, created: created, err: ensureErr}
		}(handler)
	}
	close(start)

	createdCount := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Pi ensure: %v", result.err)
		}
		if result.session != sharedSession {
			t.Fatalf("concurrent Pi session = %q, want %q", result.session, sharedSession)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent Pi created count = %d, want 1", createdCount)
	}

	piWindow, found, err := first.tmuxToolWindow(sharedSession, "pi")
	if err != nil || !found {
		t.Fatalf("find concurrent Pi window: found=%t err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, first, piWindow, "pi-mutation-ready")
	piPID := tmuxPanePID(t, first, piWindow.ID)
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("concurrent Pi launch count = %d, want 1", launches)
	}

	detailed, err := first.tmuxDetailedWindows(sharedSession)
	if err != nil {
		t.Fatal(err)
	}
	piWindows := 0
	for _, window := range detailed {
		if window.Tool == "pi" {
			piWindows++
		}
	}
	if piWindows != 1 {
		t.Fatalf("Pi metadata windows = %#v, want exactly one", detailed)
	}

	nvimAfter, found, err := second.tmuxToolWindow(sharedSession, "nvim")
	if err != nil || !found {
		t.Fatalf("find nvim after Pi ensure: found=%t err=%v", found, err)
	}
	if nvimAfter.ID != nvimBefore.ID || tmuxPanePID(t, second, nvimAfter.ID) != nvimPIDBefore {
		t.Fatalf("pre-existing nvim incarnation changed: before=%#v pid=%s after=%#v", nvimBefore, nvimPIDBefore, nvimAfter)
	}
	if _, _, created, err := second.ensureTmuxSession(item, thread, "pi"); err != nil || created {
		t.Fatalf("repeat Pi ensure: created=%t err=%v", created, err)
	}
	piAfter, found, err := second.tmuxToolWindow(sharedSession, "pi")
	if err != nil || !found || piAfter.ID != piWindow.ID || tmuxPanePID(t, second, piAfter.ID) != piPID {
		t.Fatalf("Pi incarnation changed after repeat ensure: before=%#v pid=%s after=%#v found=%t err=%v", piWindow, piPID, piAfter, found, err)
	}
}

func TestCrossHandlerMutationLockCreatesOneClaudePane(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]
	setMockCodingAgents(t)

	sessionName, _, _, err := first.ensureTmuxSession(item, thread, "pi")
	if err != nil {
		t.Fatal(err)
	}
	window, found, err := first.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find initial Pi window: found=%t err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, first, window, "pi-ready")
	initialPanes, err := first.tmuxAgentPanes(window.ID)
	if err != nil || len(initialPanes) != 1 || initialPanes[0].Agent != codingAgentPi {
		t.Fatalf("initial Pi panes = %#v, err=%v", initialPanes, err)
	}
	piPaneID := initialPanes[0].ID
	piPID := tmuxPanePID(t, first, piPaneID)

	type paneResult struct {
		paneID  string
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan paneResult, 2)
	for _, handler := range []*terminalHandler{first, second} {
		go func(handler *terminalHandler) {
			<-start
			paneID, _, created, paneErr := handler.ensureCodingAgentPane(item, thread, codingAgentClaude, "", sessionName)
			results <- paneResult{paneID: paneID, created: created, err: paneErr}
		}(handler)
	}
	close(start)

	createdCount := 0
	claudePaneID := ""
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Claude ensure: %v", result.err)
		}
		if claudePaneID != "" && result.paneID != claudePaneID {
			t.Fatalf("concurrent Claude panes = %q and %q", claudePaneID, result.paneID)
		}
		claudePaneID = result.paneID
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent Claude created count = %d, want 1", createdCount)
	}
	waitForTmuxPaneOutput(t, first, tmuxWindowTarget{ID: claudePaneID}, "claude-ready")
	claudePID := tmuxPanePID(t, first, claudePaneID)

	panes, err := first.tmuxAgentPanes(window.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 2 {
		t.Fatalf("coding agent panes = %#v, want Pi and Claude", panes)
	}
	assertCodingAgentPane(t, panes, codingAgentPi, piPaneID, false)
	assertCodingAgentPane(t, panes, codingAgentClaude, claudePaneID, true)
	if tmuxPanePID(t, second, piPaneID) != piPID {
		t.Fatal("concurrent Claude creation replaced the live Pi process")
	}
	repeatedPaneID, _, repeatedCreated, err := second.ensureCodingAgentPane(item, thread, codingAgentClaude, "", sessionName)
	if err != nil || repeatedCreated || repeatedPaneID != claudePaneID {
		t.Fatalf("repeat Claude ensure = pane %q created=%t err=%v, want stable %q", repeatedPaneID, repeatedCreated, err, claudePaneID)
	}
	if tmuxPanePID(t, second, repeatedPaneID) != claudePID {
		t.Fatal("repeat Claude ensure replaced the live Claude process")
	}
}

func TestCrossHandlerMutationLockRejectsDuplicateProcessName(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	second := overlappingTerminalHandler(first)
	thread := item.Threads[0]

	type processResult struct {
		window processWindow
		err    error
	}
	start := make(chan struct{})
	results := make(chan processResult, 2)
	command := "printf 'process-mutation-ready\\n'; while :; do /bin/sleep 1; done"
	for _, handler := range []*terminalHandler{first, second} {
		go func(handler *terminalHandler) {
			<-start
			window, processErr := handler.newProcessWindow(item, thread, "server", command)
			results <- processResult{window: window, err: processErr}
		}(handler)
	}
	close(start)

	successCount := 0
	duplicateCount := 0
	var created processWindow
	for range 2 {
		result := <-results
		if result.err == nil {
			successCount++
			created = result.window
			continue
		}
		if strings.Contains(result.err.Error(), "already exists") {
			duplicateCount++
			continue
		}
		t.Fatalf("concurrent process creation: %v", result.err)
	}
	if successCount != 1 || duplicateCount != 1 {
		t.Fatalf("concurrent process outcomes: success=%d duplicate=%d", successCount, duplicateCount)
	}

	sessionName := tmuxSessionName(item.ID, thread.ID, "process")
	processes, err := first.processWindows(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].ID != created.ID || processes[0].Name != "server" {
		t.Fatalf("process windows = %#v, want only %#v", processes, created)
	}
	_, target, found, err := first.tmuxProcessWindow(sessionName, created.ID)
	if err != nil || !found || target.ServerPID == "" {
		t.Fatalf("find created process incarnation: target=%#v found=%t err=%v", target, found, err)
	}
	waitForTmuxPaneOutput(t, first, target, "process-mutation-ready")
	processPID := tmuxPanePID(t, first, target.ID)

	observed, observedTarget, found, err := second.processForRequest(item, thread, created.ID)
	if err != nil || !found {
		t.Fatalf("observe process from second handler: found=%t err=%v", found, err)
	}
	if observed.ID != created.ID || observedTarget.ID != target.ID || observedTarget.ServerPID != target.ServerPID || tmuxPanePID(t, second, observedTarget.ID) != processPID {
		t.Fatalf("process incarnation changed: created=%#v target=%#v observed=%#v target=%#v", created, target, observed, observedTarget)
	}
	detailed, err := second.tmuxDetailedWindows(sessionName)
	if err != nil {
		t.Fatal(err)
	}
	processWindowCount := 0
	for _, window := range detailed {
		if window.Tool == "process" && window.Name == "server" {
			processWindowCount++
		}
	}
	if processWindowCount != 1 {
		t.Fatalf("process metadata windows = %#v, want exactly one server", detailed)
	}
}

func TestCrossHandlerMutationLockLaunchesOneExplicitRestart(t *testing.T) {
	first, item := newIsolatedTmuxHandler(t)
	thread := item.Threads[0]
	sessionName, window, pane, state := newRetainedDeadCodingAgentPane(t, first, item, thread, "exit 19")
	first.handleCodingAgentExit(item.ID, thread.ID, sessionName, window.ID, pane.ID, codingAgentPi, state)
	waitForTmuxSessionGone(t, first, sessionName)
	if !first.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("retained dead Pi pane did not establish a restart fence")
	}

	launchesPath := filepath.Join(t.TempDir(), "restart-launches")
	setMockPi(t, "#!/bin/sh\nprintf x >> "+shellQuote(launchesPath)+"\nprintf 'pi-concurrent-restart-ready\\n'\nwhile :; do /bin/sleep 1; done\n")
	second := overlappingTerminalHandler(first)
	type restartResult struct {
		session string
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan restartResult, 2)
	for _, handler := range []*terminalHandler{first, second} {
		go func(handler *terminalHandler) {
			<-start
			session, _, created, restartErr := handler.ensureTmuxSessionWithCodingAgent(item, thread, "pi", "", codingAgentPi, true)
			results <- restartResult{session: session, created: created, err: restartErr}
		}(handler)
	}
	close(start)

	createdCount := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent explicit restart: %v", result.err)
		}
		if result.session != sessionName {
			t.Fatalf("restart session = %q, want %q", result.session, sessionName)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent restart created count = %d, want 1", createdCount)
	}

	restartedWindow, found, err := first.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found {
		t.Fatalf("find restarted Pi window: found=%t err=%v", found, err)
	}
	waitForTmuxPaneOutput(t, first, restartedWindow, "pi-concurrent-restart-ready")
	restartedPID := tmuxPanePID(t, first, restartedWindow.ID)
	if launches := codingAgentLaunchCount(t, launchesPath); launches != 1 {
		t.Fatalf("concurrent explicit restart launches = %d, want 1", launches)
	}
	if first.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) || second.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgentPi) {
		t.Fatal("successful concurrent restart left the restart fence active")
	}
	if _, _, created, err := second.ensureTmuxSessionWithCodingAgent(item, thread, "pi", "", codingAgentPi, true); err != nil || created {
		t.Fatalf("repeat explicit restart: created=%t err=%v", created, err)
	}
	restartedAfter, found, err := second.tmuxToolWindow(sessionName, "pi")
	if err != nil || !found || restartedAfter.ID != restartedWindow.ID || tmuxPanePID(t, second, restartedAfter.ID) != restartedPID {
		t.Fatalf("restarted Pi incarnation changed: before=%#v pid=%s after=%#v found=%t err=%v", restartedWindow, restartedPID, restartedAfter, found, err)
	}
}

func overlappingTerminalHandler(source *terminalHandler) *terminalHandler {
	handler := newTerminalHandlerUnreconciledWithOptions(source.projects, originPolicy{}, source.tmuxSocket)
	handler.tmuxPath = source.tmuxPath
	handler.envPath = source.envPath
	return handler
}

func isolatedTmuxServer(t *testing.T) (string, string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}

	t.Setenv("SHELL", "/bin/sh")
	// Keep the platform-limited tmux socket path short and verify tmux can run
	// before treating a capability restriction as a product test failure.
	t.Setenv("TMUX_TMPDIR", os.TempDir())
	var socketID [8]byte
	if _, err := rand.Read(socketID[:]); err != nil {
		t.Fatal(err)
	}
	socketName := fmt.Sprintf("d%x", socketID)
	probe := exec.Command(
		tmuxPath,
		"-L", socketName,
		"new-session", "-d",
		"-s", "capability-probe",
		shellCommand("/bin/sh", []string{"-c", "sleep 30"}),
	)
	probe.Env = tmuxEnvironment()
	t.Cleanup(func() {
		command := exec.Command(tmuxPath, "-L", socketName, "kill-server")
		command.Env = tmuxEnvironment()
		_ = command.Run()
	})
	output, probeErr := probe.CombinedOutput()
	if message := strings.TrimSpace(string(output)); probeErr != nil || message != "" {
		t.Skipf("tmux cannot start cleanly in this test environment: %v: %s", probeErr, message)
	}
	verify := exec.Command(tmuxPath, "-L", socketName, "has-session", "-t", "=capability-probe")
	verify.Env = tmuxEnvironment()
	output, verifyErr := verify.CombinedOutput()
	if message := strings.TrimSpace(string(output)); verifyErr != nil || message != "" {
		t.Skipf("tmux capability probe did not establish its exact session: %v: %s", verifyErr, message)
	}
	return tmuxPath, socketName
}

func setMockPi(t *testing.T, script string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "pi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
}

func setMockCodingAgents(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	for name, ready := range map[string]string{"pi": "pi-ready", "claude": "claude-ready"} {
		script := "#!/bin/sh\nprintf '" + ready + "\\n'\nwhile :; do /bin/sleep 1; done\n"
		if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
}

func newRetainedDeadCodingAgentPane(t *testing.T, handler *terminalHandler, item project.Project, thread project.Thread, command string) (string, tmuxWindowTarget, tmuxAgentPane, tmuxPaneExitState) {
	t.Helper()
	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	launchCommand, launchArgs := handler.codingAgentLaunchCommand(codingAgentPi, "/bin/sh", []string{"-c", command})
	window, err := handler.createTmuxSession(sessionName, thread.Cwd, "pi", launchCommand, launchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.configureSharedToolWindow(sessionName, window, "pi"); err != nil {
		t.Fatal(err)
	}
	panes, err := handler.tmuxAgentPanes(window.ID)
	if err != nil || len(panes) != 1 {
		t.Fatalf("find retained coding-agent pane: panes=%#v err=%v", panes, err)
	}
	waitForTmuxPaneDead(t, handler, panes[0].ID)
	state, err := handler.tmuxPaneExitState(panes[0].ID)
	if err != nil || !state.Found || !state.Dead {
		t.Fatalf("inspect retained coding-agent pane: state=%#v err=%v", state, err)
	}
	return sessionName, window, panes[0], state
}

func newIsolatedTmuxHandler(t *testing.T) (*terminalHandler, project.Project) {
	t.Helper()
	tmuxPath, socketName := isolatedTmuxServer(t)

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	handler.tmuxPath = tmuxPath
	return handler, item
}

func newTerminalTestServer(handler *terminalHandler) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal", handler.serve)
	return httptest.NewServer(mux)
}

func dialTerminal(t *testing.T, serverURL, projectID, threadID string) *websocket.Conn {
	t.Helper()
	return dialTerminalWithQuery(t, serverURL, projectID, threadID, "tool=terminal&cols=80&rows=24")
}

func dialTerminalWithQuery(t *testing.T, serverURL, projectID, threadID, query string) *websocket.Conn {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/projects/" + projectID + "/threads/" + threadID + "/terminal?" + query
	connection, response, err := websocket.DefaultDialer.Dial(websocketURL, nil)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("dial terminal: %v (status %d, body %s)", err, response.StatusCode, body)
		}
		t.Fatal(err)
	}
	return connection
}

func readTerminalClose(t *testing.T, connection *websocket.Conn) (int, string) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer connection.SetReadDeadline(time.Time{})
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
			var closeError *websocket.CloseError
			if !errors.As(err, &closeError) {
				t.Fatalf("read terminal close: %v", err)
			}
			return closeError.Code, closeError.Text
		}
	}
}

func codingAgentLaunchCount(t *testing.T, path string) int {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		t.Fatal(err)
	}
	return len(contents)
}

func readTerminalUntil(t *testing.T, connection *websocket.Conn, expected string) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer connection.SetReadDeadline(time.Time{})

	var output strings.Builder
	for {
		_, message, err := connection.ReadMessage()
		if err != nil {
			t.Fatalf("read terminal output: %v\noutput: %q", err, output.String())
		}
		output.Write(message)
		if strings.Contains(output.String(), expected) {
			return
		}
	}
}

func readTerminalMessage(t *testing.T, connection *websocket.Conn) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer connection.SetReadDeadline(time.Time{})
	if _, _, err := connection.ReadMessage(); err != nil {
		t.Fatalf("read initial terminal output: %v", err)
	}
}

func waitForTmuxSession(t *testing.T, handler *terminalHandler, sessionName string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := handler.tmuxSessionExists(sessionName)
		if err == nil && exists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux session %q did not persist", sessionName)
}

func waitForTmuxSessionGone(t *testing.T, handler *terminalHandler, sessionName string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := handler.tmuxSessionExists(sessionName)
		if err == nil && !exists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux session %q did not exit", sessionName)
}

func waitForTmuxPaneDead(t *testing.T, handler *terminalHandler, paneID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, err := handler.tmuxPaneExitState(paneID)
		if err == nil && state.Found && state.Dead {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux pane %q did not become retained-dead", paneID)
}

func waitForTmuxPaneOutput(t *testing.T, handler *terminalHandler, target tmuxWindowTarget, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, err := handler.tmuxCommand("capture-pane", "-p", "-J", "-S", "-200", "-t", target.ID).CombinedOutput()
		if err == nil && strings.Contains(string(output), expected) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux pane %q did not contain %q", target.ID, expected)
}

func windowsTargetID(t *testing.T, handler *terminalHandler, sessionName, tool string) string {
	t.Helper()
	target, found, err := handler.tmuxToolWindow(sessionName, tool)
	if err != nil || !found {
		t.Fatalf("find %s window: found=%t err=%v", tool, found, err)
	}
	return target.ID
}

func tmuxPanePID(t *testing.T, handler *terminalHandler, target string) string {
	t.Helper()
	output, err := handler.tmuxCommand("display-message", "-p", "-t", target, "#{pane_pid}").CombinedOutput()
	if err != nil {
		t.Fatalf("read tmux pane pid: %v: %s", err, output)
	}
	pid := strings.TrimSpace(string(output))
	if pid == "" {
		t.Fatal("tmux returned an empty pane pid")
	}
	return pid
}

func assertCodingAgentPane(t *testing.T, panes []tmuxAgentPane, agent, paneID string, active bool) {
	t.Helper()
	for _, pane := range panes {
		if pane.Agent != agent {
			continue
		}
		if pane.ID != paneID || pane.Active != active {
			t.Fatalf("%s pane = %#v, want id=%q active=%t", agent, pane, paneID, active)
		}
		return
	}
	t.Fatalf("coding agent panes = %#v, missing %s", panes, agent)
}

func assertTmuxWindowNames(t *testing.T, windows []tmuxWindow, expected ...string) {
	t.Helper()
	if len(windows) != len(expected) {
		t.Fatalf("tmux windows = %#v, want names %v", windows, expected)
	}
	counts := make(map[string]int, len(windows))
	for _, window := range windows {
		counts[window.Name]++
	}
	for _, name := range expected {
		if counts[name] != 1 {
			t.Fatalf("tmux windows = %#v, want one %q window", windows, name)
		}
	}
}
