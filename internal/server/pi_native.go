package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dire-kiwi/kiwi-code/internal/agent/native"
	"github.com/dire-kiwi/kiwi-code/internal/datadir"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

var piNativeWebSocketSequence atomic.Uint64

const (
	piNativeSessionDirectoryName    = datadir.PiNativeSessionsDirectoryName
	piNativeActiveSessionMarkerName = ".active-session"
	piNativeMaxClientMessage        = 1 << 20
	piNativeMaxCompactPrompt        = 64 << 10
	piNativeMaxPromptImages         = 20
	piNativeMaxTrackedOutput        = 1 << 20
	piNativeStopTimeout             = 3 * time.Second
)

type piNativeManager struct {
	native.ManagerCore[*piNativeProcess]
	dataDirectory    string
	piPath           string
	extensionPaths   []string
	extensionErr     error
	figmaExtension   string
	figmaMCPURL      func(project.Project) string
	agentToken       string
	history          map[piNativeProcessKey]*piNativeProcess
	contextWatchOnce sync.Once
	usageReporter    func(piNativeProcessKey, string, threadUsageTotals)
	removeThreadHook func(string, string) error
}

type piNativeProcess struct {
	*native.Core
	launchOptions    codingAgentLaunchOptions
	sessionDirectory string
	runMu            sync.RWMutex
	nextRun          uint64
	activeRun        uint64
	runs             map[uint64]piNativeRunSnapshot
	usageReporter    func(piNativeProcessKey, string, threadUsageTotals)
}

var piProcessSpec = native.Spec{
	DisplayName:         "Pi",
	EndedMessage:        "Pi session ended.",
	UnexpectedMessage:   "Pi exited unexpectedly. Reconnect to resume the saved conversation.",
	WriteAfterExitError: "native Pi process ended",
	StopTimeout:         piNativeStopTimeout,
}

type piNativeRPCImage struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

type piNativeRPCCommand struct {
	ID                 string             `json:"id,omitempty"`
	Type               string             `json:"type"`
	Message            *string            `json:"message,omitempty"`
	Images             []piNativeRPCImage `json:"images,omitempty"`
	StreamingBehavior  string             `json:"streamingBehavior,omitempty"`
	CustomInstructions string             `json:"customInstructions,omitempty"`
	Provider           string             `json:"provider,omitempty"`
	ModelID            string             `json:"modelId,omitempty"`
	Level              string             `json:"level,omitempty"`
}

type piNativeRunSnapshot struct {
	ID         uint64     `json:"id"`
	State      string     `json:"state"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type piNativeClientImage struct {
	Path string `json:"path"`
}

type piNativeClientMessage struct {
	Type               string                `json:"type"`
	Message            string                `json:"message,omitempty"`
	Images             []piNativeClientImage `json:"images,omitempty"`
	StreamingBehavior  string                `json:"streamingBehavior,omitempty"`
	CustomInstructions string                `json:"customInstructions,omitempty"`
	Provider           string                `json:"provider,omitempty"`
	ModelID            string                `json:"modelId,omitempty"`
	Level              string                `json:"level,omitempty"`
}

type piNativeSessionEntry struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	ParentID         *string         `json:"parentId"`
	Timestamp        string          `json:"timestamp"`
	Message          json.RawMessage `json:"message"`
	Summary          string          `json:"summary"`
	FromID           string          `json:"fromId"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int64           `json:"tokensBefore"`
}

type piNativeSessionEntriesSnapshot struct {
	Entries []piNativeSessionEntry `json:"entries"`
	LeafID  *string                `json:"leafId"`
}

type piNativeHistorySnapshot struct {
	Messages []json.RawMessage `json:"messages"`
}

type piNativeHistoryEvent struct {
	Type string                  `json:"type"`
	Data piNativeHistorySnapshot `json:"data"`
}

type piNativeClientAction uint8

const (
	piNativeClientSendCommand piNativeClientAction = iota
	piNativeClientRefresh
	piNativeClientRestart
)

func newPiNativeManager(
	dataDirectory string,
	extensionPaths []string,
	extensionErr error,
	agentToken string,
	figmaExtension string,
) *piNativeManager {
	return &piNativeManager{
		dataDirectory:  dataDirectory,
		extensionPaths: append([]string(nil), extensionPaths...),
		extensionErr:   extensionErr,
		figmaExtension: figmaExtension,
		agentToken:     agentToken,
		ManagerCore:    native.NewManagerCore[*piNativeProcess](),
		history:        make(map[piNativeProcessKey]*piNativeProcess),
	}
}

func (m *piNativeManager) resolveFigmaMCPURL(item project.Project) string {
	if m == nil || m.figmaMCPURL == nil {
		return ""
	}
	return m.figmaMCPURL(item)
}

func (m *piNativeManager) stopOnContext(ctx context.Context) {
	if m == nil {
		return
	}
	native.StopOnContext(ctx, &m.contextWatchOnce, m.stopAll)
}

func piNativeBrowserEventCoalesceKey(payload []byte) string {
	var event struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return ""
	}
	switch event.Type {
	case "message_update":
		// Pi includes the full accumulated assistant message in every update.
		return event.Type
	case "tool_execution_update":
		// partialResult is also cumulative. Keep tools distinct in case an
		// extension reports overlapping executions.
		if event.ToolCallID != "" {
			return event.Type + "\x00" + event.ToolCallID
		}
	}
	return ""
}

func (h *terminalHandler) servePiNative(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "The native Pi endpoint requires a WebSocket connection.")
		return
	}

	item, thread, err := h.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if thread.RollbackPending {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return
	}
	// Upgrade before loading Pi extensions or starting Pi. A rejected WebSocket
	// origin must not cause agent-side code to run.
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(piNativeMaxClientMessage)
	connectionID := piNativeWebSocketSequence.Add(1)
	logSocketEnd := func(category string, socketErr error) {
		log.Printf(
			"native Pi websocket ended: connection=%d project=%q thread=%q category=%q error=%v",
			connectionID, item.ID, thread.ID, category, socketErr,
		)
	}

	writer := newWebSocketWriter(connection)
	write := writer.Write
	writeStatus := func(statusType, message string) error {
		payload, _ := json.Marshal(map[string]string{"type": statusType, "message": message})
		return write(websocket.TextMessage, payload)
	}
	closeWithError := func(message string) {
		_ = writeStatus("pi_native_error", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, message))
		logSocketEnd("server-error", errors.New(message))
	}
	closeWithFatal := func(message string) {
		_ = writeStatus("pi_native_fatal", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, message))
		logSocketEnd("policy-error", errors.New(message))
	}

	launchOptions, err := piNativeBrowserLaunchOptions(
		thread,
		r.URL.Query().Get("model"),
		r.URL.Query().Get("thinking"),
	)
	if err != nil {
		closeWithFatal(err.Error())
		return
	}
	process, err := h.startPiNativeProcess(
		item,
		thread,
		threadEndpointURL(r, item.ID, thread.ID),
		launchOptions,
	)
	if err != nil {
		closeWithError(piNativeStartErrorMessage(err))
		return
	}

	subscription := process.Events.SubscribeCoalesced(piNativeBrowserEventCoalesceKey)
	defer func() { subscription.Close() }()
	if err := writeStatus("pi_native_ready", "Pi is ready."); err != nil {
		logSocketEnd("ready-write-failed", err)
		return
	}
	_ = process.refresh()

	peer := startWebSocketPeer(connection, writer, rawWebSocketMessage, "native Pi input stalled")
	defer peer.Stop()
	for {
		select {
		case payload, open := <-subscription.Events():
			if !open {
				_ = writeStatus("pi_native_error", "The native Pi client fell behind; reconnecting to an authoritative snapshot.")
				_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Native Pi client fell behind"))
				logSocketEnd("slow-subscriber", errors.New("broadcast subscription closed"))
				return
			}
			if err := write(websocket.TextMessage, payload); err != nil {
				logSocketEnd("event-write-failed", err)
				return
			}
		case payload := <-peer.Messages:
			command, action, commandErr := normalizePiNativeClientMessage(payload)
			if commandErr != nil {
				_ = writeStatus("pi_native_error", commandErr.Error())
				continue
			}
			switch action {
			case piNativeClientRefresh:
				if err := process.refresh(); err != nil {
					_ = writeStatus("pi_native_error", "Could not refresh the Pi conversation.")
				}
				continue
			case piNativeClientRestart:
				restartOptions := launchOptions
				if command.Provider != "" && command.ModelID != "" {
					restartOptions.Model = command.Provider + "/" + command.ModelID
				}
				if command.Level != "" {
					restartOptions.ThinkingLevel = command.Level
				}
				if err := writeStatus("pi_native_restarting", "Restarting Pi to reload extensions…"); err != nil {
					return
				}
				replacement, err := h.restartPiNativeProcess(
					item,
					thread,
					threadEndpointURL(r, item.ID, thread.ID),
					restartOptions,
					process,
				)
				if err != nil {
					closeWithError("Could not restart the native Pi session.")
					return
				}
				subscription.Close()
				process = replacement
				launchOptions = restartOptions
				subscription = process.Events.SubscribeCoalesced(piNativeBrowserEventCoalesceKey)
				if err := writeStatus("pi_native_reloaded", "Pi restarted and extensions reloaded."); err != nil {
					return
				}
				_ = process.refresh()
				continue
			}
			if command.Type == "prompt" {
				reached, _, budgetErr := h.budgetReached(item.ID, thread.ID)
				if budgetErr != nil {
					_ = writeStatus("pi_native_error", "Could not verify the thread usage limit.")
					continue
				}
				if reached {
					_ = writeStatus("pi_native_error", "Thread token or cost limit reached. Increase or remove the limit in Thread details to continue.")
					continue
				}
			}
			if err := process.sendClientCommand(command); err != nil {
				_ = writeStatus("pi_native_error", "Could not send the message to Pi.")
			}
		case <-process.Done:
			message := process.ExitMessage()
			_ = writeStatus("pi_native_exit", message)
			_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Pi native process ended"))
			logSocketEnd("process-ended", errors.New(message))
			return
		case peerErr := <-peer.Done:
			logSocketEnd("peer-ended", peerErr)
			return
		case <-peer.Ping.C:
			if err := peer.WritePing(); err != nil {
				logSocketEnd("ping-write-failed", err)
				return
			}
		}
	}
}

func (h *terminalHandler) startPiNativeProcess(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*piNativeProcess, error) {
	if thread.RollbackPending && !launchOptions.AllowPendingCreation {
		return nil, project.ErrThreadRollbackPending
	}
	if project.EnvironmentSetupBlocksAgent(thread) {
		return nil, errEnvironmentSetupPending
	}
	if h.nativePi == nil {
		return nil, errors.New("native Pi is unavailable")
	}
	return withTerminalThreadMutation(h, item, thread, func() (*piNativeProcess, error) {
		return h.nativePi.getOrStart(item, thread, threadEndpoint, launchOptions)
	}, func(process *piNativeProcess) {
		if process != nil {
			_ = h.nativePi.stopThread(item.ID, thread.ID)
		}
	})
}

func (h *terminalHandler) restartPiNativeProcess(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
	expected *piNativeProcess,
) (*piNativeProcess, error) {
	if thread.RollbackPending {
		return nil, project.ErrThreadRollbackPending
	}
	if h.nativePi == nil {
		return nil, errors.New("native Pi is unavailable")
	}
	return withTerminalThreadMutation(h, item, thread, func() (*piNativeProcess, error) {
		return h.nativePi.restart(expected, item, thread, threadEndpoint, launchOptions)
	}, func(process *piNativeProcess) {
		if process != nil && process != expected {
			_ = h.nativePi.stopThread(item.ID, thread.ID)
		}
	})
}

func (m *piNativeManager) getOrStart(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*piNativeProcess, error) {
	if m == nil {
		return nil, errors.New("native Pi manager is unavailable")
	}
	if m.extensionErr != nil {
		return nil, m.extensionErr
	}
	launchOptions.FigmaMCPURL = m.resolveFigmaMCPURL(item)
	key := piNativeProcessKey{ProjectID: item.ID, ThreadID: thread.ID}

	m.Mu.Lock()
	if current := m.Processes[key]; current != nil && !channelClosed(current.Done) {
		m.Mu.Unlock()
		return current, nil
	}

	process, err := m.startProcess(key, thread, threadEndpoint, launchOptions)
	if err != nil {
		m.Mu.Unlock()
		return nil, err
	}
	m.Processes[key] = process
	delete(m.history, key)
	process.run(func() {
		m.Mu.Lock()
		if m.Processes[key] == process {
			delete(m.Processes, key)
			m.history[key] = process
		}
		m.Mu.Unlock()
	})
	m.Mu.Unlock()
	return process, nil
}

func (m *piNativeManager) restart(
	expected *piNativeProcess,
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*piNativeProcess, error) {
	if m == nil {
		return nil, errors.New("native Pi manager is unavailable")
	}
	if m.extensionErr != nil {
		return nil, m.extensionErr
	}
	launchOptions.FigmaMCPURL = m.resolveFigmaMCPURL(item)
	key := piNativeProcessKey{ProjectID: item.ID, ThreadID: thread.ID}

	m.Mu.Lock()
	defer m.Mu.Unlock()
	current := m.Processes[key]
	if current != nil && current != expected && !channelClosed(current.Done) {
		// Another client already replaced the process. Reuse that replacement
		// rather than immediately restarting it again.
		return current, nil
	}
	if current == nil && expected != nil && !channelClosed(expected.Done) {
		current = expected
	}
	if current != nil {
		if err := current.stop(); err != nil {
			return nil, fmt.Errorf("stop native Pi before restart: %w", err)
		}
		if m.Processes[key] == current {
			delete(m.Processes, key)
		}
	}

	process, err := m.startProcess(key, thread, threadEndpoint, launchOptions)
	if err != nil {
		return nil, err
	}
	m.Processes[key] = process
	process.run(func() {
		m.Mu.Lock()
		if m.Processes[key] == process {
			delete(m.Processes, key)
		}
		m.Mu.Unlock()
	})
	return process, nil
}

func piNativeThreadEnvironment(
	threadEndpoint string,
	projectID string,
	threadID string,
	agentToken string,
	browserThreadEndpoint string,
) []string {
	environment := kiwiCodeThreadEnvironment(threadEndpoint, projectID, threadID)
	if agentToken != "" {
		environment = append(environment, "KIWI_CODE_AGENT_TOKEN="+agentToken)
	}
	if browserThreadEndpoint != "" {
		environment = append(environment, "KIWI_CODE_BROWSER_THREAD_ENDPOINT="+browserThreadEndpoint)
	}
	return environment
}

func (m *piNativeManager) startProcess(
	key piNativeProcessKey,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*piNativeProcess, error) {
	if err := validPiNativePathSegment(key.ProjectID); err != nil {
		return nil, err
	}
	if err := validPiNativePathSegment(key.ThreadID); err != nil {
		return nil, err
	}
	sessionDirectory := filepath.Join(m.dataDirectory, piNativeSessionDirectoryName, key.ProjectID, key.ThreadID)
	if err := os.MkdirAll(sessionDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create native Pi session directory: %w", err)
	}

	piPath := m.piPath
	if piPath == "" {
		var err error
		piPath, err = exec.LookPath(codingAgentPi)
		if err != nil {
			return nil, errors.New("Pi is not installed or not on PATH")
		}
	}
	activeSessionFile, err := piNativeActiveSessionFile(sessionDirectory)
	if err != nil {
		return nil, fmt.Errorf("resolve native Pi session: %w", err)
	}
	if activeSessionFile != "" {
		if err := alignPiNativeSessionCwd(activeSessionFile, thread.Cwd); err != nil {
			return nil, fmt.Errorf("update native Pi session working directory: %w", err)
		}
	}
	command := exec.Command(piPath, piNativeArguments(
		sessionDirectory,
		activeSessionFile,
		m.extensionPaths,
		m.figmaExtension,
		launchOptions,
	)...)
	command.Dir = thread.Cwd
	threadEnvironment := piNativeThreadEnvironment(
		threadEndpoint,
		key.ProjectID,
		key.ThreadID,
		m.agentToken,
		launchOptions.BrowserThreadEndpoint,
	)
	if launchOptions.FigmaMCPURL != "" {
		threadEnvironment = append(threadEnvironment, figmaMCPEnvironmentName+"="+launchOptions.FigmaMCPURL)
	}
	command.Env = append(
		os.Environ(),
		append(
			threadEnvironment,
			"NO_COLOR=1",
			"PI_SKIP_VERSION_CHECK=1",
		)...,
	)
	core, stdout, stderr, err := native.StartCommand(key, piProcessSpec, command)
	if err != nil {
		return nil, err
	}

	process := &piNativeProcess{
		Core:             core,
		launchOptions:    launchOptions,
		sessionDirectory: sessionDirectory,
		runs:             make(map[uint64]piNativeRunSnapshot),
		usageReporter:    m.usageReporter,
	}
	process.ReadOutput(stdout, process.publishPiEvent)
	process.ReadDiagnostics(stderr)
	return process, nil
}

func piNativeArguments(
	sessionDirectory string,
	activeSessionFile string,
	extensionPaths []string,
	figmaExtensionPath string,
	launchOptions codingAgentLaunchOptions,
) []string {
	arguments := []string{
		"--mode", "rpc",
		"--session-dir", sessionDirectory,
	}
	if activeSessionFile != "" {
		// The directory belongs to exactly one Kiwi Code thread, so resume the
		// selected file directly instead of relying on Pi's --continue cwd filter.
		// That filter can strand a valid conversation when a thread's persisted
		// working-directory spelling changes across an application restart.
		arguments = append(arguments, "--session", activeSessionFile)
	} else {
		arguments = append(arguments, "--continue")
	}
	arguments = append(arguments, "--approve")
	for _, extensionPath := range extensionPaths {
		arguments = append(arguments, "--extension", extensionPath)
	}
	// Pi has no built-in MCP support, so Figma is bridged by an extension that
	// only loads for projects that enabled it.
	if launchOptions.FigmaMCPURL != "" && figmaExtensionPath != "" {
		arguments = append(arguments, "--extension", figmaExtensionPath)
	}
	if launchOptions.Model != "" {
		arguments = append(arguments, "--model", launchOptions.Model)
	}
	if launchOptions.ThinkingLevel != "" {
		arguments = append(arguments, "--thinking", launchOptions.ThinkingLevel)
	}
	if launchOptions.AppendSystemPrompt != "" {
		arguments = append(arguments, "--append-system-prompt", launchOptions.AppendSystemPrompt)
	}
	return arguments
}

func piNativeActiveSessionFile(sessionDirectory string) (string, error) {
	markerPath := filepath.Join(sessionDirectory, piNativeActiveSessionMarkerName)
	if contents, err := os.ReadFile(markerPath); err == nil {
		name := strings.TrimSpace(string(contents))
		if validPiNativeSessionFileName(name) {
			candidate := filepath.Join(sessionDirectory, name)
			if _, statErr := os.Lstat(candidate); errors.Is(statErr, os.ErrNotExist) {
				// Pi does not materialize a new session file until it has an assistant
				// message. Keep an intentionally empty or interrupted session selected.
				return candidate, nil
			} else if statErr != nil {
				return "", statErr
			}
			if candidate, valid, candidateErr := piNativeSessionFileByName(sessionDirectory, name); candidateErr != nil {
				return "", candidateErr
			} else if valid {
				return candidate, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	entries, err := os.ReadDir(sessionDirectory)
	if err != nil {
		return "", err
	}
	var selected string
	var selectedModTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		candidate, valid, candidateErr := piNativeSessionFileByName(sessionDirectory, entry.Name())
		if candidateErr != nil {
			return "", candidateErr
		}
		if !valid {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return "", infoErr
		}
		if selected == "" || info.ModTime().After(selectedModTime) ||
			(info.ModTime().Equal(selectedModTime) && entry.Name() > filepath.Base(selected)) {
			selected = candidate
			selectedModTime = info.ModTime()
		}
	}
	return selected, nil
}

func validPiNativeSessionFileName(name string) bool {
	return name != "" && name == filepath.Base(name) && filepath.Ext(name) == ".jsonl"
}

func piNativeSessionFileByName(sessionDirectory, name string) (string, bool, error) {
	if !validPiNativeSessionFileName(name) {
		return "", false, nil
	}
	candidate := filepath.Join(sessionDirectory, name)
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}
	file, err := os.Open(candidate)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	line, readErr := bufio.NewReader(io.LimitReader(file, 64<<10)).ReadBytes('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", false, readErr
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if len(line) == 0 || json.Unmarshal(bytes.TrimSpace(line), &header) != nil || header.Type != "session" || header.ID == "" {
		return "", false, nil
	}
	return candidate, true, nil
}

func alignPiNativeSessionCwd(sessionFile, cwd string) error {
	resolvedCwd, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	file, err := os.Open(sessionFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	headerLine, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		_ = file.Close()
		return err
	}
	var header map[string]any
	if json.Unmarshal(bytes.TrimSpace(headerLine), &header) != nil || header["type"] != "session" {
		_ = file.Close()
		return errors.New("native Pi session has an invalid header")
	}
	if savedCwd, _ := header["cwd"].(string); savedCwd == resolvedCwd {
		return file.Close()
	}
	header["cwd"] = resolvedCwd
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		_ = file.Close()
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(sessionFile), ".session-cwd-*")
	if err != nil {
		_ = file.Close()
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = file.Close()
		cleanup()
		return err
	}
	if _, err := temporary.Write(append(encodedHeader, '\n')); err == nil {
		_, err = io.Copy(temporary, reader)
	}
	closeErr := temporary.Close()
	fileCloseErr := file.Close()
	if err != nil || closeErr != nil || fileCloseErr != nil {
		cleanup()
		return errors.Join(err, closeErr, fileCloseErr)
	}
	if err := os.Rename(temporaryPath, sessionFile); err != nil {
		cleanup()
		return err
	}
	return nil
}

func rememberPiNativeActiveSession(sessionDirectory, sessionFile string) error {
	if strings.TrimSpace(sessionFile) == "" {
		return nil
	}
	resolvedDirectory, err := filepath.Abs(sessionDirectory)
	if err != nil {
		return err
	}
	resolvedFile := sessionFile
	if !filepath.IsAbs(resolvedFile) {
		resolvedFile = filepath.Join(resolvedDirectory, resolvedFile)
	}
	resolvedFile, err = filepath.Abs(resolvedFile)
	if err != nil {
		return err
	}
	if filepath.Dir(resolvedFile) != resolvedDirectory {
		return errors.New("native Pi reported a session outside its thread directory")
	}
	name := filepath.Base(resolvedFile)
	if !validPiNativeSessionFileName(name) {
		return errors.New("native Pi reported an invalid session file name")
	}
	_, valid, err := piNativeSessionFileByName(resolvedDirectory, name)
	if err != nil {
		return err
	}
	if !valid {
		if _, statErr := os.Lstat(resolvedFile); statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		} else if statErr == nil {
			return errors.New("native Pi reported an invalid session file")
		}
	}
	markerPath := filepath.Join(resolvedDirectory, piNativeActiveSessionMarkerName)
	if contents, readErr := os.ReadFile(markerPath); readErr == nil && strings.TrimSpace(string(contents)) == name {
		return nil
	}
	temporary, err := os.CreateTemp(resolvedDirectory, piNativeActiveSessionMarkerName+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if _, err := temporary.WriteString(name + "\n"); err != nil {
		_ = temporary.Close()
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(temporaryPath, markerPath); err != nil {
		cleanup()
		return err
	}
	return nil
}

func validPiNativePathSegment(value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return errors.New("invalid native Pi session identity")
	}
	return nil
}

func (p *piNativeProcess) publishPiEvent(payload []byte) {
	if !json.Valid(payload) {
		log.Printf("ignore malformed native Pi event: project=%q thread=%q", p.Key.ProjectID, p.Key.ThreadID)
		return
	}
	p.trackRunEvent(payload)

	var event struct {
		Type         string          `json:"type"`
		Command      string          `json:"command"`
		Success      bool            `json:"success"`
		Data         json.RawMessage `json:"data"`
		Reason       string          `json:"reason"`
		Aborted      bool            `json:"aborted"`
		ErrorMessage string          `json:"errorMessage"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	if event.Type == "response" && event.Success && (event.Command == "get_state" || event.Command == "get_session_stats") {
		var session struct {
			SessionFile string `json:"sessionFile"`
		}
		if json.Unmarshal(event.Data, &session) == nil && session.SessionFile != "" {
			if err := rememberPiNativeActiveSession(p.sessionDirectory, session.SessionFile); err != nil {
				log.Printf("remember native Pi session: project=%q thread=%q error=%v", p.Key.ProjectID, p.Key.ThreadID, err)
			}
		}
	}
	if event.Type == "response" && event.Command == "get_entries" {
		// get_entries is an internal display-history probe. Unlike get_messages,
		// it includes entries removed from model context by compaction. Publish
		// only the current branch's renderable messages so extension state and
		// abandoned branches do not leak through the browser protocol.
		if event.Success {
			history, err := piNativeDisplayHistoryEvent(event.Data)
			if err != nil {
				log.Printf("build native Pi display history: project=%q thread=%q error=%v", p.Key.ProjectID, p.Key.ThreadID, err)
			} else {
				p.Events.Publish(history)
			}
		}
		// Older Pi versions may reject get_entries. Suppress that private probe
		// response and let the ordinary get_messages snapshot remain the fallback.
		return
	}

	p.Events.Publish(bytes.Clone(payload))
	switch event.Type {
	case "message_end":
		_ = errors.Join(
			p.requestSnapshot("get_messages"),
			p.requestSnapshot("get_session_stats"),
		)
	case "compaction_end":
		if event.Reason != "manual" && !event.Aborted && event.ErrorMessage == "" {
			_ = p.requestSnapshot("get_entries")
		}
	case "agent_start", "agent_settled":
		_ = p.requestSnapshot("get_state")
	case "response":
		if event.Success && event.Command == "get_session_stats" {
			p.reportSessionUsage(event.Data)
		}
		if event.Success && piNativeCommandChangesSession(event.Command) {
			_ = p.refresh()
		}
	}
}

func piNativeDisplayHistoryEvent(data json.RawMessage) ([]byte, error) {
	var snapshot piNativeSessionEntriesSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode session entries: %w", err)
	}

	entriesByID := make(map[string]piNativeSessionEntry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if entry.ID == "" {
			return nil, errors.New("session entry is missing an id")
		}
		if _, duplicate := entriesByID[entry.ID]; duplicate {
			return nil, fmt.Errorf("duplicate session entry id %q", entry.ID)
		}
		entriesByID[entry.ID] = entry
	}

	path := make([]piNativeSessionEntry, 0, len(snapshot.Entries))
	if snapshot.LeafID != nil {
		cursor := *snapshot.LeafID
		visited := make(map[string]struct{}, len(snapshot.Entries))
		for cursor != "" {
			if _, cycle := visited[cursor]; cycle {
				return nil, fmt.Errorf("session entry cycle at %q", cursor)
			}
			visited[cursor] = struct{}{}
			entry, found := entriesByID[cursor]
			if !found {
				return nil, fmt.Errorf("session leaf path references missing entry %q", cursor)
			}
			path = append(path, entry)
			if entry.ParentID == nil {
				break
			}
			cursor = *entry.ParentID
		}
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}

	pathIndexByID := make(map[string]int, len(path))
	for index, entry := range path {
		pathIndexByID[entry.ID] = index
	}
	// A compaction entry is appended after the retained messages, even though
	// its summary replaces the history before firstKeptEntryId in model context.
	// Put the display card at that boundary so later retained work follows it.
	compactionsBeforeEntry := make(map[string][]piNativeSessionEntry)
	relocatedCompactions := make(map[string]struct{})
	for index, entry := range path {
		if entry.Type != "compaction" || entry.FirstKeptEntryID == "" {
			continue
		}
		boundaryIndex, found := pathIndexByID[entry.FirstKeptEntryID]
		if !found || boundaryIndex >= index {
			continue
		}
		compactionsBeforeEntry[entry.FirstKeptEntryID] = append(compactionsBeforeEntry[entry.FirstKeptEntryID], entry)
		relocatedCompactions[entry.ID] = struct{}{}
	}

	messages := make([]json.RawMessage, 0, len(path))
	appendCompaction := func(entry piNativeSessionEntry) error {
		message, err := json.Marshal(struct {
			Role         string `json:"role"`
			Summary      string `json:"summary"`
			TokensBefore int64  `json:"tokensBefore"`
			Timestamp    string `json:"timestamp"`
		}{
			Role:         "compactionSummary",
			Summary:      entry.Summary,
			TokensBefore: entry.TokensBefore,
			Timestamp:    entry.Timestamp,
		})
		if err != nil {
			return fmt.Errorf("encode compaction entry %q: %w", entry.ID, err)
		}
		messages = append(messages, message)
		return nil
	}

	for _, entry := range path {
		for _, compaction := range compactionsBeforeEntry[entry.ID] {
			if err := appendCompaction(compaction); err != nil {
				return nil, err
			}
		}

		switch entry.Type {
		case "message":
			if len(entry.Message) == 0 || !json.Valid(entry.Message) {
				return nil, fmt.Errorf("session message entry %q is malformed", entry.ID)
			}
			messages = append(messages, bytes.Clone(entry.Message))
		case "compaction":
			if _, relocated := relocatedCompactions[entry.ID]; relocated {
				continue
			}
			if err := appendCompaction(entry); err != nil {
				return nil, err
			}
		case "branch_summary":
			message, err := json.Marshal(struct {
				Role      string `json:"role"`
				Summary   string `json:"summary"`
				FromID    string `json:"fromId"`
				Timestamp string `json:"timestamp"`
			}{
				Role:      "branchSummary",
				Summary:   entry.Summary,
				FromID:    entry.FromID,
				Timestamp: entry.Timestamp,
			})
			if err != nil {
				return nil, fmt.Errorf("encode branch summary entry %q: %w", entry.ID, err)
			}
			messages = append(messages, message)
		}
	}

	return json.Marshal(piNativeHistoryEvent{
		Type: "pi_native_history",
		Data: piNativeHistorySnapshot{Messages: messages},
	})
}

func (p *piNativeProcess) reportSessionUsage(data json.RawMessage) {
	if p.usageReporter == nil || len(data) == 0 {
		return
	}
	var stats struct {
		SessionID string `json:"sessionId"`
		Tokens    struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheRead  int64 `json:"cacheRead"`
			CacheWrite int64 `json:"cacheWrite"`
			Total      int64 `json:"total"`
		} `json:"tokens"`
		Cost float64 `json:"cost"`
	}
	if json.Unmarshal(data, &stats) != nil || strings.TrimSpace(stats.SessionID) == "" {
		return
	}
	totals := threadUsageTotals{
		InputTokens: stats.Tokens.Input, OutputTokens: stats.Tokens.Output,
		CacheReadTokens: stats.Tokens.CacheRead, CacheWriteTokens: stats.Tokens.CacheWrite,
		TotalTokens: stats.Tokens.Total, CostUSD: stats.Cost,
	}
	if !validThreadUsageTotals(totals) {
		return
	}
	p.usageReporter(p.Key, stats.SessionID, totals)
}

func (p *piNativeProcess) startPrompt(message string) (piNativeRunSnapshot, error) {
	now := time.Now().UTC()
	p.runMu.Lock()
	p.nextRun++
	run := piNativeRunSnapshot{
		ID:        p.nextRun,
		State:     "starting",
		StartedAt: now,
	}
	p.activeRun = run.ID
	p.runs[run.ID] = run
	p.pruneRunsLocked()
	p.runMu.Unlock()

	command := piNativeRPCCommand{Type: "prompt", Message: &message}
	if err := p.sendClientCommand(command); err != nil {
		p.finishRun(run.ID, "failed", "", err.Error())
		failed, _ := p.runSnapshot(run.ID)
		return failed, err
	}
	return run, nil
}

func (p *piNativeProcess) runSnapshot(runID uint64) (piNativeRunSnapshot, bool) {
	p.runMu.RLock()
	defer p.runMu.RUnlock()
	run, found := p.runs[runID]
	return run, found
}

func (p *piNativeProcess) latestRunSnapshot() (piNativeRunSnapshot, bool) {
	p.runMu.RLock()
	defer p.runMu.RUnlock()
	if p.activeRun == 0 {
		return piNativeRunSnapshot{}, false
	}
	run, found := p.runs[p.activeRun]
	return run, found
}

func (p *piNativeProcess) trackRunEvent(payload []byte) {
	var event struct {
		Type    string          `json:"type"`
		Command string          `json:"command"`
		Success bool            `json:"success"`
		Error   json.RawMessage `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}

	switch event.Type {
	case "agent_start":
		p.runMu.Lock()
		now := time.Now().UTC()
		run, found := p.runs[p.activeRun]
		if !found || run.State == "finished" || run.State == "failed" {
			p.nextRun++
			run = piNativeRunSnapshot{ID: p.nextRun, StartedAt: now}
			p.activeRun = run.ID
		}
		run.State = "working"
		if run.StartedAt.IsZero() {
			run.StartedAt = now
		}
		run.FinishedAt = nil
		p.runs[run.ID] = run
		p.pruneRunsLocked()
		p.runMu.Unlock()
	case "message_end":
		output, stopReason, errorMessage, assistant := piNativeAssistantOutput(event.Message)
		if !assistant {
			return
		}
		p.runMu.Lock()
		runID := p.activeRun
		if run, found := p.runs[runID]; found {
			run.Output = truncatePiNativeTrackedOutput(output)
			p.runs[run.ID] = run
		}
		p.runMu.Unlock()
		if stopReason == "error" || stopReason == "aborted" {
			if errorMessage == "" {
				errorMessage = "Pi " + stopReason + " the child run."
			}
			p.finishRun(runID, "failed", output, errorMessage)
		}
	case "agent_settled":
		p.runMu.RLock()
		runID := p.activeRun
		p.runMu.RUnlock()
		if runID != 0 {
			p.finishRun(runID, "finished", "", "")
		}
	case "response":
		if event.Command != "prompt" || event.Success {
			return
		}
		errorText := strings.TrimSpace(string(event.Error))
		if errorText == "" || errorText == "null" {
			errorText = "Pi rejected the child prompt."
		}
		p.runMu.RLock()
		runID := p.activeRun
		p.runMu.RUnlock()
		if runID != 0 {
			p.finishRun(runID, "failed", "", errorText)
		}
	}
}

func piNativeAssistantOutput(raw json.RawMessage) (output, stopReason, errorMessage string, assistant bool) {
	if len(raw) == 0 {
		return "", "", "", false
	}
	var message struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		StopReason   string          `json:"stopReason"`
		ErrorMessage string          `json:"errorMessage"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Role != "assistant" {
		return "", "", "", false
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text, message.StopReason, message.ErrorMessage, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(message.Content, &parts) != nil {
		return "", message.StopReason, message.ErrorMessage, true
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), message.StopReason, message.ErrorMessage, true
}

func truncatePiNativeTrackedOutput(output string) string {
	if len(output) <= piNativeMaxTrackedOutput {
		return output
	}
	contents := []byte(output)
	contents = contents[:piNativeMaxTrackedOutput]
	for len(contents) > 0 && !utf8.Valid(contents) {
		contents = contents[:len(contents)-1]
	}
	return string(contents) + "\n\n[Output truncated by Kiwi Code.]"
}

func (p *piNativeProcess) finishRun(runID uint64, state, output, errorText string) {
	p.runMu.Lock()
	defer p.runMu.Unlock()
	run, found := p.runs[runID]
	if !found || run.State == "finished" || run.State == "failed" {
		return
	}
	if output != "" {
		run.Output = truncatePiNativeTrackedOutput(output)
	}
	run.State = state
	run.Error = errorText
	now := time.Now().UTC()
	run.FinishedAt = &now
	p.runs[runID] = run
}

func (p *piNativeProcess) failActiveRun(message string) {
	p.runMu.RLock()
	runID := p.activeRun
	p.runMu.RUnlock()
	if runID != 0 {
		p.finishRun(runID, "failed", "", message)
	}
}

func (p *piNativeProcess) pruneRunsLocked() {
	const maxRuns = 32
	if len(p.runs) <= maxRuns {
		return
	}
	oldest := p.nextRun - maxRuns
	for runID := range p.runs {
		if runID <= oldest {
			delete(p.runs, runID)
		}
	}
}

func (p *piNativeProcess) run(onExit func()) {
	p.Core.Run(p.failActiveRun, onExit)
}

func (p *piNativeProcess) send(command piNativeRPCCommand) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	return p.WriteLine(payload)
}

func (p *piNativeProcess) requestSnapshot(command string) error {
	id := fmt.Sprintf("kiwi-code-%s-%d", strings.TrimPrefix(command, "get_"), p.Request.Add(1))
	return p.send(piNativeRPCCommand{ID: id, Type: command})
}

func (p *piNativeProcess) sendClientCommand(command piNativeRPCCommand) error {
	command.ID = fmt.Sprintf("kiwi-code-client-%s-%d", strings.ReplaceAll(command.Type, "_", "-"), p.Request.Add(1))
	return p.send(command)
}

func (p *piNativeProcess) refresh() error {
	return errors.Join(
		p.requestSnapshot("get_state"),
		p.requestSnapshot("get_messages"),
		p.requestSnapshot("get_entries"),
		p.requestSnapshot("get_session_stats"),
	)
}

func (p *piNativeProcess) exitMessage() string {
	if p == nil {
		return piProcessSpec.EndedMessage
	}
	return p.Core.ExitMessage()
}

func (p *piNativeProcess) stop() error {
	if p == nil {
		return nil
	}
	return p.Core.Stop()
}

func (m *piNativeManager) stopThread(projectID, threadID string) error {
	if m == nil {
		return nil
	}
	return m.StopThread(piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}, (*piNativeProcess).Stop)
}

func (m *piNativeManager) stopProject(projectID string) error {
	if m == nil {
		return nil
	}
	return m.StopProject(projectID, (*piNativeProcess).Stop)
}

func (m *piNativeManager) removeThread(projectID, threadID string) error {
	if m == nil {
		return nil
	}
	if m.removeThreadHook != nil {
		return m.removeThreadHook(projectID, threadID)
	}
	if err := validPiNativePathSegment(projectID); err != nil {
		return err
	}
	if err := validPiNativePathSegment(threadID); err != nil {
		return err
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	return m.Remove(
		key,
		filepath.Join(m.dataDirectory, piNativeSessionDirectoryName),
		(*piNativeProcess).Stop,
		func() { delete(m.history, key) },
	)
}

func (m *piNativeManager) removeProject(projectID string) error {
	if m == nil {
		return nil
	}
	if err := validPiNativePathSegment(projectID); err != nil {
		return err
	}
	return m.RemoveProject(
		projectID,
		filepath.Join(m.dataDirectory, piNativeSessionDirectoryName),
		(*piNativeProcess).Stop,
		func() {
			for key := range m.history {
				if key.ProjectID == projectID {
					delete(m.history, key)
				}
			}
		},
	)
}

func (m *piNativeManager) stopAll() {
	if m == nil {
		return
	}
	m.StopAll("Pi", (*piNativeProcess).Stop, func(p *piNativeProcess) native.Key { return p.Key })
}

func normalizePiNativeClientMessage(payload []byte) (piNativeRPCCommand, piNativeClientAction, error) {
	var message piNativeClientMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Invalid native Pi message.")
	}
	switch message.Type {
	case "refresh":
		return piNativeRPCCommand{}, piNativeClientRefresh, nil
	case "reload", "restart":
		provider := strings.TrimSpace(message.Provider)
		modelID := strings.TrimSpace(message.ModelID)
		level := strings.TrimSpace(message.Level)
		if (provider == "") != (modelID == "") || (provider != "" && (strings.Contains(provider, "/") || !validCodingAgentModel(provider+"/"+modelID))) {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Choose a valid Pi model as provider/model before restarting.")
		}
		if level != "" && !codingAgentChoiceExists(piThinkingLevels, level) {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Choose a valid Pi thinking level before restarting.")
		}
		return piNativeRPCCommand{Provider: provider, ModelID: modelID, Level: level}, piNativeClientRestart, nil
	case "abort":
		return piNativeRPCCommand{Type: "abort"}, piNativeClientSendCommand, nil
	case "get_state", "get_commands", "get_available_models", "get_session_stats", "new_session":
		return piNativeRPCCommand{Type: message.Type}, piNativeClientSendCommand, nil
	case "compact":
		instructions := strings.TrimSpace(message.CustomInstructions)
		if len(instructions) > piNativeMaxCompactPrompt {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Compaction instructions are too long.")
		}
		if strings.ContainsRune(instructions, '\x00') {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Compaction instructions contain an invalid character.")
		}
		return piNativeRPCCommand{Type: "compact", CustomInstructions: instructions}, piNativeClientSendCommand, nil
	case "set_model":
		provider := strings.TrimSpace(message.Provider)
		modelID := strings.TrimSpace(message.ModelID)
		if provider == "" || modelID == "" || strings.Contains(provider, "/") || !validCodingAgentModel(provider+"/"+modelID) {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Choose a valid Pi model as provider/model.")
		}
		return piNativeRPCCommand{Type: "set_model", Provider: provider, ModelID: modelID}, piNativeClientSendCommand, nil
	case "set_thinking_level":
		level := strings.TrimSpace(message.Level)
		if level == "" || !codingAgentChoiceExists(piThinkingLevels, level) {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Choose a valid Pi thinking level.")
		}
		return piNativeRPCCommand{Type: "set_thinking_level", Level: level}, piNativeClientSendCommand, nil
	case "prompt":
		if strings.TrimSpace(message.Message) == "" && len(message.Images) == 0 {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Enter a prompt or attach an image before sending.")
		}
		if strings.ContainsRune(message.Message, '\x00') {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("The prompt contains an invalid character.")
		}
		if message.StreamingBehavior != "" && message.StreamingBehavior != "steer" && message.StreamingBehavior != "followUp" {
			return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Unknown Pi message queue mode.")
		}
		images, err := loadPiNativePromptImages(message.Images)
		if err != nil {
			return piNativeRPCCommand{}, piNativeClientSendCommand, err
		}
		return piNativeRPCCommand{
			Type:              "prompt",
			Message:           &message.Message,
			Images:            images,
			StreamingBehavior: message.StreamingBehavior,
		}, piNativeClientSendCommand, nil
	default:
		return piNativeRPCCommand{}, piNativeClientSendCommand, errors.New("Unknown native Pi message.")
	}
}

func loadPiNativePromptImages(references []piNativeClientImage) ([]piNativeRPCImage, error) {
	if len(references) == 0 {
		return nil, nil
	}
	if len(references) > piNativeMaxPromptImages {
		return nil, fmt.Errorf("Attach at most %d images to one Pi prompt.", piNativeMaxPromptImages)
	}

	images := make([]piNativeRPCImage, 0, len(references))
	var totalBytes int64
	for _, reference := range references {
		contents, err := readPiUploadedImage(reference.Path, maxPiImageBytes-totalBytes)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(contents))
		mimeType, ok := piImageMIMEType(contents)
		if !ok {
			return nil, errors.New("Pi accepts PNG, JPEG, GIF, and WebP images.")
		}
		images = append(images, piNativeRPCImage{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(contents),
			MIMEType: mimeType,
		})
	}
	return images, nil
}

func readPiUploadedImage(path string, remainingBytes int64) ([]byte, error) {
	if remainingBytes <= 0 {
		return nil, errors.New("Images in one Pi prompt must total 50 MB or smaller.")
	}
	cleanPath := filepath.Clean(path)
	if path == "" || cleanPath != path || !filepath.IsAbs(cleanPath) {
		return nil, errors.New("Could not read an attached image.")
	}
	absoluteTempDirectory, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, errors.New("Could not read an attached image.")
	}
	absolutePath, err := filepath.Abs(cleanPath)
	if err != nil || filepath.Dir(absolutePath) != absoluteTempDirectory || !strings.HasPrefix(filepath.Base(absolutePath), piImageTempPrefix) {
		return nil, errors.New("Could not read an attached image.")
	}
	pathInfo, err := os.Lstat(absolutePath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, errors.New("Could not read an attached image.")
	}
	if pathInfo.Size() <= 0 {
		return nil, errors.New("The attached image is empty.")
	}
	if pathInfo.Size() > maxPiImageBytes || pathInfo.Size() > remainingBytes {
		return nil, errors.New("Images in one Pi prompt must total 50 MB or smaller.")
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		return nil, errors.New("Could not read an attached image.")
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return nil, errors.New("Could not read an attached image.")
	}
	contents, err := io.ReadAll(io.LimitReader(file, remainingBytes+1))
	if err != nil {
		return nil, errors.New("Could not read an attached image.")
	}
	if len(contents) == 0 {
		return nil, errors.New("The attached image is empty.")
	}
	if int64(len(contents)) > remainingBytes {
		return nil, errors.New("Images in one Pi prompt must total 50 MB or smaller.")
	}
	return contents, nil
}

func piNativeBrowserLaunchOptions(_ project.Thread, model, thinking string) (codingAgentLaunchOptions, error) {
	return normalizeCodingAgentLaunchOptions(codingAgentPi, model, thinking)
}

func piNativeCommandChangesSession(command string) bool {
	switch command {
	case "compact", "new_session", "set_model", "set_thinking_level":
		return true
	default:
		return false
	}
}

func piNativeStartErrorMessage(err error) string {
	switch {
	case err == nil:
		return "Could not start Pi."
	case strings.Contains(err.Error(), "not installed or not on PATH"):
		return "Pi is not installed or not on PATH."
	case errors.Is(err, errTerminalStopping):
		return "This thread is being removed."
	default:
		return "Could not start the native Pi session."
	}
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
