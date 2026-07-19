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

	"github.com/gorilla/websocket"
	"github.com/ivan/dire-mux/internal/broadcast"
	"github.com/ivan/dire-mux/internal/project"
)

const (
	piNativeSessionDirectoryName = "pi-native-sessions"
	piNativeMaxClientMessage     = 1 << 20
	piNativeMaxCompactPrompt     = 64 << 10
	piNativeMaxPromptImages      = 20
	piNativeMaxTrackedOutput     = 1 << 20
	piNativeStopTimeout          = 3 * time.Second
)

type piNativeProcessKey struct {
	ProjectID string
	ThreadID  string
}

type piNativeManager struct {
	mu               sync.Mutex
	dataDirectory    string
	piPath           string
	extensionPaths   []string
	extensionErr     error
	agentToken       string
	processes        map[piNativeProcessKey]*piNativeProcess
	history          map[piNativeProcessKey]*piNativeProcess
	reviewClients    map[piNativeProcessKey]int
	reviewStops      map[piNativeProcessKey]chan struct{}
	contextWatchOnce sync.Once
	usageReporter    func(piNativeProcessKey, string, threadUsageTotals)
	removeThreadHook func(string, string) error
}

type piNativeProcess struct {
	key           piNativeProcessKey
	launchOptions codingAgentLaunchOptions
	command       *exec.Cmd
	stdin         io.WriteCloser
	events        *broadcast.Broker[[]byte]
	done          chan struct{}
	writeMu       sync.Mutex
	exitMu        sync.RWMutex
	exitText      string
	request       atomic.Uint64
	stopping      atomic.Bool
	runMu         sync.RWMutex
	nextRun       uint64
	activeRun     uint64
	runs          map[uint64]piNativeRunSnapshot
	usageReporter func(piNativeProcessKey, string, threadUsageTotals)
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
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	ParentID     *string         `json:"parentId"`
	Timestamp    string          `json:"timestamp"`
	Message      json.RawMessage `json:"message"`
	Summary      string          `json:"summary"`
	FromID       string          `json:"fromId"`
	TokensBefore int64           `json:"tokensBefore"`
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

func newPiNativeManager(dataDirectory string, extensionPaths []string, extensionErr error, agentToken string) *piNativeManager {
	return &piNativeManager{
		dataDirectory:  dataDirectory,
		extensionPaths: append([]string(nil), extensionPaths...),
		extensionErr:   extensionErr,
		agentToken:     agentToken,
		processes:      make(map[piNativeProcessKey]*piNativeProcess),
		history:        make(map[piNativeProcessKey]*piNativeProcess),
		reviewClients:  make(map[piNativeProcessKey]int),
		reviewStops:    make(map[piNativeProcessKey]chan struct{}),
	}
}

func (m *piNativeManager) stopOnContext(ctx context.Context) {
	if m == nil || ctx == nil || ctx.Done() == nil {
		return
	}
	m.contextWatchOnce.Do(func() {
		go func() {
			<-ctx.Done()
			m.stopAll()
		}()
	})
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

	writer := newWebSocketWriter(connection)
	write := writer.Write
	writeStatus := func(statusType, message string) error {
		payload, _ := json.Marshal(map[string]string{"type": statusType, "message": message})
		return write(websocket.TextMessage, payload)
	}
	closeWithError := func(message string) {
		_ = writeStatus("pi_native_error", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, message))
	}
	closeWithFatal := func(message string) {
		_ = writeStatus("pi_native_fatal", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, message))
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
	if thread.ClosedAt != nil {
		h.nativePi.addReviewClient(item.ID, thread.ID)
		defer func() {
			if !h.nativePi.removeReviewClient(item.ID, thread.ID) {
				return
			}
			_, current, currentErr := h.projects.GetThread(item.ID, thread.ID)
			if currentErr == nil && current.ClosedAt == nil {
				return
			}
			_ = h.nativePi.stopReviewThreadIfUnused(item.ID, thread.ID)
		}()
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

	subscription := process.events.Subscribe()
	defer func() { subscription.Close() }()
	if err := writeStatus("pi_native_ready", "Pi is ready."); err != nil {
		return
	}
	_ = process.refresh()

	peer := startWebSocketPeer(connection, writer, rawWebSocketMessage, "native Pi input stalled")
	defer peer.Stop()
	for {
		select {
		case payload, open := <-subscription.Events():
			if !open {
				_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Native Pi client fell behind"))
				return
			}
			if err := write(websocket.TextMessage, payload); err != nil {
				return
			}
		case payload := <-peer.messages:
			if thread.ParentThreadID != "" {
				allowed, policyErr := piNativeChildClientPayloadAllowed(payload)
				if policyErr != nil {
					_ = writeStatus("pi_native_error", policyErr.Error())
					continue
				}
				if !allowed {
					_ = writeStatus("pi_native_error", "Subagent controls are managed by the parent thread.")
					continue
				}
			}
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
				subscription = process.events.Subscribe()
				if err := writeStatus("pi_native_reloaded", "Pi restarted and extensions reloaded."); err != nil {
					return
				}
				_ = process.refresh()
				continue
			}
			if command.Type == "prompt" && h.budgetReached != nil {
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
			if command.Type == "prompt" && thread.ParentThreadID != "" && thread.ClosedAt != nil {
				reopened, reopenErr := h.projects.ReopenChildThread(item.ID, thread.ParentThreadID, thread.ID)
				if reopenErr != nil {
					_ = writeStatus("pi_native_error", "Could not reopen the completed child thread.")
					continue
				}
				thread = reopened
			}
			if err := process.sendClientCommand(command); err != nil {
				_ = writeStatus("pi_native_error", "Could not send the message to Pi.")
			}
		case <-process.done:
			message := process.exitMessage()
			_ = writeStatus("pi_native_exit", message)
			_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Pi native process ended"))
			return
		case <-peer.done:
			return
		case <-peer.ping.C:
			if err := peer.WritePing(); err != nil {
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
	if h.nativePi == nil {
		return nil, errors.New("native Pi is unavailable")
	}
	launchOptions, err := h.withSubAgentNestingPrompt(item.ID, thread, launchOptions)
	if err != nil {
		return nil, fmt.Errorf("resolve sub-agent nesting context: %w", err)
	}

	h.sessionMu.Lock()
	mutation, err := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if err != nil {
		h.sessionMu.Unlock()
		return nil, err
	}
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return nil, errors.Join(err, releaseErr)
	}
	process, startErr := h.nativePi.getOrStart(item, thread, threadEndpoint, launchOptions)
	fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
	releaseErr := mutation.Release()
	h.sessionMu.Unlock()

	if combined := errors.Join(startErr, fenceErr, releaseErr); combined != nil {
		if process != nil {
			_ = h.nativePi.stopThread(item.ID, thread.ID)
		}
		return nil, combined
	}
	return process, nil
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
	launchOptions, err := h.withSubAgentNestingPrompt(item.ID, thread, launchOptions)
	if err != nil {
		return nil, fmt.Errorf("resolve sub-agent nesting context: %w", err)
	}

	h.sessionMu.Lock()
	mutation, err := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if err != nil {
		h.sessionMu.Unlock()
		return nil, err
	}
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return nil, errors.Join(err, releaseErr)
	}
	process, restartErr := h.nativePi.restart(expected, item, thread, threadEndpoint, launchOptions)
	fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
	releaseErr := mutation.Release()
	h.sessionMu.Unlock()

	if combined := errors.Join(restartErr, fenceErr, releaseErr); combined != nil {
		if process != nil && process != expected {
			_ = h.nativePi.stopThread(item.ID, thread.ID)
		}
		return nil, combined
	}
	return process, nil
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
	key := piNativeProcessKey{ProjectID: item.ID, ThreadID: thread.ID}

	for {
		m.mu.Lock()
		if stopping := m.reviewStops[key]; stopping != nil {
			m.mu.Unlock()
			<-stopping
			continue
		}
		if current := m.processes[key]; current != nil && !channelClosed(current.done) {
			m.mu.Unlock()
			return current, nil
		}

		process, err := m.startProcess(key, thread, threadEndpoint, launchOptions)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		m.processes[key] = process
		delete(m.history, key)
		process.run(func() {
			m.mu.Lock()
			if m.processes[key] == process {
				delete(m.processes, key)
				m.history[key] = process
			}
			m.mu.Unlock()
		})
		m.mu.Unlock()
		return process, nil
	}
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
	key := piNativeProcessKey{ProjectID: item.ID, ThreadID: thread.ID}

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.processes[key]
	if current != nil && current != expected && !channelClosed(current.done) {
		// Another client already replaced the process. Reuse that replacement
		// rather than immediately restarting it again.
		return current, nil
	}
	if current == nil && expected != nil && !channelClosed(expected.done) {
		current = expected
	}
	if current != nil {
		if err := current.stop(); err != nil {
			return nil, fmt.Errorf("stop native Pi before restart: %w", err)
		}
		if m.processes[key] == current {
			delete(m.processes, key)
		}
	}

	process, err := m.startProcess(key, thread, threadEndpoint, launchOptions)
	if err != nil {
		return nil, err
	}
	m.processes[key] = process
	process.run(func() {
		m.mu.Lock()
		if m.processes[key] == process {
			delete(m.processes, key)
		}
		m.mu.Unlock()
	})
	return process, nil
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
	command := exec.Command(piPath, piNativeArguments(sessionDirectory, m.extensionPaths, launchOptions)...)
	command.Dir = thread.Cwd
	threadEnvironment := direMuxThreadEnvironment(threadEndpoint, key.ProjectID, key.ThreadID)
	if m.agentToken != "" {
		threadEnvironment = append(threadEnvironment, "DIRE_MUX_AGENT_TOKEN="+m.agentToken)
	}
	if thread.ParentThreadID != "" {
		threadEnvironment = append(threadEnvironment, "DIRE_MUX_PARENT_THREAD_ID="+thread.ParentThreadID)
	}
	command.Env = append(
		os.Environ(),
		append(
			threadEnvironment,
			"NO_COLOR=1",
			"PI_SKIP_VERSION_CHECK=1",
		)...,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open native Pi input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open native Pi output: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open native Pi diagnostics: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start native Pi: %w", err)
	}

	process := &piNativeProcess{
		key:           key,
		launchOptions: launchOptions,
		command:       command,
		stdin:         stdin,
		events:        broadcast.NewBroker[[]byte](broadcast.DefaultMaxPending * 2),
		done:          make(chan struct{}),
		runs:          make(map[uint64]piNativeRunSnapshot),
		usageReporter: m.usageReporter,
	}
	process.readOutput(stdout)
	process.readDiagnostics(stderr)
	return process, nil
}

func (h *terminalHandler) withSubAgentNestingPrompt(
	projectID string,
	thread project.Thread,
	launchOptions codingAgentLaunchOptions,
) (codingAgentLaunchOptions, error) {
	if thread.ParentThreadID == "" {
		return launchOptions, nil
	}
	if h.projects == nil {
		return codingAgentLaunchOptions{}, errors.New("project store is unavailable")
	}
	context, err := h.projects.SubAgentNestingContext(projectID, thread.ID)
	if err != nil {
		return codingAgentLaunchOptions{}, err
	}
	nestingPrompt := fmt.Sprintf(
		"You are a sub-agent at nesting depth %d. The effective maximum sub-agent nesting depth for this thread tree is %d after applying project and ancestor limits. Root agents are at depth 0. Delegate further work only through an available context: fork skill or an explicitly activated Dire Mux workflow, and only while your current depth is below the effective maximum.",
		context.CurrentDepth,
		context.MaxDepth,
	)
	if launchOptions.AppendSystemPrompt != "" {
		launchOptions.AppendSystemPrompt += "\n\n"
	}
	launchOptions.AppendSystemPrompt += nestingPrompt
	return launchOptions, nil
}

func piNativeArguments(
	sessionDirectory string,
	extensionPaths []string,
	launchOptions codingAgentLaunchOptions,
) []string {
	arguments := []string{
		"--mode", "rpc",
		"--session-dir", sessionDirectory,
		"--continue",
		"--approve",
	}
	for _, extensionPath := range extensionPaths {
		arguments = append(arguments, "--extension", extensionPath)
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

func validPiNativePathSegment(value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return errors.New("invalid native Pi session identity")
	}
	return nil
}

func (p *piNativeProcess) readOutput(output io.Reader) {
	go func() {
		reader := bufio.NewReader(output)
		for {
			line, err := reader.ReadBytes('\n')
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) > 0 {
				p.publishPiEvent(line)
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("read native Pi output: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
				}
				return
			}
		}
	}()
}

func (p *piNativeProcess) readDiagnostics(output io.Reader) {
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				log.Printf("native Pi: project=%q thread=%q %s", p.key.ProjectID, p.key.ThreadID, line)
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("read native Pi diagnostics: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
		}
	}()
}

func (p *piNativeProcess) publishPiEvent(payload []byte) {
	if !json.Valid(payload) {
		log.Printf("ignore malformed native Pi event: project=%q thread=%q", p.key.ProjectID, p.key.ThreadID)
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
	if event.Type == "response" && event.Command == "get_entries" {
		// get_entries is an internal display-history probe. Unlike get_messages,
		// it includes entries removed from model context by compaction. Publish
		// only the current branch's renderable messages so extension state and
		// abandoned branches do not leak through the browser protocol.
		if event.Success {
			history, err := piNativeDisplayHistoryEvent(event.Data)
			if err != nil {
				log.Printf("build native Pi display history: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
			} else {
				p.events.Publish(history)
			}
		}
		// Older Pi versions may reject get_entries. Suppress that private probe
		// response and let the ordinary get_messages snapshot remain the fallback.
		return
	}

	p.events.Publish(bytes.Clone(payload))
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

	messages := make([]json.RawMessage, 0, len(path))
	for _, entry := range path {
		switch entry.Type {
		case "message":
			if len(entry.Message) == 0 || !json.Valid(entry.Message) {
				return nil, fmt.Errorf("session message entry %q is malformed", entry.ID)
			}
			messages = append(messages, bytes.Clone(entry.Message))
		case "compaction":
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
				return nil, fmt.Errorf("encode compaction entry %q: %w", entry.ID, err)
			}
			messages = append(messages, message)
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
	p.usageReporter(p.key, stats.SessionID, totals)
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
	return string(contents) + "\n\n[Output truncated by Dire Mux.]"
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
	go func() {
		err := p.command.Wait()
		message := "Pi session ended."
		if err != nil && !p.stopping.Load() {
			message = "Pi exited unexpectedly. Reconnect to resume the saved conversation."
			log.Printf("native Pi exited: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
		}
		p.failActiveRun(message)
		p.exitMu.Lock()
		p.exitText = message
		p.exitMu.Unlock()
		close(p.done)
		onExit()
	}()
}

func (p *piNativeProcess) send(command piNativeRPCCommand) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if channelClosed(p.done) {
		return errors.New("native Pi process ended")
	}
	_, err = p.stdin.Write(payload)
	return err
}

func (p *piNativeProcess) requestSnapshot(command string) error {
	id := fmt.Sprintf("dire-mux-%s-%d", strings.TrimPrefix(command, "get_"), p.request.Add(1))
	return p.send(piNativeRPCCommand{ID: id, Type: command})
}

func (p *piNativeProcess) sendClientCommand(command piNativeRPCCommand) error {
	command.ID = fmt.Sprintf("dire-mux-client-%s-%d", strings.ReplaceAll(command.Type, "_", "-"), p.request.Add(1))
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
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitText == "" {
		return "Pi session ended."
	}
	return p.exitText
}

func (p *piNativeProcess) stop() error {
	if p == nil || channelClosed(p.done) {
		return nil
	}
	p.stopping.Store(true)
	_ = p.stdin.Close()
	if p.command.Process != nil {
		_ = p.command.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(piNativeStopTimeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		if p.command.Process != nil {
			if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
		}
		<-p.done
		return nil
	}
}

func (m *piNativeManager) childRun(projectID, threadID string, runID uint64) (piNativeRunSnapshot, bool) {
	if m == nil {
		return piNativeRunSnapshot{}, false
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	process := m.processes[key]
	if process == nil {
		process = m.history[key]
	}
	m.mu.Unlock()
	if process == nil {
		return piNativeRunSnapshot{}, false
	}
	return process.runSnapshot(runID)
}

func (m *piNativeManager) latestChildRun(projectID, threadID string) (piNativeRunSnapshot, bool) {
	if m == nil {
		return piNativeRunSnapshot{}, false
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	process := m.processes[key]
	if process == nil {
		process = m.history[key]
	}
	m.mu.Unlock()
	if process == nil {
		return piNativeRunSnapshot{}, false
	}
	return process.latestRunSnapshot()
}

func (m *piNativeManager) addReviewClient(projectID, threadID string) {
	if m == nil {
		return
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	m.reviewClients[key]++
	m.mu.Unlock()
}

func (m *piNativeManager) removeReviewClient(projectID, threadID string) bool {
	if m == nil {
		return false
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	defer m.mu.Unlock()
	clients := m.reviewClients[key]
	if clients <= 1 {
		delete(m.reviewClients, key)
		return clients == 1
	}
	m.reviewClients[key] = clients - 1
	return false
}

func (m *piNativeManager) stopReviewThreadIfUnused(projectID, threadID string) error {
	if m == nil {
		return nil
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	if m.reviewClients[key] != 0 {
		m.mu.Unlock()
		return nil
	}
	if stopping := m.reviewStops[key]; stopping != nil {
		m.mu.Unlock()
		<-stopping
		return nil
	}
	process := m.processes[key]
	if process == nil || channelClosed(process.done) {
		m.mu.Unlock()
		return nil
	}
	stopped := make(chan struct{})
	m.reviewStops[key] = stopped
	m.mu.Unlock()

	stopErr := process.stop()
	m.mu.Lock()
	if m.reviewStops[key] == stopped {
		delete(m.reviewStops, key)
		close(stopped)
	}
	m.mu.Unlock()
	return stopErr
}

func (m *piNativeManager) stopThread(projectID, threadID string) error {
	if m == nil {
		return nil
	}
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	m.mu.Lock()
	process := m.processes[key]
	m.mu.Unlock()
	if process == nil {
		return nil
	}
	return process.stop()
}

func (m *piNativeManager) stopProject(projectID string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	processes := make([]*piNativeProcess, 0)
	for key, process := range m.processes {
		if key.ProjectID == projectID {
			processes = append(processes, process)
		}
	}
	m.mu.Unlock()
	var stopErrors []error
	for _, process := range processes {
		stopErrors = append(stopErrors, process.stop())
	}
	return errors.Join(stopErrors...)
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
	stopErr := m.stopThread(projectID, threadID)
	m.mu.Lock()
	delete(m.processes, piNativeProcessKey{ProjectID: projectID, ThreadID: threadID})
	delete(m.history, piNativeProcessKey{ProjectID: projectID, ThreadID: threadID})
	delete(m.reviewClients, piNativeProcessKey{ProjectID: projectID, ThreadID: threadID})
	m.mu.Unlock()
	removeErr := os.RemoveAll(filepath.Join(
		m.dataDirectory,
		piNativeSessionDirectoryName,
		projectID,
		threadID,
	))
	return errors.Join(stopErr, removeErr)
}

func (m *piNativeManager) removeProject(projectID string) error {
	if m == nil {
		return nil
	}
	if err := validPiNativePathSegment(projectID); err != nil {
		return err
	}
	stopErr := m.stopProject(projectID)
	m.mu.Lock()
	for key := range m.history {
		if key.ProjectID == projectID {
			delete(m.history, key)
		}
	}
	for key := range m.reviewClients {
		if key.ProjectID == projectID {
			delete(m.reviewClients, key)
		}
	}
	m.mu.Unlock()
	removeErr := os.RemoveAll(filepath.Join(
		m.dataDirectory,
		piNativeSessionDirectoryName,
		projectID,
	))
	return errors.Join(stopErr, removeErr)
}

func (m *piNativeManager) stopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	processes := make([]*piNativeProcess, 0, len(m.processes))
	for _, process := range m.processes {
		processes = append(processes, process)
	}
	m.mu.Unlock()
	for _, process := range processes {
		if err := process.stop(); err != nil {
			log.Printf("stop native Pi: project=%q thread=%q error=%v", process.key.ProjectID, process.key.ThreadID, err)
		}
	}
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

func piNativeBrowserLaunchOptions(thread project.Thread, model, thinking string) (codingAgentLaunchOptions, error) {
	if thread.ParentThreadID != "" {
		if strings.TrimSpace(model) != "" || strings.TrimSpace(thinking) != "" {
			return codingAgentLaunchOptions{}, errors.New("Subagent launch settings are managed by the parent thread.")
		}
		options, err := normalizeCodingAgentLaunchOptions(
			codingAgentPi,
			thread.AgentModel,
			thread.AgentThinkingLevel,
		)
		if err != nil {
			return codingAgentLaunchOptions{}, err
		}
		return options, nil
	}
	return normalizeCodingAgentLaunchOptions(codingAgentPi, model, thinking)
}

func piNativeChildClientPayloadAllowed(payload []byte) (bool, error) {
	var message struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return false, errors.New("Invalid native Pi message.")
	}
	switch message.Type {
	case "refresh", "get_state", "get_commands", "get_available_models", "get_session_stats":
		return true, nil
	default:
		return false, nil
	}
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
