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

	"github.com/dire-kiwi/kiwi-code/internal/broadcast"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

const (
	claudeNativeSessionDirectoryName = "claude-native-sessions"
	claudeNativeSessionFileName      = "session-id"
	claudeNativeHistoryFileName      = "events.jsonl"
	claudeNativeMaxClientMessage     = 1 << 20
	claudeNativeMaxHistoryBytes      = 8 << 20
	claudeNativeMaxPromptImages      = 20
	claudeNativeStopTimeout          = 3 * time.Second
)

type claudeNativeManager struct {
	mu               sync.Mutex
	dataDirectory    string
	claudePath       string
	pluginPath       string
	pluginErr        error
	processes        map[piNativeProcessKey]*claudeNativeProcess
	contextWatchOnce sync.Once
	usageReporter    func(piNativeProcessKey, string, threadUsageTotals)
}

type claudeNativeProcess struct {
	key              piNativeProcessKey
	launchOptions    codingAgentLaunchOptions
	sessionDirectory string
	command          *exec.Cmd
	stdin            io.WriteCloser
	events           *broadcast.Broker[[]byte]
	done             chan struct{}
	writeMu          sync.Mutex
	exitMu           sync.RWMutex
	exitText         string
	request          atomic.Uint64
	stopping         atomic.Bool

	stateMu   sync.Mutex
	streaming bool
	sessionID string
	model     string

	historyMu sync.Mutex

	usageMu       sync.Mutex
	usageSession  string
	usageTotals   threadUsageTotals
	usageReporter func(piNativeProcessKey, string, threadUsageTotals)
}

type claudeNativeClientMessage struct {
	Type    string                `json:"type"`
	Message string                `json:"message,omitempty"`
	Images  []piNativeClientImage `json:"images,omitempty"`
	ModelID string                `json:"modelId,omitempty"`
	Level   string                `json:"level,omitempty"`
}

type claudeNativeClientAction uint8

const (
	claudeNativeClientPrompt claudeNativeClientAction = iota
	claudeNativeClientAbort
	claudeNativeClientRefresh
	claudeNativeClientState
	claudeNativeClientRestart
	claudeNativeClientNewSession
)

type claudeNativeHistoryEntry struct {
	At    int64           `json:"at"`
	Event json.RawMessage `json:"event"`
}

func newClaudeNativeManager(dataDirectory, pluginPath string, pluginErr error) *claudeNativeManager {
	return &claudeNativeManager{
		dataDirectory: dataDirectory,
		pluginPath:    pluginPath,
		pluginErr:     pluginErr,
		processes:     make(map[piNativeProcessKey]*claudeNativeProcess),
	}
}

func (m *claudeNativeManager) stopOnContext(ctx context.Context) {
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

func (h *terminalHandler) serveClaudeNative(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "The native Claude endpoint requires a WebSocket connection.")
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
	if thread.ParentThreadID != "" {
		writeError(w, http.StatusForbidden, "Subagent threads use native Pi.")
		return
	}
	// Upgrade before starting Claude. A rejected WebSocket origin must not
	// cause agent-side code to run.
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(claudeNativeMaxClientMessage)

	writer := newWebSocketWriter(connection)
	write := writer.Write
	writeStatus := func(statusType, message string) error {
		payload, _ := json.Marshal(map[string]string{"type": statusType, "message": message})
		return write(websocket.TextMessage, payload)
	}
	closeWithError := func(message string) {
		_ = writeStatus("claude_native_error", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, message))
	}
	closeWithFatal := func(message string) {
		_ = writeStatus("claude_native_fatal", message)
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, message))
	}

	launchOptions, err := normalizeCodingAgentLaunchOptions(
		codingAgentClaude,
		r.URL.Query().Get("model"),
		r.URL.Query().Get("thinking"),
	)
	if err != nil {
		closeWithFatal(err.Error())
		return
	}
	process, err := h.startClaudeNativeProcess(
		item,
		thread,
		threadEndpointURL(r, item.ID, thread.ID),
		launchOptions,
	)
	if err != nil {
		closeWithError(claudeNativeStartErrorMessage(err))
		return
	}

	subscription := process.events.Subscribe()
	defer func() { subscription.Close() }()
	if err := writeStatus("claude_native_ready", "Claude is ready."); err != nil {
		return
	}
	sendSnapshot := func(target *claudeNativeProcess) error {
		history, historyErr := target.historySnapshot()
		if historyErr != nil {
			_ = writeStatus("claude_native_error", "Could not load the saved Claude conversation.")
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"type":   "claude_native_history",
			"events": history,
		})
		if marshalErr != nil {
			return marshalErr
		}
		if err := write(websocket.TextMessage, payload); err != nil {
			return err
		}
		return write(websocket.TextMessage, target.statePayload())
	}
	if err := sendSnapshot(process); err != nil {
		return
	}

	peer := startWebSocketPeer(connection, writer, rawWebSocketMessage, "native Claude input stalled")
	defer peer.Stop()
	for {
		select {
		case payload, open := <-subscription.Events():
			if !open {
				_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Native Claude client fell behind"))
				return
			}
			if err := write(websocket.TextMessage, payload); err != nil {
				return
			}
		case payload := <-peer.messages:
			message, action, commandErr := normalizeClaudeNativeClientMessage(payload)
			if commandErr != nil {
				_ = writeStatus("claude_native_error", commandErr.Error())
				continue
			}
			switch action {
			case claudeNativeClientRefresh:
				if err := sendSnapshot(process); err != nil {
					return
				}
				continue
			case claudeNativeClientState:
				if err := write(websocket.TextMessage, process.statePayload()); err != nil {
					return
				}
				continue
			case claudeNativeClientRestart, claudeNativeClientNewSession:
				restartOptions := launchOptions
				if message.ModelID != "" {
					restartOptions.Model = message.ModelID
				}
				if message.Level != "" {
					restartOptions.ThinkingLevel = message.Level
				}
				if err := writeStatus("claude_native_restarting", "Restarting the Claude session…"); err != nil {
					return
				}
				replacement, restartErr := h.restartClaudeNativeProcess(
					item,
					thread,
					threadEndpointURL(r, item.ID, thread.ID),
					restartOptions,
					process,
					action == claudeNativeClientNewSession,
				)
				if restartErr != nil {
					closeWithError("Could not restart the native Claude session.")
					return
				}
				subscription.Close()
				process = replacement
				launchOptions = restartOptions
				subscription = process.events.Subscribe()
				reloadedMessage := "Claude restarted and resumed this conversation."
				if action == claudeNativeClientNewSession {
					reloadedMessage = "Started a new Claude session."
				}
				if err := writeStatus("claude_native_reloaded", reloadedMessage); err != nil {
					return
				}
				if err := sendSnapshot(process); err != nil {
					return
				}
				continue
			case claudeNativeClientAbort:
				if err := process.sendInterrupt(); err != nil {
					_ = writeStatus("claude_native_error", "Could not interrupt Claude.")
				}
				continue
			}
			if h.budgetReached != nil {
				reached, _, budgetErr := h.budgetReached(item.ID, thread.ID)
				if budgetErr != nil {
					_ = writeStatus("claude_native_error", "Could not verify the thread usage limit.")
					continue
				}
				if reached {
					_ = writeStatus("claude_native_error", "Thread token or cost limit reached. Increase or remove the limit in Thread details to continue.")
					continue
				}
			}
			if err := process.sendPrompt(message.Message, message.Images); err != nil {
				_ = writeStatus("claude_native_error", "Could not send the message to Claude.")
			}
		case <-process.done:
			message := process.exitMessage()
			_ = writeStatus("claude_native_exit", message)
			_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Claude native process ended"))
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

func (h *terminalHandler) startClaudeNativeProcess(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*claudeNativeProcess, error) {
	if thread.RollbackPending && !launchOptions.AllowPendingCreation {
		return nil, project.ErrThreadRollbackPending
	}
	if h.nativeClaude == nil {
		return nil, errors.New("native Claude is unavailable")
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
	process, startErr := h.nativeClaude.getOrStart(item, thread, threadEndpoint, launchOptions)
	fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
	releaseErr := mutation.Release()
	h.sessionMu.Unlock()

	if combined := errors.Join(startErr, fenceErr, releaseErr); combined != nil {
		if process != nil {
			_ = h.nativeClaude.stopThread(item.ID, thread.ID)
		}
		return nil, combined
	}
	return process, nil
}

func (h *terminalHandler) restartClaudeNativeProcess(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
	expected *claudeNativeProcess,
	resetSession bool,
) (*claudeNativeProcess, error) {
	if thread.RollbackPending {
		return nil, project.ErrThreadRollbackPending
	}
	if h.nativeClaude == nil {
		return nil, errors.New("native Claude is unavailable")
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
	process, restartErr := h.nativeClaude.restart(expected, item, thread, threadEndpoint, launchOptions, resetSession)
	fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
	releaseErr := mutation.Release()
	h.sessionMu.Unlock()

	if combined := errors.Join(restartErr, fenceErr, releaseErr); combined != nil {
		if process != nil && process != expected {
			_ = h.nativeClaude.stopThread(item.ID, thread.ID)
		}
		return nil, combined
	}
	return process, nil
}

func (m *claudeNativeManager) getOrStart(
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
) (*claudeNativeProcess, error) {
	if m == nil {
		return nil, errors.New("native Claude manager is unavailable")
	}
	if m.pluginErr != nil {
		return nil, m.pluginErr
	}
	key := piNativeProcessKey{ProjectID: item.ID, ThreadID: thread.ID}

	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.processes[key]; current != nil && !channelClosed(current.done) {
		return current, nil
	}

	process, err := m.startProcess(key, thread, threadEndpoint, launchOptions, false)
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

func (m *claudeNativeManager) restart(
	expected *claudeNativeProcess,
	item project.Project,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
	resetSession bool,
) (*claudeNativeProcess, error) {
	if m == nil {
		return nil, errors.New("native Claude manager is unavailable")
	}
	if m.pluginErr != nil {
		return nil, m.pluginErr
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
			return nil, fmt.Errorf("stop native Claude before restart: %w", err)
		}
		if m.processes[key] == current {
			delete(m.processes, key)
		}
	}

	process, err := m.startProcess(key, thread, threadEndpoint, launchOptions, resetSession)
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

func (m *claudeNativeManager) startProcess(
	key piNativeProcessKey,
	thread project.Thread,
	threadEndpoint string,
	launchOptions codingAgentLaunchOptions,
	resetSession bool,
) (*claudeNativeProcess, error) {
	if err := validPiNativePathSegment(key.ProjectID); err != nil {
		return nil, err
	}
	if err := validPiNativePathSegment(key.ThreadID); err != nil {
		return nil, err
	}
	sessionDirectory := filepath.Join(m.dataDirectory, claudeNativeSessionDirectoryName, key.ProjectID, key.ThreadID)
	if err := os.MkdirAll(sessionDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create native Claude session directory: %w", err)
	}
	if resetSession {
		if err := errors.Join(
			removeIfExists(filepath.Join(sessionDirectory, claudeNativeSessionFileName)),
			removeIfExists(filepath.Join(sessionDirectory, claudeNativeHistoryFileName)),
		); err != nil {
			return nil, fmt.Errorf("reset native Claude session: %w", err)
		}
	}
	resumeSessionID := readClaudeNativeSessionID(sessionDirectory)

	claudePath := m.claudePath
	if claudePath == "" {
		var err error
		claudePath, err = exec.LookPath(codingAgentClaude)
		if err != nil {
			return nil, errors.New("Claude Code is not installed or not on PATH")
		}
	}
	command := exec.Command(claudePath, claudeNativeArguments(m.pluginPath, resumeSessionID, launchOptions)...)
	command.Dir = thread.Cwd
	threadEnvironment := kiwiCodeThreadEnvironment(threadEndpoint, key.ProjectID, key.ThreadID)
	// Match the terminal Claude launch: no Kiwi Code agent token or child-thread
	// metadata. The Claude browser MCP server reads its capability from the
	// protected data directory instead of the environment.
	piPath := codingAgentPi
	if resolvedPiPath, err := exec.LookPath(codingAgentPi); err == nil {
		piPath = resolvedPiPath
	}
	command.Env = append(
		os.Environ(),
		append(
			threadEnvironment,
			"KIWI_CODE_PI_PATH="+piPath,
			"KIWI_CODE_CODING_AGENT="+codingAgentClaude,
		)...,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open native Claude input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open native Claude output: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open native Claude diagnostics: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start native Claude: %w", err)
	}

	process := &claudeNativeProcess{
		key:              key,
		launchOptions:    launchOptions,
		sessionDirectory: sessionDirectory,
		command:          command,
		stdin:            stdin,
		events:           broadcast.NewBroker[[]byte](broadcast.DefaultMaxPending * 2),
		done:             make(chan struct{}),
		sessionID:        resumeSessionID,
		usageReporter:    m.usageReporter,
	}
	process.readOutput(stdout)
	process.readDiagnostics(stderr)
	return process, nil
}

func claudeNativeArguments(pluginPath, resumeSessionID string, launchOptions codingAgentLaunchOptions) []string {
	arguments := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
		"--verbose",
		"--dangerously-skip-permissions",
		"--settings", `{"skipDangerousModePermissionPrompt":true}`,
	}
	if pluginPath != "" {
		arguments = append(arguments, "--plugin-dir", pluginPath)
	}
	if resumeSessionID != "" {
		arguments = append(arguments, "--resume", resumeSessionID)
	}
	if launchOptions.Model != "" {
		arguments = append(arguments, "--model", launchOptions.Model)
	}
	if launchOptions.ThinkingLevel != "" {
		arguments = append(arguments, "--effort", launchOptions.ThinkingLevel)
	}
	if launchOptions.AppendSystemPrompt != "" {
		arguments = append(arguments, "--append-system-prompt", launchOptions.AppendSystemPrompt)
	}
	return arguments
}

func readClaudeNativeSessionID(sessionDirectory string) string {
	contents, err := os.ReadFile(filepath.Join(sessionDirectory, claudeNativeSessionFileName))
	if err != nil {
		return ""
	}
	sessionID := strings.TrimSpace(string(contents))
	if sessionID == "" || len(sessionID) > 512 || strings.ContainsAny(sessionID, " \t\n\r/\\") || strings.HasPrefix(sessionID, "-") {
		return ""
	}
	return sessionID
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (p *claudeNativeProcess) readOutput(output io.Reader) {
	go func() {
		reader := bufio.NewReader(output)
		for {
			line, err := reader.ReadBytes('\n')
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) > 0 {
				p.publishClaudeEvent(line)
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("read native Claude output: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
				}
				return
			}
		}
	}()
}

func (p *claudeNativeProcess) readDiagnostics(output io.Reader) {
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				log.Printf("native Claude: project=%q thread=%q %s", p.key.ProjectID, p.key.ThreadID, line)
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("read native Claude diagnostics: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
		}
	}()
}

func (p *claudeNativeProcess) publishClaudeEvent(payload []byte) {
	if !json.Valid(payload) {
		log.Printf("ignore malformed native Claude event: project=%q thread=%q", p.key.ProjectID, p.key.ThreadID)
		return
	}
	var event struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
		IsError   bool   `json:"is_error"`
		Usage     *struct {
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
		TotalCostUSD *float64 `json:"total_cost_usd"`
		RequestID    string   `json:"request_id"`
	}
	if json.Unmarshal(payload, &event) != nil {
		p.events.Publish(bytes.Clone(payload))
		return
	}

	if event.SessionID != "" {
		p.adoptSessionID(event.SessionID)
	}

	stateChanged := false
	switch event.Type {
	case "system":
		if event.Subtype == "init" {
			p.stateMu.Lock()
			if event.Model != "" && p.model != event.Model {
				p.model = event.Model
				stateChanged = true
			}
			p.stateMu.Unlock()
		}
	case "assistant", "stream_event":
		stateChanged = p.setStreaming(true)
	case "result":
		stateChanged = p.setStreaming(false)
		p.reportResultUsage(event.SessionID, event.Usage, event.TotalCostUSD)
	case "control_request":
		// The process should never need client-side capabilities in this
		// bridge. Answer instead of leaving the request pending forever.
		if event.RequestID != "" {
			response, _ := json.Marshal(map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype":    "error",
					"request_id": event.RequestID,
					"error":      "Kiwi Code does not support this control request.",
				},
			})
			_ = p.send(response)
		}
	}

	if claudeNativeHistoryEventType(event.Type, event.Subtype) {
		p.appendHistory(payload)
	}
	p.events.Publish(bytes.Clone(payload))
	if stateChanged {
		p.events.Publish(p.statePayload())
	}
}

func claudeNativeHistoryEventType(eventType, subtype string) bool {
	switch eventType {
	case "assistant", "user", "result":
		return true
	case "system":
		return subtype == "init"
	default:
		return false
	}
}

func (p *claudeNativeProcess) adoptSessionID(sessionID string) {
	p.stateMu.Lock()
	changed := p.sessionID != sessionID
	p.sessionID = sessionID
	p.stateMu.Unlock()
	if !changed {
		return
	}
	path := filepath.Join(p.sessionDirectory, claudeNativeSessionFileName)
	if err := os.WriteFile(path, []byte(sessionID+"\n"), 0o600); err != nil {
		log.Printf("save native Claude session id: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
	}
}

func (p *claudeNativeProcess) setStreaming(streaming bool) bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.streaming == streaming {
		return false
	}
	p.streaming = streaming
	return true
}

func (p *claudeNativeProcess) statePayload() []byte {
	p.stateMu.Lock()
	state := map[string]any{
		"type":        "claude_native_state",
		"isStreaming": p.streaming,
		"sessionId":   p.sessionID,
		"model":       p.model,
		"effort":      p.launchOptions.ThinkingLevel,
	}
	p.stateMu.Unlock()
	payload, _ := json.Marshal(state)
	return payload
}

func (p *claudeNativeProcess) reportResultUsage(
	sessionID string,
	usage *struct {
		InputTokens         int64 `json:"input_tokens"`
		OutputTokens        int64 `json:"output_tokens"`
		CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	},
	totalCostUSD *float64,
) {
	if p.usageReporter == nil || usage == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheCreationTokens < 0 || usage.CacheReadTokens < 0 {
		return
	}
	p.usageMu.Lock()
	if p.usageSession != sessionID {
		// Resuming can fork to a fresh session id; usage is tracked per
		// session so a new id restarts the cumulative totals.
		p.usageSession = sessionID
		p.usageTotals = threadUsageTotals{}
	}
	p.usageTotals.InputTokens += usage.InputTokens
	p.usageTotals.OutputTokens += usage.OutputTokens
	p.usageTotals.CacheReadTokens += usage.CacheReadTokens
	p.usageTotals.CacheWriteTokens += usage.CacheCreationTokens
	p.usageTotals.TotalTokens = p.usageTotals.InputTokens +
		p.usageTotals.OutputTokens +
		p.usageTotals.CacheReadTokens +
		p.usageTotals.CacheWriteTokens
	if totalCostUSD != nil && *totalCostUSD > p.usageTotals.CostUSD {
		p.usageTotals.CostUSD = *totalCostUSD
	}
	totals := p.usageTotals
	p.usageMu.Unlock()
	if !validThreadUsageTotals(totals) {
		return
	}
	p.usageReporter(p.key, sessionID, totals)
}

func (p *claudeNativeProcess) appendHistory(payload []byte) {
	entry, err := json.Marshal(claudeNativeHistoryEntry{
		At:    time.Now().UnixMilli(),
		Event: json.RawMessage(payload),
	})
	if err != nil {
		return
	}
	entry = append(entry, '\n')
	p.historyMu.Lock()
	defer p.historyMu.Unlock()
	file, err := os.OpenFile(
		filepath.Join(p.sessionDirectory, claudeNativeHistoryFileName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		log.Printf("append native Claude history: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(entry); err != nil {
		log.Printf("append native Claude history: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
	}
}

func (p *claudeNativeProcess) historySnapshot() ([]claudeNativeHistoryEntry, error) {
	p.historyMu.Lock()
	contents, err := os.ReadFile(filepath.Join(p.sessionDirectory, claudeNativeHistoryFileName))
	p.historyMu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return []claudeNativeHistoryEntry{}, nil
	}
	if err != nil {
		return []claudeNativeHistoryEntry{}, err
	}
	if len(contents) > claudeNativeMaxHistoryBytes {
		contents = contents[len(contents)-claudeNativeMaxHistoryBytes:]
		if newline := bytes.IndexByte(contents, '\n'); newline >= 0 {
			contents = contents[newline+1:]
		}
	}
	entries := make([]claudeNativeHistoryEntry, 0, 64)
	for _, line := range bytes.Split(contents, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry claudeNativeHistoryEntry
		if json.Unmarshal(line, &entry) != nil || len(entry.Event) == 0 {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (p *claudeNativeProcess) run(onExit func()) {
	go func() {
		err := p.command.Wait()
		message := "Claude session ended."
		if err != nil && !p.stopping.Load() {
			message = "Claude exited unexpectedly. Reconnect to resume the saved conversation."
			log.Printf("native Claude exited: project=%q thread=%q error=%v", p.key.ProjectID, p.key.ThreadID, err)
		}
		p.setStreaming(false)
		p.exitMu.Lock()
		p.exitText = message
		p.exitMu.Unlock()
		close(p.done)
		onExit()
	}()
}

func (p *claudeNativeProcess) send(payload []byte) error {
	payload = append(bytes.Clone(payload), '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if channelClosed(p.done) {
		return errors.New("native Claude process ended")
	}
	_, err := p.stdin.Write(payload)
	return err
}

func (p *claudeNativeProcess) sendPrompt(message string, images []piNativeClientImage) error {
	content, err := claudeNativePromptContent(message, images)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	})
	if err != nil {
		return err
	}
	if err := p.send(payload); err != nil {
		return err
	}
	if p.setStreaming(true) {
		p.events.Publish(p.statePayload())
	}
	return nil
}

func (p *claudeNativeProcess) sendInterrupt() error {
	payload, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("kiwi-code-interrupt-%d", p.request.Add(1)),
		"request":    map[string]any{"subtype": "interrupt"},
	})
	if err != nil {
		return err
	}
	return p.send(payload)
}

func claudeNativePromptContent(message string, references []piNativeClientImage) ([]map[string]any, error) {
	if len(references) > claudeNativeMaxPromptImages {
		return nil, fmt.Errorf("Attach at most %d images to one Claude prompt.", claudeNativeMaxPromptImages)
	}
	content := make([]map[string]any, 0, len(references)+1)
	var totalBytes int64
	for _, reference := range references {
		contents, err := readPiUploadedImage(reference.Path, maxPiImageBytes-totalBytes)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(contents))
		mimeType, ok := piImageMIMEType(contents)
		if !ok {
			return nil, errors.New("Claude accepts PNG, JPEG, GIF, and WebP images.")
		}
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mimeType,
				"data":       base64.StdEncoding.EncodeToString(contents),
			},
		})
	}
	if message != "" {
		content = append(content, map[string]any{"type": "text", "text": message})
	}
	if len(content) == 0 {
		return nil, errors.New("Enter a prompt or attach an image before sending.")
	}
	return content, nil
}

func (p *claudeNativeProcess) exitMessage() string {
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitText == "" {
		return "Claude session ended."
	}
	return p.exitText
}

func (p *claudeNativeProcess) stop() error {
	if p == nil || channelClosed(p.done) {
		return nil
	}
	p.stopping.Store(true)
	_ = p.stdin.Close()
	if p.command.Process != nil {
		_ = p.command.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(claudeNativeStopTimeout)
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

func (m *claudeNativeManager) stopThread(projectID, threadID string) error {
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

func (m *claudeNativeManager) stopProject(projectID string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	processes := make([]*claudeNativeProcess, 0)
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

func (m *claudeNativeManager) removeThread(projectID, threadID string) error {
	if m == nil {
		return nil
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
	m.mu.Unlock()
	removeErr := os.RemoveAll(filepath.Join(
		m.dataDirectory,
		claudeNativeSessionDirectoryName,
		projectID,
		threadID,
	))
	return errors.Join(stopErr, removeErr)
}

func (m *claudeNativeManager) removeProject(projectID string) error {
	if m == nil {
		return nil
	}
	if err := validPiNativePathSegment(projectID); err != nil {
		return err
	}
	stopErr := m.stopProject(projectID)
	removeErr := os.RemoveAll(filepath.Join(
		m.dataDirectory,
		claudeNativeSessionDirectoryName,
		projectID,
	))
	return errors.Join(stopErr, removeErr)
}

func (m *claudeNativeManager) stopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	processes := make([]*claudeNativeProcess, 0, len(m.processes))
	for _, process := range m.processes {
		processes = append(processes, process)
	}
	m.mu.Unlock()
	for _, process := range processes {
		if err := process.stop(); err != nil {
			log.Printf("stop native Claude: project=%q thread=%q error=%v", process.key.ProjectID, process.key.ThreadID, err)
		}
	}
}

func normalizeClaudeNativeClientMessage(payload []byte) (claudeNativeClientMessage, claudeNativeClientAction, error) {
	var message claudeNativeClientMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Invalid native Claude message.")
	}
	switch message.Type {
	case "refresh":
		return claudeNativeClientMessage{}, claudeNativeClientRefresh, nil
	case "get_state":
		return claudeNativeClientMessage{}, claudeNativeClientState, nil
	case "abort":
		return claudeNativeClientMessage{}, claudeNativeClientAbort, nil
	case "new_session":
		return claudeNativeClientMessage{}, claudeNativeClientNewSession, nil
	case "reload", "restart", "set_model", "set_thinking_level":
		modelID := strings.TrimSpace(message.ModelID)
		level := strings.TrimSpace(message.Level)
		if message.Type == "set_model" && modelID == "" {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Choose a valid Claude model.")
		}
		if message.Type == "set_thinking_level" && level == "" {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Choose a valid Claude thinking level.")
		}
		if modelID != "" && !codingAgentChoiceExists(claudeModels, modelID) {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Choose a valid Claude model.")
		}
		if level != "" && !codingAgentChoiceExists(claudeThinkingLevels, level) {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Choose a valid Claude thinking level.")
		}
		return claudeNativeClientMessage{ModelID: modelID, Level: level}, claudeNativeClientRestart, nil
	case "prompt":
		if strings.TrimSpace(message.Message) == "" && len(message.Images) == 0 {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Enter a prompt or attach an image before sending.")
		}
		if strings.ContainsRune(message.Message, '\x00') {
			return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("The prompt contains an invalid character.")
		}
		return claudeNativeClientMessage{
			Message: message.Message,
			Images:  message.Images,
		}, claudeNativeClientPrompt, nil
	default:
		return claudeNativeClientMessage{}, claudeNativeClientPrompt, errors.New("Unknown native Claude message.")
	}
}

func claudeNativeStartErrorMessage(err error) string {
	switch {
	case err == nil:
		return "Could not start Claude."
	case strings.Contains(err.Error(), "not installed or not on PATH"):
		return "Claude Code is not installed or not on PATH."
	case errors.Is(err, errTerminalStopping):
		return "This thread is being removed."
	default:
		return "Could not start the native Claude session."
	}
}
