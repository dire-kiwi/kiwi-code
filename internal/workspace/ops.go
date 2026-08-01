package workspace

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/tmux"
)

// Manager performs Kiwi Code's tmux operations against one server: typed
// inspection, option management, and incarnation-guarded destruction. The
// client provider is called per operation so callers that reconfigure the
// tmux binary or socket (tests) stay consistent.
type Manager struct {
	client func() *tmux.Client
}

func NewManager(client func() *tmux.Client) *Manager {
	return &Manager{client: client}
}

func (m *Manager) command(args ...string) *exec.Cmd {
	return m.client().Command(args...)
}

func (m *Manager) commandContext(ctx context.Context, args ...string) *exec.Cmd {
	return m.client().CommandContext(ctx, args...)
}

func (m *Manager) KillPaneIncarnation(paneID, serverPID string, requireDead bool) error {
	if pid, err := strconv.Atoi(serverPID); err != nil || pid <= 0 {
		return fmt.Errorf("invalid tmux server pid %q", serverPID)
	}
	condition := fmt.Sprintf("#{==:#{pid},%s}", serverPID)
	if requireDead {
		condition = fmt.Sprintf("#{&&:%s,#{pane_dead}}", condition)
	}
	command := "kill-pane -t " + tmux.ShellQuote(paneID)
	output, err := m.command("if-shell", "-t", paneID, "-F", condition, command, "").CombinedOutput()
	if err != nil {
		state, stateErr := m.PaneExitState(paneID)
		if stateErr == nil && (!state.Found || state.ServerPID != serverPID) {
			return nil
		}
		return tmux.CommandError("remove exact coding agent pane", output, err)
	}
	return nil
}

func (m *Manager) KillWindowIncarnation(windowID, serverPID string) error {
	return m.killTargetIncarnation(
		windowID,
		serverPID,
		"kill-window -t "+tmux.ShellQuote(windowID),
		"remove exact tmux window",
	)
}

func (m *Manager) KillSessionIncarnation(sessionName, serverPID string) error {
	target := "=" + sessionName
	return m.killTargetIncarnation(
		target,
		serverPID,
		"kill-session -t "+tmux.ShellQuote(target),
		"remove exact tmux session",
	)
}

func (m *Manager) killTargetIncarnation(target, serverPID, command, action string) error {
	if pid, err := strconv.Atoi(serverPID); err != nil || pid <= 0 {
		return fmt.Errorf("invalid tmux server pid %q", serverPID)
	}
	condition := fmt.Sprintf("#{==:#{pid},%s}", serverPID)
	output, err := m.command("if-shell", "-t", target, "-F", condition, command, "").CombinedOutput()
	if err == nil {
		return nil
	}
	observedPID, found, stateErr := m.TargetServerPID(target)
	if stateErr == nil && (!found || observedPID != serverPID) {
		return nil
	}
	return tmux.CommandError(action, output, err)
}

func (m *Manager) TargetServerPID(target string) (string, bool, error) {
	identityFormat := "#{window_id}"
	expectedIdentity := target
	if strings.HasPrefix(target, "%") {
		identityFormat = "#{pane_id}"
	} else if strings.HasPrefix(target, "=") {
		identityFormat = "#{session_name}"
		expectedIdentity = strings.TrimPrefix(target, "=")
	}
	output, err := m.command("display-message", "-p", "-t", target, "#{pid}\t"+identityFormat).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, nil
		}
		return "", false, tmux.CommandError("inspect tmux target incarnation", output, err)
	}
	parts := strings.SplitN(strings.TrimRight(string(output), "\r\n"), "\t", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", false, nil
	}
	if parts[1] != expectedIdentity {
		return "", false, fmt.Errorf("tmux target identity changed: got %q, want %q", parts[1], expectedIdentity)
	}
	serverPID := parts[0]
	pid, err := strconv.Atoi(serverPID)
	if err != nil {
		return "", false, fmt.Errorf("parse tmux target server pid %q: %w", serverPID, err)
	}
	if pid <= 0 {
		return "", false, fmt.Errorf("parse tmux target server pid: invalid value %q", serverPID)
	}
	return serverPID, true, nil
}

func (m *Manager) PaneExitState(paneID string) (PaneExitState, error) {
	output, err := m.command(
		"display-message", "-p",
		"-t", paneID,
		"#{pid}\t#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_dead_signal}\t#{pane_dead_time}",
	).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return PaneExitState{}, nil
		}
		return PaneExitState{}, tmux.CommandError("inspect coding agent exit", output, err)
	}
	line := strings.TrimRight(string(output), "\r\n")
	if line == "" {
		return PaneExitState{}, nil
	}
	parts := strings.SplitN(line, "\t", 6)
	if len(parts) == 6 && parts[1] == "" {
		// With no matching pane, some tmux versions still expand server-wide
		// formats such as #{pid} and exit successfully. An empty pane id is the
		// authoritative indication that the requested incarnation is gone.
		return PaneExitState{}, nil
	}
	if len(parts) != 6 || parts[0] == "" || parts[1] != paneID || (parts[2] != "0" && parts[2] != "1") {
		return PaneExitState{}, fmt.Errorf("parse coding agent exit: %q", line)
	}
	return PaneExitState{
		ServerPID: parts[0],
		Dead:      parts[2] == "1",
		Status:    parts[3],
		Signal:    parts[4],
		ExitedAt:  parts[5],
		Found:     true,
	}, nil
}

func (m *Manager) PaneAlive(paneID string) (bool, error) {
	output, err := m.command("display-message", "-p", "-t", paneID, "#{pane_dead}").CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return false, nil
		}
		return false, tmux.CommandError("check coding agent pane", output, err)
	}
	return strings.TrimSpace(string(output)) == "0", nil
}

func (m *Manager) WindowZoomed(windowID string) (bool, error) {
	output, err := m.command("display-message", "-p", "-t", windowID, "#{window_zoomed_flag}").CombinedOutput()
	if err != nil {
		return false, tmux.CommandError("read coding agent layout", output, err)
	}
	switch strings.TrimSpace(string(output)) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("parse coding agent layout: %q", strings.TrimSpace(string(output)))
	}
}

func (m *Manager) SetWindowOption(target, option, value string) error {
	output, err := m.command("set-option", "-w", "-t", target, option, value).CombinedOutput()
	if err != nil {
		return tmux.CommandError("configure tmux window", output, err)
	}
	return nil
}

func (m *Manager) SetPaneOption(paneID, option, value string) error {
	output, err := m.command("set-option", "-p", "-t", paneID, option, value).CombinedOutput()
	if err != nil {
		return tmux.CommandError("configure coding agent pane", output, err)
	}
	return nil
}

func (m *Manager) ActivateWindow(sessionName string, index int) ([]Window, error) {
	if index < 0 {
		return nil, errors.New("invalid tmux window index")
	}
	target := tmux.ExactWindowTarget(sessionName, index)
	output, err := m.command("select-window", "-t", target).CombinedOutput()
	if err != nil {
		return nil, tmux.CommandError("select tmux window", output, err)
	}
	return m.Windows(sessionName)
}

func (m *Manager) UnzoomWindow(windowID string, panes []AgentPane) error {
	zoomed, err := m.WindowZoomed(windowID)
	if err != nil || !zoomed {
		return err
	}
	paneID := ""
	for _, pane := range panes {
		if pane.Active {
			paneID = pane.ID
			break
		}
	}
	if paneID == "" && len(panes) > 0 {
		paneID = panes[0].ID
	}
	output, err := m.command("resize-pane", "-Z", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmux.CommandError("unzoom coding agent pane", output, err)
	}
	return nil
}

func (m *Manager) SessionExistsContext(ctx context.Context, sessionName string) (bool, error) {
	err := m.commandContext(ctx, "has-session", "-t", tmux.ExactSessionTarget(sessionName)).Run()
	if err == nil {
		return true, nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return false, nil
	}
	return false, fmt.Errorf("check tmux session: %w", err)
}

func (m *Manager) SessionExists(sessionName string) (bool, error) {
	return m.SessionExistsContext(context.Background(), sessionName)
}

func (m *Manager) ExactSessionExists(sessionName string) (bool, error) {
	err := m.command("has-session", "-t", tmux.ExactSessionTarget(sessionName)).Run()
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return false, nil
	}
	return false, fmt.Errorf("check exact tmux session: %w", err)
}

func (m *Manager) DetailedWindows(sessionName string) ([]DetailedWindow, error) {
	output, err := m.command(
		"list-windows",
		"-t", tmux.ExactSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_id}\t#{window_name}\t#{@kiwi-code-tool}\t#{pane_start_command}",
	).CombinedOutput()
	if err != nil {
		return nil, tmux.CommandError("list detailed tmux windows", output, err)
	}
	var windows []DetailedWindow
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("parse detailed tmux window: %q", line)
		}
		index, parseErr := strconv.Atoi(parts[0])
		if parseErr != nil || parts[1] == "" {
			return nil, fmt.Errorf("parse detailed tmux window target: %q", line)
		}
		windows = append(windows, DetailedWindow{
			Target:       tmux.WindowTarget{Index: index, ID: parts[1]},
			Name:         parts[2],
			Tool:         parts[3],
			StartCommand: parts[4],
		})
	}
	return windows, nil
}

func (m *Manager) WindowsContext(ctx context.Context, sessionName string) ([]Window, error) {
	output, err := m.commandContext(
		ctx,
		"list-windows",
		"-t", tmux.ExactSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_name}\t#{window_active}",
	).CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, tmux.CommandError("list tmux windows", output, err)
	}

	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	windows := make([]Window, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("parse tmux window: %q", line)
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("parse tmux window index: %w", err)
		}
		if parts[2] != "0" && parts[2] != "1" {
			return nil, fmt.Errorf("parse tmux window active state: %q", parts[2])
		}
		name := strings.TrimSpace(parts[1])
		if name == "" {
			name = "shell"
		}
		windows = append(windows, Window{Index: index, Name: name, Active: parts[2] == "1"})
	}
	if len(windows) == 0 {
		return nil, errors.New("tmux session has no windows")
	}
	return windows, nil
}

func (m *Manager) Windows(sessionName string) ([]Window, error) {
	return m.WindowsContext(context.Background(), sessionName)
}

func (m *Manager) ToolWindow(sessionName, tool string) (tmux.WindowTarget, bool, error) {
	output, err := m.command(
		"list-windows",
		"-t", tmux.ExactSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_id}\t#{window_name}\t#{@kiwi-code-tool}\t#{pid}",
	).CombinedOutput()
	if err != nil {
		return tmux.WindowTarget{}, false, tmux.CommandError("find tmux window", output, err)
	}

	var namedTarget tmux.WindowTarget
	hasNamedTarget := false
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) != 5 {
			return tmux.WindowTarget{}, false, fmt.Errorf("parse tmux tool window: %q", line)
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return tmux.WindowTarget{}, false, fmt.Errorf("parse tmux tool window index: %w", err)
		}
		target := tmux.WindowTarget{Index: index, ID: parts[1], ServerPID: parts[4]}
		if parts[3] == tool {
			target.Tagged = true
			return target, true, nil
		}
		if !hasNamedTarget && parts[3] == "" && parts[2] == tool {
			namedTarget = target
			hasNamedTarget = true
		}
	}
	return namedTarget, hasNamedTarget, nil
}

func (m *Manager) WindowSession(windowID string) (string, error) {
	output, err := m.command("list-windows", "-a", "-F", "#{session_name}\t#{window_id}").CombinedOutput()
	if err != nil {
		return "", tmux.CommandError("find tmux window session", output, err)
	}
	fallback := ""
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[1] != windowID || strings.HasPrefix(parts[0], ViewSessionPrefix) {
			continue
		}
		if strings.HasPrefix(parts[0], SessionPrefix) {
			return parts[0], nil
		}
		if fallback == "" {
			fallback = parts[0]
		}
	}
	return fallback, nil
}

func (m *Manager) AgentPanes(windowID string) ([]AgentPane, error) {
	output, err := m.command(
		"list-panes",
		"-t", windowID,
		"-F", "#{pane_id}\t#{@kiwi-code-agent}\t#{pane_active}",
	).CombinedOutput()
	if err != nil {
		return nil, tmux.CommandError("list coding agent panes", output, err)
	}

	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	panes := make([]AgentPane, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[0] == "" || (parts[2] != "0" && parts[2] != "1") {
			return nil, fmt.Errorf("parse coding agent pane: %q", line)
		}
		panes = append(panes, AgentPane{
			ID:     parts[0],
			Agent:  parts[1],
			Active: parts[2] == "1",
		})
	}
	if len(panes) == 0 {
		return nil, errors.New("Pi window has no panes")
	}
	return panes, nil
}

func (m *Manager) ViewSessions() ([]ViewSession, error) {
	output, err := m.command(
		"list-sessions",
		"-F", "#{session_name}\t#{session_attached}\t#{@kiwi-code-source-session}",
	).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return []ViewSession{}, nil
		}
		return nil, tmux.CommandError("list tmux terminal views", output, err)
	}
	var views []ViewSession
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[0], ViewSessionPrefix) {
			continue
		}
		if parts[1] != "0" && parts[1] != "1" {
			return nil, fmt.Errorf("parse tmux terminal view: %q", line)
		}
		views = append(views, ViewSession{Name: parts[0], Attached: parts[1] == "1", SourceSession: parts[2]})
	}
	return views, nil
}

func (m *Manager) CreateSession(sessionName, directory, windowName, command string, args []string) (tmux.WindowTarget, error) {
	output, err := m.command(
		"new-session",
		"-d",
		"-P", "-F", "#{window_index}\t#{window_id}\t#{pid}",
		"-s", sessionName,
		"-c", directory,
		"-n", windowName,
		tmux.ShellCommand(command, args),
	).CombinedOutput()
	if err != nil {
		return tmux.WindowTarget{}, tmux.CommandError("create tmux session", output, err)
	}
	target, err := tmux.ParseWindowTarget(output)
	if err != nil {
		// The creation output is the only atomic proof of this incarnation. If
		// it cannot be parsed, a name-based cleanup could kill a replacement.
		return tmux.WindowTarget{}, err
	}

	output, err = m.command("set-option", "-t", tmux.ExactCurrentWindowTarget(sessionName), "status", "off").CombinedOutput()
	if err == nil {
		return target, nil
	}
	_ = m.KillSessionIncarnation(sessionName, target.ServerPID)
	return tmux.WindowTarget{}, tmux.CommandError("configure tmux session", output, err)
}

func (m *Manager) CreateWindow(cwd, sessionName, windowName, command string, args []string, selectWindow bool) (tmux.WindowTarget, error) {
	arguments := []string{"new-window"}
	if !selectWindow {
		arguments = append(arguments, "-d")
	}
	arguments = append(
		arguments,
		"-P", "-F", "#{window_index}\t#{window_id}\t#{pid}",
		"-t", tmux.ExactCurrentWindowTarget(sessionName),
		"-c", cwd,
		"-n", windowName,
		tmux.ShellCommand(command, args),
	)
	output, err := m.command(arguments...).CombinedOutput()
	if err != nil {
		return tmux.WindowTarget{}, tmux.CommandError("create tmux window", output, err)
	}
	return tmux.ParseWindowTarget(output)
}

func (m *Manager) ConfigureSharedToolWindow(sessionName string, target tmux.WindowTarget, tool string) error {
	targetName := target.ID
	options := [][2]string{
		{"remain-on-exit", "off"},
		{"automatic-rename", "off"},
		{"allow-rename", "off"},
		{"@kiwi-code-tool", tool},
	}
	arguments := make([]string, 0, len(options)*7+5)
	for index, option := range options {
		if index > 0 {
			arguments = append(arguments, ";")
		}
		arguments = append(arguments, "set-option", "-w", "-t", targetName, option[0], option[1])
	}
	arguments = append(arguments, ";", "rename-window", "-t", targetName, tool)
	output, err := m.command(arguments...).CombinedOutput()
	if err != nil {
		return tmux.CommandError("configure shared tmux window", output, err)
	}
	return nil
}

func (m *Manager) ActivateAgentPane(windowID, paneID string, panes []AgentPane) error {
	zoomed, err := m.WindowZoomed(windowID)
	if err != nil {
		return err
	}
	if zoomed {
		for _, pane := range panes {
			if pane.Active && pane.ID == paneID {
				return nil
			}
		}
		if err := m.UnzoomWindow(windowID, panes); err != nil {
			return err
		}
	}

	output, err := m.command("select-pane", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmux.CommandError("select coding agent pane", output, err)
	}
	if len(panes) == 1 {
		return nil
	}
	output, err = m.command("resize-pane", "-Z", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmux.CommandError("zoom coding agent pane", output, err)
	}
	return nil
}

func (m *Manager) CanonicalSessionLinkedToWindow(windowID string) (string, error) {
	sessionName, err := m.WindowSession(windowID)
	if err != nil || strings.HasSuffix(sessionName, "-tools") {
		return sessionName, err
	}
	return "", nil
}
