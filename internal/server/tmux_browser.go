package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/creack/pty"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

const tmuxViewSessionPrefix = "kiwi-code-view-"

type tmuxBrowserWindow struct {
	ID             string `json:"id"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Active         bool   `json:"active"`
	PaneCount      int    `json:"paneCount"`
	CurrentCommand string `json:"currentCommand"`
	ServerPID      string `json:"-"`
	ProcessID      string `json:"-"`
}

type tmuxBrowserSession struct {
	Name        string              `json:"name"`
	Attached    bool                `json:"attached"`
	Kind        string              `json:"kind,omitempty"`
	ProjectID   string              `json:"projectId,omitempty"`
	ProjectName string              `json:"projectName,omitempty"`
	ThreadID    string              `json:"threadId,omitempty"`
	ThreadTitle string              `json:"threadTitle,omitempty"`
	Windows     []tmuxBrowserWindow `json:"windows"`
}

func (h *terminalHandler) listTmuxSessions(w http.ResponseWriter, _ *http.Request) {
	if h.tmuxPath == "" {
		writeError(w, http.StatusServiceUnavailable, "tmux is required to inspect sessions.")
		return
	}
	sessions, err := h.tmuxBrowserSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load tmux sessions.")
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (h *terminalHandler) tmuxBrowserSessions() ([]tmuxBrowserSession, error) {
	output, err := h.tmuxCommand(
		"list-windows", "-a",
		"-F", "#{session_name}\t#{?session_attached,1,0}\t#{window_id}\t#{window_index}\t#{window_name}\t#{window_active}\t#{window_panes}\t#{pane_current_command}\t#{pid}\t#{@kiwi-code-process-id}",
	).CombinedOutput()
	if err != nil {
		if isMissingTmuxServer(output, err) {
			return []tmuxBrowserSession{}, nil
		}
		return nil, tmuxCommandError("list tmux sessions", output, err)
	}

	byName := make(map[string]*tmuxBrowserSession)
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 10)
		if len(parts) != 10 {
			return nil, fmt.Errorf("parse tmux session window: %q", line)
		}
		sessionName := strings.TrimSpace(parts[0])
		if sessionName == "" || strings.HasPrefix(sessionName, tmuxViewSessionPrefix) {
			continue
		}
		if parts[1] != "0" && parts[1] != "1" {
			return nil, fmt.Errorf("parse tmux session attached state: %q", parts[1])
		}
		index, err := strconv.Atoi(parts[3])
		if err != nil {
			return nil, fmt.Errorf("parse tmux browser window index: %w", err)
		}
		if parts[5] != "0" && parts[5] != "1" {
			return nil, fmt.Errorf("parse tmux browser window active state: %q", parts[5])
		}
		paneCount, err := strconv.Atoi(parts[6])
		if err != nil {
			return nil, fmt.Errorf("parse tmux browser pane count: %w", err)
		}
		serverPID, err := strconv.Atoi(parts[8])
		if err != nil || serverPID <= 0 {
			return nil, fmt.Errorf("parse tmux browser server pid: %q", parts[8])
		}
		name := strings.TrimSpace(parts[4])
		if name == "" {
			name = "shell"
		}

		session := byName[sessionName]
		if session == nil {
			session = &tmuxBrowserSession{
				Name:     sessionName,
				Attached: parts[1] == "1",
				Windows:  []tmuxBrowserWindow{},
			}
			byName[sessionName] = session
		}
		session.Windows = append(session.Windows, tmuxBrowserWindow{
			ID:             strings.TrimSpace(parts[2]),
			Index:          index,
			Name:           name,
			Active:         parts[5] == "1",
			PaneCount:      paneCount,
			CurrentCommand: strings.TrimSpace(parts[7]),
			ServerPID:      parts[8],
			ProcessID:      strings.TrimSpace(parts[9]),
		})
	}

	sessions := make([]tmuxBrowserSession, 0, len(byName))
	for _, session := range byName {
		sort.Slice(session.Windows, func(i, j int) bool {
			return session.Windows[i].Index < session.Windows[j].Index
		})
		sessions = append(sessions, *session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	if err := h.annotateTmuxBrowserSessions(sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func isMissingTmuxServer(output []byte, err error) bool {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(string(output)))
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "no sessions") ||
		strings.Contains(message, "error connecting to") ||
		strings.Contains(message, "failed to connect to server") ||
		strings.Contains(message, "no such file or directory")
}

func (h *terminalHandler) annotateTmuxBrowserSessions(sessions []tmuxBrowserSession) error {
	if h.projects == nil || len(sessions) == 0 {
		return nil
	}
	projects, err := h.projects.ListPersisted()
	if err != nil {
		return fmt.Errorf("load persisted tmux session owners: %w", err)
	}
	for sessionIndex := range sessions {
		session := &sessions[sessionIndex]
		item, thread, found := tmuxBrowserSessionOwnerInProjects(session.Name, projects)
		if !found {
			continue
		}
		session.Kind = tmuxBrowserSessionKind(item, thread, session.Name)
		session.ProjectID = item.ID
		session.ProjectName = item.Name
		session.ThreadID = thread.ID
		session.ThreadTitle = thread.Title
	}
	return nil
}

func tmuxBrowserSessionOwnerInProjects(sessionName string, projects []project.Project) (project.Project, project.Thread, bool) {
	for _, item := range projects {
		for _, thread := range item.Threads {
			if _, found := threadTmuxSessionNameSet(item, thread.ID)[sessionName]; found {
				return item, thread, true
			}
		}
	}
	return project.Project{}, project.Thread{}, false
}

func tmuxBrowserSessionKind(item project.Project, thread project.Thread, sessionName string) string {
	if sessionName == tmuxSessionName(item.ID, thread.ID, "terminal") {
		return "shell"
	}
	return "tools"
}

func (h *terminalHandler) tmuxBrowserSessionOwner(sessionName string) (project.Project, project.Thread, bool, error) {
	if h.projects != nil {
		projects, err := h.projects.ListPersisted()
		if err != nil {
			return project.Project{}, project.Thread{}, false, fmt.Errorf("load persisted tmux session owners: %w", err)
		}
		item, thread, found := tmuxBrowserSessionOwnerInProjects(sessionName, projects)
		if found {
			item, thread, err = h.projects.GetThreadPersisted(item.ID, thread.ID)
			if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
				return project.Project{}, project.Thread{}, false, errTerminalStopping
			}
			if err != nil {
				return project.Project{}, project.Thread{}, false, err
			}
			return item, thread, true, nil
		}
	}
	stopping, err := h.tmuxBrowserSessionInStopRecipe(sessionName)
	if err != nil {
		return project.Project{}, project.Thread{}, false, err
	}
	if stopping {
		return project.Project{}, project.Thread{}, false, errTerminalStopping
	}
	return project.Project{}, project.Thread{}, false, nil
}

func (h *terminalHandler) tmuxBrowserSessionInStopRecipe(sessionName string) (bool, error) {
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return false, nil
	}
	refs, listErr := manager.listMarkers()
	for _, ref := range refs {
		var marker terminalStopMarker
		var found bool
		var err error
		switch ref.Scope {
		case terminalStopScopeProject:
			marker, found, err = manager.readProject(ref.ProjectID)
		case terminalStopScopeThread:
			marker, found, err = manager.readThread(ref.ProjectID, ref.ThreadID)
		default:
			err = errors.New("terminal stop marker ref has an invalid scope")
		}
		if err != nil {
			return false, errors.Join(listErr, err)
		}
		if !found {
			continue
		}
		for _, stoppedSessionName := range marker.SessionNames {
			if stoppedSessionName == sessionName {
				return true, nil
			}
		}
	}
	return false, listErr
}

func (h *terminalHandler) serveTmuxBrowserTerminal(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "The tmux terminal endpoint requires a WebSocket connection.")
		return
	}
	if h.tmuxPath == "" {
		writeError(w, http.StatusServiceUnavailable, "tmux is required to attach sessions.")
		return
	}

	sessionName := strings.TrimSpace(r.URL.Query().Get("session"))
	windowID := strings.TrimSpace(r.URL.Query().Get("window"))
	if sessionName == "" || windowID == "" {
		writeError(w, http.StatusBadRequest, "A tmux session and window are required.")
		return
	}
	sessions, err := h.tmuxBrowserSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load tmux sessions.")
		return
	}
	var target tmuxWindowTarget
	found := false
	for _, session := range sessions {
		if session.Name != sessionName {
			continue
		}
		for _, window := range session.Windows {
			if window.ID == windowID {
				target = tmuxWindowTarget{
					Index:     window.Index,
					ID:        window.ID,
					ServerPID: window.ServerPID,
					ProcessID: window.ProcessID,
				}
				found = true
				break
			}
		}
		break
	}
	if !found {
		writeError(w, http.StatusNotFound, "Tmux window not found.")
		return
	}

	// Validate the target before upgrading, but create the temporary linked
	// view only after the WebSocket handshake succeeds.
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1 << 20)

	writer := newWebSocketWriter(connection)
	closeWithError := func(message string) {
		_ = writer.Close(websocket.CloseInternalServerErr, message)
	}

	item, thread, managed, err := h.tmuxBrowserSessionOwner(sessionName)
	if err != nil {
		closeWithError("Could not verify the tmux session owner")
		return
	}
	var viewSessionName string
	if managed {
		viewSessionName, err = h.createTmuxViewSession(item, thread, sessionName, target)
	} else {
		viewSessionName, err = h.createTmuxBrowserViewSession(sessionName, target)
	}
	if err != nil {
		closeWithError("Could not create the tmux terminal view")
		return
	}
	defer h.closeTmuxViewSession(viewSessionName)

	if err := h.configureTmuxClipboard(); err != nil {
		log.Printf("configure tmux clipboard: %v", err)
	}
	cols := boundedDimension(r.URL.Query().Get("cols"), 80)
	rows := boundedDimension(r.URL.Query().Get("rows"), 24)
	attachArguments := []string{"attach-session"}
	if managed {
		attachArguments = append(attachArguments, "-c", thread.Cwd)
	}
	attachArguments = append(attachArguments, "-t", exactTmuxSessionTarget(viewSessionName))
	command := h.tmuxCommand(attachArguments...)
	ptmx, err := pty.StartWithSize(command, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		closeWithError("Could not attach to the tmux session")
		return
	}
	defer func() {
		_ = ptmx.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()

	bridge := startPTYWebSocketBridge(connection, writer, ptmx)
	defer bridge.Stop()
	for {
		select {
		case <-bridge.terminalDone:
			_ = writer.Close(websocket.CloseNormalClosure, "Tmux window ended")
			return
		case <-bridge.peer.done:
			return
		case <-bridge.peer.ping.C:
			if err := bridge.peer.WritePing(); err != nil {
				return
			}
		case message := <-bridge.peer.messages:
			if err := bridge.Handle(message); err != nil {
				return
			}
		}
	}
}
