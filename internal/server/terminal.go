package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/ivan/dire-mux/internal/project"
)

type terminalHandler struct {
	projects                *project.Store
	tmuxPath                string
	tmuxSocket              string
	tmuxSocketMigration     *tmuxSocketMigration
	tmuxLogDirectory        string
	tmuxLogErr              error
	piExtensionPaths        []string
	piExtensionErr          error
	piModelMu               sync.Mutex
	piModelCache            map[string]piModelCapabilityCacheEntry
	piModelInflight         map[string]*piModelCapabilityInflight
	agentToken              string
	agentTokenErr           error
	nativePi                *piNativeManager
	nativeClaude            *claudeNativeManager
	claudePluginPath        string
	claudePluginErr         error
	claudeConfigPath        string
	claudeConfigErr         error
	claudePluginRootPath    string
	claudePluginRootErr     error
	claudeSandboxPluginPath string
	claudeSandboxPluginErr  error
	claudeGPTProfilePath    string
	claudeGPTProfileErr     error
	cliProxyAPIBaseURL      string
	cliProxyAPIKey          string
	cliProxyAPIErr          error
	cliProxyAPIHTTPClient   *http.Client
	envPath                 string
	sessionMu               sync.Mutex
	terminalStops           *terminalStopManager
	terminalMutations       *terminalMutationManager
	stoppingProjects        map[string]struct{}
	stoppingThreads         map[terminalThreadKey]struct{}
	viewCounter             atomic.Uint64
	activeViews             map[string]struct{}
	tmuxWatchMu             sync.Mutex
	tmuxWatches             map[string]*tmuxSessionWatch
	agentWatchMu            sync.Mutex
	agentWatches            map[codingAgentWatchKey]struct{}
	agentExits              map[codingAgentExitKey]tmuxPaneExitState
	agentExitLogs           map[codingAgentExitKey]struct{}
	agentExitSuppressed     map[codingAgentExitKey]tmuxPaneExitState
	agentExitMarkerMu       sync.Mutex
	agentExitDirectory      string
	threadStatusChanged     func(projectID, threadID string)
	budgetReached           func(projectID, threadID string) (bool, string, error)
	upgrader                websocket.Upgrader
}

const (
	tmuxSocketName            = "kiwi-code"
	legacyTmuxSocketName      = "dire-mux"
	tmuxSessionNamePrefix     = "kiwi-code-"
	legacyTmuxSessionPrefix   = "dire-mux-"
	tmuxLogDirectoryName      = "tmux-logs"
	terminalWriteTimeout      = 10 * time.Second
	terminalPongTimeout       = 45 * time.Second
	terminalPingInterval      = 15 * time.Second
	terminalAgentPollInterval = time.Second
	terminalViewCreationGrace = 2 * time.Second
	codingAgentPi             = "pi"
	codingAgentClaude         = "claude"
	codingAgentClaudeGPT      = "claude-gpt"
)

var threadSessionTools = [...]string{"terminal", "nvim", "lazygit", "pi"}

type clientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type tmuxWindow struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type tmuxWindowTarget struct {
	Index     int
	ID        string
	ServerPID string
	ProcessID string
}

type tmuxAgentPane struct {
	ID     string
	Agent  string
	Active bool
}

type tmuxViewSession struct {
	Name          string
	Attached      bool
	SourceSession string
}

type tmuxDetailedWindow struct {
	Target       tmuxWindowTarget
	Name         string
	Tool         string
	StartCommand string
}

type tmuxPaneExitState struct {
	ServerPID string
	Dead      bool
	Status    string
	Signal    string
	ExitedAt  string
	Found     bool
}

type codingAgentExitKey struct {
	ProjectID string
	ThreadID  string
	Agent     string
	PaneID    string
	ServerPID string
}

type codingAgentWatchKey struct {
	ServerPID string
	PaneID    string
}

type terminalThreadKey struct {
	ProjectID string
	ThreadID  string
}

type codingAgentExitMarker struct {
	ProjectID string `json:"projectId"`
	ThreadID  string `json:"threadId"`
	Agent     string `json:"agent"`
	PaneID    string `json:"paneId,omitempty"`
	ServerPID string `json:"serverPid,omitempty"`
	Status    string `json:"status,omitempty"`
	Signal    string `json:"signal,omitempty"`
	ExitedAt  string `json:"exitedAt,omitempty"`
}

type codingAgentPaneIncarnation struct {
	PaneID    string
	ServerPID string
}

var (
	errCodingAgentEnded = errors.New("coding agent ended")
	errTerminalStopping = errors.New("terminal sessions are stopping")
)

func newTerminalHandler(projects *project.Store) *terminalHandler {
	return newTerminalHandlerWithOriginPolicy(projects, originPolicy{})
}

func newTerminalHandlerWithOriginPolicy(projects *project.Store, policy originPolicy) *terminalHandler {
	return newTerminalHandlerWithOptions(projects, policy, tmuxSocketName)
}

func newTerminalHandlerWithOptions(projects *project.Store, policy originPolicy, tmuxSocket string) *terminalHandler {
	handler := newTerminalHandlerUnreconciledWithOptions(projects, policy, tmuxSocket)
	if err := handler.reconcileTerminalStops(); err != nil {
		log.Printf("reconcile durable terminal stops: error=%v", err)
	}
	return handler
}

func newTerminalHandlerUnreconciled(projects *project.Store) *terminalHandler {
	return newTerminalHandlerUnreconciledWithOriginPolicy(projects, originPolicy{})
}

func newTerminalHandlerUnreconciledWithOriginPolicy(projects *project.Store, policy originPolicy) *terminalHandler {
	return newTerminalHandlerUnreconciledWithOptions(projects, policy, tmuxSocketName)
}

func newTerminalHandlerUnreconciledWithOptions(projects *project.Store, policy originPolicy, tmuxSocket string) *terminalHandler {
	tmuxPath, _ := exec.LookPath("tmux")
	envPath, _ := exec.LookPath("env")
	tmuxLogDirectory, tmuxLogErr := prepareTmuxLogDirectory(projects.DataDirectory(), tmuxSocket)
	extensionPaths, extensionErr := materializePiExtensions(projects.DataDirectory())
	agentToken, agentTokenErr := loadOrCreateAgentToken(projects.DataDirectory())
	claudePluginPath, claudePluginErr := materializeClaudePlugin(projects.DataDirectory())
	claudeConfigPath, claudeConfigErr := defaultClaudeConfigDirectory()
	claudePluginRootPath, claudePluginRootErr := defaultClaudePluginDirectory(claudeConfigPath)
	claudeSandboxPluginPath, claudeSandboxPluginErr := discoverClaudeSandboxPluginPathFrom(
		claudeConfigPath,
		claudePluginRootPath,
		errors.Join(claudeConfigErr, claudePluginRootErr),
	)
	claudeGPTProfilePath, claudeGPTProfileErr := prepareClaudeGPTProfileDirectory(projects.DataDirectory())
	cliProxyAPIBaseURL, cliProxyAPIKey, cliProxyAPIErr := configuredCLIProxyAPI()
	return &terminalHandler{
		projects:                projects,
		tmuxPath:                tmuxPath,
		tmuxSocket:              tmuxSocket,
		tmuxLogDirectory:        tmuxLogDirectory,
		tmuxLogErr:              tmuxLogErr,
		piExtensionPaths:        extensionPaths,
		piExtensionErr:          extensionErr,
		agentToken:              agentToken,
		agentTokenErr:           agentTokenErr,
		nativePi:                newPiNativeManager(projects.DataDirectory(), extensionPaths, extensionErr, agentToken),
		nativeClaude:            newClaudeNativeManager(projects.DataDirectory(), claudePluginPath, claudePluginErr),
		claudePluginPath:        claudePluginPath,
		claudePluginErr:         claudePluginErr,
		claudeConfigPath:        claudeConfigPath,
		claudeConfigErr:         claudeConfigErr,
		claudePluginRootPath:    claudePluginRootPath,
		claudePluginRootErr:     claudePluginRootErr,
		claudeSandboxPluginPath: claudeSandboxPluginPath,
		claudeSandboxPluginErr:  claudeSandboxPluginErr,
		claudeGPTProfilePath:    claudeGPTProfilePath,
		claudeGPTProfileErr:     claudeGPTProfileErr,
		cliProxyAPIBaseURL:      cliProxyAPIBaseURL,
		cliProxyAPIKey:          cliProxyAPIKey,
		cliProxyAPIErr:          cliProxyAPIErr,
		envPath:                 envPath,
		terminalStops:           newTerminalStopManager(projects.DataDirectory()),
		terminalMutations:       newTerminalMutationManager(projects.DataDirectory()),
		agentExitDirectory: filepath.Join(
			projects.DataDirectory(),
			"coding-agent-exits",
		),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     policy.allows,
		},
	}
}

func (h *terminalHandler) startCodingAgent(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusForbidden, "Subagent coding agents are managed by their parent thread.")
		return
	}

	var input struct {
		Agent         string `json:"agent"`
		Model         string `json:"model"`
		ThinkingLevel string `json:"thinkingLevel"`
		Prompt        string `json:"prompt"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid coding agent details.")
		return
	}

	selection := strings.TrimSpace(input.Agent)
	if selection == "" {
		writeError(w, http.StatusBadRequest, "A coding agent is required.")
		return
	}
	agent := selection
	native := false
	switch selection {
	case "pi-native":
		agent = codingAgentPi
		native = true
	case "claude-native":
		agent = codingAgentClaude
		native = true
	}
	launchOptions, err := normalizeCodingAgentLaunchOptions(agent, input.Model, input.ThinkingLevel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	launchOptions.InitialPrompt = strings.TrimSpace(input.Prompt)
	if strings.ContainsRune(launchOptions.InitialPrompt, '\x00') {
		writeError(w, http.StatusBadRequest, "The initial prompt contains an invalid character.")
		return
	}

	threadEndpoint := threadEndpointURL(r, item.ID, thread.ID)
	if native {
		switch agent {
		case codingAgentPi:
			process, startErr := h.startPiNativeProcess(item, thread, threadEndpoint, launchOptions)
			if startErr != nil {
				writeError(w, http.StatusInternalServerError, piNativeStartErrorMessage(startErr))
				return
			}
			if launchOptions.InitialPrompt != "" {
				if _, promptErr := process.startPrompt(launchOptions.InitialPrompt); promptErr != nil {
					writeError(w, http.StatusInternalServerError, "Pi started, but the initial prompt could not be sent.")
					return
				}
			}
		case codingAgentClaude:
			process, startErr := h.startClaudeNativeProcess(item, thread, threadEndpoint, launchOptions)
			if startErr != nil {
				writeError(w, http.StatusInternalServerError, claudeNativeStartErrorMessage(startErr))
				return
			}
			if launchOptions.InitialPrompt != "" {
				if promptErr := process.sendPrompt(launchOptions.InitialPrompt, nil); promptErr != nil {
					writeError(w, http.StatusInternalServerError, "Claude started, but the initial prompt could not be sent.")
					return
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if h.tmuxPath == "" {
		writeError(w, http.StatusServiceUnavailable, "tmux is required for terminal coding agents. Install tmux and restart dire-mux.")
		return
	}
	sessionName, _, sessionCreated, err := h.ensureTmuxSessionWithCodingAgentOptions(
		item,
		thread,
		"pi",
		threadEndpoint,
		agent,
		launchOptions,
		false,
	)
	if err != nil {
		if errors.Is(err, errCodingAgentEnded) {
			writeError(w, http.StatusConflict, "The coding agent has ended and must be restarted explicitly.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not start the coding agent.")
		return
	}
	_, _, paneCreated, err := h.ensureCodingAgentPaneWithOptions(
		item,
		thread,
		agent,
		threadEndpoint,
		sessionName,
		launchOptions,
		false,
		nil,
	)
	if err != nil {
		if errors.Is(err, errCodingAgentEnded) {
			writeError(w, http.StatusConflict, "The coding agent has ended and must be restarted explicitly.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not start the coding agent.")
		return
	}
	if !sessionCreated && !paneCreated && launchOptions.InitialPrompt != "" {
		writeError(w, http.StatusConflict, "The coding agent is already running; the initial prompt was not sent.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *terminalHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "The terminal endpoint requires a WebSocket connection.")
		return
	}

	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}
	if thread.RollbackPending {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return
	}

	tool, err := normalizeTerminalTool(r.URL.Query().Get("tool"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tool == "pi" && thread.ParentThreadID != "" {
		writeError(w, http.StatusForbidden, "Subagents use the read-only Pi Native conversation.")
		return
	}
	codingAgent := codingAgentPi
	launchOptions := codingAgentLaunchOptions{}
	restartCodingAgent := false
	if tool == "pi" {
		codingAgent, err = normalizeCodingAgent(r.URL.Query().Get("agent"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		launchOptions, err = normalizeCodingAgentLaunchOptions(
			codingAgent,
			r.URL.Query().Get("model"),
			r.URL.Query().Get("thinking"),
		)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		launchOptions.InitialPrompt = strings.TrimSpace(r.URL.Query().Get("prompt"))
		if strings.ContainsRune(launchOptions.InitialPrompt, '\x00') {
			writeError(w, http.StatusBadRequest, "The initial prompt contains an invalid character.")
			return
		}
		restartCodingAgent = r.URL.Query().Get("restart") == "1"
	}
	processID := ""
	if tool == "process" {
		processID = strings.TrimSpace(r.URL.Query().Get("processId"))
		if processID == "" {
			writeError(w, http.StatusBadRequest, "A process ID is required.")
			return
		}
	}

	// Upgrade before creating or attaching tmux. A rejected origin or failed
	// handshake must not leave behind a session or temporary view.
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
	closeCodingAgentEnded := func() {
		_ = writer.Close(websocket.CloseNormalClosure, "Coding agent ended")
	}

	piThreadEndpoint := ""
	if tool == "pi" {
		piThreadEndpoint = threadEndpointURL(r, item.ID, thread.ID)
	}

	var sessionName, notice string
	var created bool
	var target tmuxWindowTarget
	agentPaneID := ""
	agentServerPID := ""
	launchedAgent := false
	if tool == "process" {
		if err := h.reconcileThreadTmuxState(item, thread); err != nil {
			closeWithError("Could not load the process terminal")
			return
		}
		sessionName = tmuxSessionName(item.ID, thread.ID, tool)
		_, processTarget, found, processErr := h.tmuxProcessWindow(sessionName, processID)
		if processErr != nil {
			closeWithError("Could not load the process terminal")
			return
		}
		if !found {
			closeWithError("Process not found")
			return
		}
		target = processTarget
	} else {
		sessionName, notice, created, err = h.ensureTmuxSessionWithCodingAgentOptions(
			item,
			thread,
			tool,
			piThreadEndpoint,
			codingAgent,
			launchOptions,
			restartCodingAgent,
		)
		if err != nil {
			if errors.Is(err, errCodingAgentEnded) || (tool == "pi" && h.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgent)) {
				closeCodingAgentEnded()
				return
			}
			closeWithError("Could not create the terminal session")
			return
		}
		if tool == "pi" {
			var agentNotice string
			var agentCreated bool
			agentPaneID, agentNotice, agentCreated, err = h.ensureCodingAgentPaneWithOptions(
				item,
				thread,
				codingAgent,
				piThreadEndpoint,
				sessionName,
				launchOptions,
				restartCodingAgent,
				&agentServerPID,
			)
			if err != nil {
				if errors.Is(err, errCodingAgentEnded) || h.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgent) {
					closeCodingAgentEnded()
					return
				}
				closeWithError("Could not start the coding agent")
				return
			}
			launchedAgent = created || agentCreated
			if agentCreated {
				created = true
				notice = agentNotice
			}
			state, stateErr := h.tmuxPaneExitState(agentPaneID)
			if stateErr != nil || !state.Found || state.ServerPID == "" {
				if h.hasLogicalCodingAgentExit(item.ID, thread.ID, codingAgent) {
					closeCodingAgentEnded()
					return
				}
				closeWithError("Could not inspect the coding agent")
				return
			}
			if state.ServerPID != agentServerPID {
				closeWithError("Coding agent incarnation changed during setup")
				return
			}
			if state.Dead {
				h.handleCodingAgentExit(item.ID, thread.ID, sessionName, "", agentPaneID, codingAgent, state)
				closeCodingAgentEnded()
				return
			}
			if !restartCodingAgent && launchedAgent {
				marked, markerErr := h.stopCodingAgentPaneIfExitMarked(item.ID, thread.ID, codingAgent, agentPaneID, agentServerPID)
				if markerErr != nil {
					closeWithError("Could not verify the coding agent launch")
					return
				}
				if marked {
					closeCodingAgentEnded()
					return
				}
			}
		}
	}
	closeSetupFailure := func(message string) {
		if agentPaneID != "" {
			state, stateErr := h.tmuxPaneExitState(agentPaneID)
			if stateErr == nil && state.Found && state.ServerPID == agentServerPID && state.Dead {
				h.handleCodingAgentExit(item.ID, thread.ID, sessionName, target.ID, agentPaneID, codingAgent, state)
				closeCodingAgentEnded()
				return
			}
			if h.hasCodingAgentExit(item.ID, thread.ID, codingAgent, agentPaneID, agentServerPID) {
				closeCodingAgentEnded()
				return
			}
		}
		closeWithError(message)
	}

	// Shell clients attach to their standalone session so the shell tab API can
	// select its current window. Each shared window gets a temporary one-window
	// view. This lets several browser panes view different windows without
	// changing one another's current tmux window.
	attachSessionName := sessionName
	viewSessionName := ""
	if created {
		h.wakeThreadTmuxWatchers(item.ID, thread.ID)
		h.notifyThreadStatusChanged(item.ID, thread.ID)
	}
	if tool == "process" {
		defer h.notifyThreadStatusChanged(item.ID, thread.ID)
	}
	if tool != "terminal" {
		if target.ID == "" {
			var found bool
			target, found, err = h.tmuxToolWindow(sessionName, tool)
			if err != nil || !found {
				closeSetupFailure("Could not find the terminal window")
				return
			}
		}
		viewSessionName, err = h.createTmuxViewSession(item, thread, sessionName, target)
		if err != nil {
			closeSetupFailure("Could not create the terminal view")
			return
		}
		attachSessionName = viewSessionName
		defer h.closeTmuxViewSession(viewSessionName)
	}
	if err := h.configureTmuxClipboard(); err != nil {
		// Clipboard integration should not prevent the terminal from opening on
		// older tmux versions, but keep the failure visible to the server owner.
		log.Printf("configure tmux clipboard: %v", err)
	}

	cols := boundedDimension(r.URL.Query().Get("cols"), 80)
	rows := boundedDimension(r.URL.Query().Get("rows"), 24)
	cmd := h.tmuxCommand(
		"attach-session",
		"-c", thread.Cwd,
		"-t", exactTmuxSessionTarget(attachSessionName),
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		closeSetupFailure("Could not attach to the terminal session")
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	if created && notice != "" {
		if err := writer.Write(websocket.BinaryMessage, []byte(notice)); err != nil {
			return
		}
	}
	bridge := startPTYWebSocketBridge(connection, writer, ptmx)
	defer bridge.Stop()
	var agentPoll <-chan time.Time
	var agentPollTicker *time.Ticker
	if agentPaneID != "" {
		agentPollTicker = time.NewTicker(terminalAgentPollInterval)
		agentPoll = agentPollTicker.C
		defer agentPollTicker.Stop()
	}
	for {
		select {
		case <-bridge.terminalDone:
			reason := "Terminal session ended"
			if agentPaneID != "" {
				state, stateErr := h.tmuxPaneExitState(agentPaneID)
				if stateErr == nil && state.Found && state.ServerPID == agentServerPID && state.Dead {
					h.handleCodingAgentExit(item.ID, thread.ID, sessionName, target.ID, agentPaneID, codingAgent, state)
					reason = "Coding agent ended"
				} else if h.hasCodingAgentExit(item.ID, thread.ID, codingAgent, agentPaneID, agentServerPID) {
					reason = "Coding agent ended"
				}
			}
			_ = writer.Close(websocket.CloseNormalClosure, reason)
			return
		case <-bridge.peer.done:
			return
		case <-bridge.peer.ping.C:
			if err := bridge.peer.WritePing(); err != nil {
				return
			}
		case <-agentPoll:
			state, stateErr := h.tmuxPaneExitState(agentPaneID)
			if stateErr != nil || !state.Found || state.ServerPID != agentServerPID {
				reason := "Terminal session ended"
				if h.hasCodingAgentExit(item.ID, thread.ID, codingAgent, agentPaneID, agentServerPID) {
					reason = "Coding agent ended"
				}
				_ = writer.Close(websocket.CloseNormalClosure, reason)
				return
			}
			if state.Dead {
				h.handleCodingAgentExit(item.ID, thread.ID, sessionName, target.ID, agentPaneID, codingAgent, state)
				closeCodingAgentEnded()
				return
			}
		case message := <-bridge.peer.messages:
			if err := bridge.Handle(message); err != nil {
				return
			}
		}
	}
}

func (h *terminalHandler) listShellWindows(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}

	windows, err := h.shellWindows(item, thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load shell tabs.")
		return
	}
	h.wakeThreadTmuxWatchers(item.ID, thread.ID)
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusOK, windows)
}

func (h *terminalHandler) createShellWindow(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}

	windows, err := h.newShellWindow(item, thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a shell tab.")
		return
	}
	h.wakeThreadTmuxWatchers(item.ID, thread.ID)
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusCreated, windows)
}

func (h *terminalHandler) selectShellWindow(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}

	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeError(w, http.StatusBadRequest, "Invalid shell tab.")
		return
	}

	windows, err := h.activateShellWindow(item, thread, index)
	if err != nil {
		writeError(w, http.StatusNotFound, "Shell tab not found.")
		return
	}
	h.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusOK, windows)
}

func (h *terminalHandler) tmuxThread(w http.ResponseWriter, r *http.Request) (project.Project, project.Thread, bool) {
	item, thread, err := h.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return project.Project{}, project.Thread{}, false
	}
	if thread.RollbackPending {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return project.Project{}, project.Thread{}, false
	}
	if h.tmuxPath == "" {
		writeError(w, http.StatusServiceUnavailable, "tmux is required for persistent terminal sessions. Install tmux and restart dire-mux.")
		return project.Project{}, project.Thread{}, false
	}
	return item, thread, true
}

func (h *terminalHandler) ensureTmuxSession(item project.Project, thread project.Thread, tool string) (sessionName, notice string, created bool, err error) {
	return h.ensureTmuxSessionWithEndpoint(item, thread, tool, "")
}

func (h *terminalHandler) ensureTmuxSessionWithEndpoint(item project.Project, thread project.Thread, tool, threadEndpoint string) (sessionName, notice string, created bool, err error) {
	return h.ensureTmuxSessionWithCodingAgent(item, thread, tool, threadEndpoint, codingAgentPi, false)
}

func (h *terminalHandler) ensureTmuxSessionWithCodingAgent(
	item project.Project,
	thread project.Thread,
	tool string,
	threadEndpoint string,
	initialAgent string,
	restartCodingAgent bool,
) (sessionName, notice string, created bool, err error) {
	return h.ensureTmuxSessionWithCodingAgentOptions(
		item,
		thread,
		tool,
		threadEndpoint,
		initialAgent,
		codingAgentLaunchOptions{},
		restartCodingAgent,
	)
}

func (h *terminalHandler) ensureTmuxSessionWithCodingAgentOptions(
	item project.Project,
	thread project.Thread,
	tool string,
	threadEndpoint string,
	initialAgent string,
	launchOptions codingAgentLaunchOptions,
	restartCodingAgent bool,
) (sessionName, notice string, created bool, err error) {
	tool, err = normalizeTerminalTool(tool)
	if err != nil {
		return "", "", false, err
	}
	if tool == "pi" {
		initialAgent, err = normalizeCodingAgent(initialAgent)
		if err != nil {
			return "", "", false, err
		}
	}
	sessionName = tmuxSessionName(item.ID, thread.ID, tool)

	// Starting a session and adding its fixed windows must be atomic within the
	// server. Otherwise two browser connections can create duplicate named
	// windows at the same time.
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return "", "", false, mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return "", "", false, err
	}
	defer func() {
		if fenceErr := h.finishTerminalThreadMutationLocked(item, thread); fenceErr != nil {
			sessionName = ""
			notice = ""
			created = false
			err = errors.Join(err, fenceErr)
		}
	}()
	if tool == "terminal" {
		if err := h.adoptPreviousTmuxSessionLocked(item.ID, thread.ID, tool, sessionName); err != nil {
			return "", "", false, err
		}
		if err := h.adoptLegacyTerminalSessionLocked(item, thread, sessionName); err != nil {
			return "", "", false, err
		}
	} else {
		if err := h.reconcileThreadTmuxStateLocked(item, thread); err != nil {
			return "", "", false, err
		}
	}
	if tool == "pi" && restartCodingAgent {
		if err := h.prepareCodingAgentRestartLocked(item.ID, thread.ID, sessionName, initialAgent); err != nil {
			return "", "", false, err
		}
	}

	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return "", "", false, err
	}
	if tool == "terminal" {
		if exists {
			if err := h.ensurePreviousTmuxCompatibilityAliasLocked(item.ID, thread.ID, tool, sessionName); err != nil {
				return "", "", false, err
			}
			return sessionName, "", false, nil
		}
		command, args, notice, err := commandFor(tool)
		if err != nil {
			return "", "", false, err
		}
		if _, err := h.createTmuxSession(sessionName, thread.Cwd, "shell", command, args); err != nil {
			// A newly restarted server may briefly overlap the old handler and
			// lose the race to create the persistent session.
			if exists, checkErr := h.tmuxSessionExists(sessionName); checkErr == nil && exists {
				if aliasErr := h.ensurePreviousTmuxCompatibilityAliasLocked(item.ID, thread.ID, tool, sessionName); aliasErr != nil {
					return "", "", false, aliasErr
				}
				return sessionName, "", false, nil
			}
			return "", "", false, err
		}
		if err := h.ensurePreviousTmuxCompatibilityAliasLocked(item.ID, thread.ID, tool, sessionName); err != nil {
			return "", "", false, err
		}
		return sessionName, notice, true, nil
	}

	if tool == "process" {
		return "", "", false, errors.New("process windows must be created through the process API")
	}
	if exists {
		if err := h.removeLegacyProcessWindows(sessionName); err != nil {
			return "", "", false, err
		}
		exists, err = h.tmuxSessionExists(sessionName)
		if err != nil {
			return "", "", false, err
		}
	}
	if !exists {
		if tool == "pi" && !restartCodingAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, initialAgent) {
			return sessionName, "", false, errCodingAgentEnded
		}
		var command string
		var args []string
		var commandErr error
		if tool == "pi" {
			command, args, notice, commandErr = h.commandForCodingAgentPaneWithOptions(
				item,
				thread,
				initialAgent,
				threadEndpoint,
				sessionName,
				launchOptions,
			)
		} else {
			command, args, notice, commandErr = h.commandForTmuxWindow(item, thread, tool, threadEndpoint, sessionName)
		}
		if commandErr != nil {
			return "", "", false, commandErr
		}
		launchCommand, launchArgs := command, args
		if tool == codingAgentPi {
			launchCommand, launchArgs = h.codingAgentLaunchCommand(initialAgent, command, args)
		}
		target, createErr := h.createTmuxSession(sessionName, thread.Cwd, tool, launchCommand, launchArgs)
		if createErr == nil {
			if configureErr := h.configureSharedToolWindow(sessionName, target, tool); configureErr != nil {
				_ = h.killTmuxSessionIncarnation(sessionName, target.ServerPID)
				return "", "", false, configureErr
			}
			if tool == codingAgentPi {
				if startErr := h.startCodingAgentWindow(item, thread, sessionName, target, initialAgent); startErr != nil {
					if !errors.Is(startErr, errCodingAgentEnded) {
						_ = h.killTmuxSessionIncarnation(sessionName, target.ServerPID)
					}
					return sessionName, "", false, startErr
				}
				if restartCodingAgent {
					if confirmErr := h.confirmStartedCodingAgentRestart(item.ID, thread.ID, initialAgent, target.ID); confirmErr != nil {
						_ = h.killTmuxSessionIncarnation(sessionName, target.ServerPID)
						return sessionName, "", false, confirmErr
					}
				}
			}
			if aliasErr := h.ensurePreviousTmuxCompatibilityAliasLocked(item.ID, thread.ID, tool, sessionName); aliasErr != nil {
				return "", "", false, aliasErr
			}
			return sessionName, notice, true, nil
		}
		// A newly restarted server may briefly overlap the old handler. If it
		// won the race to create the shared session, ensure our named window in
		// that session instead.
		if sessionExists, checkErr := h.tmuxSessionExists(sessionName); checkErr != nil || !sessionExists {
			return "", "", false, createErr
		}
	}

	_, notice, created, err = h.ensureSharedTmuxWindow(
		item,
		thread,
		tool,
		threadEndpoint,
		sessionName,
		initialAgent,
		launchOptions,
		restartCodingAgent,
	)
	if err != nil {
		return "", "", false, err
	}
	if err := h.ensurePreviousTmuxCompatibilityAliasLocked(item.ID, thread.ID, tool, sessionName); err != nil {
		return "", "", false, err
	}
	return sessionName, notice, created, nil
}

func (h *terminalHandler) ensureSharedTmuxWindow(
	item project.Project,
	thread project.Thread,
	tool string,
	threadEndpoint string,
	sessionName string,
	initialAgent string,
	launchOptions codingAgentLaunchOptions,
	allowExitedAgent bool,
) (tmuxWindowTarget, string, bool, error) {
	target, found, err := h.tmuxToolWindow(sessionName, tool)
	if err != nil {
		return tmuxWindowTarget{}, "", false, err
	}
	if found {
		if err := h.configureSharedToolWindow(sessionName, target, tool); err != nil {
			return tmuxWindowTarget{}, "", false, err
		}
		return target, "", false, nil
	}

	if tool == "pi" && !allowExitedAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, initialAgent) {
		return tmuxWindowTarget{}, "", false, errCodingAgentEnded
	}
	var command string
	var args []string
	var notice string
	if tool == "pi" {
		command, args, notice, err = h.commandForCodingAgentPaneWithOptions(
			item,
			thread,
			initialAgent,
			threadEndpoint,
			sessionName,
			launchOptions,
		)
	} else {
		command, args, notice, err = h.commandForTmuxWindow(item, thread, tool, threadEndpoint, sessionName)
	}
	if err != nil {
		return tmuxWindowTarget{}, "", false, err
	}
	launchCommand, launchArgs := command, args
	if tool == codingAgentPi {
		launchCommand, launchArgs = h.codingAgentLaunchCommand(initialAgent, command, args)
	}
	target, err = h.createTmuxWindow(thread.Cwd, sessionName, tool, launchCommand, launchArgs, false)
	if err != nil {
		return tmuxWindowTarget{}, "", false, err
	}
	if err := h.configureSharedToolWindow(sessionName, target, tool); err != nil {
		_ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID)
		return tmuxWindowTarget{}, "", false, err
	}
	if tool == codingAgentPi {
		if err := h.startCodingAgentWindow(item, thread, sessionName, target, initialAgent); err != nil {
			if !errors.Is(err, errCodingAgentEnded) {
				_ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID)
			}
			return tmuxWindowTarget{}, "", false, err
		}
		if allowExitedAgent {
			if err := h.confirmStartedCodingAgentRestart(item.ID, thread.ID, initialAgent, target.ID); err != nil {
				_ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID)
				return tmuxWindowTarget{}, "", false, err
			}
		}
	}
	return target, notice, true, nil
}

func (h *terminalHandler) confirmStartedCodingAgentRestart(projectID, threadID, agent, windowID string) error {
	panes, err := h.tmuxAgentPanes(windowID)
	if err != nil {
		return err
	}
	for _, pane := range panes {
		if pane.Agent != agent {
			continue
		}
		state, err := h.tmuxPaneExitState(pane.ID)
		if err != nil || !state.Found || state.ServerPID == "" {
			if err != nil {
				return err
			}
			return errors.New("replacement coding agent pane disappeared before confirmation")
		}
		if state.Dead {
			h.handleCodingAgentExit(projectID, threadID, tmuxSessionName(projectID, threadID, "pi"), windowID, pane.ID, agent, state)
			return errCodingAgentEnded
		}
		return h.confirmCodingAgentRestart(projectID, threadID, agent, pane.ID, state.ServerPID, true)
	}
	return errors.New("replacement coding agent pane was not found")
}

// reconcileThreadTmuxStateLocked adopts live windows created by older Dire Mux
// layouts before the normal existence check is allowed to create replacements.
// The caller must hold sessionMu and the thread mutation lease so browser
// requests in this server and overlapping backend processes cannot race
// migration with fixed-window creation.
func (h *terminalHandler) reconcileThreadTmuxStateLocked(item project.Project, thread project.Thread) (err error) {
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, h.finishTerminalThreadMutationLocked(item, thread))
	}()
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "process")
	if err := h.adoptPreviousTmuxSessionLocked(item.ID, thread.ID, "process", canonicalSession); err != nil {
		return err
	}
	if err := h.adoptLegacyToolSessionsLocked(item, thread, canonicalSession); err != nil {
		return err
	}
	if err := h.reconcileStaleTmuxViewsLocked(item.ID, thread.ID, canonicalSession); err != nil {
		return err
	}
	return h.prepareCanonicalCodingAgentWindowsLocked(item.ID, thread.ID, canonicalSession)
}

func (h *terminalHandler) reconcileThreadTmuxState(item project.Project, thread project.Thread) (err error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	return h.reconcileThreadTmuxStateLocked(item, thread)
}

// lockTerminalMutationLocked must be called with sessionMu held. Keeping this
// order consistent prevents an overlapping backend from inspecting or
// creating the same thread's tmux state between our stop precheck and fence.
func (h *terminalHandler) lockTerminalMutationLocked(projectID, threadID string) (*terminalMutationLease, error) {
	manager := h.terminalMutations
	if manager == nil {
		if h.projects == nil {
			return nil, errors.New("terminal mutation manager is unavailable")
		}
		manager = newTerminalMutationManager(h.projects.DataDirectory())
		h.terminalMutations = manager
	}
	return manager.LockThread(projectID, threadID)
}

func (h *terminalHandler) ensureTerminalThreadActiveLocked(projectID, threadID string) error {
	stopped, err := h.terminalThreadStopStateLocked(projectID, threadID)
	if err != nil {
		return errors.Join(errTerminalStopping, err)
	}
	if stopped {
		return errTerminalStopping
	}
	return nil
}

func (h *terminalHandler) terminalThreadStopStateLocked(projectID, threadID string) (bool, error) {
	if _, stopping := h.stoppingProjects[projectID]; stopping {
		return true, nil
	}
	if _, stopping := h.stoppingThreads[terminalThreadKey{ProjectID: projectID, ThreadID: threadID}]; stopping {
		return true, nil
	}
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return false, nil
	}
	stopped, err := manager.threadStopped(projectID, threadID)
	if err != nil {
		return false, err
	}
	return stopped, nil
}

func (h *terminalHandler) durableTerminalStopManager() *terminalStopManager {
	if h.terminalStops != nil {
		return h.terminalStops
	}
	if h.projects == nil {
		return nil
	}
	return newTerminalStopManager(h.projects.DataDirectory())
}

// finishTerminalThreadMutationLocked is the cross-handler half of the
// mutation fence. A durable marker blocks the request immediately, but its
// sessions are only removed once Store proves that marker's own scope was
// committed. Pending deletion must remain non-destructive so Store rollback
// preserves the exact user processes that existed before DELETE.
func (h *terminalHandler) finishTerminalThreadMutationLocked(item project.Project, thread project.Thread) error {
	localStopped := false
	if _, stopping := h.stoppingProjects[item.ID]; stopping {
		localStopped = true
	}
	if _, stopping := h.stoppingThreads[terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}]; stopping {
		localStopped = true
	}
	manager := h.durableTerminalStopManager()
	if manager == nil {
		if localStopped {
			return errTerminalStopping
		}
		return nil
	}

	projectMarker, projectFound, projectErr := manager.readProject(item.ID)
	threadMarker, threadFound, threadErr := manager.readThread(item.ID, thread.ID)
	if projectErr != nil || threadErr != nil {
		// Unknown marker state is not permission to destroy a process. Preserve
		// everything while still failing the mutation closed.
		return errors.Join(errTerminalStopping, projectErr, threadErr)
	}
	if !localStopped && !projectFound && !threadFound {
		return nil
	}

	committedSessions := make(map[string]struct{})
	for _, observed := range []struct {
		marker terminalStopMarker
		found  bool
		ref    terminalStopMarkerRef
	}{
		{
			marker: projectMarker,
			found:  projectFound,
			ref: terminalStopMarkerRef{
				Scope:     terminalStopScopeProject,
				ProjectID: item.ID,
			},
		},
		{
			marker: threadMarker,
			found:  threadFound,
			ref: terminalStopMarkerRef{
				Scope:     terminalStopScopeThread,
				ProjectID: item.ID,
				ThreadID:  thread.ID,
			},
		},
	} {
		if !observed.found {
			continue
		}
		if !observed.marker.Committed {
			exists, err := h.terminalStopResourceExists(observed.ref)
			if err != nil {
				return errors.Join(errTerminalStopping, err)
			}
			if exists {
				continue
			}
		}
		for _, sessionName := range observed.marker.SessionNames {
			committedSessions[sessionName] = struct{}{}
		}
	}

	var cleanupErr error
	if len(committedSessions) > 0 && h.tmuxPath != "" {
		cleanupErr = h.stopNamedTmuxSessionsAndViews(committedSessions)
	}
	return errors.Join(errTerminalStopping, cleanupErr)
}

func (h *terminalHandler) terminalStopResourceExists(ref terminalStopMarkerRef) (bool, error) {
	if h.projects == nil {
		return false, errors.New("project Store is unavailable")
	}
	switch ref.Scope {
	case terminalStopScopeProject:
		exists, err := h.projects.PersistedResourceExists(ref.ProjectID, "")
		if err != nil {
			return false, fmt.Errorf("inspect persisted project terminal stop Store state: %w", err)
		}
		return exists, nil
	case terminalStopScopeThread:
		exists, err := h.projects.PersistedResourceExists(ref.ProjectID, ref.ThreadID)
		if err != nil {
			return false, fmt.Errorf("inspect persisted thread terminal stop Store state: %w", err)
		}
		return exists, nil
	default:
		return false, errors.New("terminal stop marker ref has an invalid scope")
	}
}

func (h *terminalHandler) markThreadStoppingLocked(projectID, threadID string) error {
	if err := h.ensureTerminalThreadActiveLocked(projectID, threadID); err != nil {
		return err
	}
	if h.stoppingThreads == nil {
		h.stoppingThreads = make(map[terminalThreadKey]struct{})
	}
	h.stoppingThreads[terminalThreadKey{ProjectID: projectID, ThreadID: threadID}] = struct{}{}
	return nil
}

func (h *terminalHandler) unmarkThreadStoppingLocked(projectID, threadID string) {
	delete(h.stoppingThreads, terminalThreadKey{ProjectID: projectID, ThreadID: threadID})
}

func (h *terminalHandler) prepareCanonicalCodingAgentWindowsLocked(projectID, threadID, canonicalSession string) error {
	exists, err := h.tmuxSessionExists(canonicalSession)
	if err != nil || !exists {
		return err
	}
	windows, err := h.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		return err
	}
	for _, window := range windows {
		if fixedTmuxTool(window) != "pi" {
			continue
		}
		if err := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, canonicalSession, window.Target.ID); err != nil {
			return err
		}
	}
	return nil
}

// adoptPreviousTmuxSessionLocked exposes a session created under the previous
// canonical name through the current name without moving or restarting any
// window. Keeping the previous session as a grouped compatibility alias is
// required because processes that are already running may still target it via
// DIRE_MUX_TMUX_SESSION.
func (h *terminalHandler) adoptPreviousTmuxSessionLocked(projectID, threadID, tool, canonicalSession string) error {
	previousSession := previousTmuxSessionName(projectID, threadID, tool)
	if previousSession == canonicalSession {
		return nil
	}
	previousExists, err := h.tmuxSessionExists(previousSession)
	if err != nil || !previousExists {
		return err
	}
	previousWindows, err := h.tmuxDetailedWindows(previousSession)
	if err != nil {
		return err
	}
	canonicalExists, err := h.tmuxSessionExists(canonicalSession)
	if err != nil {
		return err
	}
	if !canonicalExists {
		output, groupErr := h.tmuxCommand(
			"new-session", "-d",
			"-s", canonicalSession,
			"-t", exactTmuxSessionTarget(previousSession),
		).CombinedOutput()
		if groupErr != nil {
			if exists, checkErr := h.tmuxSessionExists(canonicalSession); checkErr != nil || !exists {
				return tmuxCommandError("adopt previous canonical tmux session", output, groupErr)
			}
		}
	}

	canonicalWindows, err := h.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		return err
	}
	canonicalWindowIDs := make(map[string]struct{}, len(canonicalWindows))
	canonicalTools := make(map[string]string)
	for _, window := range canonicalWindows {
		canonicalWindowIDs[window.Target.ID] = struct{}{}
		if fixedTool := fixedTmuxTool(window); fixedTool != "" {
			canonicalTools[fixedTool] = window.Target.ID
		}
	}

	for _, window := range previousWindows {
		fixedTool := fixedTmuxTool(window)
		if _, linked := canonicalWindowIDs[window.Target.ID]; !linked {
			if conflictingWindow := canonicalTools[fixedTool]; fixedTool != "" && conflictingWindow != "" && conflictingWindow != window.Target.ID {
				log.Printf("preserve previous canonical tmux window: previous_session=%q canonical_session=%q tool=%q window=%q canonical_window=%q reason=conflicting canonical window", previousSession, canonicalSession, fixedTool, window.Target.ID, conflictingWindow)
				if fixedTool == "pi" {
					if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, previousSession, window.Target.ID); prepareErr != nil {
						return prepareErr
					}
				}
				continue
			}
			destination, destinationErr := h.nextTmuxWindowIndex(canonicalSession)
			if destinationErr != nil {
				return destinationErr
			}
			output, linkErr := h.tmuxCommand(
				"link-window",
				"-s", window.Target.ID,
				"-t", exactTmuxWindowTarget(canonicalSession, destination),
			).CombinedOutput()
			if linkErr != nil {
				linkedAfterRace := false
				if current, checkErr := h.tmuxDetailedWindows(canonicalSession); checkErr == nil {
					for _, existing := range current {
						if existing.Target.ID == window.Target.ID {
							linkedAfterRace = true
							break
						}
					}
				}
				if !linkedAfterRace {
					return tmuxCommandError("link previous canonical tmux window", output, linkErr)
				}
			}
			canonicalWindowIDs[window.Target.ID] = struct{}{}
			if fixedTool != "" {
				canonicalTools[fixedTool] = window.Target.ID
			}
		}
		if fixedTool == "" {
			continue
		}
		if err := h.configureSharedToolWindow(canonicalSession, window.Target, fixedTool); err != nil {
			return err
		}
		if fixedTool == "pi" {
			if err := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, canonicalSession, window.Target.ID); err != nil {
				return err
			}
		}
	}
	if output, optionErr := h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "status", "off").CombinedOutput(); optionErr != nil {
		return tmuxCommandError("configure adopted previous tmux session", output, optionErr)
	}
	return h.rewriteAdoptedTmuxViewSourcesLocked(previousSession, canonicalSession, previousWindows)
}

func (h *terminalHandler) ensurePreviousTmuxCompatibilityAliasLocked(projectID, threadID, tool, canonicalSession string) error {
	if h.tmuxSocketMigration == nil || !h.tmuxSocketMigration.active() {
		return nil
	}
	previousSession := previousTmuxSessionName(projectID, threadID, tool)
	previousExists, err := h.tmuxSessionExists(previousSession)
	if err != nil {
		return err
	}
	if previousExists {
		return h.adoptPreviousTmuxSessionLocked(projectID, threadID, tool, canonicalSession)
	}
	output, err := h.tmuxCommand(
		"new-session", "-d",
		"-s", previousSession,
		"-t", exactTmuxSessionTarget(canonicalSession),
	).CombinedOutput()
	if err != nil {
		if exists, checkErr := h.tmuxSessionExists(previousSession); checkErr != nil || !exists {
			return tmuxCommandError("create previous tmux compatibility session", output, err)
		}
	}
	if output, optionErr := h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(previousSession), "status", "off").CombinedOutput(); optionErr != nil {
		return tmuxCommandError("configure previous tmux compatibility session", output, optionErr)
	}
	return h.adoptPreviousTmuxSessionLocked(projectID, threadID, tool, canonicalSession)
}

func (h *terminalHandler) adoptLegacyToolSessionsLocked(item project.Project, thread project.Thread, canonicalSession string) error {
	for _, tool := range []string{"nvim", "lazygit", "pi"} {
		legacyNames := []string{legacyThreadTmuxSessionName(item.ID, thread.ID, tool)}
		if len(item.Threads) > 0 && item.Threads[0].ID == thread.ID {
			legacyNames = append(legacyNames, legacyProjectTmuxSessionName(item.ID, tool))
		}
		for _, legacySession := range legacyNames {
			if legacySession == canonicalSession {
				continue
			}
			exists, err := h.tmuxSessionExists(legacySession)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			if err := h.adoptLegacyToolSessionLocked(item.ID, thread.ID, legacySession, canonicalSession, tool); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *terminalHandler) adoptLegacyTerminalSessionLocked(item project.Project, thread project.Thread, canonicalSession string) error {
	if len(item.Threads) == 0 || item.Threads[0].ID != thread.ID {
		return nil
	}
	legacySession := legacyProjectTmuxSessionName(item.ID, "terminal")
	if legacySession == canonicalSession {
		return nil
	}
	legacyExists, err := h.tmuxSessionExists(legacySession)
	if err != nil || !legacyExists {
		return err
	}
	legacyWindows, err := h.tmuxDetailedWindows(legacySession)
	if err != nil {
		return err
	}
	canonicalExists, err := h.tmuxSessionExists(canonicalSession)
	if err != nil {
		return err
	}
	if !canonicalExists {
		output, renameErr := h.tmuxCommand("rename-session", "-t", exactTmuxSessionTarget(legacySession), canonicalSession).CombinedOutput()
		if renameErr == nil {
			if output, optionErr := h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "status", "off").CombinedOutput(); optionErr != nil {
				return tmuxCommandError("configure adopted terminal session", output, optionErr)
			}
			return h.rewriteAdoptedTmuxViewSourcesLocked(legacySession, canonicalSession, legacyWindows)
		}
		if exists, checkErr := h.tmuxSessionExists(canonicalSession); checkErr != nil || !exists {
			return tmuxCommandError("adopt legacy terminal session", output, renameErr)
		}
	}

	var preservedMigrationErr error
	for _, window := range legacyWindows {
		canonicalWindows, listErr := h.tmuxDetailedWindows(canonicalSession)
		if listErr != nil {
			return listErr
		}
		alreadyLinked := false
		for _, existing := range canonicalWindows {
			if existing.Target.ID == window.Target.ID {
				alreadyLinked = true
				break
			}
		}
		sourceTarget := exactTmuxWindowTarget(legacySession, window.Target.Index)
		if alreadyLinked {
			output, unlinkErr := h.tmuxCommand("unlink-window", "-t", sourceTarget).CombinedOutput()
			if unlinkErr != nil {
				log.Printf("preserve legacy terminal link: legacy_session=%q canonical_session=%q window=%q error=%v output=%q", legacySession, canonicalSession, window.Target.ID, unlinkErr, strings.TrimSpace(string(output)))
				if preservedMigrationErr == nil {
					preservedMigrationErr = tmuxCommandError("unlink adopted legacy terminal window", output, unlinkErr)
				}
			}
			continue
		}
		destination, destinationErr := h.nextTmuxWindowIndex(canonicalSession)
		if destinationErr != nil {
			return destinationErr
		}
		output, moveErr := h.tmuxCommand(
			"move-window",
			"-s", sourceTarget,
			"-t", exactTmuxWindowTarget(canonicalSession, destination),
		).CombinedOutput()
		if moveErr != nil {
			adopted := false
			if current, checkErr := h.tmuxDetailedWindows(canonicalSession); checkErr == nil {
				for _, existing := range current {
					if existing.Target.ID == window.Target.ID {
						adopted = true
						break
					}
				}
			}
			if !adopted {
				log.Printf("preserve conflicting legacy terminal window: legacy_session=%q canonical_session=%q window=%q error=%v output=%q", legacySession, canonicalSession, window.Target.ID, moveErr, strings.TrimSpace(string(output)))
				if preservedMigrationErr == nil {
					preservedMigrationErr = tmuxCommandError("move legacy terminal window", output, moveErr)
				}
			}
		}
	}
	if output, optionErr := h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "status", "off").CombinedOutput(); optionErr != nil {
		return tmuxCommandError("configure merged terminal session", output, optionErr)
	}
	if err := h.rewriteAdoptedTmuxViewSourcesLocked(legacySession, canonicalSession, legacyWindows); err != nil {
		return err
	}
	return preservedMigrationErr
}

func (h *terminalHandler) adoptLegacyToolSessionLocked(projectID, threadID, legacySession, canonicalSession, tool string) error {
	legacyWindows, err := h.tmuxDetailedWindows(legacySession)
	if err != nil {
		return err
	}
	canonicalExists, err := h.tmuxSessionExists(canonicalSession)
	if err != nil {
		return err
	}
	if !canonicalExists {
		output, renameErr := h.tmuxCommand("rename-session", "-t", exactTmuxSessionTarget(legacySession), canonicalSession).CombinedOutput()
		if renameErr != nil {
			if exists, checkErr := h.tmuxSessionExists(canonicalSession); checkErr != nil || !exists {
				return tmuxCommandError("adopt legacy tmux session", output, renameErr)
			}
		} else {
			canonicalExists = true
		}
	}

	if !canonicalExists {
		return errors.New("adopt legacy tmux session: canonical session is unavailable")
	}
	for index, window := range legacyWindows {
		prepareLegacyPi := tool == "pi" && (index == 0 || fixedTmuxTool(window) == "pi")
		if windowSession, findErr := h.tmuxWindowSession(window.Target.ID); findErr != nil {
			return findErr
		} else if windowSession == legacySession {
			conflict, conflictErr := h.tmuxFixedToolWindowConflict(canonicalSession, tool, window.Target.ID)
			if conflictErr != nil {
				return conflictErr
			}
			if conflict {
				log.Printf("preserve legacy tmux window: legacy_session=%q canonical_session=%q tool=%q window=%q reason=conflicting canonical window", legacySession, canonicalSession, tool, window.Target.ID)
				if prepareLegacyPi {
					if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, legacySession, window.Target.ID); prepareErr != nil {
						return prepareErr
					}
				}
				continue
			}
			destination, destinationErr := h.nextTmuxWindowIndex(canonicalSession)
			if destinationErr != nil {
				return destinationErr
			}
			output, moveErr := h.tmuxCommand(
				"move-window",
				"-s", window.Target.ID,
				"-t", exactTmuxWindowTarget(canonicalSession, destination),
			).CombinedOutput()
			if moveErr != nil {
				return tmuxCommandError("adopt legacy tmux window", output, moveErr)
			}
		}
		if prepareLegacyPi {
			windowSession, sessionErr := h.tmuxWindowSession(window.Target.ID)
			if sessionErr != nil {
				return sessionErr
			}
			if windowSession == "" {
				windowSession = canonicalSession
			}
			if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, windowSession, window.Target.ID); prepareErr != nil {
				return prepareErr
			}
		}
		if index > 0 {
			log.Printf("preserve additional legacy tmux window: canonical_session=%q tool=%q window=%q", canonicalSession, tool, window.Target.ID)
			continue
		}
		if err := h.configureSharedToolWindow(canonicalSession, window.Target, tool); err != nil {
			return err
		}
	}
	return h.rewriteAdoptedTmuxViewSourcesLocked(legacySession, canonicalSession, legacyWindows)
}

func (h *terminalHandler) rewriteAdoptedTmuxViewSourcesLocked(legacySession, canonicalSession string, legacyWindows []tmuxDetailedWindow) error {
	canonicalWindows, err := h.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		return err
	}
	legacyIDs := make(map[string]struct{}, len(legacyWindows))
	for _, window := range legacyWindows {
		legacyIDs[window.Target.ID] = struct{}{}
	}
	adoptedIDs := make(map[string]struct{}, len(legacyWindows))
	for _, window := range canonicalWindows {
		if _, wasLegacy := legacyIDs[window.Target.ID]; wasLegacy {
			adoptedIDs[window.Target.ID] = struct{}{}
		}
	}
	if len(adoptedIDs) == 0 {
		return nil
	}
	views, err := h.tmuxViewSessions()
	if err != nil {
		return err
	}
	for _, view := range views {
		windows, windowErr := h.tmuxDetailedWindows(view.Name)
		if windowErr != nil {
			if exists, checkErr := h.tmuxSessionExists(view.Name); checkErr == nil && !exists {
				continue
			}
			return windowErr
		}
		linkedToAdoptedWindow := false
		for _, window := range windows {
			if _, adopted := adoptedIDs[window.Target.ID]; adopted {
				linkedToAdoptedWindow = true
				break
			}
		}
		if !linkedToAdoptedWindow {
			continue
		}
		output, optionErr := h.tmuxCommand(
			"set-option",
			"-t", exactTmuxCurrentWindowTarget(view.Name),
			"@dire-mux-source-session", canonicalSession,
		).CombinedOutput()
		if optionErr != nil {
			return tmuxCommandError("rewrite adopted tmux view source", output, optionErr)
		}
	}
	return nil
}

func (h *terminalHandler) reconcileStaleTmuxViewsLocked(projectID, threadID, canonicalSession string) error {
	views, err := h.tmuxViewSessions()
	if err != nil {
		return err
	}
	for _, view := range views {
		if view.Attached || h.tmuxViewIsActiveLocked(view.Name) || tmuxViewHasLiveCreationGrace(view.Name, time.Now()) {
			continue
		}
		windows, windowErr := h.tmuxDetailedWindows(view.Name)
		if windowErr != nil {
			if exists, checkErr := h.tmuxSessionExists(view.Name); checkErr == nil && !exists {
				continue
			}
			return windowErr
		}
		if len(windows) != 1 {
			log.Printf("preserve stale tmux view: view_session=%q windows=%d reason=unexpected window count", view.Name, len(windows))
			continue
		}
		window := windows[0]
		sourceSession := ""
		linkedCanonical, linkedErr := h.tmuxCanonicalSessionLinkedToWindow(window.Target.ID)
		if linkedErr != nil {
			return linkedErr
		}
		if linkedCanonical != "" {
			sourceSession = linkedCanonical
			if linkedCanonical == canonicalSession && view.SourceSession != canonicalSession {
				output, optionErr := h.tmuxCommand(
					"set-option",
					"-t", exactTmuxCurrentWindowTarget(view.Name),
					"@dire-mux-source-session", canonicalSession,
				).CombinedOutput()
				if optionErr != nil {
					return tmuxCommandError("repair adopted tmux view source", output, optionErr)
				}
			}
		}
		if sourceSession == "" {
			sourceSession = view.SourceSession
		}
		if sourceSession == "" {
			sourceSession = tmuxSessionFromStartCommand(window.StartCommand)
		}
		if sourceSession != canonicalSession {
			continue
		}
		if err := h.adoptStaleTmuxViewLocked(projectID, threadID, view.Name, canonicalSession, window); err != nil {
			return err
		}
	}
	return nil
}

func (h *terminalHandler) adoptStaleTmuxViewLocked(projectID, threadID, viewSession, canonicalSession string, window tmuxDetailedWindow) error {
	canonicalExists, err := h.tmuxSessionExists(canonicalSession)
	if err != nil {
		return err
	}
	if !canonicalExists {
		output, renameErr := h.tmuxCommand("rename-session", "-t", exactTmuxSessionTarget(viewSession), canonicalSession).CombinedOutput()
		if renameErr != nil {
			if exists, checkErr := h.tmuxSessionExists(canonicalSession); checkErr != nil || !exists {
				return tmuxCommandError("adopt stale tmux terminal view", output, renameErr)
			}
		} else {
			_ = h.tmuxCommand("set-option", "-u", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "@dire-mux-source-session").Run()
			_ = h.tmuxCommand("set-option", "-u", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "@dire-mux-owner-pid").Run()
			h.configureAdoptedTmuxWindow(canonicalSession, window)
			if fixedTmuxTool(window) == "pi" {
				if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, canonicalSession, window.Target.ID); prepareErr != nil {
					return prepareErr
				}
			}
			h.wakeThreadTmuxWatchers(projectID, threadID)
			h.notifyThreadStatusChanged(projectID, threadID)
			return nil
		}
	}

	canonicalWindows, err := h.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		return err
	}
	for _, existing := range canonicalWindows {
		if existing.Target.ID == window.Target.ID {
			if fixedTmuxTool(window) == "pi" {
				if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, canonicalSession, window.Target.ID); prepareErr != nil {
					return prepareErr
				}
			}
			return h.removeStaleTmuxView(viewSession, canonicalSession, window.Target.ID)
		}
	}
	tool := fixedTmuxTool(window)
	if tool != "" {
		for _, existing := range canonicalWindows {
			if fixedTmuxTool(existing) == tool {
				log.Printf("preserve stale tmux view: view_session=%q canonical_session=%q tool=%q orphan_window=%q canonical_window=%q reason=conflicting canonical window", viewSession, canonicalSession, tool, window.Target.ID, existing.Target.ID)
				if tool == "pi" {
					if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, viewSession, window.Target.ID); prepareErr != nil {
						return prepareErr
					}
				}
				return nil
			}
		}
	}
	destination, err := h.nextTmuxWindowIndex(canonicalSession)
	if err != nil {
		return err
	}
	output, err := h.tmuxCommand(
		"link-window",
		"-s", window.Target.ID,
		"-t", exactTmuxWindowTarget(canonicalSession, destination),
	).CombinedOutput()
	if err != nil {
		return tmuxCommandError("adopt stale tmux terminal view", output, err)
	}
	h.configureAdoptedTmuxWindow(canonicalSession, window)
	if fixedTmuxTool(window) == "pi" {
		if prepareErr := h.prepareCodingAgentWindowForReconciliation(projectID, threadID, canonicalSession, window.Target.ID); prepareErr != nil {
			return prepareErr
		}
	}
	if err := h.removeStaleTmuxView(viewSession, canonicalSession, window.Target.ID); err != nil {
		return err
	}
	h.wakeThreadTmuxWatchers(projectID, threadID)
	h.notifyThreadStatusChanged(projectID, threadID)
	return nil
}

func (h *terminalHandler) configureAdoptedTmuxWindow(sessionName string, window tmuxDetailedWindow) {
	tool := fixedTmuxTool(window)
	if tool == "" {
		return
	}
	if err := h.configureSharedToolWindow(sessionName, window.Target, tool); err != nil {
		log.Printf("configure adopted tmux window: session=%q window=%q tool=%q error=%v", sessionName, window.Target.ID, tool, err)
	}
}

func (h *terminalHandler) removeStaleTmuxView(viewSession, canonicalSession, windowID string) error {
	linked := false
	windows, err := h.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		return err
	}
	for _, window := range windows {
		if window.Target.ID == windowID {
			linked = true
			break
		}
	}
	if !linked {
		return fmt.Errorf("preserve stale tmux view %q: window %q is not linked to canonical session %q", viewSession, windowID, canonicalSession)
	}
	output, err := h.tmuxCommand("kill-session", "-t", exactTmuxSessionTarget(viewSession)).CombinedOutput()
	if err != nil {
		if exists, checkErr := h.tmuxSessionExists(viewSession); checkErr == nil && !exists {
			return nil
		}
		return tmuxCommandError("remove stale tmux terminal view", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxViewSessions() ([]tmuxViewSession, error) {
	output, err := h.tmuxCommand(
		"list-sessions",
		"-F", "#{session_name}\t#{session_attached}\t#{@dire-mux-source-session}",
	).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return []tmuxViewSession{}, nil
		}
		return nil, tmuxCommandError("list tmux terminal views", output, err)
	}
	var views []tmuxViewSession
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[0], "dire-mux-view-") {
			continue
		}
		if parts[1] != "0" && parts[1] != "1" {
			return nil, fmt.Errorf("parse tmux terminal view: %q", line)
		}
		views = append(views, tmuxViewSession{Name: parts[0], Attached: parts[1] == "1", SourceSession: parts[2]})
	}
	return views, nil
}

func (h *terminalHandler) tmuxDetailedWindows(sessionName string) ([]tmuxDetailedWindow, error) {
	output, err := h.tmuxCommand(
		"list-windows",
		"-t", exactTmuxSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_id}\t#{window_name}\t#{@dire-mux-tool}\t#{pane_start_command}",
	).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError("list detailed tmux windows", output, err)
	}
	var windows []tmuxDetailedWindow
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("parse detailed tmux window: %q", line)
		}
		index, parseErr := strconv.Atoi(parts[0])
		if parseErr != nil || parts[1] == "" {
			return nil, fmt.Errorf("parse detailed tmux window target: %q", line)
		}
		windows = append(windows, tmuxDetailedWindow{
			Target:       tmuxWindowTarget{Index: index, ID: parts[1]},
			Name:         parts[2],
			Tool:         parts[3],
			StartCommand: parts[4],
		})
	}
	return windows, nil
}

func (h *terminalHandler) tmuxWindowSession(windowID string) (string, error) {
	output, err := h.tmuxCommand("list-windows", "-a", "-F", "#{session_name}\t#{window_id}").CombinedOutput()
	if err != nil {
		return "", tmuxCommandError("find tmux window session", output, err)
	}
	fallback := ""
	for _, line := range strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' }) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[1] != windowID || strings.HasPrefix(parts[0], tmuxViewSessionPrefix) {
			continue
		}
		if strings.HasPrefix(parts[0], tmuxSessionNamePrefix) {
			return parts[0], nil
		}
		if fallback == "" {
			fallback = parts[0]
		}
	}
	return fallback, nil
}

func (h *terminalHandler) tmuxCanonicalSessionLinkedToWindow(windowID string) (string, error) {
	sessionName, err := h.tmuxWindowSession(windowID)
	if err != nil || strings.HasSuffix(sessionName, "-tools") {
		return sessionName, err
	}
	return "", nil
}

func (h *terminalHandler) tmuxFixedToolWindowConflict(sessionName, tool, windowID string) (bool, error) {
	windows, err := h.tmuxDetailedWindows(sessionName)
	if err != nil {
		return false, err
	}
	for _, window := range windows {
		if window.Target.ID != windowID && fixedTmuxTool(window) == tool {
			return true, nil
		}
	}
	return false, nil
}

func (h *terminalHandler) nextTmuxWindowIndex(sessionName string) (int, error) {
	windows, err := h.tmuxDetailedWindows(sessionName)
	if err != nil {
		return 0, err
	}
	next := 0
	for _, window := range windows {
		if window.Target.Index >= next {
			next = window.Target.Index + 1
		}
	}
	return next, nil
}

func fixedTmuxTool(window tmuxDetailedWindow) string {
	for _, tool := range []string{"nvim", "lazygit", "pi"} {
		if window.Tool == tool || (window.Tool == "" && window.Name == tool) {
			return tool
		}
	}
	return ""
}

func tmuxSessionFromStartCommand(command string) string {
	const marker = "DIRE_MUX_TMUX_SESSION="
	index := strings.Index(command, marker)
	if index < 0 {
		return ""
	}
	value := command[index+len(marker):]
	end := 0
	for end < len(value) {
		character := value[end]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			end++
			continue
		}
		break
	}
	return value[:end]
}

func tmuxViewIdentity(sessionName string) (int, time.Time, bool) {
	remainder := strings.TrimPrefix(sessionName, "dire-mux-view-")
	rawPID, remainder, ok := strings.Cut(remainder, "-")
	if !ok {
		return 0, time.Time{}, false
	}
	pid, err := strconv.Atoi(rawPID)
	if err != nil || pid <= 0 {
		return 0, time.Time{}, false
	}
	rawCreatedAt, _, ok := strings.Cut(remainder, "-")
	if !ok {
		return 0, time.Time{}, false
	}
	createdAtUnixNano, err := strconv.ParseInt(rawCreatedAt, 16, 64)
	if err != nil || createdAtUnixNano <= 0 {
		return 0, time.Time{}, false
	}
	return pid, time.Unix(0, createdAtUnixNano), true
}

func tmuxViewHasLiveCreationGrace(sessionName string, now time.Time) bool {
	pid, createdAt, ok := tmuxViewIdentity(sessionName)
	if !ok || pid == os.Getpid() {
		return false
	}
	age := now.Sub(createdAt)
	if age < 0 || age > terminalViewCreationGrace {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func legacyThreadTmuxSessionName(projectID, threadID, tool string) string {
	return legacyTmuxSessionPrefix + projectID + "-" + threadID + "-" + tool
}

func legacyProjectTmuxSessionName(projectID, tool string) string {
	return legacyTmuxSessionPrefix + projectID + "-" + tool
}

const codingAgentLaunchScript = `set -eu
"$1" set-option -p -t "$TMUX_PANE" @dire-mux-agent "$2"
"$1" set-option -p -t "$TMUX_PANE" remain-on-exit on
shift 2
exec "$@"`

func (h *terminalHandler) codingAgentLaunchCommand(agent, command string, args []string) (string, []string) {
	launchArgs := make([]string, 0, len(args)+6)
	launchArgs = append(launchArgs,
		"-c",
		codingAgentLaunchScript,
		"dire-mux-agent-launch",
		h.tmuxPath,
		agent,
		command,
	)
	launchArgs = append(launchArgs, args...)
	return "/bin/sh", launchArgs
}

func (h *terminalHandler) startCodingAgentWindow(
	item project.Project,
	thread project.Thread,
	sessionName string,
	window tmuxWindowTarget,
	agent string,
) error {
	output, err := h.tmuxCommand("list-panes", "-t", window.ID, "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		return tmuxCommandError("find coding agent pane", output, err)
	}
	paneIDs := strings.Fields(string(output))
	if len(paneIDs) != 1 {
		return fmt.Errorf("find coding agent pane: window %q has %d panes", window.ID, len(paneIDs))
	}
	return h.startCodingAgentPane(item, thread, sessionName, window.ID, paneIDs[0], agent)
}

func (h *terminalHandler) startCodingAgentPane(
	item project.Project,
	thread project.Thread,
	sessionName string,
	windowID string,
	paneID string,
	agent string,
) error {
	if err := h.setTmuxPaneOption(paneID, "@dire-mux-agent", agent); err != nil {
		return err
	}
	ended, err := h.prepareExistingCodingAgentPane(item.ID, thread.ID, sessionName, windowID, tmuxAgentPane{ID: paneID, Agent: agent})
	if err != nil {
		return err
	}
	if ended {
		return errCodingAgentEnded
	}
	return nil
}

func (h *terminalHandler) prepareExistingCodingAgentPane(projectID, threadID, sessionName, windowID string, pane tmuxAgentPane) (bool, error) {
	if err := h.setTmuxPaneOption(pane.ID, "remain-on-exit", "on"); err != nil {
		if h.hasLogicalCodingAgentExit(projectID, threadID, pane.Agent) {
			return true, nil
		}
		return false, err
	}
	state, err := h.tmuxPaneExitState(pane.ID)
	if err != nil {
		return false, err
	}
	if !state.Found {
		if h.hasLogicalCodingAgentExit(projectID, threadID, pane.Agent) {
			return true, nil
		}
		return false, errors.New("coding agent pane disappeared during setup")
	}
	if state.Dead {
		h.handleCodingAgentExit(projectID, threadID, sessionName, windowID, pane.ID, pane.Agent, state)
		return true, nil
	}
	h.watchCodingAgentPane(projectID, threadID, sessionName, windowID, state.ServerPID, pane.ID, pane.Agent)
	return false, nil
}

// prepareCodingAgentWindow is only for the fixed Pi window. It adopts the
// historical first unmarked pane as Pi, leaves every other unmarked pane
// untouched, and prepares only panes explicitly identified as Pi or Claude.
func (h *terminalHandler) prepareCodingAgentWindow(projectID, threadID, sessionName, windowID, requiredAgent string) ([]tmuxAgentPane, map[string]bool, error) {
	panes, err := h.tmuxAgentPanes(windowID)
	if err != nil {
		return nil, nil, err
	}
	hasPiPane := false
	for _, pane := range panes {
		if pane.Agent == codingAgentPi {
			hasPiPane = true
			break
		}
	}
	if !hasPiPane {
		for index := range panes {
			if panes[index].Agent != "" {
				continue
			}
			if err := h.setTmuxPaneOption(panes[index].ID, "@dire-mux-agent", codingAgentPi); err != nil {
				return nil, nil, err
			}
			panes[index].Agent = codingAgentPi
			break
		}
	}

	alivePanes := make([]tmuxAgentPane, 0, len(panes))
	endedAgents := make(map[string]bool)
	for _, pane := range panes {
		if pane.Agent != codingAgentPi && !isClaudeCodingAgent(pane.Agent) {
			alivePanes = append(alivePanes, pane)
			continue
		}
		ended, prepareErr := h.prepareExistingCodingAgentPane(projectID, threadID, sessionName, windowID, pane)
		if prepareErr != nil {
			if requiredAgent == "" || pane.Agent == requiredAgent {
				return nil, nil, prepareErr
			}
			log.Printf("prepare coding agent pane: agent=%q project=%q thread=%q session=%q window=%q pane=%q error=%v", pane.Agent, projectID, threadID, sessionName, windowID, pane.ID, prepareErr)
			continue
		}
		if ended {
			endedAgents[pane.Agent] = true
			continue
		}
		alivePanes = append(alivePanes, pane)
	}
	return alivePanes, endedAgents, nil
}

func (h *terminalHandler) prepareCodingAgentWindowForReconciliation(projectID, threadID, sessionName, windowID string) error {
	_, _, err := h.prepareCodingAgentWindow(projectID, threadID, sessionName, windowID, "")
	return err
}

func (h *terminalHandler) watchCodingAgentPane(projectID, threadID, sessionName, windowID, serverPID, paneID, agent string) {
	key := codingAgentWatchKey{ServerPID: serverPID, PaneID: paneID}
	h.agentWatchMu.Lock()
	if h.agentWatches == nil {
		h.agentWatches = make(map[codingAgentWatchKey]struct{})
	}
	if _, watching := h.agentWatches[key]; watching {
		h.agentWatchMu.Unlock()
		return
	}
	h.agentWatches[key] = struct{}{}
	h.agentWatchMu.Unlock()

	go func() {
		defer func() {
			h.agentWatchMu.Lock()
			delete(h.agentWatches, key)
			delete(h.agentExitSuppressed, codingAgentExitKey{
				ProjectID: projectID,
				ThreadID:  threadID,
				Agent:     agent,
				PaneID:    paneID,
				ServerPID: serverPID,
			})
			h.agentWatchMu.Unlock()
		}()
		ticker := time.NewTicker(terminalAgentPollInterval)
		defer ticker.Stop()
		for {
			state, err := h.tmuxPaneExitState(paneID)
			if err != nil {
				log.Printf("monitor coding agent pane: agent=%q project=%q thread=%q session=%q window=%q pane=%q error=%v", agent, projectID, threadID, sessionName, windowID, paneID, err)
				return
			}
			if !state.Found {
				return
			}
			if state.ServerPID != serverPID {
				return
			}
			if state.Dead {
				h.handleCodingAgentExit(projectID, threadID, sessionName, windowID, paneID, agent, state)
				return
			}
			<-ticker.C
		}
	}()
}

func (h *terminalHandler) handleCodingAgentExit(projectID, threadID, sessionName, windowID, paneID, agent string, state tmuxPaneExitState) {
	if !state.Dead {
		return
	}
	key := codingAgentExitKey{
		ProjectID: projectID,
		ThreadID:  threadID,
		Agent:     agent,
		PaneID:    paneID,
		ServerPID: state.ServerPID,
	}
	h.agentWatchMu.Lock()
	_, suppressed := h.agentExitSuppressed[key]
	h.agentWatchMu.Unlock()
	if suppressed {
		// Explicit restart owns reaping while the durable marker flock is held.
		// A late watcher must not bypass that fence.
		return
	}
	h.logCodingAgentExitOnce(key, sessionName, windowID, state)

	var current, durable, firstRecord bool
	recordErr := h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		h.agentWatchMu.Lock()
		if _, suppressed := h.agentExitSuppressed[key]; suppressed {
			h.agentWatchMu.Unlock()
			return nil
		}
		if h.agentExits == nil {
			h.agentExits = make(map[codingAgentExitKey]tmuxPaneExitState)
		}
		_, alreadyRecorded := h.agentExits[key]
		firstRecord = !alreadyRecorded
		if firstRecord {
			h.agentExits[key] = state
		}
		h.agentWatchMu.Unlock()

		var persistErr error
		current, persistErr = h.persistCodingAgentExitMarkerLocked(path, projectID, threadID, agent, paneID, state)
		if persistErr != nil || !current {
			if firstRecord {
				h.agentWatchMu.Lock()
				delete(h.agentExits, key)
				h.agentWatchMu.Unlock()
			}
			return persistErr
		}
		durable = true
		if err := h.killTmuxPaneIncarnation(paneID, state.ServerPID, true); err != nil {
			return fmt.Errorf("remove retained dead pane: %w", err)
		}
		return nil
	})
	if recordErr != nil {
		log.Printf("record coding agent exit: agent=%q project=%q thread=%q pane=%q server_pid=%q error=%v; preserving retained dead pane or durable marker", agent, projectID, threadID, paneID, state.ServerPID, recordErr)
	}
	if durable && firstRecord {
		h.wakeThreadTmuxWatchers(projectID, threadID)
		h.notifyThreadStatusChanged(projectID, threadID)
	}
}

func (h *terminalHandler) logCodingAgentExitOnce(key codingAgentExitKey, sessionName, windowID string, state tmuxPaneExitState) bool {
	h.agentWatchMu.Lock()
	if h.agentExitLogs == nil {
		h.agentExitLogs = make(map[codingAgentExitKey]struct{})
	}
	if _, logged := h.agentExitLogs[key]; logged {
		h.agentWatchMu.Unlock()
		return false
	}
	h.agentExitLogs[key] = struct{}{}
	h.agentWatchMu.Unlock()

	// log.Printf writes synchronously. The caller invokes this while retained
	// pane evidence still exists, independently of whether marker persistence
	// later succeeds.
	logCodingAgentExit(key.ProjectID, key.ThreadID, sessionName, windowID, key.PaneID, key.Agent, state)
	return true
}

func logCodingAgentExit(projectID, threadID, sessionName, windowID, paneID, agent string, state tmuxPaneExitState) {
	status := state.Status
	if status == "" {
		status = "unavailable"
	}
	signal := state.Signal
	if signal == "" {
		signal = "none"
	}
	log.Printf("coding agent exited: agent=%q project=%q thread=%q session=%q window=%q pane=%q server_pid=%q status=%s signal=%s exited_at=%q", agent, projectID, threadID, sessionName, windowID, paneID, state.ServerPID, status, signal, state.ExitedAt)
}

func (h *terminalHandler) prepareCodingAgentRestartLocked(projectID, threadID, sessionName, agent string) error {
	var deadPanes []codingAgentPaneIncarnation
	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return err
	}
	if exists {
		window, found, findErr := h.tmuxToolWindow(sessionName, "pi")
		if findErr != nil {
			return findErr
		}
		if found {
			panes, panesErr := h.tmuxAgentPanes(window.ID)
			if panesErr != nil {
				return panesErr
			}
			for _, pane := range panes {
				if pane.Agent != agent {
					continue
				}
				state, stateErr := h.tmuxPaneExitState(pane.ID)
				if stateErr != nil {
					return stateErr
				}
				if state.Found && state.Dead {
					h.handleCodingAgentExit(projectID, threadID, sessionName, window.ID, pane.ID, agent, state)
					h.suppressCodingAgentExit(projectID, threadID, agent, pane.ID, state)
					deadPanes = append(deadPanes, codingAgentPaneIncarnation{PaneID: pane.ID, ServerPID: state.ServerPID})
				}
			}
		}
	}
	if err := h.removeCodingAgentPanesForRestart(projectID, threadID, agent, deadPanes); err != nil {
		return fmt.Errorf("prepare coding agent restart: %w", err)
	}
	h.clearCodingAgentExits(projectID, threadID, agent)
	return nil
}

func (h *terminalHandler) suppressCodingAgentExit(projectID, threadID, agent, paneID string, state tmuxPaneExitState) {
	key := codingAgentExitKey{
		ProjectID: projectID,
		ThreadID:  threadID,
		Agent:     agent,
		PaneID:    paneID,
		ServerPID: state.ServerPID,
	}
	h.agentWatchMu.Lock()
	defer h.agentWatchMu.Unlock()
	if _, watching := h.agentWatches[codingAgentWatchKey{ServerPID: state.ServerPID, PaneID: paneID}]; watching {
		if h.agentExitSuppressed == nil {
			h.agentExitSuppressed = make(map[codingAgentExitKey]tmuxPaneExitState)
		}
		h.agentExitSuppressed[key] = state
	}
	delete(h.agentExits, key)
}

func (h *terminalHandler) hasCodingAgentExit(projectID, threadID, agent, paneID, serverPID string) bool {
	key := codingAgentExitKey{
		ProjectID: projectID,
		ThreadID:  threadID,
		Agent:     agent,
		PaneID:    paneID,
		ServerPID: serverPID,
	}
	marker, found, err := h.readCodingAgentExitMarker(projectID, threadID, agent)
	if err != nil {
		log.Printf("inspect exact coding agent exit marker: agent=%q project=%q thread=%q pane=%q server_pid=%q error=%v", agent, projectID, threadID, paneID, serverPID, err)
		return true
	}
	if !found {
		h.agentWatchMu.Lock()
		delete(h.agentExits, key)
		h.agentWatchMu.Unlock()
		return false
	}
	return marker.PaneID == paneID && marker.ServerPID == serverPID
}

func (h *terminalHandler) hasLogicalCodingAgentExit(projectID, threadID, agent string) bool {
	_, found, err := h.readCodingAgentExitMarker(projectID, threadID, agent)
	if err != nil {
		// Failing closed prevents a permissions or filesystem fault from
		// silently relaunching a coding agent whose durable state is unknown.
		log.Printf("inspect coding agent exit marker: agent=%q project=%q thread=%q error=%v", agent, projectID, threadID, err)
		return true
	}
	if found {
		return true
	}
	// Disk is the logical source of truth. A different backend may have
	// successfully processed an explicit restart, so discard stale local
	// logical entries when its atomic marker is absent.
	h.clearCodingAgentExits(projectID, threadID, agent)
	return false
}

func (h *terminalHandler) clearCodingAgentExits(projectID, threadID, agent string) {
	h.agentWatchMu.Lock()
	defer h.agentWatchMu.Unlock()
	for key := range h.agentExits {
		if key.ProjectID == projectID && key.ThreadID == threadID && key.Agent == agent {
			delete(h.agentExits, key)
		}
	}
	for key := range h.agentExitLogs {
		if key.ProjectID == projectID && key.ThreadID == threadID && key.Agent == agent {
			delete(h.agentExitLogs, key)
		}
	}
	for key := range h.agentExitSuppressed {
		if key.ProjectID != projectID || key.ThreadID != threadID || key.Agent != agent {
			continue
		}
		if _, watching := h.agentWatches[codingAgentWatchKey{ServerPID: key.ServerPID, PaneID: key.PaneID}]; !watching {
			delete(h.agentExitSuppressed, key)
		}
	}
}

func (h *terminalHandler) codingAgentExitMarkerPath(projectID, threadID, agent string) string {
	root := h.agentExitDirectory
	if root == "" && h.projects != nil {
		root = filepath.Join(h.projects.DataDirectory(), "coding-agent-exits")
	}
	component := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(value))
	}
	return filepath.Join(root, component(projectID), component(threadID), component(agent)+".json")
}

func (h *terminalHandler) recordCodingAgentExit(projectID, threadID, agent, paneID string, state tmuxPaneExitState) (bool, error) {
	current := false
	err := h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		var err error
		current, err = h.persistCodingAgentExitMarkerLocked(path, projectID, threadID, agent, paneID, state)
		return err
	})
	return current, err
}

func (h *terminalHandler) persistCodingAgentExitMarkerLocked(path, projectID, threadID, agent, paneID string, state tmuxPaneExitState) (bool, error) {
	observed, err := h.tmuxPaneExitState(paneID)
	if err != nil {
		return true, fmt.Errorf("recheck retained pane: %w", err)
	}
	if !observed.Found || !observed.Dead || observed.ServerPID != state.ServerPID {
		return false, nil
	}
	marker := codingAgentExitMarkerFromState(projectID, threadID, agent, paneID, observed)
	if err := writeCodingAgentExitMarker(path, marker); err != nil {
		return true, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return true, fmt.Errorf("sync marker directory: %w", err)
	}
	return true, nil
}

func codingAgentExitMarkerFromState(projectID, threadID, agent, paneID string, state tmuxPaneExitState) codingAgentExitMarker {
	return codingAgentExitMarker{
		ProjectID: projectID,
		ThreadID:  threadID,
		Agent:     agent,
		PaneID:    paneID,
		ServerPID: state.ServerPID,
		Status:    state.Status,
		Signal:    state.Signal,
		ExitedAt:  state.ExitedAt,
	}
}

func (h *terminalHandler) withCodingAgentExitMarkerLock(projectID, threadID, agent string, operation func(path string) error) error {
	path := h.codingAgentExitMarkerPath(projectID, threadID, agent)
	if path == "" {
		return errors.New("coding agent exit marker directory is unavailable")
	}
	directory := filepath.Dir(path)
	h.agentExitMarkerMu.Lock()
	defer h.agentExitMarkerMu.Unlock()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create marker directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure marker directory: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open marker lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0o600); err != nil {
		return fmt.Errorf("secure marker lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock marker: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return operation(path)
}

func writeCodingAgentExitMarker(path string, marker codingAgentExitMarker) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode marker: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".coding-agent-exit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary marker: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace marker: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readCodingAgentExitMarkerFile(path, projectID, threadID, agent string) (codingAgentExitMarker, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return codingAgentExitMarker{}, false, nil
	}
	if err != nil {
		return codingAgentExitMarker{}, false, err
	}
	var marker codingAgentExitMarker
	if err := json.Unmarshal(contents, &marker); err != nil {
		return codingAgentExitMarker{}, false, fmt.Errorf("decode marker: %w", err)
	}
	if marker.ProjectID != projectID || marker.ThreadID != threadID || marker.Agent != agent {
		return codingAgentExitMarker{}, false, errors.New("marker identity does not match its path")
	}
	return marker, true, nil
}

func (h *terminalHandler) readCodingAgentExitMarker(projectID, threadID, agent string) (codingAgentExitMarker, bool, error) {
	var marker codingAgentExitMarker
	var found bool
	err := h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		var err error
		marker, found, err = readCodingAgentExitMarkerFile(path, projectID, threadID, agent)
		return err
	})
	return marker, found, err
}

func (h *terminalHandler) removeCodingAgentPanesForRestart(projectID, threadID, agent string, panes []codingAgentPaneIncarnation) error {
	return h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		for _, pane := range panes {
			observed, err := h.tmuxPaneExitState(pane.PaneID)
			if err != nil {
				return fmt.Errorf("inspect retained pane before restart: %w", err)
			}
			if !observed.Found || observed.ServerPID != pane.ServerPID {
				continue
			}
			if !observed.Dead {
				return fmt.Errorf("retained pane %q is no longer dead", pane.PaneID)
			}

			marker, found, markerErr := readCodingAgentExitMarkerFile(path, projectID, threadID, agent)
			if markerErr != nil {
				info, statErr := os.Stat(path)
				if statErr != nil {
					if !errors.Is(statErr, os.ErrNotExist) {
						return fmt.Errorf("inspect invalid coding agent exit marker: %w", statErr)
					}
				} else if !info.Mode().IsRegular() {
					return fmt.Errorf("coding agent exit marker is not a regular file: %s", path)
				}
				found = false
			}
			if !found || marker.PaneID != pane.PaneID || marker.ServerPID != pane.ServerPID {
				marker = codingAgentExitMarkerFromState(projectID, threadID, agent, pane.PaneID, observed)
				if err := writeCodingAgentExitMarker(path, marker); err != nil {
					return fmt.Errorf("repair coding agent restart fence: %w", err)
				}
				if err := syncDirectory(filepath.Dir(path)); err != nil {
					return fmt.Errorf("sync repaired coding agent restart fence: %w", err)
				}
				verified, verifiedFound, err := readCodingAgentExitMarkerFile(path, projectID, threadID, agent)
				if err != nil || !verifiedFound || verified.PaneID != pane.PaneID || verified.ServerPID != pane.ServerPID {
					if err != nil {
						return fmt.Errorf("verify coding agent restart fence: %w", err)
					}
					return errors.New("verify coding agent restart fence: marker does not match retained pane")
				}
			}
			if err := h.killTmuxPaneIncarnation(pane.PaneID, pane.ServerPID, true); err != nil {
				return fmt.Errorf("remove fenced coding agent pane: %w", err)
			}
		}
		return nil
	})
}

func (h *terminalHandler) confirmCodingAgentRestart(projectID, threadID, agent, paneID, serverPID string, replacementCreated bool) error {
	return h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		marker, found, err := readCodingAgentExitMarkerFile(path, projectID, threadID, agent)
		if err != nil {
			if replacementCreated {
				_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			}
			return err
		}
		if !found {
			return nil
		}

		state, err := h.tmuxPaneExitState(paneID)
		if err != nil || !state.Found || state.ServerPID != serverPID {
			if replacementCreated {
				_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			}
			if err != nil {
				return err
			}
			return errors.New("replacement coding agent pane changed before restart confirmation")
		}
		if state.Dead {
			newMarker := codingAgentExitMarker{
				ProjectID: projectID,
				ThreadID:  threadID,
				Agent:     agent,
				PaneID:    paneID,
				ServerPID: state.ServerPID,
				Status:    state.Status,
				Signal:    state.Signal,
				ExitedAt:  state.ExitedAt,
			}
			if err := writeCodingAgentExitMarker(path, newMarker); err != nil {
				return err
			}
			if err := syncDirectory(filepath.Dir(path)); err != nil {
				return err
			}
			// This branch owns the marker lock, so a watcher cannot record the
			// replacement concurrently. Log its exit synchronously after the
			// durable marker and before removing the retained pane evidence.
			h.logCodingAgentExitOnce(codingAgentExitKey{
				ProjectID: projectID,
				ThreadID:  threadID,
				Agent:     agent,
				PaneID:    paneID,
				ServerPID: state.ServerPID,
			}, tmuxSessionName(projectID, threadID, "pi"), "", state)
			_ = h.killTmuxPaneIncarnation(paneID, serverPID, true)
			return errCodingAgentEnded
		}
		if marker.PaneID == paneID && marker.ServerPID == serverPID {
			if replacementCreated {
				_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			}
			return errors.New("replacement coding agent already has a durable exit marker")
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			if replacementCreated {
				_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			}
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			// Restore the restart fence if the durable removal could not be
			// confirmed, then tear down only the replacement incarnation.
			_ = writeCodingAgentExitMarker(path, marker)
			_ = syncDirectory(filepath.Dir(path))
			if replacementCreated {
				_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			}
			return fmt.Errorf("sync restart marker removal: %w", err)
		}
		return nil
	})
}

func (h *terminalHandler) stopCodingAgentPaneIfExitMarked(projectID, threadID, agent, paneID, serverPID string) (bool, error) {
	marked := false
	err := h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		_, found, err := readCodingAgentExitMarkerFile(path, projectID, threadID, agent)
		if err != nil {
			marked = true
			_ = h.killTmuxPaneIncarnation(paneID, serverPID, false)
			return err
		}
		if !found {
			return nil
		}
		marked = true
		state, stateErr := h.tmuxPaneExitState(paneID)
		if stateErr == nil && state.Found && state.ServerPID == serverPID {
			h.suppressCodingAgentExit(projectID, threadID, agent, paneID, state)
			if killErr := h.killTmuxPaneIncarnation(paneID, serverPID, false); killErr != nil {
				return killErr
			}
		}
		return stateErr
	})
	return marked, err
}

func (h *terminalHandler) killTmuxPaneIncarnation(paneID, serverPID string, requireDead bool) error {
	if pid, err := strconv.Atoi(serverPID); err != nil || pid <= 0 {
		return fmt.Errorf("invalid tmux server pid %q", serverPID)
	}
	condition := fmt.Sprintf("#{==:#{pid},%s}", serverPID)
	if requireDead {
		condition = fmt.Sprintf("#{&&:%s,#{pane_dead}}", condition)
	}
	command := "kill-pane -t " + shellQuote(paneID)
	output, err := h.tmuxCommand("if-shell", "-t", paneID, "-F", condition, command, "").CombinedOutput()
	if err != nil {
		state, stateErr := h.tmuxPaneExitState(paneID)
		if stateErr == nil && (!state.Found || state.ServerPID != serverPID) {
			return nil
		}
		return tmuxCommandError("remove exact coding agent pane", output, err)
	}
	return nil
}

func (h *terminalHandler) killTmuxWindowIncarnation(windowID, serverPID string) error {
	return h.killTmuxTargetIncarnation(
		windowID,
		serverPID,
		"kill-window -t "+shellQuote(windowID),
		"remove exact tmux window",
	)
}

func (h *terminalHandler) killTmuxSessionIncarnation(sessionName, serverPID string) error {
	target := "=" + sessionName
	return h.killTmuxTargetIncarnation(
		target,
		serverPID,
		"kill-session -t "+shellQuote(target),
		"remove exact tmux session",
	)
}

func (h *terminalHandler) killTmuxTargetIncarnation(target, serverPID, command, action string) error {
	if pid, err := strconv.Atoi(serverPID); err != nil || pid <= 0 {
		return fmt.Errorf("invalid tmux server pid %q", serverPID)
	}
	condition := fmt.Sprintf("#{==:#{pid},%s}", serverPID)
	output, err := h.tmuxCommand("if-shell", "-t", target, "-F", condition, command, "").CombinedOutput()
	if err == nil {
		return nil
	}
	observedPID, found, stateErr := h.tmuxTargetServerPID(target)
	if stateErr == nil && (!found || observedPID != serverPID) {
		return nil
	}
	return tmuxCommandError(action, output, err)
}

func (h *terminalHandler) tmuxTargetServerPID(target string) (string, bool, error) {
	identityFormat := "#{window_id}"
	expectedIdentity := target
	if strings.HasPrefix(target, "%") {
		identityFormat = "#{pane_id}"
	} else if strings.HasPrefix(target, "=") {
		identityFormat = "#{session_name}"
		expectedIdentity = strings.TrimPrefix(target, "=")
	}
	output, err := h.tmuxCommand("display-message", "-p", "-t", target, "#{pid}\t"+identityFormat).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, nil
		}
		return "", false, tmuxCommandError("inspect tmux target incarnation", output, err)
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

func (h *terminalHandler) removeCodingAgentExitMarkersForThread(projectID, threadID string) error {
	for _, agent := range []string{codingAgentPi, codingAgentClaude, codingAgentClaudeGPT} {
		if err := h.removeCodingAgentExitMarker(projectID, threadID, agent); err != nil {
			return err
		}
		h.clearCodingAgentExits(projectID, threadID, agent)
	}
	return nil
}

func (h *terminalHandler) removeCodingAgentExitMarkersForProject(item project.Project) error {
	for _, thread := range item.Threads {
		if err := h.removeCodingAgentExitMarkersForThread(item.ID, thread.ID); err != nil {
			return err
		}
	}
	return nil
}

func (h *terminalHandler) removeCodingAgentExitMarker(projectID, threadID, agent string) error {
	return h.withCodingAgentExitMarkerLock(projectID, threadID, agent, func(path string) error {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	})
}

func (h *terminalHandler) tmuxPaneExitState(paneID string) (tmuxPaneExitState, error) {
	output, err := h.tmuxCommand(
		"display-message", "-p",
		"-t", paneID,
		"#{pid}\t#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_dead_signal}\t#{pane_dead_time}",
	).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return tmuxPaneExitState{}, nil
		}
		return tmuxPaneExitState{}, tmuxCommandError("inspect coding agent exit", output, err)
	}
	line := strings.TrimRight(string(output), "\r\n")
	if line == "" {
		return tmuxPaneExitState{}, nil
	}
	parts := strings.SplitN(line, "\t", 6)
	if len(parts) == 6 && parts[1] == "" {
		// With no matching pane, some tmux versions still expand server-wide
		// formats such as #{pid} and exit successfully. An empty pane id is the
		// authoritative indication that the requested incarnation is gone.
		return tmuxPaneExitState{}, nil
	}
	if len(parts) != 6 || parts[0] == "" || parts[1] != paneID || (parts[2] != "0" && parts[2] != "1") {
		return tmuxPaneExitState{}, fmt.Errorf("parse coding agent exit: %q", line)
	}
	return tmuxPaneExitState{
		ServerPID: parts[0],
		Dead:      parts[2] == "1",
		Status:    parts[3],
		Signal:    parts[4],
		ExitedAt:  parts[5],
		Found:     true,
	}, nil
}

// Coding agents are panes inside the fixed `pi` window. Keeping that window
// name preserves existing sessions while allowing both processes to stay alive.
func (h *terminalHandler) ensureCodingAgentPane(
	item project.Project,
	thread project.Thread,
	agent string,
	threadEndpoint string,
	sessionName string,
) (paneID, notice string, created bool, err error) {
	return h.ensureCodingAgentPaneWithRestart(item, thread, agent, threadEndpoint, sessionName, false, nil)
}

func (h *terminalHandler) ensureCodingAgentPaneWithRestart(
	item project.Project,
	thread project.Thread,
	agent string,
	threadEndpoint string,
	sessionName string,
	restartCodingAgent bool,
	expectedServerPID *string,
) (paneID, notice string, created bool, err error) {
	return h.ensureCodingAgentPaneWithOptions(
		item,
		thread,
		agent,
		threadEndpoint,
		sessionName,
		codingAgentLaunchOptions{},
		restartCodingAgent,
		expectedServerPID,
	)
}

func (h *terminalHandler) ensureCodingAgentPaneWithOptions(
	item project.Project,
	thread project.Thread,
	agent string,
	threadEndpoint string,
	sessionName string,
	launchOptions codingAgentLaunchOptions,
	restartCodingAgent bool,
	expectedServerPID *string,
) (paneID, notice string, created bool, err error) {
	agent, err = normalizeCodingAgent(agent)
	if err != nil {
		return "", "", false, err
	}

	// Pane creation and selection must be serialized with fixed-window setup.
	// Otherwise simultaneous browser connections can create duplicate agents.
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return "", "", false, mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return "", "", false, err
	}
	defer func() {
		if fenceErr := h.finishTerminalThreadMutationLocked(item, thread); fenceErr != nil {
			paneID = ""
			notice = ""
			created = false
			err = errors.Join(err, fenceErr)
		}
	}()

	window, found, err := h.tmuxToolWindow(sessionName, "pi")
	if err != nil {
		if !restartCodingAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, agent) {
			return "", "", false, errCodingAgentEnded
		}
		return "", "", false, err
	}
	if !found {
		if !restartCodingAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, agent) {
			return "", "", false, errCodingAgentEnded
		}
		return "", "", false, errors.New("Pi window not found")
	}

	panes, endedAgents, err := h.prepareCodingAgentWindow(item.ID, thread.ID, sessionName, window.ID, agent)
	if err != nil {
		if !restartCodingAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, agent) {
			return "", "", false, errCodingAgentEnded
		}
		return "", "", false, err
	}
	if endedAgents[agent] {
		return "", "", false, errCodingAgentEnded
	}
	// A durable marker is also a restart-commit fence. Check it before
	// returning an existing live pane so a backend that died between creating
	// and confirming a replacement cannot expose that pane to an implicit
	// reconnect.
	if !restartCodingAgent && h.hasLogicalCodingAgentExit(item.ID, thread.ID, agent) {
		return "", "", false, errCodingAgentEnded
	}

	for _, pane := range panes {
		if pane.Agent == agent {
			if err := h.activateCodingAgentPane(window.ID, pane.ID, panes); err != nil {
				return "", "", false, err
			}
			if expectedServerPID != nil || restartCodingAgent {
				state, stateErr := h.tmuxPaneExitState(pane.ID)
				if stateErr != nil || !state.Found || state.ServerPID == "" {
					return "", "", false, errors.New("coding agent pane disappeared during setup")
				}
				if state.Dead {
					h.handleCodingAgentExit(item.ID, thread.ID, sessionName, window.ID, pane.ID, agent, state)
					return "", "", false, errCodingAgentEnded
				}
				if expectedServerPID != nil {
					*expectedServerPID = state.ServerPID
				}
				if restartCodingAgent {
					if err := h.confirmCodingAgentRestart(item.ID, thread.ID, agent, pane.ID, state.ServerPID, false); err != nil {
						return "", "", false, err
					}
				}
			}
			return pane.ID, "", false, nil
		}
	}

	command, args, notice, err := h.commandForCodingAgentPaneWithOptions(
		item,
		thread,
		agent,
		threadEndpoint,
		sessionName,
		launchOptions,
	)
	if err != nil {
		return "", "", false, err
	}
	if err := h.unzoomTmuxWindow(window.ID, panes); err != nil {
		return "", "", false, err
	}
	launchCommand, launchArgs := h.codingAgentLaunchCommand(agent, command, args)
	output, err := h.tmuxCommand(
		"split-window",
		"-d",
		"-P", "-F", "#{pane_id}\t#{pid}",
		"-t", window.ID,
		"-c", thread.Cwd,
		shellCommand(launchCommand, launchArgs),
	).CombinedOutput()
	if err != nil {
		return "", "", false, tmuxCommandError("create coding agent pane", output, err)
	}
	incarnation, err := parseTmuxPaneIncarnation(output)
	if err != nil {
		return "", "", false, err
	}
	paneID = incarnation.PaneID
	if err := h.startCodingAgentPane(item, thread, sessionName, window.ID, paneID, agent); err != nil {
		if !errors.Is(err, errCodingAgentEnded) {
			_ = h.killTmuxPaneIncarnation(paneID, incarnation.ServerPID, false)
		}
		return "", "", false, err
	}

	panes, err = h.tmuxAgentPanes(window.ID)
	if err != nil {
		_ = h.killTmuxPaneIncarnation(paneID, incarnation.ServerPID, false)
		return "", "", false, err
	}
	if err := h.activateCodingAgentPane(window.ID, paneID, panes); err != nil {
		_ = h.killTmuxPaneIncarnation(paneID, incarnation.ServerPID, false)
		return "", "", false, err
	}
	if expectedServerPID != nil || restartCodingAgent {
		state, stateErr := h.tmuxPaneExitState(paneID)
		if stateErr != nil || !state.Found || state.ServerPID == "" {
			_ = h.killTmuxPaneIncarnation(paneID, incarnation.ServerPID, false)
			return "", "", false, errors.New("coding agent pane disappeared during setup")
		}
		if state.Dead {
			h.handleCodingAgentExit(item.ID, thread.ID, sessionName, window.ID, paneID, agent, state)
			return "", "", false, errCodingAgentEnded
		}
		if state.ServerPID != incarnation.ServerPID {
			_ = h.killTmuxPaneIncarnation(paneID, incarnation.ServerPID, false)
			return "", "", false, errors.New("coding agent pane changed during setup")
		}
		if expectedServerPID != nil {
			*expectedServerPID = incarnation.ServerPID
		}
		if restartCodingAgent {
			if err := h.confirmCodingAgentRestart(item.ID, thread.ID, agent, paneID, incarnation.ServerPID, true); err != nil {
				return "", "", false, err
			}
		}
	}
	return paneID, notice, true, nil
}

func (h *terminalHandler) tmuxAgentPanes(windowID string) ([]tmuxAgentPane, error) {
	output, err := h.tmuxCommand(
		"list-panes",
		"-t", windowID,
		"-F", "#{pane_id}\t#{@dire-mux-agent}\t#{pane_active}",
	).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError("list coding agent panes", output, err)
	}

	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	panes := make([]tmuxAgentPane, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[0] == "" || (parts[2] != "0" && parts[2] != "1") {
			return nil, fmt.Errorf("parse coding agent pane: %q", line)
		}
		panes = append(panes, tmuxAgentPane{
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

func (h *terminalHandler) activateCodingAgentPane(windowID, paneID string, panes []tmuxAgentPane) error {
	zoomed, err := h.tmuxWindowZoomed(windowID)
	if err != nil {
		return err
	}
	if zoomed {
		for _, pane := range panes {
			if pane.Active && pane.ID == paneID {
				return nil
			}
		}
		if err := h.unzoomTmuxWindow(windowID, panes); err != nil {
			return err
		}
	}

	output, err := h.tmuxCommand("select-pane", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmuxCommandError("select coding agent pane", output, err)
	}
	if len(panes) == 1 {
		return nil
	}
	output, err = h.tmuxCommand("resize-pane", "-Z", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmuxCommandError("zoom coding agent pane", output, err)
	}
	return nil
}

func (h *terminalHandler) unzoomTmuxWindow(windowID string, panes []tmuxAgentPane) error {
	zoomed, err := h.tmuxWindowZoomed(windowID)
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
	output, err := h.tmuxCommand("resize-pane", "-Z", "-t", paneID).CombinedOutput()
	if err != nil {
		return tmuxCommandError("unzoom coding agent pane", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxWindowZoomed(windowID string) (bool, error) {
	output, err := h.tmuxCommand("display-message", "-p", "-t", windowID, "#{window_zoomed_flag}").CombinedOutput()
	if err != nil {
		return false, tmuxCommandError("read coding agent layout", output, err)
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

func (h *terminalHandler) setTmuxPaneOption(paneID, option, value string) error {
	output, err := h.tmuxCommand("set-option", "-p", "-t", paneID, option, value).CombinedOutput()
	if err != nil {
		return tmuxCommandError("configure coding agent pane", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxPaneAlive(paneID string) (bool, error) {
	output, err := h.tmuxCommand("display-message", "-p", "-t", paneID, "#{pane_dead}").CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return false, nil
		}
		return false, tmuxCommandError("check coding agent pane", output, err)
	}
	return strings.TrimSpace(string(output)) == "0", nil
}

func (h *terminalHandler) commandForTmuxWindow(item project.Project, thread project.Thread, tool, threadEndpoint, sessionName string) (string, []string, string, error) {
	return h.commandForTmuxTarget(item, thread, tool, tool, threadEndpoint, sessionName, codingAgentLaunchOptions{})
}

func (h *terminalHandler) commandForCodingAgentPane(item project.Project, thread project.Thread, agent, threadEndpoint, sessionName string) (string, []string, string, error) {
	return h.commandForCodingAgentPaneWithOptions(
		item,
		thread,
		agent,
		threadEndpoint,
		sessionName,
		codingAgentLaunchOptions{},
	)
}

func (h *terminalHandler) commandForCodingAgentPaneWithOptions(
	item project.Project,
	thread project.Thread,
	agent string,
	threadEndpoint string,
	sessionName string,
	launchOptions codingAgentLaunchOptions,
) (string, []string, string, error) {
	if agent == codingAgentClaudeGPT && launchOptions.Model == "" {
		_, _, notice, err := commandFor(agent)
		if err == nil && notice == "" {
			ctx, cancel := context.WithTimeout(context.Background(), codingAgentModelDiscoveryTimeout)
			defer cancel()
			models, discoveryErr := h.availableCLIProxyAPIGPTModels(ctx)
			if discoveryErr != nil {
				return "", nil, "", fmt.Errorf("load CLIProxyAPI GPT models: %w", discoveryErr)
			}
			launchOptions.Model = models[0].ID
		}
	}

	command, args, notice, err := h.commandForTmuxTarget(
		item,
		thread,
		agent,
		"pi",
		threadEndpoint,
		sessionName,
		launchOptions,
	)
	if err != nil || notice != "" {
		return command, args, notice, err
	}
	if launchOptions.Model != "" {
		args = append(args, "--model", launchOptions.Model)
	}
	if launchOptions.ThinkingLevel != "" {
		thinkingFlag := "--thinking"
		if isClaudeCodingAgent(agent) {
			thinkingFlag = "--effort"
		}
		args = append(args, thinkingFlag, launchOptions.ThinkingLevel)
	}
	if (agent == codingAgentPi || isClaudeCodingAgent(agent)) && launchOptions.InitialPrompt != "" {
		// Terminal agents treat a positional message as the first interactive
		// turn. Passing it at launch avoids racing a TUI that is not ready to
		// receive synthetic paste and Enter input yet.
		args = append(args, launchOptions.InitialPrompt)
	}
	return command, args, notice, nil
}

func (h *terminalHandler) commandForTmuxTarget(
	item project.Project,
	thread project.Thread,
	tool string,
	windowName string,
	threadEndpoint string,
	sessionName string,
	launchOptions codingAgentLaunchOptions,
) (string, []string, string, error) {
	command, args, notice, err := commandFor(tool)
	if err != nil {
		return "", nil, "", err
	}
	if tool == "pi" && notice == "" {
		if h.piExtensionErr != nil {
			return "", nil, "", h.piExtensionErr
		}
		extensionArgs := make([]string, 0, len(h.piExtensionPaths)*2+len(args))
		for _, extensionPath := range h.piExtensionPaths {
			extensionArgs = append(extensionArgs, "--extension", extensionPath)
		}
		args = append(extensionArgs, args...)
	}
	if isClaudeCodingAgent(tool) && notice == "" {
		if h.claudePluginErr != nil {
			return "", nil, "", h.claudePluginErr
		}
		if h.claudePluginPath == "" {
			return "", nil, "", errors.New("Claude plugin path is unavailable")
		}
		pluginArguments := []string{"--plugin-dir", h.claudePluginPath}
		if tool == codingAgentClaudeGPT {
			if h.claudePluginRootErr != nil {
				return "", nil, "", h.claudePluginRootErr
			}
			if h.claudePluginRootPath == "" {
				return "", nil, "", errors.New("Claude plugin root is unavailable")
			}
			if h.claudeSandboxPluginErr != nil {
				return "", nil, "", h.claudeSandboxPluginErr
			}
			if h.claudeSandboxPluginPath == "" {
				return "", nil, "", errors.New("Claude sandbox plugin path is unavailable")
			}
			if launchOptions.Model == "" || !isCLIProxyAPIGPTModel(launchOptions.Model) {
				return "", nil, "", errors.New("Claude Code (with gpt) requires a CLIProxyAPI GPT model")
			}
			pluginArguments = append(pluginArguments, "--plugin-dir", h.claudeSandboxPluginPath)
		}
		pluginArguments = append(pluginArguments,
			"--dangerously-skip-permissions",
			"--settings", `{"skipDangerousModePermissionPrompt":true}`,
		)
		args = append(pluginArguments, args...)
	}

	environment := []string{
		"DIRE_MUX_TMUX_SESSION=" + sessionName,
		"DIRE_MUX_TMUX_WINDOW=" + windowName,
	}
	if (tool == "pi" || isClaudeCodingAgent(tool)) && threadEndpoint != "" {
		environment = append(environment, direMuxThreadEnvironment(threadEndpoint, item.ID, thread.ID)...)
	}
	// Dire Mux child and workflow execution is deliberately Pi Native. Do not
	// pass the broad managed-agent capability or relationship metadata to other
	// coding harnesses. Claude's browser MCP reads its capability from the
	// protected data directory.
	if tool == codingAgentPi && threadEndpoint != "" {
		if h.agentToken != "" {
			environment = append(environment, "DIRE_MUX_AGENT_TOKEN="+h.agentToken)
		}
		if thread.ParentThreadID != "" {
			environment = append(environment, "DIRE_MUX_PARENT_THREAD_ID="+thread.ParentThreadID)
		}
	}
	if isClaudeCodingAgent(tool) && notice == "" {
		piPath := codingAgentPi
		if resolvedPiPath, err := exec.LookPath(codingAgentPi); err == nil {
			piPath = resolvedPiPath
		}
		environment = append(environment,
			"DIRE_MUX_PI_PATH="+piPath,
			"DIRE_MUX_CODING_AGENT="+tool,
		)
	}
	if tool == codingAgentClaudeGPT && notice == "" {
		profilePath, err := h.claudeGPTProfileDirectory()
		if err != nil {
			return "", nil, "", err
		}
		baseURL, apiKey, err := h.cliProxyAPIConfiguration()
		if err != nil {
			return "", nil, "", err
		}
		unsetArguments := make([]string, 0, len(claudeGPTUnsetEnvironment)*2+len(environment))
		for _, name := range claudeGPTUnsetEnvironment {
			unsetArguments = append(unsetArguments, "-u", name)
		}
		environment = append(unsetArguments, environment...)
		environment = append(environment, claudeGPTProxyEnvironment(
			profilePath,
			h.claudePluginRootPath,
			baseURL,
			apiKey,
			launchOptions.Model,
		)...)
	}
	environment = append(environment, command)
	args = append(environment, args...)
	envPath := h.envPath
	if envPath == "" {
		envPath = "env"
	}
	return envPath, args, notice, nil
}

func (h *terminalHandler) shellWindows(item project.Project, thread project.Thread) ([]tmuxWindow, error) {
	sessionName, _, _, err := h.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		return nil, err
	}
	return h.tmuxWindows(sessionName)
}

// existingShellWindows observes shell state without creating a terminal
// session merely because a browser subscribed to status events.
func (h *terminalHandler) existingShellWindows(item project.Project, thread project.Thread) ([]tmuxWindow, error) {
	sessionName := tmuxSessionName(item.ID, thread.ID, "terminal")
	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []tmuxWindow{}, nil
	}
	return h.tmuxWindows(sessionName)
}

func (h *terminalHandler) newShellWindow(item project.Project, thread project.Thread) (windows []tmuxWindow, err error) {
	sessionName, _, _, err := h.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		return nil, err
	}
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return nil, mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return nil, err
	}
	defer func() {
		if fenceErr := h.finishTerminalThreadMutationLocked(item, thread); fenceErr != nil {
			windows = nil
			err = errors.Join(err, fenceErr)
		}
	}()
	return h.newTmuxWindow(thread.Cwd, sessionName, "terminal", "shell")
}

func (h *terminalHandler) activateShellWindow(item project.Project, thread project.Thread, index int) ([]tmuxWindow, error) {
	if index < 0 {
		return nil, errors.New("invalid shell window index")
	}

	sessionName, _, _, err := h.ensureTmuxSession(item, thread, "terminal")
	if err != nil {
		return nil, err
	}
	return h.activateTmuxWindow(sessionName, index)
}

func (h *terminalHandler) newTmuxWindow(cwd, sessionName, tool, windowName string) ([]tmuxWindow, error) {
	command, args, _, err := commandFor(tool)
	if err != nil {
		return nil, err
	}
	target, err := h.createTmuxWindow(cwd, sessionName, windowName, command, args, true)
	if err != nil {
		return nil, err
	}
	if tool != "terminal" {
		if err := h.configureSharedToolWindow(sessionName, target, windowName); err != nil {
			_ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID)
			return nil, err
		}
	}
	return h.tmuxWindows(sessionName)
}

func (h *terminalHandler) createTmuxWindow(cwd, sessionName, windowName, command string, args []string, selectWindow bool) (tmuxWindowTarget, error) {
	arguments := []string{"new-window"}
	if !selectWindow {
		arguments = append(arguments, "-d")
	}
	arguments = append(
		arguments,
		"-P", "-F", "#{window_index}\t#{window_id}\t#{pid}",
		"-t", exactTmuxCurrentWindowTarget(sessionName),
		"-c", cwd,
		"-n", windowName,
		shellCommand(command, args),
	)
	output, err := h.tmuxCommand(arguments...).CombinedOutput()
	if err != nil {
		return tmuxWindowTarget{}, tmuxCommandError("create tmux window", output, err)
	}
	return parseTmuxWindowTarget(output)
}

func (h *terminalHandler) activateTmuxWindow(sessionName string, index int) ([]tmuxWindow, error) {
	if index < 0 {
		return nil, errors.New("invalid tmux window index")
	}
	target := exactTmuxWindowTarget(sessionName, index)
	output, err := h.tmuxCommand("select-window", "-t", target).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError("select tmux window", output, err)
	}
	return h.tmuxWindows(sessionName)
}

func (h *terminalHandler) configureSharedToolWindow(sessionName string, target tmuxWindowTarget, tool string) error {
	targetName := target.ID
	options := [][2]string{
		{"remain-on-exit", "off"},
		{"automatic-rename", "off"},
		{"allow-rename", "off"},
		{"@dire-mux-tool", tool},
	}
	for _, option := range options {
		if err := h.setTmuxWindowOption(targetName, option[0], option[1]); err != nil {
			return err
		}
	}
	output, err := h.tmuxCommand("rename-window", "-t", targetName, tool).CombinedOutput()
	if err != nil {
		return tmuxCommandError("name tmux window", output, err)
	}
	return nil
}

func (h *terminalHandler) configureTmuxClipboard() error {
	output, err := h.tmuxCommand("set-option", "-s", "set-clipboard", "external").CombinedOutput()
	if err != nil {
		return tmuxCommandError("enable tmux clipboard forwarding", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxToolWindow(sessionName, tool string) (tmuxWindowTarget, bool, error) {
	output, err := h.tmuxCommand(
		"list-windows",
		"-t", exactTmuxSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_id}\t#{window_name}\t#{@dire-mux-tool}",
	).CombinedOutput()
	if err != nil {
		return tmuxWindowTarget{}, false, tmuxCommandError("find tmux window", output, err)
	}

	var namedTarget tmuxWindowTarget
	hasNamedTarget := false
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			return tmuxWindowTarget{}, false, fmt.Errorf("parse tmux tool window: %q", line)
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return tmuxWindowTarget{}, false, fmt.Errorf("parse tmux tool window index: %w", err)
		}
		target := tmuxWindowTarget{Index: index, ID: parts[1]}
		if parts[3] == tool {
			return target, true, nil
		}
		if !hasNamedTarget && parts[3] == "" && parts[2] == tool {
			namedTarget = target
			hasNamedTarget = true
		}
	}
	return namedTarget, hasNamedTarget, nil
}

func (h *terminalHandler) createTmuxViewSession(item project.Project, thread project.Thread, sourceSession string, sourceWindow tmuxWindowTarget) (viewSessionName string, err error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		return "", mutationErr
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	var viewServerPID string
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return "", err
	}
	defer func() {
		if fenceErr := h.finishTerminalThreadMutationLocked(item, thread); fenceErr != nil {
			if viewSessionName != "" {
				_ = h.killTmuxSessionIncarnation(viewSessionName, viewServerPID)
				h.unregisterTmuxViewLocked(viewSessionName)
			}
			viewSessionName = ""
			err = errors.Join(err, fenceErr)
		}
	}()
	viewSessionName, viewServerPID, err = h.createTmuxViewSessionLocked(sourceSession, sourceWindow)
	return viewSessionName, err
}

// createTmuxBrowserViewSession creates a temporary view for an arbitrary tmux
// session that may not belong to a Dire Mux project. Managed project views use
// createTmuxViewSession so their durable deletion fence remains authoritative.
func (h *terminalHandler) createTmuxBrowserViewSession(sourceSession string, sourceWindow tmuxWindowTarget) (string, error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	viewSessionName, _, err := h.createTmuxViewSessionLocked(sourceSession, sourceWindow)
	return viewSessionName, err
}

func (h *terminalHandler) createTmuxViewSessionLocked(sourceSession string, sourceWindow tmuxWindowTarget) (viewSessionName, viewServerPID string, err error) {
	// The view session contains a link to exactly one canonical tool window.
	// When that tool exits, tmux removes the linked window and closes the view,
	// while closing the browser merely removes the extra link.
	for attempt := 0; attempt < 5; attempt++ {
		viewName := fmt.Sprintf(tmuxViewSessionPrefix+"%d-%x-%d", os.Getpid(), time.Now().UnixNano(), h.viewCounter.Add(1))
		h.registerTmuxViewLocked(viewName)
		dummy, err := h.createTmuxSession(viewName, "/", "view", "/bin/sleep", []string{"60"})
		if err != nil {
			h.unregisterTmuxViewLocked(viewName)
			if exists, checkErr := h.tmuxSessionExists(viewName); checkErr == nil && exists {
				continue
			}
			return "", "", err
		}
		cleanup := func() {
			_ = h.killTmuxSessionIncarnation(viewName, dummy.ServerPID)
			h.unregisterTmuxViewLocked(viewName)
		}
		destinationIndex := dummy.Index + 1
		linkArgs := []string{
			"-s", sourceWindow.ID,
			"-t", exactTmuxWindowTarget(viewName, destinationIndex),
		}
		var output []byte
		switch {
		case sourceWindow.ProcessID != "":
			output, err = h.tmuxProcessCommand(sourceWindow, "link-window", linkArgs...)
		case sourceWindow.ServerPID != "":
			output, err = h.tmuxWindowCommand(sourceWindow, "link-window", linkArgs...)
		default:
			commandArgs := append([]string{"link-window"}, linkArgs...)
			output, err = h.tmuxCommand(commandArgs...).CombinedOutput()
		}
		if err != nil {
			cleanup()
			if sourceWindow.ProcessID != "" || sourceWindow.ServerPID != "" {
				return "", "", err
			}
			return "", "", tmuxCommandError("link tmux window", output, err)
		}
		output, err = h.tmuxCommand(
			"display-message", "-p",
			"-t", exactTmuxWindowTarget(viewName, destinationIndex),
			"#{window_id}",
		).CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != sourceWindow.ID {
			cleanup()
			if err != nil {
				return "", "", tmuxCommandError("verify tmux window link", output, err)
			}
			return "", "", errors.New("linked the wrong tmux window")
		}
		if err = h.killTmuxWindowIncarnation(dummy.ID, dummy.ServerPID); err != nil {
			cleanup()
			return "", "", fmt.Errorf("finish tmux terminal view: %w", err)
		}
		for option, value := range map[string]string{
			"@dire-mux-source-session": sourceSession,
			"@dire-mux-owner-pid":      strconv.Itoa(os.Getpid()),
		} {
			output, optionErr := h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(viewName), option, value).CombinedOutput()
			if optionErr != nil {
				cleanup()
				return "", "", tmuxCommandError("configure tmux terminal view", output, optionErr)
			}
		}
		return viewName, dummy.ServerPID, nil
	}
	return "", "", errors.New("could not allocate a tmux terminal view")
}

func (h *terminalHandler) closeTmuxViewSession(viewName string) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	_ = h.tmuxCommand("kill-session", "-t", exactTmuxSessionTarget(viewName)).Run()
	h.unregisterTmuxViewLocked(viewName)
}

func (h *terminalHandler) registerTmuxViewLocked(viewName string) {
	if h.activeViews == nil {
		h.activeViews = make(map[string]struct{})
	}
	h.activeViews[viewName] = struct{}{}
}

func (h *terminalHandler) unregisterTmuxViewLocked(viewName string) {
	delete(h.activeViews, viewName)
}

func (h *terminalHandler) tmuxViewIsActiveLocked(viewName string) bool {
	_, active := h.activeViews[viewName]
	return active
}

func (h *terminalHandler) setTmuxWindowOption(target, option, value string) error {
	output, err := h.tmuxCommand("set-option", "-w", "-t", target, option, value).CombinedOutput()
	if err != nil {
		return tmuxCommandError("configure tmux window", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxWindows(sessionName string) ([]tmuxWindow, error) {
	output, err := h.tmuxCommand(
		"list-windows",
		"-t", exactTmuxSessionTarget(sessionName),
		"-F", "#{window_index}\t#{window_name}\t#{window_active}",
	).CombinedOutput()
	if err != nil {
		return nil, tmuxCommandError("list tmux windows", output, err)
	}

	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	windows := make([]tmuxWindow, 0, len(lines))
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
		windows = append(windows, tmuxWindow{Index: index, Name: name, Active: parts[2] == "1"})
	}
	if len(windows) == 0 {
		return nil, errors.New("tmux session has no windows")
	}
	return windows, nil
}

func threadTmuxSessionNameSet(item project.Project, threadID string) map[string]struct{} {
	sessionNames := map[string]struct{}{
		tmuxSessionName(item.ID, threadID, "terminal"):         {},
		tmuxSessionName(item.ID, threadID, "process"):          {},
		previousTmuxSessionName(item.ID, threadID, "terminal"): {},
		previousTmuxSessionName(item.ID, threadID, "process"):  {},
	}
	if len(item.Threads) > 0 && item.Threads[0].ID == threadID {
		sessionNames[legacyProjectTmuxSessionName(item.ID, "terminal")] = struct{}{}
	}
	for _, tool := range []string{"nvim", "lazygit", "pi"} {
		sessionNames[legacyThreadTmuxSessionName(item.ID, threadID, tool)] = struct{}{}
		if len(item.Threads) > 0 && item.Threads[0].ID == threadID {
			sessionNames[legacyProjectTmuxSessionName(item.ID, tool)] = struct{}{}
		}
	}
	return sessionNames
}

func projectTmuxSessionNameSet(item project.Project) map[string]struct{} {
	sessionNames := make(map[string]struct{}, len(item.Threads)*5+len(threadSessionTools))
	for _, thread := range item.Threads {
		for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
			sessionNames[sessionName] = struct{}{}
		}
	}
	for _, tool := range threadSessionTools {
		sessionNames[legacyProjectTmuxSessionName(item.ID, tool)] = struct{}{}
	}
	return sessionNames
}

func exactTmuxSessionNames(sessionNames map[string]struct{}) []string {
	names := make([]string, 0, len(sessionNames))
	for sessionName := range sessionNames {
		names = append(names, sessionName)
	}
	return names
}

func exactTmuxSessionNameSet(sessionNames []string) map[string]struct{} {
	names := make(map[string]struct{}, len(sessionNames))
	for _, sessionName := range sessionNames {
		names[sessionName] = struct{}{}
	}
	return names
}

func projectThreadIDs(item project.Project) []string {
	threadIDs := make([]string, 0, len(item.Threads))
	for _, thread := range item.Threads {
		threadIDs = append(threadIDs, thread.ID)
	}
	return threadIDs
}

func (h *terminalHandler) stopThreadSessions(item project.Project, threadID string) (*terminalStopLease, error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if _, stopping := h.stoppingProjects[item.ID]; stopping {
		return nil, errTerminalStopping
	}
	key := terminalThreadKey{ProjectID: item.ID, ThreadID: threadID}
	if _, stopping := h.stoppingThreads[key]; stopping {
		return nil, errTerminalStopping
	}
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return nil, errors.New("terminal stop marker manager is unavailable")
	}
	sessionNames := threadTmuxSessionNameSet(item, threadID)
	lease, err := manager.beginThread(item.ID, threadID, exactTmuxSessionNames(sessionNames))
	if err != nil {
		return nil, err
	}
	if h.stoppingThreads == nil {
		h.stoppingThreads = make(map[terminalThreadKey]struct{})
	}
	h.stoppingThreads[key] = struct{}{}
	return lease, nil
}

func (h *terminalHandler) stopThreadSessionsLocked(item project.Project, threadID string) error {
	return h.stopNamedTmuxSessionsAndViews(threadTmuxSessionNameSet(item, threadID))
}

func (h *terminalHandler) cancelStopThread(projectID, threadID string, lease *terminalStopLease) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.unmarkThreadStoppingLocked(projectID, threadID)
	if lease == nil {
		return nil
	}
	return lease.Rollback()
}

func (h *terminalHandler) retainStopThread(projectID, threadID string, lease *terminalStopLease) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.unmarkThreadStoppingLocked(projectID, threadID)
	if lease == nil {
		return nil
	}
	return lease.Retain()
}

// resolveStopThreadStoreError decides a failed Store call from persisted
// state. The write may have been published before a later fsync/unlock error;
// only a still-present resource permits rolling its terminal marker back.
func (h *terminalHandler) resolveStopThreadStoreError(item project.Project, threadID string, lease *terminalStopLease) (published bool, err error) {
	exists, inspectErr := h.terminalStopResourceExists(terminalStopMarkerRef{
		Scope: terminalStopScopeThread, ProjectID: item.ID, ThreadID: threadID,
	})
	if inspectErr != nil {
		return false, errors.Join(inspectErr, h.retainStopThread(item.ID, threadID, lease))
	}
	if exists {
		return false, h.cancelStopThread(item.ID, threadID, lease)
	}
	return true, h.finishStopThread(item, threadID, lease)
}

func (h *terminalHandler) stopNamedTmuxSessionsAndViews(sessionNames map[string]struct{}) error {
	var stopErrors []error
	views, err := h.tmuxViewSessions()
	if err != nil {
		stopErrors = append(stopErrors, err)
	} else {
		for _, view := range views {
			sourceSession := view.SourceSession
			if sourceSession == "" {
				windows, windowErr := h.tmuxDetailedWindows(view.Name)
				if windowErr != nil {
					if exists, checkErr := h.tmuxExactSessionExists(view.Name); checkErr == nil && !exists {
						h.unregisterTmuxViewLocked(view.Name)
						continue
					}
					stopErrors = append(stopErrors, windowErr)
					continue
				}
				if len(windows) == 1 {
					sourceSession = tmuxSessionFromStartCommand(windows[0].StartCommand)
				}
				if sourceSession == "" && len(windows) == 1 {
					resolved, resolveErr := h.tmuxWindowSession(windows[0].Target.ID)
					if resolveErr != nil {
						stopErrors = append(stopErrors, resolveErr)
						continue
					}
					sourceSession = resolved
				}
			}
			if _, belongsToStoppedSession := sessionNames[sourceSession]; !belongsToStoppedSession {
				continue
			}
			output, killErr := h.tmuxCommand("kill-session", "-t", "="+view.Name).CombinedOutput()
			if killErr != nil {
				if exists, checkErr := h.tmuxExactSessionExists(view.Name); checkErr != nil || exists {
					stopErrors = append(stopErrors, tmuxCommandError("stop tmux terminal view", output, killErr))
					continue
				}
			}
			h.unregisterTmuxViewLocked(view.Name)
		}
	}

	for sessionName := range sessionNames {
		exists, checkErr := h.tmuxExactSessionExists(sessionName)
		if checkErr != nil {
			stopErrors = append(stopErrors, checkErr)
			continue
		}
		if !exists {
			continue
		}

		output, killErr := h.tmuxCommand("kill-session", "-t", "="+sessionName).CombinedOutput()
		if killErr == nil {
			continue
		}
		// A short-lived tool can exit between the existence check and kill.
		if stillExists, recheckErr := h.tmuxExactSessionExists(sessionName); recheckErr == nil && !stillExists {
			continue
		}
		stopErrors = append(stopErrors, tmuxCommandError("stop tmux session", output, killErr))
	}
	return errors.Join(stopErrors...)
}

func (h *terminalHandler) stopProjectSessions(item project.Project) (project.Project, *terminalStopLease, error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if _, stopping := h.stoppingProjects[item.ID]; stopping {
		return project.Project{}, nil, errTerminalStopping
	}
	for _, thread := range item.Threads {
		if _, stopping := h.stoppingThreads[terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}]; stopping {
			return project.Project{}, nil, errTerminalStopping
		}
	}
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return project.Project{}, nil, errors.New("terminal stop marker manager is unavailable")
	}
	lease, err := manager.beginProject(
		item.ID,
		projectThreadIDs(item),
		exactTmuxSessionNames(projectTmuxSessionNameSet(item)),
	)
	if err != nil {
		return project.Project{}, nil, err
	}
	if h.stoppingProjects == nil {
		h.stoppingProjects = make(map[string]struct{})
	}
	if h.stoppingThreads == nil {
		h.stoppingThreads = make(map[terminalThreadKey]struct{})
	}
	h.stoppingProjects[item.ID] = struct{}{}
	markedThreads := make([]string, 0, len(item.Threads))
	clearStopping := func() {
		delete(h.stoppingProjects, item.ID)
		for _, threadID := range markedThreads {
			h.unmarkThreadStoppingLocked(item.ID, threadID)
		}
	}
	rollback := func(operationErr error) (project.Project, *terminalStopLease, error) {
		clearStopping()
		return project.Project{}, nil, errors.Join(operationErr, lease.Rollback())
	}

	// The handler may have loaded item before another request finished adding a
	// thread. Once the project guard is visible, no terminal operation for any
	// such thread can create more tmux state, so refresh the exact set that must
	// be stopped before deleting the project.
	currentItem, err := h.projects.GetPersisted(item.ID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			// Store deletion won but its owner may have crashed before recording
			// the committed phase. Upgrade the retained marker before a stale
			// handler is allowed to proceed with an idempotent DELETE.
			if commitErr := lease.Commit(); commitErr != nil {
				clearStopping()
				return project.Project{}, nil, errors.Join(commitErr, lease.Retain())
			}
			currentItem = item
		} else if lease.Marker().Committed {
			clearStopping()
			return project.Project{}, nil, errors.Join(err, lease.Retain())
		} else {
			return rollback(err)
		}
	}
	item = currentItem
	if lease.Marker().Committed {
		// This is an idempotent DELETE through a stale Store. The committed
		// marker's exact recipe is immutable and remains the cleanup authority.
		for _, thread := range item.Threads {
			h.stoppingThreads[terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}] = struct{}{}
			markedThreads = append(markedThreads, thread.ID)
		}
		return item, lease, nil
	}
	if err := lease.RecheckProjectThreads(projectThreadIDs(item)); err != nil {
		clearStopping()
		return project.Project{}, nil, err
	}
	sessionNames := projectTmuxSessionNameSet(item)
	if err := lease.UpdateCleanupRecipe(projectThreadIDs(item), exactTmuxSessionNames(sessionNames)); err != nil {
		clearStopping()
		return project.Project{}, nil, err
	}
	for _, thread := range item.Threads {
		if _, stopping := h.stoppingThreads[terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}]; stopping {
			return rollback(errTerminalStopping)
		}
	}
	for _, thread := range item.Threads {
		h.stoppingThreads[terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}] = struct{}{}
		markedThreads = append(markedThreads, thread.ID)
	}
	return item, lease, nil
}

func (h *terminalHandler) cancelStopProject(item project.Project, lease *terminalStopLease) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.stoppingProjects, item.ID)
	for _, thread := range item.Threads {
		h.unmarkThreadStoppingLocked(item.ID, thread.ID)
	}
	if lease == nil {
		return nil
	}
	return lease.Rollback()
}

func (h *terminalHandler) retainStopProject(item project.Project, lease *terminalStopLease) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.stoppingProjects, item.ID)
	for _, thread := range item.Threads {
		h.unmarkThreadStoppingLocked(item.ID, thread.ID)
	}
	if lease == nil {
		return nil
	}
	return lease.Retain()
}

func (h *terminalHandler) resolveStopProjectStoreError(item project.Project, lease *terminalStopLease) (published bool, err error) {
	exists, inspectErr := h.terminalStopResourceExists(terminalStopMarkerRef{
		Scope: terminalStopScopeProject, ProjectID: item.ID,
	})
	if inspectErr != nil {
		return false, errors.Join(inspectErr, h.retainStopProject(item, lease))
	}
	if exists {
		return false, h.cancelStopProject(item, lease)
	}
	return true, h.finishStopProject(item, lease)
}

func (h *terminalHandler) finishStopThread(item project.Project, threadID string, lease *terminalStopLease) error {
	if lease == nil {
		return errors.New("terminal stop lease is required")
	}
	marker := lease.Marker()
	var identityErr error
	if marker.Scope != terminalStopScopeThread || marker.ProjectID != item.ID || marker.ThreadID != threadID {
		identityErr = errors.New("thread terminal stop lease identity mismatch")
	}
	h.sessionMu.Lock()
	var commitErr error
	var cleanupErr error
	if identityErr == nil {
		commitErr = lease.Commit()
		if commitErr == nil {
			if h.tmuxPath != "" {
				cleanupErr = h.stopNamedTmuxSessionsAndViews(exactTmuxSessionNameSet(marker.SessionNames))
			}
			if h.nativePi != nil {
				cleanupErr = errors.Join(cleanupErr, h.nativePi.removeThread(item.ID, threadID))
			}
			if h.nativeClaude != nil {
				cleanupErr = errors.Join(cleanupErr, h.nativeClaude.removeThread(item.ID, threadID))
			}
		}
	}
	h.unmarkThreadStoppingLocked(item.ID, threadID)
	h.sessionMu.Unlock()
	return errors.Join(identityErr, commitErr, cleanupErr, lease.Retain())
}

func (h *terminalHandler) finishStopProject(item project.Project, lease *terminalStopLease) error {
	if lease == nil {
		return errors.New("terminal stop lease is required")
	}
	marker := lease.Marker()
	var identityErr error
	if marker.Scope != terminalStopScopeProject || marker.ProjectID != item.ID || marker.ThreadID != "" {
		identityErr = errors.New("project terminal stop lease identity mismatch")
	}
	h.sessionMu.Lock()
	var commitErr error
	var cleanupErr error
	if identityErr == nil {
		commitErr = lease.Commit()
		if commitErr == nil {
			if h.tmuxPath != "" {
				cleanupErr = h.stopNamedTmuxSessionsAndViews(exactTmuxSessionNameSet(marker.SessionNames))
			}
			if h.nativePi != nil {
				cleanupErr = errors.Join(cleanupErr, h.nativePi.removeProject(item.ID))
			}
			if h.nativeClaude != nil {
				cleanupErr = errors.Join(cleanupErr, h.nativeClaude.removeProject(item.ID))
			}
		}
	}
	delete(h.stoppingProjects, item.ID)
	for _, thread := range item.Threads {
		h.unmarkThreadStoppingLocked(item.ID, thread.ID)
	}
	h.sessionMu.Unlock()
	return errors.Join(identityErr, commitErr, cleanupErr, lease.Retain())
}

// reconcileTerminalStops resolves unlocked markers left by an interrupted
// backend. Store is the commit oracle: an absent resource runs the exact
// persisted cleanup recipe and retains the committed tombstone. A pending
// marker whose resource still exists is ambiguous, so recovery preserves its
// safety fence for an explicit DELETE to adopt and finish. Valid refs are
// processed even when listMarkers also reports a separate malformed entry.
func (h *terminalHandler) reconcileTerminalStops() error {
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return errors.New("terminal stop marker manager is unavailable")
	}
	refs, listErr := manager.listMarkers()
	reconcileErrors := []error{listErr}
	for _, ref := range refs {
		_, err := h.reconcileTerminalStop(ref)
		if errors.Is(err, errTerminalStopping) {
			// Either a live deletion owns the sidecar flock, or an unlocked
			// pre-commit marker remains deliberately fenced for explicit retry.
			continue
		}
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"reconcile %s terminal stop for project %q thread %q: %w",
				ref.Scope,
				ref.ProjectID,
				ref.ThreadID,
				err,
			))
		}
	}
	return errors.Join(reconcileErrors...)
}

func (h *terminalHandler) terminalStopBrowserThreadIDs(ref terminalStopMarkerRef) ([]string, bool, error) {
	manager := h.durableTerminalStopManager()
	if manager == nil {
		return nil, false, errors.New("terminal stop marker manager is unavailable")
	}
	if ref.Scope == terminalStopScopeThread {
		marker, found, err := manager.readThread(ref.ProjectID, ref.ThreadID)
		if err != nil || !found {
			return nil, found, err
		}
		return []string{marker.ThreadID}, true, nil
	}
	marker, found, err := manager.readProject(ref.ProjectID)
	if err != nil || !found {
		return nil, found, err
	}
	return append([]string(nil), marker.ThreadIDs...), true, nil
}

// reconcileTerminalStop returns found=true whenever the exact marker path was
// present, including active, malformed, or cleanup-error states. DELETE uses
// that distinction to separate an idempotent committed retry from a true 404.
func (h *terminalHandler) reconcileTerminalStop(ref terminalStopMarkerRef) (found bool, err error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()

	manager := h.durableTerminalStopManager()
	if manager == nil {
		return false, errors.New("terminal stop marker manager is unavailable")
	}
	lease, found, err := manager.acquireExisting(ref)
	if err != nil || !found {
		return found, err
	}
	if lease == nil {
		return true, errors.New("terminal stop marker acquisition returned no lease")
	}

	marker := lease.Marker()
	if !marker.Committed {
		exists, inspectErr := h.terminalStopResourceExists(ref)
		if inspectErr != nil {
			return true, errors.Join(inspectErr, lease.Retain())
		}
		if exists {
			// An unlocked pending marker is ambiguous: its owner may have crashed
			// before Store deletion, or a stale backend may have resurrected a
			// resource after deletion. Never erase the safety fence from recovery.
			// An explicit DELETE can adopt this lease and finish the transaction.
			h.clearLocalTerminalStopLocked(ref)
			return true, errors.Join(errTerminalStopping, lease.Retain())
		}
		if commitErr := lease.Commit(); commitErr != nil {
			return true, errors.Join(commitErr, lease.Retain())
		}
		marker = lease.Marker()
	}

	var cleanupErr error
	if h.tmuxPath != "" {
		cleanupErr = h.stopNamedTmuxSessionsAndViews(exactTmuxSessionNameSet(marker.SessionNames))
	}
	h.clearLocalTerminalStopLocked(ref)
	return true, errors.Join(cleanupErr, lease.Retain())
}

func (h *terminalHandler) clearLocalTerminalStopLocked(ref terminalStopMarkerRef) {
	switch ref.Scope {
	case terminalStopScopeProject:
		delete(h.stoppingProjects, ref.ProjectID)
		for key := range h.stoppingThreads {
			if key.ProjectID == ref.ProjectID {
				delete(h.stoppingThreads, key)
			}
		}
	case terminalStopScopeThread:
		h.unmarkThreadStoppingLocked(ref.ProjectID, ref.ThreadID)
	}
}

func (h *terminalHandler) tmuxSessionExists(sessionName string) (bool, error) {
	err := h.tmuxCommand("has-session", "-t", exactTmuxSessionTarget(sessionName)).Run()
	if err == nil {
		return true, nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return false, nil
	}
	return false, fmt.Errorf("check tmux session: %w", err)
}

func (h *terminalHandler) tmuxExactSessionExists(sessionName string) (bool, error) {
	err := h.tmuxCommand("has-session", "-t", exactTmuxSessionTarget(sessionName)).Run()
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return false, nil
	}
	return false, fmt.Errorf("check exact tmux session: %w", err)
}

func (h *terminalHandler) createTmuxSession(sessionName, directory, windowName, command string, args []string) (tmuxWindowTarget, error) {
	if h.tmuxSocketMigration != nil {
		if err := h.tmuxSocketMigration.prepareServerStart(); err != nil {
			return tmuxWindowTarget{}, err
		}
	}
	output, err := h.tmuxCommand(
		"new-session",
		"-d",
		"-P", "-F", "#{window_index}\t#{window_id}\t#{pid}",
		"-s", sessionName,
		"-c", directory,
		"-n", windowName,
		shellCommand(command, args),
	).CombinedOutput()
	if err != nil {
		return tmuxWindowTarget{}, tmuxCommandError("create tmux session", output, err)
	}
	target, err := parseTmuxWindowTarget(output)
	if err != nil {
		// The creation output is the only atomic proof of this incarnation. If
		// it cannot be parsed, a name-based cleanup could kill a replacement.
		return tmuxWindowTarget{}, err
	}

	output, err = h.tmuxCommand("set-option", "-t", exactTmuxCurrentWindowTarget(sessionName), "status", "off").CombinedOutput()
	if err == nil {
		return target, nil
	}
	_ = h.killTmuxSessionIncarnation(sessionName, target.ServerPID)
	return tmuxWindowTarget{}, tmuxCommandError("configure tmux session", output, err)
}

func (h *terminalHandler) notifyThreadStatusChanged(projectID, threadID string) {
	h.publishThreadStatusChanged(projectID, threadID)
}

func (h *terminalHandler) tmuxCommand(args ...string) *exec.Cmd {
	arguments, verbose := h.tmuxCommandArguments(args...)
	command := exec.Command(h.tmuxPath, arguments...)
	h.configureTmuxCommand(command, verbose)
	return command
}

func (h *terminalHandler) tmuxCommandArguments(args ...string) ([]string, bool) {
	// tmux creates a separate tmux-client-PID.log for every process passed -v.
	// Inspection commands run as often as once per second, so logging all of
	// them would create an unbounded stream of tiny files. new-session starts a
	// missing server with verbose server logging, while attach-session covers
	// the long-lived terminal and control clients involved when a server dies.
	verbose := h.tmuxLogDirectory != "" && tmuxCommandNeedsVerboseLogging(args)
	arguments := make([]string, 0, len(args)+3)
	if verbose {
		arguments = append(arguments, "-v")
	}
	arguments = append(arguments, "-L", h.tmuxSocket)
	arguments = append(arguments, args...)
	return arguments, verbose
}

func (h *terminalHandler) configureTmuxCommand(command *exec.Cmd, verbose bool) {
	command.Env = tmuxEnvironment()
	if verbose {
		// tmux has no log-directory option; native logs are always opened in
		// the client's working directory, which the daemon also inherits.
		command.Dir = h.tmuxLogDirectory
	}
}

func tmuxCommandNeedsVerboseLogging(args []string) bool {
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		switch argument {
		case "new-session", "attach-session":
			return true
		default:
			return false
		}
	}
	return false
}

func prepareTmuxLogDirectory(dataDirectory, tmuxSocket string) (string, error) {
	directory := filepath.Join(dataDirectory, tmuxLogDirectoryName, tmuxSocket)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create tmux log directory: %w", err)
	}
	return directory, nil
}

func exactTmuxSessionTarget(sessionName string) string {
	return "=" + sessionName
}

func exactTmuxCurrentWindowTarget(sessionName string) string {
	return exactTmuxSessionTarget(sessionName) + ":"
}

func exactTmuxWindowTarget(sessionName string, index int) string {
	return exactTmuxSessionTarget(sessionName) + ":" + strconv.Itoa(index)
}

func tmuxSessionName(projectID, threadID, tool string) string {
	return tmuxSessionNamePrefix + projectID + "-" + threadID + "-" + tmuxSessionSuffix(tool)
}

func previousTmuxSessionName(projectID, threadID, tool string) string {
	return legacyTmuxSessionPrefix + projectID + "-" + threadID + "-" + tmuxSessionSuffix(tool)
}

func tmuxSessionSuffix(tool string) string {
	switch tool {
	case "", "terminal":
		return "terminal"
	case "nvim", "lazygit", "pi", "process":
		return "tools"
	default:
		return tool
	}
}

func threadEndpointURL(r *http.Request, projectID, threadID string) string {
	scheme := "http"
	host := r.Host
	if r.TLS != nil {
		scheme = "https"
	} else if localAddress, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		host = localAddress.String()
		addressHost, port, err := net.SplitHostPort(host)
		if err == nil && (addressHost == "" || addressHost == "0.0.0.0" || addressHost == "::") {
			host = net.JoinHostPort("127.0.0.1", port)
		}
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/threads/" + url.PathEscape(threadID)
	return scheme + "://" + host + path
}

func direMuxThreadEnvironment(threadEndpoint, projectID, threadID string) []string {
	environment := []string{
		"DIRE_MUX_THREAD_ENDPOINT=" + threadEndpoint,
		"DIRE_MUX_PROJECT_ID=" + projectID,
		"DIRE_MUX_THREAD_ID=" + threadID,
	}
	if endpoint, err := url.Parse(threadEndpoint); err == nil {
		port := endpoint.Port()
		if port == "" {
			switch endpoint.Scheme {
			case "http":
				port = "80"
			case "https":
				port = "443"
			}
		}
		if port != "" {
			environment = append(environment, "DIRE_MUX_PORT="+port)
		}
		if endpoint.Scheme != "" {
			environment = append(environment, "DIRE_MUX_SCHEME="+endpoint.Scheme)
		}
	}
	return environment
}

func parseTmuxWindowTarget(output []byte) (tmuxWindowTarget, error) {
	line := strings.TrimSpace(string(output))
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 || parts[1] == "" {
		return tmuxWindowTarget{}, fmt.Errorf("parse tmux window target: %q", line)
	}
	index, err := strconv.Atoi(parts[0])
	if err != nil {
		return tmuxWindowTarget{}, fmt.Errorf("parse tmux window target index: %w", err)
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil {
		return tmuxWindowTarget{}, fmt.Errorf("parse tmux window target server pid: %w", err)
	}
	if pid <= 0 {
		return tmuxWindowTarget{}, fmt.Errorf("parse tmux window target server pid: invalid value %q", parts[2])
	}
	return tmuxWindowTarget{Index: index, ID: parts[1], ServerPID: parts[2]}, nil
}

func parseTmuxPaneIncarnation(output []byte) (codingAgentPaneIncarnation, error) {
	line := strings.TrimSpace(string(output))
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 || parts[0] == "" {
		return codingAgentPaneIncarnation{}, fmt.Errorf("parse tmux pane incarnation: %q", line)
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		return codingAgentPaneIncarnation{}, fmt.Errorf("parse tmux pane server pid: %w", err)
	}
	if pid <= 0 {
		return codingAgentPaneIncarnation{}, fmt.Errorf("parse tmux pane server pid: invalid value %q", parts[1])
	}
	return codingAgentPaneIncarnation{PaneID: parts[0], ServerPID: parts[1]}, nil
}

func tmuxCommandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s", action, message)
}

func shellCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(command))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func normalizeTerminalTool(tool string) (string, error) {
	switch tool {
	case "", "terminal":
		return "terminal", nil
	case "nvim", "lazygit", "pi", "process":
		return tool, nil
	default:
		return "", errors.New("unknown terminal tool")
	}
}

func normalizeCodingAgent(agent string) (string, error) {
	switch agent {
	case "", codingAgentPi:
		return codingAgentPi, nil
	case codingAgentClaude:
		return codingAgentClaude, nil
	case codingAgentClaudeGPT:
		return codingAgentClaudeGPT, nil
	default:
		return "", errors.New("unknown coding agent")
	}
}

func isClaudeCodingAgent(agent string) bool {
	return agent == codingAgentClaude || agent == codingAgentClaudeGPT
}

func commandFor(tool string) (string, []string, string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if isClaudeCodingAgent(tool) {
		path, err := exec.LookPath(codingAgentClaude)
		if err == nil {
			return path, nil, "", nil
		}
		notice := "\r\n\x1b[38;5;214mClaude Code is not installed or not on PATH. Opened a shell instead.\x1b[0m\r\n\r\n"
		return shell, []string{"-l"}, notice, nil
	}

	tool, err := normalizeTerminalTool(tool)
	if err != nil {
		return "", nil, "", err
	}

	switch tool {
	case "terminal", "process":
		return shell, []string{"-l"}, "", nil
	case "nvim", "lazygit", "pi":
		path, err := exec.LookPath(tool)
		if err == nil {
			if tool == "nvim" {
				return path, []string{"."}, "", nil
			}
			return path, nil, "", nil
		}
		notice := fmt.Sprintf("\r\n\x1b[38;5;214m%s is not installed or not on PATH. Opened a shell instead.\x1b[0m\r\n\r\n", tool)
		return shell, []string{"-l"}, notice, nil
	}

	return "", nil, "", errors.New("unknown terminal tool")
}

func boundedDimension(raw string, fallback uint16) uint16 {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 2 || value > 1000 {
		return fallback
	}
	return uint16(value)
}

func terminalEnvironment() []string {
	overrides := map[string]string{
		"COLORTERM":    "truecolor",
		"TERM":         "xterm-256color",
		"TERM_PROGRAM": "dire-mux",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func tmuxEnvironment() []string {
	environment := terminalEnvironment()
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
