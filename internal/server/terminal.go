package server

import (
	"context"
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
	"github.com/dire-kiwi/kiwi-code/internal/agent"
	"github.com/dire-kiwi/kiwi-code/internal/agent/tmuxpane"
	"github.com/dire-kiwi/kiwi-code/internal/datadir"
	"github.com/dire-kiwi/kiwi-code/internal/durable"
	"github.com/dire-kiwi/kiwi-code/internal/events"
	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/thread"
	"github.com/dire-kiwi/kiwi-code/internal/tmux"
	"github.com/dire-kiwi/kiwi-code/internal/usage"
	"github.com/dire-kiwi/kiwi-code/internal/workspace"
	"github.com/gorilla/websocket"
)

type terminalHandler struct {
	projects              *project.Store
	tmuxPath              string
	tmuxSocket            string
	piExtensionPaths      []string
	piExtensionErr        error
	piFigmaExtensionPath  string
	piFigmaExtensionErr   error
	piModelMu             sync.Mutex
	piModelCache          map[string]piModelCapabilityCacheEntry
	piModelInflight       map[string]*piModelCapabilityInflight
	agentToken            string
	agentTokenPath        string
	agentTokenErr         error
	nativePi              *piNativeManager
	nativeClaude          *claudeNativeManager
	codexPlugin           codexPluginInstallation
	codexPluginErr        error
	codexConfigPath       string
	codexConfigErr        error
	codexProfileName      string
	codexPluginMu         sync.Mutex
	codexPluginPrepared   bool
	codexPluginPrepareErr error
	claudePluginPath      string
	claudePluginErr       error
	claudeConfigPath      string
	claudeConfigErr       error
	claudePluginRootPath  string
	claudePluginRootErr   error
	claudeGPTProfilePath  string
	claudeGPTProfileErr   error
	cliProxyAPIBaseURL    string
	cliProxyAPIKey        string
	cliProxyAPIErr        error
	cliProxyAPIHTTPClient *http.Client
	envPath               string
	sessionMu             sync.Mutex
	terminalStops         *terminalStopManager
	terminalMutations     *terminalMutationManager
	stoppingProjects      map[string]struct{}
	stoppingThreads       map[terminalThreadKey]struct{}
	viewCounter           atomic.Uint64
	activeSetups          atomic.Int64
	viewMu                sync.Mutex
	activeViews           map[string]struct{}
	viewReconciledAt      map[terminalThreadKey]time.Time
	tmuxWatchMu           sync.Mutex
	tmuxWatches           map[string]*tmuxSessionWatch
	tmuxWatchesStopped    bool
	tmuxWatchesStopDone   chan struct{}
	agentWatchMu          sync.Mutex
	agentWatches          map[codingAgentWatchKey]struct{}
	agentExits            map[codingAgentExitKey]tmuxPaneExitState
	agentExitLogs         map[codingAgentExitKey]struct{}
	agentExitSuppressed   map[codingAgentExitKey]tmuxPaneExitState
	agentExitMarkerMu     sync.Mutex
	agentExitDirectory    string
	exitStoreOnce         sync.Once
	exitStoreValue        *tmuxpane.ExitStore
	stateChanges          *events.Bus
	usage                 *usage.Tracker
	upgrader              websocket.Upgrader
}

const (
	tmuxSocketName                    = workspace.SocketName
	tmuxSessionNamePrefix             = workspace.SessionPrefix
	terminalAgentPollInterval         = time.Second
	terminalViewCreationGrace         = 2 * time.Second
	codingAgentPi                     = "pi"
	codingAgentCodex                  = "codex"
	codingAgentClaude                 = "claude"
	codingAgentClaudeGPT              = "claude-gpt"
	codingAgentClaudeProfilePrefix    = "claude-profile-"
	codingAgentClaudeGPTProfilePrefix = "claude-gpt-profile-"
	maxClaudeCodeProfileAgentIDLength = 64
)

var threadSessionTools = [...]string{"terminal", "nvim", "lazygit", "pi"}

type tmuxWindow = workspace.Window

type tmuxWindowTarget = tmux.WindowTarget

type tmuxAgentPane = workspace.AgentPane

type tmuxViewSession = workspace.ViewSession

type tmuxDetailedWindow = workspace.DetailedWindow

type tmuxPaneExitState = workspace.PaneExitState

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

type terminalThreadKey = thread.Key

type codingAgentExitMarker = tmuxpane.ExitMarker

type codingAgentPaneIncarnation struct {
	PaneID    string
	ServerPID string
}

var (
	errCodingAgentEnded        = errors.New("coding agent ended")
	errEnvironmentSetupPending = errors.New("environment setup has not completed")
	errTerminalStopping        = durable.ErrStopping
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
	return newTerminalHandlerUnreconciledWithDependencies(projects, policy, tmuxSocket, nil, nil)
}

// newTerminalHandlerUnreconciledWithDependencies wires the handler's outward
// dependencies at construction: bus carries state invalidations to the HTTP
// layer's topics, usageTracker records agent usage and answers budget checks.
// Both may be nil in tests that exercise tmux behavior only.
func newTerminalHandlerUnreconciledWithDependencies(
	projects *project.Store,
	policy originPolicy,
	tmuxSocket string,
	bus *events.Bus,
	usageTracker *usage.Tracker,
) *terminalHandler {
	tmuxPath, _ := exec.LookPath("tmux")
	envPath, _ := exec.LookPath("env")
	extensionPaths, extensionErr := materializePiExtensions(projects.DataDirectory())
	// The Figma bridge is materialized separately from the always-on extensions
	// so it only loads for projects that enabled Figma MCP support.
	figmaExtensionPath, figmaExtensionErr := materializePiFigmaMCPExtension(projects.DataDirectory())
	agentToken, agentTokenErr := loadOrCreateAgentToken(projects.DataDirectory())
	codexPlugin, codexPluginErr := materializeCodexPlugin(projects.DataDirectory())
	codexConfigPath, codexConfigErr := defaultCodexConfigDirectory()
	claudePluginPath, claudePluginErr := materializeClaudePlugin(projects.DataDirectory())
	claudeConfigPath, claudeConfigErr := defaultClaudeConfigDirectory()
	claudePluginRootPath, claudePluginRootErr := defaultClaudePluginDirectory(claudeConfigPath)
	claudeGPTProfilePath, claudeGPTProfileErr := prepareClaudeGPTProfileDirectory(projects.DataDirectory())
	cliProxyAPIBaseURL, cliProxyAPIKey, cliProxyAPIErr := configuredCLIProxyAPI()
	handler := &terminalHandler{
		projects:             projects,
		tmuxPath:             tmuxPath,
		tmuxSocket:           tmuxSocket,
		piExtensionPaths:     extensionPaths,
		piExtensionErr:       extensionErr,
		piFigmaExtensionPath: figmaExtensionPath,
		piFigmaExtensionErr:  figmaExtensionErr,
		agentToken:           agentToken,
		agentTokenPath:       filepath.Join(projects.DataDirectory(), agentTokenFileName),
		agentTokenErr:        agentTokenErr,
		nativePi: newPiNativeManager(
			projects.DataDirectory(),
			extensionPaths,
			extensionErr,
			agentToken,
			figmaExtensionPath,
		),
		nativeClaude: newClaudeNativeManager(
			projects.DataDirectory(),
			claudePluginPath,
			claudePluginErr,
		),
		codexPlugin:          codexPlugin,
		codexPluginErr:       codexPluginErr,
		codexConfigPath:      codexConfigPath,
		codexConfigErr:       codexConfigErr,
		codexProfileName:     managedCodexProfileName(projects.DataDirectory()),
		claudePluginPath:     claudePluginPath,
		claudePluginErr:      claudePluginErr,
		claudeConfigPath:     claudeConfigPath,
		claudeConfigErr:      claudeConfigErr,
		claudePluginRootPath: claudePluginRootPath,
		claudePluginRootErr:  claudePluginRootErr,
		claudeGPTProfilePath: claudeGPTProfilePath,
		claudeGPTProfileErr:  claudeGPTProfileErr,
		cliProxyAPIBaseURL:   cliProxyAPIBaseURL,
		cliProxyAPIKey:       cliProxyAPIKey,
		cliProxyAPIErr:       cliProxyAPIErr,
		envPath:              envPath,
		terminalStops:        newTerminalStopManager(projects.DataDirectory()),
		terminalMutations:    newTerminalMutationManager(projects.DataDirectory()),
		agentExitDirectory: filepath.Join(
			projects.DataDirectory(),
			datadir.CodingAgentExitsDirectoryName,
		),
		stateChanges: bus,
		usage:        usageTracker,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     policy.allows,
		},
	}
	handler.nativePi.figmaMCPURL = handler.figmaMCPURLForProject
	handler.nativeClaude.figmaMCPURL = handler.figmaMCPURLForProject
	if usageTracker != nil {
		handler.nativePi.usageReporter = func(key piNativeProcessKey, sessionID string, totals threadUsageTotals) {
			if err := usageTracker.Report(key.ProjectID, key.ThreadID, sessionID, totals); err != nil {
				log.Printf("record native Pi usage: project=%q thread=%q error=%v", key.ProjectID, key.ThreadID, err)
			}
		}
		handler.nativeClaude.usageReporter = func(key piNativeProcessKey, sessionID string, totals threadUsageTotals) {
			if err := usageTracker.Report(key.ProjectID, key.ThreadID, sessionID, totals); err != nil {
				log.Printf("record native Claude usage: project=%q thread=%q error=%v", key.ProjectID, key.ThreadID, err)
			}
		}
	}
	return handler
}

// budgetReached reports whether the thread's usage budget is exhausted. With
// no usage tracker wired (tmux-only tests) budgets never trip.
func (h *terminalHandler) budgetReached(projectID, threadID string) (bool, string, error) {
	if h.usage == nil || h.projects == nil {
		return false, "", nil
	}
	item, thread, err := h.projects.GetThread(projectID, threadID)
	if err != nil {
		return false, "", err
	}
	reached, sourceID := h.usage.BudgetReached(item, thread.ID)
	return reached, sourceID, nil
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
	if project.EnvironmentSetupBlocksAgent(thread) {
		writeError(w, http.StatusConflict, "Wait for the environment setup to finish before starting a coding agent.")
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
	if err := h.validateCodingAgentConfiguration(agent); err != nil {
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
		writeError(w, http.StatusServiceUnavailable, "tmux is required for terminal coding agents. Install tmux and restart kiwi-code.")
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

const (
	environmentSetupCompletedCloseReason = "Environment setup completed"
	environmentSetupFailedCloseReason    = "Environment setup failed"
)

func (h *terminalHandler) serveEnvironmentSetupTerminal(w http.ResponseWriter, r *http.Request, item project.Project, thread project.Thread) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "The environment setup terminal requires a WebSocket connection.")
		return
	}
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
	if !project.EnvironmentSetupBlocksAgent(thread) {
		_ = writer.Close(websocket.CloseNormalClosure, environmentSetupCompletedCloseReason)
		return
	}
	if _, _, required := project.ResolveEnvironmentSetup(item, thread); !required {
		if _, updateErr := h.projects.SetThreadEnvironmentSetupStatus(item.ID, thread.ID, project.EnvironmentSetupSucceeded); updateErr != nil {
			closeWithError("Could not save the environment setup state")
			return
		}
		h.notifyThreadStatusChanged(item.ID, thread.ID)
		_ = writer.Close(websocket.CloseNormalClosure, environmentSetupCompletedCloseReason)
		return
	}

	sessionName, target, paneID, created, err := h.ensureEnvironmentSetupSession(item, thread)
	if err != nil {
		closeWithError("Could not start the environment setup")
		return
	}
	if created || thread.EnvironmentSetupStatus == project.EnvironmentSetupPending {
		updated, updateErr := h.projects.SetThreadEnvironmentSetupStatus(item.ID, thread.ID, project.EnvironmentSetupRunning)
		if updateErr != nil {
			closeWithError("Could not save the environment setup state")
			return
		}
		thread = updated
		h.notifyThreadStatusChanged(item.ID, thread.ID)
	}
	if created {
		h.wakeThreadTmuxWatchers(item.ID, thread.ID)
	}

	viewSessionName, err := h.createTmuxViewSession(item, thread, sessionName, target)
	if err != nil {
		closeWithError("Could not create the environment setup terminal view")
		return
	}
	defer h.closeTmuxViewSession(viewSessionName)
	cols := boundedDimension(r.URL.Query().Get("cols"), 80)
	rows := boundedDimension(r.URL.Query().Get("rows"), 24)
	cmd := h.tmuxCommand(
		"set-option", "-q", "-s", "set-clipboard", "external",
		";",
		"attach-session", "-c", thread.Cwd,
		"-t", exactTmuxSessionTarget(viewSessionName),
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		closeWithError("Could not attach to the environment setup terminal")
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	protocol := 1
	if r.URL.Query().Get("protocol") == strconv.Itoa(terminalProtocolV2) {
		protocol = terminalProtocolV2
	}
	if protocol >= terminalProtocolV2 {
		if err := writer.Write(websocket.TextMessage, []byte(`{"type":"terminal_ready","protocol":2}`)); err != nil {
			return
		}
	}
	bridge := startPTYWebSocketBridge(connection, writer, ptmx, protocol, func() {})
	defer bridge.Stop()
	defer func() { _ = h.markTmuxSessionUsed(sessionName, time.Now()) }()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			state, stateErr := h.tmuxPaneExitState(paneID)
			if stateErr != nil {
				closeWithError("Could not inspect the environment setup")
				return
			}
			if !state.Found || !state.Dead {
				continue
			}
			status := project.EnvironmentSetupFailed
			closeReason := environmentSetupFailedCloseReason
			if state.Status == "0" && state.Signal == "" {
				status = project.EnvironmentSetupSucceeded
				closeReason = environmentSetupCompletedCloseReason
			}
			if _, updateErr := h.projects.SetThreadEnvironmentSetupStatus(item.ID, thread.ID, status); updateErr != nil {
				closeWithError("Could not save the environment setup result")
				return
			}
			h.notifyThreadStatusChanged(item.ID, thread.ID)
			_ = writer.Close(websocket.CloseNormalClosure, closeReason)
			return
		case <-bridge.Done:
			return
		case <-bridge.Peer.Done:
			return
		}
	}
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
	if tool == "pi" && r.URL.Query().Get("environmentSetup") == "1" {
		h.serveEnvironmentSetupTerminal(w, r, item, thread)
		return
	}
	if tool == "pi" && project.EnvironmentSetupBlocksAgent(thread) {
		writeError(w, http.StatusConflict, "Wait for the environment setup to finish before starting a coding agent.")
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
		if err := h.validateCodingAgentConfiguration(codingAgent); err != nil {
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
	protocol := 1
	if r.URL.Query().Get("protocol") == strconv.Itoa(terminalProtocolV2) {
		protocol = terminalProtocolV2
	}
	diagnostics := newTerminalConnectionDiagnostics(r, item.ID, thread.ID, tool)
	diagnostics.mark("accepted")
	defer diagnostics.finish()

	// Upgrade before creating or attaching tmux. A rejected origin or failed
	// handshake must not leave behind a session or temporary view.
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1 << 20)
	h.activeSetups.Add(1)
	setupActive := true
	defer func() {
		if setupActive {
			h.activeSetups.Add(-1)
		}
	}()
	diagnostics.mark("websocket-upgraded")

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
			&target,
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
	diagnostics.mark("canonical-target-ready")
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

	defer func() { _ = h.markTmuxSessionUsed(sessionName, time.Now()) }()

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
	diagnostics.mark("view-ready")

	cols := boundedDimension(r.URL.Query().Get("cols"), 80)
	rows := boundedDimension(r.URL.Query().Get("rows"), 24)
	cmd := h.tmuxCommand(
		"set-option", "-q", "-s", "set-clipboard", "external",
		";",
		"attach-session", "-c", thread.Cwd,
		"-t", exactTmuxSessionTarget(attachSessionName),
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		log.Printf("attach tmux terminal: project=%q thread=%q tool=%q error=%v", item.ID, thread.ID, tool, err)
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
	diagnostics.mark("pty-attached")

	if protocol >= terminalProtocolV2 {
		if err := writer.Write(websocket.TextMessage, []byte(`{"type":"terminal_ready","protocol":2}`)); err != nil {
			return
		}
		diagnostics.mark("ready-sent")
	}
	if created && notice != "" {
		messageType := websocket.BinaryMessage
		payload := []byte(notice)
		if protocol >= terminalProtocolV2 {
			messageType = websocket.TextMessage
			payload, _ = json.Marshal(map[string]string{"type": "terminal_output", "data": notice})
		}
		if err := writer.Write(messageType, payload); err != nil {
			return
		}
	}
	bridge := startPTYWebSocketBridge(connection, writer, ptmx, protocol, func() {
		diagnostics.mark("first-output")
	})
	defer bridge.Stop()
	setupActive = false
	h.activeSetups.Add(-1)
	// Once tmux is attached, update cleanup bookkeeping without holding up the
	// WebSocket-ready signal or the first PTY output.
	if err := h.markTmuxSessionUsed(sessionName, time.Now()); err != nil {
		log.Printf("record tmux session use: session=%q error=%v", sessionName, err)
	}
	var agentPoll <-chan time.Time
	var agentPollTicker *time.Ticker
	if agentPaneID != "" {
		agentPollTicker = time.NewTicker(terminalAgentPollInterval)
		agentPoll = agentPollTicker.C
		defer agentPollTicker.Stop()
	}
	for {
		select {
		case <-bridge.Done:
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
		case <-bridge.Peer.Done:
			return
		case <-bridge.Peer.Ping.C:
			if err := bridge.Peer.WritePing(); err != nil {
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
		case message := <-bridge.Peer.Messages:
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
		writeError(w, http.StatusServiceUnavailable, "tmux is required for persistent terminal sessions. Install tmux and restart kiwi-code.")
		return project.Project{}, project.Thread{}, false
	}
	return item, thread, true
}

func (h *terminalHandler) ensureEnvironmentSetupSession(item project.Project, thread project.Thread) (sessionName string, target tmuxWindowTarget, paneID string, created bool, err error) {
	command, args, required := h.environmentSetupLaunchCommand(item, thread)
	if !required {
		return "", tmuxWindowTarget{}, "", false, errors.New("environment setup is not configured")
	}
	sessionName = tmuxSessionName(item.ID, thread.ID, "pi")

	h.sessionMu.Lock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		h.sessionMu.Unlock()
		return "", tmuxWindowTarget{}, "", false, mutationErr
	}
	if activeErr := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); activeErr != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return "", tmuxWindowTarget{}, "", false, errors.Join(activeErr, releaseErr)
	}
	h.sessionMu.Unlock()
	defer func() {
		releaseErr := mutation.Release()
		h.sessionMu.Lock()
		fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
		h.sessionMu.Unlock()
		err = errors.Join(err, releaseErr, fenceErr)
	}()

	if err := h.reconcileThreadTmuxStateUnderLease(item, thread); err != nil {
		return "", tmuxWindowTarget{}, "", false, err
	}
	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil {
		return "", tmuxWindowTarget{}, "", false, err
	}
	if !exists {
		target, err = h.createTmuxSession(sessionName, thread.Cwd, "pi", command, args)
		if err != nil {
			return "", tmuxWindowTarget{}, "", false, err
		}
		created = true
		if err = h.configureSharedToolWindow(sessionName, target, "pi"); err != nil {
			_ = h.killTmuxSessionIncarnation(sessionName, target.ServerPID)
			return "", tmuxWindowTarget{}, "", false, err
		}
	} else {
		var found bool
		target, found, err = h.tmuxToolWindow(sessionName, "pi")
		if err != nil {
			return "", tmuxWindowTarget{}, "", false, err
		}
		if !found {
			target, err = h.createTmuxWindow(thread.Cwd, sessionName, "pi", command, args, false)
			if err != nil {
				return "", tmuxWindowTarget{}, "", false, err
			}
			created = true
			if err = h.configureSharedToolWindow(sessionName, target, "pi"); err != nil {
				_ = h.killTmuxWindowIncarnation(target.ID, target.ServerPID)
				return "", tmuxWindowTarget{}, "", false, err
			}
		}
	}

	panes, err := h.tmuxAgentPanes(target.ID)
	if err != nil {
		return "", tmuxWindowTarget{}, "", false, err
	}
	for _, pane := range panes {
		if pane.Agent == environmentSetupAgent {
			return sessionName, target, pane.ID, created, nil
		}
	}
	if thread.EnvironmentSetupStatus == project.EnvironmentSetupFailed {
		return "", tmuxWindowTarget{}, "", false, errors.New("failed environment setup pane is unavailable")
	}
	if created {
		paneID = panes[0].ID
	} else {
		output, splitErr := h.tmuxCommand(
			"split-window", "-d", "-P", "-F", "#{pane_id}\t#{pid}",
			"-t", target.ID, "-c", thread.Cwd, shellCommand(command, args),
		).CombinedOutput()
		if splitErr != nil {
			return "", tmuxWindowTarget{}, "", false, tmuxCommandError("create environment setup pane", output, splitErr)
		}
		incarnation, parseErr := parseTmuxPaneIncarnation(output)
		if parseErr != nil {
			return "", tmuxWindowTarget{}, "", false, parseErr
		}
		paneID = incarnation.PaneID
		created = true
	}
	if err := h.setTmuxPaneOption(paneID, "@kiwi-code-agent", environmentSetupAgent); err != nil {
		return "", tmuxWindowTarget{}, "", false, err
	}
	if err := h.setTmuxPaneOption(paneID, "remain-on-exit", "on"); err != nil {
		return "", tmuxWindowTarget{}, "", false, err
	}
	return sessionName, target, paneID, created, nil
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
		nil,
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
	targetOut *tmuxWindowTarget,
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

	// Starting a session and adding its fixed windows must be atomic for this
	// thread. Acquire the cross-process per-thread lease in the canonical lock
	// order, then release the global state lock so unrelated thread tabs can
	// prepare their tmux sessions concurrently.
	h.sessionMu.Lock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		h.sessionMu.Unlock()
		return "", "", false, mutationErr
	}
	if activeErr := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); activeErr != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return "", "", false, errors.Join(activeErr, releaseErr)
	}
	h.sessionMu.Unlock()
	defer func() {
		// Release the per-thread lease before reacquiring sessionMu. Cleanup
		// operations acquire these locks in the opposite lifetime (sessionMu
		// first), so this boundary prevents deadlock while the durable stop
		// marker remains the final authority.
		releaseErr := mutation.Release()
		h.sessionMu.Lock()
		fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
		h.sessionMu.Unlock()
		if fenceErr != nil {
			sessionName = ""
			notice = ""
			created = false
		}
		err = errors.Join(err, releaseErr, fenceErr)
	}()
	if tool != "terminal" {
		if err := h.reconcileThreadTmuxStateUnderLease(item, thread); err != nil {
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
				return sessionName, "", false, nil
			}
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
			target.Tagged = true
			if targetOut != nil {
				*targetOut = target
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
			return sessionName, notice, true, nil
		}
		// A newly restarted server may briefly overlap the old handler. If it
		// won the race to create the shared session, ensure our named window in
		// that session instead.
		if sessionExists, checkErr := h.tmuxSessionExists(sessionName); checkErr != nil || !sessionExists {
			return "", "", false, createErr
		}
	}

	ensuredTarget, ensuredNotice, ensuredCreated, ensureErr := h.ensureSharedTmuxWindow(
		item,
		thread,
		tool,
		threadEndpoint,
		sessionName,
		initialAgent,
		launchOptions,
		restartCodingAgent,
	)
	if ensureErr != nil {
		return "", "", false, ensureErr
	}
	if targetOut != nil {
		*targetOut = ensuredTarget
	}
	return sessionName, ensuredNotice, ensuredCreated, nil
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
		// A matching tool tag is the completion marker for fixed-window setup.
		// Legacy name-only windows are adopted and tagged once; warm attaches do
		// not repeat four option commands and a rename before every first byte.
		if !target.Tagged {
			if err := h.configureSharedToolWindow(sessionName, target, tool); err != nil {
				return tmuxWindowTarget{}, "", false, err
			}
			target.Tagged = true
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
	target.Tagged = true
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

// reconcileThreadTmuxStateUnderLease recovers stale linked views before the
// normal existence check creates replacement fixed windows. The caller holds
// the durable per-thread mutation lease; active/stop fencing happens at the
// caller's sessionMu boundaries.
func (h *terminalHandler) reconcileThreadTmuxStateUnderLease(item project.Project, thread project.Thread) error {
	canonicalSession := tmuxSessionName(item.ID, thread.ID, "process")
	key := terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}
	h.viewMu.Lock()
	lastReconciled := h.viewReconciledAt[key]
	h.viewMu.Unlock()
	shouldReconcileViews := lastReconciled.IsZero() || time.Since(lastReconciled) >= 10*time.Second
	if !shouldReconcileViews {
		// A canonical session can disappear while its linked tool windows remain
		// alive in detached browser views. Bypass the routine scan throttle in
		// that recovery case so opening a tab adopts the existing process rather
		// than starting a replacement coding agent.
		canonicalExists, err := h.tmuxSessionExists(canonicalSession)
		if err != nil {
			return err
		}
		shouldReconcileViews = !canonicalExists
	}
	if shouldReconcileViews {
		if err := h.reconcileStaleTmuxViewsLocked(item.ID, thread.ID, canonicalSession); err != nil {
			return err
		}
		h.viewMu.Lock()
		if h.viewReconciledAt == nil {
			h.viewReconciledAt = make(map[terminalThreadKey]time.Time)
		}
		h.viewReconciledAt[key] = time.Now()
		h.viewMu.Unlock()
	}
	return h.prepareCanonicalCodingAgentWindowsLocked(item.ID, thread.ID, canonicalSession)
}

// reconcileThreadTmuxStateLocked is retained for callers that keep sessionMu
// for their complete mutation transaction.
func (h *terminalHandler) reconcileThreadTmuxStateLocked(item project.Project, thread project.Thread) (err error) {
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, h.finishTerminalThreadMutationLocked(item, thread))
	}()
	return h.reconcileThreadTmuxStateUnderLease(item, thread)
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
	stopped, err := manager.ThreadStopped(projectID, threadID)
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

	projectMarker, projectFound, projectErr := manager.ReadProject(item.ID)
	threadMarker, threadFound, threadErr := manager.ReadThread(item.ID, thread.ID)
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
					"@kiwi-code-source-session", canonicalSession,
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
			_ = h.tmuxCommand("set-option", "-u", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "@kiwi-code-source-session").Run()
			_ = h.tmuxCommand("set-option", "-u", "-t", exactTmuxCurrentWindowTarget(canonicalSession), "@kiwi-code-owner-pid").Run()
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
	return h.workspaceManager().ViewSessions()
}

func (h *terminalHandler) tmuxDetailedWindows(sessionName string) ([]tmuxDetailedWindow, error) {
	return h.workspaceManager().DetailedWindows(sessionName)
}

func (h *terminalHandler) tmuxWindowSession(windowID string) (string, error) {
	return h.workspaceManager().WindowSession(windowID)
}

func (h *terminalHandler) tmuxCanonicalSessionLinkedToWindow(windowID string) (string, error) {
	return h.workspaceManager().CanonicalSessionLinkedToWindow(windowID)
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
	return workspace.FixedTool(window)
}

func tmuxSessionFromStartCommand(command string) string {
	return workspace.SessionFromStartCommand(command)
}

func tmuxViewIdentity(sessionName string) (int, time.Time, bool) {
	return workspace.ParseViewIdentity(sessionName)
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

const codingAgentLaunchScript = `set -eu
"$1" set-option -p -t "$TMUX_PANE" @kiwi-code-agent "$2"
"$1" set-option -p -t "$TMUX_PANE" remain-on-exit on
shift 2
exec "$@"`

const environmentSetupAgent = "environment-setup"

const environmentSetupLaunchScript = `set -u
tmux_path=$1
environment_name=$2
shift 2
"$tmux_path" set-option -p -t "$TMUX_PANE" @kiwi-code-agent environment-setup
"$tmux_path" set-option -p -t "$TMUX_PANE" remain-on-exit on
printf '\033[1;36mSetting up %s...\033[0m\n\n' "$environment_name"
"$@"
status=$?
if [ "$status" -eq 0 ]; then
  printf '\n\033[1;32mEnvironment setup completed. Starting the coding agent...\033[0m\n'
else
  printf '\n\033[1;31mEnvironment setup failed (exit %s). The coding agent was not started.\033[0m\n' "$status"
fi
exit "$status"`

func (h *terminalHandler) environmentSetupLaunchCommand(item project.Project, thread project.Thread) (string, []string, bool) {
	script, variables, required := project.ResolveEnvironmentSetup(item, thread)
	if !required {
		return "", nil, false
	}
	envPath := h.envPath
	if envPath == "" {
		envPath = "env"
	}
	setupCommand := "/bin/sh"
	setupArguments := []string{"-lc", script}
	environment := make([]string, 0, len(variables)+1+len(setupArguments))
	for _, variable := range variables {
		environment = append(environment, variable.Name+"="+variable.Value)
	}
	environment = append(environment, setupCommand)
	environment = append(environment, setupArguments...)
	arguments := []string{
		"-c",
		environmentSetupLaunchScript,
		"kiwi-code-environment-setup",
		h.tmuxPath,
		item.Environment.Name,
		envPath,
	}
	arguments = append(arguments, environment...)
	return "/bin/sh", arguments, true
}

func (h *terminalHandler) codingAgentLaunchCommand(agent, command string, args []string) (string, []string) {
	launchArgs := make([]string, 0, len(args)+6)
	launchArgs = append(launchArgs,
		"-c",
		codingAgentLaunchScript,
		"kiwi-code-agent-launch",
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
	if err := h.setTmuxPaneOption(paneID, "@kiwi-code-agent", agent); err != nil {
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
// untouched, and prepares only panes explicitly identified as coding agents.
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
			if err := h.setTmuxPaneOption(panes[index].ID, "@kiwi-code-agent", codingAgentPi); err != nil {
				return nil, nil, err
			}
			panes[index].Agent = codingAgentPi
			break
		}
	}

	alivePanes := make([]tmuxAgentPane, 0, len(panes))
	endedAgents := make(map[string]bool)
	for _, pane := range panes {
		if !isTerminalCodingAgent(pane.Agent) {
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

func (h *terminalHandler) exitStore() *tmuxpane.ExitStore {
	h.exitStoreOnce.Do(func() {
		h.exitStoreValue = tmuxpane.NewExitStore(func() string {
			if h.agentExitDirectory != "" {
				return h.agentExitDirectory
			}
			if h.projects != nil {
				return filepath.Join(h.projects.DataDirectory(), datadir.CodingAgentExitsDirectoryName)
			}
			return ""
		})
	})
	return h.exitStoreValue
}

func (h *terminalHandler) codingAgentExitMarkerPath(projectID, threadID, agent string) string {
	return h.exitStore().MarkerPath(projectID, threadID, agent)
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
	return tmuxpane.ExitMarkerFromState(projectID, threadID, agent, paneID, state)
}

func (h *terminalHandler) withCodingAgentExitMarkerLock(projectID, threadID, agent string, operation func(path string) error) error {
	return h.exitStore().WithLock(projectID, threadID, agent, operation)
}

func writeCodingAgentExitMarker(path string, marker codingAgentExitMarker) error {
	return tmuxpane.WriteMarker(path, marker)
}

func syncDirectory(path string) error {
	return tmuxpane.SyncDirectory(path)
}

func readCodingAgentExitMarkerFile(path, projectID, threadID, agent string) (codingAgentExitMarker, bool, error) {
	return tmuxpane.ReadMarkerFile(path, projectID, threadID, agent)
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
	return h.workspaceManager().KillPaneIncarnation(paneID, serverPID, requireDead)
}

func (h *terminalHandler) killTmuxWindowIncarnation(windowID, serverPID string) error {
	return h.workspaceManager().KillWindowIncarnation(windowID, serverPID)
}

func (h *terminalHandler) killTmuxSessionIncarnation(sessionName, serverPID string) error {
	return h.workspaceManager().KillSessionIncarnation(sessionName, serverPID)
}

func (h *terminalHandler) tmuxTargetServerPID(target string) (string, bool, error) {
	return h.workspaceManager().TargetServerPID(target)
}

func (h *terminalHandler) removeCodingAgentExitMarkersForThread(projectID, threadID string) error {
	agents := []string{codingAgentPi, codingAgentCodex, codingAgentClaude, codingAgentClaudeGPT}
	if h.projects != nil {
		for _, configured := range h.projects.GetSettings().CodingAgents {
			if configured.Kind == project.CodingAgentKindClaude || configured.Kind == project.CodingAgentKindClaudeGPT {
				agents = append(agents, configuredCodingAgentID(configured))
			}
		}
	}
	for _, agent := range agents {
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
	return h.workspaceManager().PaneExitState(paneID)
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
	return h.workspaceManager().AgentPanes(windowID)
}

func (h *terminalHandler) activateCodingAgentPane(windowID, paneID string, panes []tmuxAgentPane) error {
	return h.workspaceManager().ActivateAgentPane(windowID, paneID, panes)
}

func (h *terminalHandler) unzoomTmuxWindow(windowID string, panes []tmuxAgentPane) error {
	return h.workspaceManager().UnzoomWindow(windowID, panes)
}

func (h *terminalHandler) tmuxWindowZoomed(windowID string) (bool, error) {
	return h.workspaceManager().WindowZoomed(windowID)
}

func (h *terminalHandler) setTmuxPaneOption(paneID, option, value string) error {
	return h.workspaceManager().SetPaneOption(paneID, option, value)
}

func (h *terminalHandler) tmuxPaneAlive(paneID string) (bool, error) {
	return h.workspaceManager().PaneAlive(paneID)
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
	if isClaudeGPTCodingAgent(agent) && launchOptions.Model == "" {
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

	return h.commandForTmuxTarget(
		item,
		thread,
		agent,
		"pi",
		threadEndpoint,
		sessionName,
		launchOptions,
	)
}

func (h *terminalHandler) prepareManagedCodexPlugin() error {
	h.codexPluginMu.Lock()
	defer h.codexPluginMu.Unlock()
	if h.codexPluginPrepared {
		return h.codexPluginPrepareErr
	}
	h.codexPluginPrepared = true
	switch {
	case h.codexPluginErr != nil:
		h.codexPluginPrepareErr = h.codexPluginErr
	case h.codexConfigErr != nil:
		h.codexPluginPrepareErr = h.codexConfigErr
	default:
		h.codexPluginPrepareErr = prepareCodexPluginProfile(
			h.codexConfigPath,
			h.codexProfileName,
			h.codexPlugin,
		)
	}
	return h.codexPluginPrepareErr
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
	if resolved, ok := h.codingAgentRegistry().Resolve(tool); ok {
		launchContext := agent.LaunchContext{
			ProjectID:      item.ID,
			ThreadID:       thread.ID,
			ThreadEndpoint: threadEndpoint,
			SessionName:    sessionName,
			WindowName:     windowName,
			FigmaMCPURL:    h.figmaMCPURLForProject(item),
			RelatedDirectories: func() ([]string, error) {
				return codingAgentRelatedProjectDirectories(item, thread)
			},
		}
		command, err := resolved.TerminalCommand(launchContext, launchOptions)
		if err != nil {
			return "", nil, "", err
		}
		args := make([]string, 0, len(command.Unset)*2+len(command.Env)+len(command.Prefix)+len(command.BaseArgs)+len(command.Suffix)+8)
		for _, name := range command.Unset {
			args = append(args, "-u", name)
		}
		args = append(args,
			"KIWI_CODE_TMUX_SESSION="+sessionName,
			"KIWI_CODE_TMUX_WINDOW="+windowName,
		)
		if threadEndpoint != "" {
			args = append(args, kiwiCodeThreadEnvironment(threadEndpoint, item.ID, thread.ID)...)
		}
		args = append(args, command.Env...)
		args = append(args, command.Program)
		args = append(args, command.Prefix...)
		args = append(args, command.BaseArgs...)
		if command.Notice == "" {
			// Option arguments never follow a fallback shell: the historic code
			// returned before appending them when the agent binary was missing.
			args = append(args, command.Suffix...)
		}
		return h.envProgram(), args, command.Notice, nil
	}

	command, args, notice, err := commandFor(tool)
	if err != nil {
		return "", nil, "", err
	}
	environment := []string{
		"KIWI_CODE_TMUX_SESSION=" + sessionName,
		"KIWI_CODE_TMUX_WINDOW=" + windowName,
	}
	environment = append(environment, command)
	args = append(environment, args...)
	return h.envProgram(), args, notice, nil
}

func (h *terminalHandler) envProgram() string {
	if h.envPath == "" {
		return "env"
	}
	return h.envPath
}

// codingAgentRegistry assembles the pluggable-agent registry over the
// handler's materialized assets and live settings.
func (h *terminalHandler) codingAgentRegistry() *agent.Registry {
	pi := agent.Pi{
		ExtensionPaths:       h.piExtensionPaths,
		ExtensionErr:         h.piExtensionErr,
		FigmaExtensionPath:   h.piFigmaExtensionPath,
		FigmaExtensionErr:    h.piFigmaExtensionErr,
		AgentToken:           h.agentToken,
		FigmaEnvironmentName: figmaMCPEnvironmentName,
	}
	codex := agent.Codex{
		AgentToken:     h.agentToken,
		AgentTokenPath: h.agentTokenPath,
		AgentTokenErr:  h.agentTokenErr,
		ProfileName:    h.codexProfileName,
		ConfigPath:     h.codexConfigPath,
		Prepare:        h.prepareManagedCodexPlugin,
	}
	claudeFactory := func(id string, profile project.CodingAgentSetting, configured, gpt bool) agent.Agent {
		return agent.Claude{
			AgentID:             id,
			GPT:                 gpt,
			Configured:          configured,
			Profile:             profile,
			PluginPath:          h.claudePluginPath,
			PluginErr:           h.claudePluginErr,
			PluginRootPath:      h.claudePluginRootPath,
			PluginRootErr:       h.claudePluginRootErr,
			ConfigPath:          h.claudeConfigPath,
			ConfigErr:           h.claudeConfigErr,
			LaunchSettings:      claudeLaunchSettings,
			SyncProfileSettings: syncClaudeCodeProfileSettings,
			FigmaConfigArgument: figmaMCPConfigArgument,
			UnsetEnvironment:    claudeGPTUnsetEnvironment,
			GPTProfileDirectory: h.claudeGPTProfileDirectory,
			ProxyConfiguration:  h.cliProxyAPIConfiguration,
			ProxyEnvironment:    claudeGPTProxyEnvironment,
			IsGPTModel:          isCLIProxyAPIGPTModel,
		}
	}
	settings := func() project.Settings {
		if h.projects == nil {
			return project.Settings{}
		}
		return h.projects.GetSettings()
	}
	return agent.NewRegistry(pi, codex, claudeFactory, settings)
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
	return h.existingShellWindowsContext(context.Background(), item, thread)
}

func (h *terminalHandler) existingShellWindowsContext(
	ctx context.Context,
	item project.Project,
	thread project.Thread,
) ([]tmuxWindow, error) {
	sessionName := tmuxSessionName(item.ID, thread.ID, "terminal")
	exists, err := h.tmuxSessionExistsContext(ctx, sessionName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []tmuxWindow{}, nil
	}
	return h.tmuxWindowsContext(ctx, sessionName)
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
	return h.workspaceManager().CreateWindow(cwd, sessionName, windowName, command, args, selectWindow)
}

func (h *terminalHandler) activateTmuxWindow(sessionName string, index int) ([]tmuxWindow, error) {
	return h.workspaceManager().ActivateWindow(sessionName, index)
}

func (h *terminalHandler) configureSharedToolWindow(sessionName string, target tmuxWindowTarget, tool string) error {
	return h.workspaceManager().ConfigureSharedToolWindow(sessionName, target, tool)
}

func (h *terminalHandler) tmuxToolWindow(sessionName, tool string) (tmuxWindowTarget, bool, error) {
	return h.workspaceManager().ToolWindow(sessionName, tool)
}

func (h *terminalHandler) createTmuxViewSession(item project.Project, thread project.Thread, sourceSession string, sourceWindow tmuxWindowTarget) (viewSessionName string, err error) {
	h.sessionMu.Lock()
	mutation, mutationErr := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if mutationErr != nil {
		h.sessionMu.Unlock()
		return "", mutationErr
	}
	if activeErr := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); activeErr != nil {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return "", errors.Join(activeErr, releaseErr)
	}
	h.sessionMu.Unlock()

	var viewServerPID string
	defer func() {
		releaseErr := mutation.Release()
		h.sessionMu.Lock()
		fenceErr := h.finishTerminalThreadMutationLocked(item, thread)
		if fenceErr != nil {
			if viewSessionName != "" {
				_ = h.killTmuxSessionIncarnation(viewSessionName, viewServerPID)
				h.unregisterTmuxViewLocked(viewSessionName)
			}
			viewSessionName = ""
		}
		h.sessionMu.Unlock()
		err = errors.Join(err, releaseErr, fenceErr)
	}()
	viewSessionName, viewServerPID, err = h.createTmuxViewSessionLocked(sourceSession, sourceWindow)
	return viewSessionName, err
}

// createTmuxBrowserViewSession creates a temporary view for an arbitrary tmux
// session that may not belong to a Kiwi Code project. Managed project views use
// createTmuxViewSession so their durable deletion fence remains authoritative.
func (h *terminalHandler) createTmuxBrowserViewSession(sourceSession string, sourceWindow tmuxWindowTarget) (string, error) {
	viewSessionName, _, err := h.createTmuxViewSessionLocked(sourceSession, sourceWindow)
	return viewSessionName, err
}

func (h *terminalHandler) createTmuxViewSessionLocked(sourceSession string, sourceWindow tmuxWindowTarget) (viewSessionName, viewServerPID string, err error) {
	// The view session contains a link to exactly one canonical tool window.
	// When that tool exits, tmux removes the linked window and closes the view,
	// while closing the browser merely removes the extra link.
	for attempt := 0; attempt < 5; attempt++ {
		viewName := workspace.ViewSessionName(os.Getpid(), time.Now(), h.viewCounter.Add(1))
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
		viewTarget := exactTmuxCurrentWindowTarget(viewName)
		output, optionErr := h.tmuxCommand(
			"set-option", "-t", viewTarget, "@kiwi-code-source-session", sourceSession,
			";",
			"set-option", "-t", viewTarget, "@kiwi-code-owner-pid", strconv.Itoa(os.Getpid()),
		).CombinedOutput()
		if optionErr != nil {
			cleanup()
			return "", "", tmuxCommandError("configure tmux terminal view", output, optionErr)
		}
		return viewName, dummy.ServerPID, nil
	}
	return "", "", errors.New("could not allocate a tmux terminal view")
}

func (h *terminalHandler) closeTmuxViewSession(viewName string) {
	_ = h.tmuxCommand("kill-session", "-t", exactTmuxSessionTarget(viewName)).Run()
	h.unregisterTmuxViewLocked(viewName)
}

func (h *terminalHandler) registerTmuxViewLocked(viewName string) {
	h.viewMu.Lock()
	defer h.viewMu.Unlock()
	if h.activeViews == nil {
		h.activeViews = make(map[string]struct{})
	}
	h.activeViews[viewName] = struct{}{}
}

func (h *terminalHandler) unregisterTmuxViewLocked(viewName string) {
	h.viewMu.Lock()
	delete(h.activeViews, viewName)
	h.viewMu.Unlock()
}

func (h *terminalHandler) tmuxViewIsActiveLocked(viewName string) bool {
	h.viewMu.Lock()
	defer h.viewMu.Unlock()
	_, active := h.activeViews[viewName]
	return active
}

func (h *terminalHandler) setTmuxWindowOption(target, option, value string) error {
	return h.workspaceManager().SetWindowOption(target, option, value)
}

func (h *terminalHandler) tmuxWindows(sessionName string) ([]tmuxWindow, error) {
	return h.workspaceManager().Windows(sessionName)
}

func (h *terminalHandler) tmuxWindowsContext(ctx context.Context, sessionName string) ([]tmuxWindow, error) {
	return h.workspaceManager().WindowsContext(ctx, sessionName)
}

func threadTmuxSessionNameSet(item project.Project, threadID string) map[string]struct{} {
	return map[string]struct{}{
		tmuxSessionName(item.ID, threadID, "terminal"): {},
		tmuxSessionName(item.ID, threadID, "process"):  {},
	}
}

func projectTmuxSessionNameSet(item project.Project) map[string]struct{} {
	sessionNames := make(map[string]struct{}, len(item.Threads)*2)
	for _, thread := range item.Threads {
		for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
			sessionNames[sessionName] = struct{}{}
		}
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
	lease, err := manager.BeginThread(item.ID, threadID, exactTmuxSessionNames(sessionNames))
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
	lease, err := manager.BeginProject(
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
	refs, listErr := manager.ListMarkers()
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
		marker, found, err := manager.ReadThread(ref.ProjectID, ref.ThreadID)
		if err != nil || !found {
			return nil, found, err
		}
		return []string{marker.ThreadID}, true, nil
	}
	marker, found, err := manager.ReadProject(ref.ProjectID)
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
	lease, found, err := manager.AcquireExisting(ref)
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
	return h.workspaceManager().SessionExists(sessionName)
}

func (h *terminalHandler) tmuxSessionExistsContext(ctx context.Context, sessionName string) (bool, error) {
	return h.workspaceManager().SessionExistsContext(ctx, sessionName)
}

func (h *terminalHandler) tmuxExactSessionExists(sessionName string) (bool, error) {
	return h.workspaceManager().ExactSessionExists(sessionName)
}

func (h *terminalHandler) createTmuxSession(sessionName, directory, windowName, command string, args []string) (tmuxWindowTarget, error) {
	return h.workspaceManager().CreateSession(sessionName, directory, windowName, command, args)
}

func (h *terminalHandler) notifyThreadStatusChanged(projectID, threadID string) {
	h.publishThreadStatusChanged(projectID, threadID)
}

// workspaceManager builds the typed tmux operation layer over the handler's
// current client configuration.
func (h *terminalHandler) workspaceManager() *workspace.Manager {
	return workspace.NewManager(h.tmuxClient)
}

// tmuxClient builds a client from the handler's current binary path and
// socket. It is constructed per call because tests adjust tmuxPath after the
// handler exists; the client itself is a cheap immutable value.
func (h *terminalHandler) tmuxClient() *tmux.Client {
	return tmux.NewClient(h.tmuxPath, h.tmuxSocket, terminalEnvironment)
}

func (h *terminalHandler) tmuxCommand(args ...string) *exec.Cmd {
	return h.tmuxClient().Command(args...)
}

func (h *terminalHandler) tmuxCommandArguments(args ...string) []string {
	return h.tmuxClient().Arguments(args...)
}

func exactTmuxSessionTarget(sessionName string) string {
	return tmux.ExactSessionTarget(sessionName)
}

func exactTmuxCurrentWindowTarget(sessionName string) string {
	return tmux.ExactCurrentWindowTarget(sessionName)
}

func exactTmuxWindowTarget(sessionName string, index int) string {
	return tmux.ExactWindowTarget(sessionName, index)
}

func tmuxSessionName(projectID, threadID, tool string) string {
	return workspace.SessionName(projectID, threadID, tool)
}

func tmuxSessionSuffix(tool string) string {
	return workspace.SessionSuffix(tool)
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

func kiwiCodeThreadEnvironment(threadEndpoint, projectID, threadID string) []string {
	environment := []string{
		"KIWI_CODE_THREAD_ENDPOINT=" + threadEndpoint,
		"KIWI_CODE_PROJECT_ID=" + projectID,
		"KIWI_CODE_THREAD_ID=" + threadID,
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
			environment = append(environment, "KIWI_CODE_PORT="+port)
		}
		if endpoint.Scheme != "" {
			environment = append(environment, "KIWI_CODE_SCHEME="+endpoint.Scheme)
		}
	}
	return environment
}

func parseTmuxWindowTarget(output []byte) (tmuxWindowTarget, error) {
	return tmux.ParseWindowTarget(output)
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
	return tmux.CommandError(action, output, err)
}

func shellCommand(command string, args []string) string {
	return tmux.ShellCommand(command, args)
}

func shellQuote(value string) string {
	return tmux.ShellQuote(value)
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
	case codingAgentCodex:
		return codingAgentCodex, nil
	case codingAgentClaude:
		return codingAgentClaude, nil
	case codingAgentClaudeGPT:
		return codingAgentClaudeGPT, nil
	default:
		if validConfiguredClaudeAgent(agent) {
			return agent, nil
		}
		return "", errors.New("unknown coding agent")
	}
}

func claudeCodeProfileAgentID(profileID string) string {
	return codingAgentClaudeProfilePrefix + profileID
}

func claudeCodeGPTProfileAgentID(profileID string) string {
	return codingAgentClaudeGPTProfilePrefix + profileID
}

func configuredCodingAgentID(setting project.CodingAgentSetting) string {
	return agent.ProfileAgentID(setting)
}

func validClaudeCodeProfileAgent(agentID string) bool {
	return agent.ValidClaudeProfileID(agentID)
}

func validClaudeCodeGPTProfileAgent(agentID string) bool {
	return agent.ValidClaudeGPTProfileID(agentID)
}

func validConfiguredClaudeAgent(agentID string) bool {
	return agent.ValidConfiguredClaudeID(agentID)
}

func isClaudeGPTCodingAgent(agentID string) bool {
	return agent.IsClaudeGPT(agentID)
}

func isClaudeCodingAgent(agentID string) bool {
	return agent.IsClaude(agentID)
}

func isTerminalCodingAgent(agentID string) bool {
	return agent.IsTerminalAgent(agentID)
}

func (h *terminalHandler) claudeCodeProfile(agent string) (project.CodingAgentSetting, bool) {
	if h == nil || h.projects == nil || !validConfiguredClaudeAgent(agent) {
		return project.CodingAgentSetting{}, false
	}
	for _, configured := range h.projects.GetSettings().CodingAgents {
		if (configured.Kind == project.CodingAgentKindClaude || configured.Kind == project.CodingAgentKindClaudeGPT) &&
			configuredCodingAgentID(configured) == agent {
			return configured, true
		}
	}
	return project.CodingAgentSetting{}, false
}

func (h *terminalHandler) validateCodingAgentConfiguration(agent string) error {
	if !validConfiguredClaudeAgent(agent) {
		return nil
	}
	if _, configured := h.claudeCodeProfile(agent); !configured {
		return errors.New("Claude Code agent is not configured")
	}
	return nil
}

func commandFor(tool string) (string, []string, string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if tool == codingAgentCodex {
		path, err := exec.LookPath(codingAgentCodex)
		if err == nil {
			return path, nil, "", nil
		}
		notice := "\r\n\x1b[38;5;214mCodex CLI is not installed or not on PATH. Opened a shell instead.\x1b[0m\r\n\r\n"
		return shell, []string{"-l"}, notice, nil
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
		"TERM_PROGRAM": "kiwi-code",
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
	return tmux.NewClient("", "", terminalEnvironment).Environment()
}
