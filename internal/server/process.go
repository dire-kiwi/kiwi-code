package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	maxProcessNameRunes      = 80
	maxProcessCommand        = 32 << 10
	maxProcessInput          = 32 << 10
	maxProcessLogLines       = 5000
	maxProcessWebServers     = 16
	maxProcessWebServerBytes = 2048

	tmuxProcessWebServersOption = "@kiwi-code-web-servers"
	tmuxProcessActionConfirmed  = "kiwi-code-process-action-confirmed"
	tmuxProcessActionRejected   = "kiwi-code-process-action-rejected"
)

var (
	errTmuxWindowIncarnationChanged  = errors.New("tmux window incarnation changed")
	errTmuxProcessIncarnationChanged = errors.New("tmux process incarnation changed")
)

type processWindow struct {
	ID             string   `json:"id"`
	Index          int      `json:"index"`
	Name           string   `json:"name"`
	CurrentCommand string   `json:"currentCommand"`
	WebServers     []string `json:"webServers"`
	TmuxID         string   `json:"-"`
	TmuxServerPID  string   `json:"-"`
}

func (h *terminalHandler) listProcesses(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	windows, err := h.processWindows(item, thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list processes.")
		return
	}
	writeJSON(w, http.StatusOK, windows)
}

func (h *terminalHandler) createProcess(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	var input struct {
		Name    string `json:"name"`
		Command string `json:"command"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProcessCommand+4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid process details.")
		return
	}

	window, err := h.newProcessWindow(item, thread, input.Name, input.Command)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.wakeThreadTmuxWatchers(item.ID, thread.ID)
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusCreated, window)
}

func (h *terminalHandler) updateProcess(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	var input struct {
		WebServers *[]string `json:"webServers"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProcessWebServers*maxProcessWebServerBytes+4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.WebServers == nil {
		writeError(w, http.StatusBadRequest, "Invalid process details.")
		return
	}
	webServers, err := normalizeProcessWebServers(*input.WebServers)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	window, target, found, err := h.processForRequest(item, thread, r.PathValue("processId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update the process.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Process not found.")
		return
	}
	encoded, err := json.Marshal(webServers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update the process.")
		return
	}
	if _, err := h.tmuxProcessCommand(
		target,
		"set-option", "-w", "-t", target.ID, tmuxProcessWebServersOption, string(encoded),
	); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update the process.")
		return
	}
	window.WebServers = webServers
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusOK, window)
}

func (h *terminalHandler) processLogs(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	lines := 200
	if raw := r.URL.Query().Get("lines"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxProcessLogLines {
			writeError(w, http.StatusBadRequest, "Log lines must be between 1 and 5000.")
			return
		}
		lines = value
	}

	window, target, found, err := h.processForRequest(item, thread, r.PathValue("processId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read process logs.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Process not found.")
		return
	}
	output, err := h.tmuxProcessCommand(
		target,
		"capture-pane", "-p", "-J",
		"-S", "-"+strconv.Itoa(lines),
		"-t", target.ID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read process logs.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"process": window,
		"output":  string(output),
	})
}

func (h *terminalHandler) sendProcessInput(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	var input struct {
		Data  string `json:"data"`
		Enter bool   `json:"enter"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProcessInput+4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Data == "" && !input.Enter) || len(input.Data) > maxProcessInput || strings.ContainsRune(input.Data, '\x00') {
		writeError(w, http.StatusBadRequest, "Invalid process input.")
		return
	}

	window, target, found, err := h.processForRequest(item, thread, r.PathValue("processId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send process input.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Process not found.")
		return
	}
	if err := h.sendTmuxInput(target, input.Data, input.Enter); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send process input.")
		return
	}
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusOK, window)
}

func (h *terminalHandler) interruptProcess(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	window, target, found, err := h.processForRequest(item, thread, r.PathValue("processId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not interrupt the process.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Process not found.")
		return
	}
	_, err = h.tmuxProcessCommand(target, "send-keys", "-t", target.ID, "C-c")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not interrupt the process.")
		return
	}
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusOK, window)
}

func (h *terminalHandler) deleteProcess(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	_, target, found, err := h.processForRequest(item, thread, r.PathValue("processId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stop the process.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Process not found.")
		return
	}
	if _, err := h.tmuxProcessCommand(target, "kill-window", "-t", target.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stop the process.")
		return
	}
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *terminalHandler) processWindows(item project.Project, thread project.Thread) ([]processWindow, error) {
	if err := h.reconcileThreadTmuxState(item, thread); err != nil {
		return nil, err
	}
	return h.tmuxProcessWindows(tmuxSessionName(item.ID, thread.ID, "process"))
}

func (h *terminalHandler) processForRequest(item project.Project, thread project.Thread, processID string) (processWindow, tmuxWindowTarget, bool, error) {
	if err := h.reconcileThreadTmuxState(item, thread); err != nil {
		return processWindow{}, tmuxWindowTarget{}, false, err
	}
	return h.tmuxProcessWindow(tmuxSessionName(item.ID, thread.ID, "process"), strings.TrimSpace(processID))
}

func (h *terminalHandler) newProcessWindow(item project.Project, thread project.Thread, rawName, rawCommand string) (result processWindow, err error) {
	return h.newProcessWindowWithEnvironment(item, thread, rawName, rawCommand, nil, false)
}

func (h *terminalHandler) newEnvironmentActionProcess(
	item project.Project,
	thread project.Thread,
	rawName string,
	rawCommand string,
	variables []project.EnvironmentVariable,
) (processWindow, error) {
	return h.newProcessWindowWithEnvironment(item, thread, rawName, rawCommand, variables, true)
}

func (h *terminalHandler) newProcessWindowWithEnvironment(
	item project.Project,
	thread project.Thread,
	rawName string,
	rawCommand string,
	variables []project.EnvironmentVariable,
	uniqueName bool,
) (result processWindow, err error) {
	name, err := normalizeProcessName(rawName)
	if err != nil {
		return processWindow{}, err
	}
	commandText := strings.TrimSpace(rawCommand)
	if commandText == "" {
		return processWindow{}, errors.New("process command is required")
	}
	if len(commandText) > maxProcessCommand {
		return processWindow{}, errors.New("process command is too long")
	}
	if strings.ContainsRune(commandText, '\x00') {
		return processWindow{}, errors.New("process command cannot contain NUL bytes")
	}

	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return processWindow{}, mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return processWindow{}, err
	}
	defer func() {
		if fenceErr := h.finishTerminalThreadMutationLocked(item, thread); fenceErr != nil {
			result = processWindow{}
			err = errors.Join(err, fenceErr)
		}
	}()
	if err := h.reconcileThreadTmuxStateLocked(item, thread); err != nil {
		return processWindow{}, err
	}

	sessionName := tmuxSessionName(item.ID, thread.ID, "process")
	existing, err := h.tmuxProcessWindows(sessionName)
	if err != nil {
		return processWindow{}, err
	}
	baseName := name
	for suffix := 1; ; suffix++ {
		available := true
		for _, window := range existing {
			if window.Name == name {
				available = false
				break
			}
		}
		if available {
			break
		}
		if !uniqueName {
			return processWindow{}, fmt.Errorf("a process named %q already exists", name)
		}
		name = fmt.Sprintf("%s %d", baseName, suffix+1)
	}
	processID, err := newProcessID()
	if err != nil {
		return processWindow{}, err
	}
	shellCommandPath, shellArgs, _, err := commandFor("process")
	if err != nil {
		return processWindow{}, err
	}
	environment := make([]string, 0, len(variables)+4)
	for _, variable := range variables {
		environment = append(environment, variable.Name+"="+variable.Value)
	}
	environment = append(environment,
		"KIWI_CODE_TMUX_SESSION="+sessionName,
		"KIWI_CODE_TMUX_WINDOW="+name,
		"KIWI_CODE_PROCESS_ID="+processID,
		shellCommandPath,
	)
	envPath := h.envPath
	if envPath == "" {
		envPath = "env"
	}
	shellArgs = append(environment, shellArgs...)

	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return processWindow{}, err
	}
	var target tmuxWindowTarget
	if exists {
		target, err = h.createTmuxWindow(thread.Cwd, sessionName, name, envPath, shellArgs, false)
	} else {
		target, err = h.createTmuxSession(sessionName, thread.Cwd, name, envPath, shellArgs)
		if err != nil {
			if sessionExists, checkErr := h.tmuxSessionExists(sessionName); checkErr == nil && sessionExists {
				target, err = h.createTmuxWindow(thread.Cwd, sessionName, name, envPath, shellArgs, false)
			}
		}
	}
	if err != nil {
		return processWindow{}, err
	}
	cleanup := func() { _ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID) }
	if err := h.configureProcessWindow(sessionName, target, processID, name); err != nil {
		cleanup()
		return processWindow{}, err
	}
	if err := h.sendTmuxInput(target, h.processCommandWithWebServerCleanup(commandText), true); err != nil {
		cleanup()
		return processWindow{}, err
	}

	window, _, found, err := h.tmuxProcessWindow(sessionName, processID)
	if err != nil {
		cleanup()
		return processWindow{}, err
	}
	if !found {
		cleanup()
		return processWindow{}, errors.New("created process window was not found")
	}
	return window, nil
}

func (h *terminalHandler) processCommandWithWebServerCleanup(commandText string) string {
	// The login shell intentionally remains available for final logs and follow-up
	// input. Clear published links as soon as its foreground command returns.
	return commandText + "\n" +
		"__kiwi_code_status=$?\n" +
		shellQuote(h.tmuxPath) + " set-option -w -t \"$TMUX_PANE\" " + tmuxProcessWebServersOption + " '[]'\n" +
		"(exit \"$__kiwi_code_status\")"
}

func normalizeProcessWebServers(values []string) ([]string, error) {
	if len(values) > maxProcessWebServers {
		return nil, fmt.Errorf("a process can publish at most %d web servers", maxProcessWebServers)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxProcessWebServerBytes {
			return nil, errors.New("web server URLs must be non-empty and 2048 bytes or fewer")
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
			return nil, fmt.Errorf("invalid web server URL %q; use an http:// or https:// URL without credentials", value)
		}
		normalized := parsed.String()
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeProcessName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("process name is required")
	}
	if utf8.RuneCountInString(value) > maxProcessNameRunes {
		return "", fmt.Errorf("process name must be %d characters or fewer", maxProcessNameRunes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("process name cannot contain control characters")
		}
	}
	return value, nil
}

func newProcessID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create process ID: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func (h *terminalHandler) configureProcessWindow(sessionName string, target tmuxWindowTarget, processID, name string) error {
	targetName := target.ID
	options := [][2]string{
		{"remain-on-exit", "off"},
		{"automatic-rename", "off"},
		{"allow-rename", "off"},
		{tmuxProcessWebServersOption, "[]"},
		// Publish the process discriminator last. Readers ignore windows without
		// it, so they cannot observe a process window before its identity exists.
		{"@kiwi-code-process-id", processID},
		{"@kiwi-code-tool", "process"},
	}
	for _, option := range options {
		if err := h.setTmuxWindowOption(targetName, option[0], option[1]); err != nil {
			return err
		}
	}
	output, err := h.tmuxCommand("rename-window", "-t", targetName, name).CombinedOutput()
	if err != nil {
		return tmuxCommandError("name process window", output, err)
	}
	return nil
}

func (h *terminalHandler) sendTmuxInput(target tmuxWindowTarget, data string, enter bool) error {
	if data != "" {
		var output []byte
		var err error
		if target.ProcessID != "" {
			output, err = h.tmuxProcessCommand(target, "send-keys", "-t", target.ID, "-l", "--", data)
		} else {
			output, err = h.tmuxCommand("send-keys", "-t", target.ID, "-l", "--", data).CombinedOutput()
		}
		if err != nil {
			if target.ProcessID != "" {
				return err
			}
			return tmuxCommandError("send process input", output, err)
		}
	}
	if enter {
		var output []byte
		var err error
		if target.ProcessID != "" {
			output, err = h.tmuxProcessCommand(target, "send-keys", "-t", target.ID, "Enter")
		} else {
			output, err = h.tmuxCommand("send-keys", "-t", target.ID, "Enter").CombinedOutput()
		}
		if err != nil {
			if target.ProcessID != "" {
				return err
			}
			return tmuxCommandError("send process input", output, err)
		}
	}
	return nil
}

// tmuxProcessCommand evaluates the captured process identity and runs the
// action in one tmux server command. Window IDs restart at @0 when the tmux
// server restarts, so a separate check followed by an action could target an
// unrelated replacement window.
func (h *terminalHandler) tmuxProcessCommand(target tmuxWindowTarget, command string, args ...string) ([]byte, error) {
	condition, err := tmuxProcessIncarnationCondition(target)
	if err != nil {
		return nil, err
	}
	return h.tmuxGuardedWindowCommand(target, condition, errTmuxProcessIncarnationChanged, "run exact process command", command, args...)
}

func (h *terminalHandler) tmuxWindowCommand(target tmuxWindowTarget, command string, args ...string) ([]byte, error) {
	condition, err := tmuxWindowIncarnationCondition(target)
	if err != nil {
		return nil, err
	}
	return h.tmuxGuardedWindowCommand(target, condition, errTmuxWindowIncarnationChanged, "run exact tmux window command", command, args...)
}

func (h *terminalHandler) tmuxGuardedWindowCommand(target tmuxWindowTarget, condition string, changedErr error, actionName, command string, args ...string) ([]byte, error) {
	confirmed := "display-message -p -t " + shellQuote(target.ID) + " " + shellQuote(tmuxProcessActionConfirmed)
	action := confirmed + " ; " + shellCommand(command, args)
	rejected := "display-message -p -t " + shellQuote(target.ID) + " " + shellQuote(tmuxProcessActionRejected)
	output, err := h.tmuxCommand("if-shell", "-t", target.ID, "-F", condition, action, rejected).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError(actionName, output, err)
	}
	confirmedPrefix := tmuxProcessActionConfirmed + "\n"
	if contents, ok := strings.CutPrefix(string(output), confirmedPrefix); ok {
		return []byte(contents), nil
	}
	if strings.TrimSpace(string(output)) == tmuxProcessActionRejected {
		return nil, changedErr
	}
	return nil, fmt.Errorf("%s: unexpected confirmation output %q", actionName, strings.TrimSpace(string(output)))
}

func tmuxWindowIncarnationCondition(target tmuxWindowTarget) (string, error) {
	serverPID, err := strconv.Atoi(target.ServerPID)
	if err != nil || serverPID <= 0 {
		return "", fmt.Errorf("invalid tmux window server pid %q", target.ServerPID)
	}
	windowIndex, err := strconv.Atoi(strings.TrimPrefix(target.ID, "@"))
	if err != nil || windowIndex < 0 || !strings.HasPrefix(target.ID, "@") {
		return "", fmt.Errorf("invalid tmux window id %q", target.ID)
	}
	return fmt.Sprintf(
		"#{&&:#{==:#{pid},%s},#{==:#{window_id},%s}}",
		target.ServerPID,
		target.ID,
	), nil
}

func tmuxProcessIncarnationCondition(target tmuxWindowTarget) (string, error) {
	windowCondition, err := tmuxWindowIncarnationCondition(target)
	if err != nil {
		return "", err
	}
	if target.ProcessID == "" {
		return "", errors.New("missing tmux process id")
	}
	for _, character := range target.ProcessID {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", fmt.Errorf("invalid tmux process id %q", target.ProcessID)
	}
	return fmt.Sprintf(
		"#{&&:%s,#{==:#{@kiwi-code-process-id},%s}}",
		windowCondition,
		target.ProcessID,
	), nil
}

func (h *terminalHandler) tmuxProcessWindows(sessionName string) ([]processWindow, error) {
	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []processWindow{}, nil
	}
	if err := h.removeLegacyProcessWindows(sessionName); err != nil {
		return nil, err
	}
	exists, err = h.tmuxSessionExists(sessionName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []processWindow{}, nil
	}
	output, err := h.tmuxCommand(
		"list-windows",
		"-t", exactTmuxSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_id}\t#{window_name}\t#{@kiwi-code-tool}\t#{@kiwi-code-process-id}\t#{pane_current_command}\t#{@kiwi-code-web-servers}\t#{pid}",
	).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError("list process windows", output, err)
	}

	return parseProcessWindows(output)
}

func (h *terminalHandler) removeLegacyProcessWindows(string) error {
	// A process-tagged window without an ID is not provably legacy. It can be
	// an in-flight creation from another server or a process whose metadata write
	// was interrupted. Incomplete windows remain hidden from the process API, but
	// must be preserved because deleting one can terminate a live user process.
	return nil
}

func parseProcessWindows(output []byte) ([]processWindow, error) {
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	windows := make([]processWindow, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 8)
		if len(parts) != 8 {
			return nil, fmt.Errorf("parse process window: %q", line)
		}
		if parts[3] != "process" || parts[4] == "" {
			continue
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("parse process window index: %w", err)
		}
		webServers := []string{}
		if parts[6] != "" {
			if err := json.Unmarshal([]byte(parts[6]), &webServers); err != nil {
				return nil, fmt.Errorf("parse process web servers: %w", err)
			}
			webServers, err = normalizeProcessWebServers(webServers)
			if err != nil {
				return nil, fmt.Errorf("parse process web servers: %w", err)
			}
		}
		serverPID, err := strconv.Atoi(parts[7])
		if err != nil || serverPID <= 0 {
			return nil, fmt.Errorf("parse process tmux server pid: %q", parts[7])
		}
		windows = append(windows, processWindow{
			ID:             parts[4],
			Index:          index,
			Name:           parts[2],
			CurrentCommand: parts[5],
			WebServers:     webServers,
			TmuxID:         parts[1],
			TmuxServerPID:  parts[7],
		})
	}
	return windows, nil
}

func (h *terminalHandler) tmuxProcessWindow(sessionName, processID string) (processWindow, tmuxWindowTarget, bool, error) {
	if processID == "" {
		return processWindow{}, tmuxWindowTarget{}, false, nil
	}
	windows, err := h.tmuxProcessWindows(sessionName)
	if err != nil {
		return processWindow{}, tmuxWindowTarget{}, false, err
	}
	for _, window := range windows {
		if window.ID == processID {
			return window, tmuxWindowTarget{
				Index:     window.Index,
				ID:        window.TmuxID,
				ServerPID: window.TmuxServerPID,
				ProcessID: window.ID,
			}, true, nil
		}
	}
	return processWindow{}, tmuxWindowTarget{}, false, nil
}
