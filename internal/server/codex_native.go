package server

// Codex is an adapter behind the provider-neutral native chat wire format. The
// browser never sends arbitrary app-server RPCs or chooses a provider thread ID.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/gorilla/websocket"
)

type codexRPC struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type codexRequestError struct{ method, message string }

func (e *codexRequestError) Error() string { return "Codex " + e.method + ": " + e.message }

type codexNativeManager struct {
	mu               sync.Mutex
	dataDirectory    string
	codexPath        string
	processes        map[nativeProcessKey]*codexNativeProcess
	contextWatchOnce sync.Once
	activityReporter func(nativeProcessKey, bool)
	usageReporter    func(nativeProcessKey, string, threadUsageTotals)
}
type codexNativeProcess struct {
	*nativeProcessCore
	mu               sync.Mutex
	pending          map[string]chan codexRPC
	state            chatState
	sequence         uint64
	threadID         string
	turnID           string
	sessionDirectory string
	attempted        bool
	nameThread       func(sessionID, prompt string)
}

func newCodexNativeManager(directory string) *codexNativeManager {
	return &codexNativeManager{dataDirectory: directory, processes: make(map[nativeProcessKey]*codexNativeProcess)}
}
func (m *codexNativeManager) stopOnContext(ctx context.Context) {
	stopNativeProcessesOnContext(ctx, &m.contextWatchOnce, func() { _ = m.stopMatching(func(nativeProcessKey) bool { return true }) })
}
func (m *codexNativeManager) stopMatching(match func(nativeProcessKey) bool) error {
	m.mu.Lock()
	selected := collectNativeProcesses(m.processes, match)
	m.mu.Unlock()
	err := stopNativeProcessSet(selected, func(p *codexNativeProcess) error { return p.stop() })
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range selected {
		if m.processes[p.key] == p && channelClosed(p.done) {
			delete(m.processes, p.key)
		}
	}
	return err
}
func (m *codexNativeManager) stopThread(projectID, threadID string) error {
	return m.stopMatching(func(k nativeProcessKey) bool { return k.ProjectID == projectID && k.ThreadID == threadID })
}
func (m *codexNativeManager) removeThread(projectID, threadID string) error {
	if err := m.stopThread(projectID, threadID); err != nil {
		return err
	}
	if validPiNativePathSegment(projectID) != nil || validPiNativePathSegment(threadID) != nil {
		return errors.New("invalid thread identity")
	}
	return os.RemoveAll(filepath.Join(m.dataDirectory, "codex-native-sessions", projectID, threadID))
}
func (m *codexNativeManager) removeProject(projectID string) error {
	if err := m.stopMatching(func(k nativeProcessKey) bool { return k.ProjectID == projectID }); err != nil {
		return err
	}
	if validPiNativePathSegment(projectID) != nil {
		return errors.New("invalid project identity")
	}
	return os.RemoveAll(filepath.Join(m.dataDirectory, "codex-native-sessions", projectID))
}
func (p *codexNativeProcess) rpc(method string, params any) (json.RawMessage, error) {
	id := p.request.Add(1)
	key := strconv.FormatUint(id, 10)
	response := make(chan codexRPC, 1)
	p.mu.Lock()
	p.pending[key] = response
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, key); p.mu.Unlock() }()
	data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := p.writeLine(data); err != nil {
		return nil, err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case result := <-response:
		if result.Error != nil {
			return nil, &codexRequestError{method: method, message: result.Error.Message}
		}
		return result.Result, nil
	case <-p.done:
		return nil, errors.New(p.exitMessage())
	case <-timer.C:
		p.stopping.Store(true)
		go func() { _ = p.stop() }()
		return nil, fmt.Errorf("Codex %s timed out; reconnect before retrying", method)
	}
}
func (p *codexNativeProcess) emitLocked(kind string, value any) {
	p.sequence++
	payload, _ := json.Marshal(map[string]any{"type": kind, "sequence": p.sequence, "value": value})
	p.events.Publish(payload)
}
func (p *codexNativeProcess) snapshot() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{"type": "chat_snapshot", "sequence": p.sequence, "value": p.state})
	return payload
}
func (p *codexNativeProcess) upsertLocked(item chatItem) {
	for i := range p.state.Items {
		if p.state.Items[i].ID == item.ID {
			p.state.Items[i] = item
			p.emitLocked("chat_item", item)
			return
		}
	}
	p.state.Items = append(p.state.Items, item)
	p.emitLocked("chat_item", item)
}
func codexChatItem(raw json.RawMessage) chatItem {
	var item struct {
		ID, Type, Text, Command, Status, AggregatedOutput string
		Content                                           []struct{ Type, Text, Path string }
		Summary                                           []string
		Changes                                           []struct{ Path, Diff string }
	}
	_ = json.Unmarshal(raw, &item)
	result := chatItem{ID: item.ID, Status: item.Status, Text: item.Text, Kind: "tool", Title: item.Type}
	switch item.Type {
	case "userMessage":
		result.Kind = "user"
		for _, c := range item.Content {
			if c.Text != "" {
				result.Text += c.Text
			}
			if c.Type == "localImage" || c.Type == "image" {
				result.Text += "\n[Attached image]"
			}
		}
	case "agentMessage":
		result.Kind = "assistant"
	case "reasoning":
		result.Kind = "reasoning"
		result.Title = "Thinking"
		result.Text = strings.Join(item.Summary, "\n")
	case "commandExecution":
		result.Title = item.Command
		result.Text = item.AggregatedOutput
	case "fileChange":
		result.Title = "File changes"
		for _, c := range item.Changes {
			result.Text += c.Path + "\n" + c.Diff + "\n"
		}
	default:
		result.Text = string(raw)
	}
	return result
}
func (p *codexNativeProcess) receive(data []byte) {
	var wire codexRPC
	if json.Unmarshal(data, &wire) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if wire.Method == "" {
		if ch := p.pending[string(wire.ID)]; ch != nil {
			select {
			case ch <- wire:
			default:
			}
		}
		return
	}
	var params struct {
		Item          json.RawMessage
		ItemID, Delta string
		Turn          struct {
			ID, Status string
			Error      *struct{ Message string }
		}
		RequestID       json.RawMessage
		Reason, Command string
		Questions       json.RawMessage
	}
	_ = json.Unmarshal(wire.Params, &params)
	if len(wire.ID) > 0 {
		kind := ""
		title := "Approval required"
		switch wire.Method {
		case "item/commandExecution/requestApproval":
			kind = "approval"
			title = "Run command?"
		case "item/fileChange/requestApproval":
			kind = "approval"
			title = "Allow file changes?"
		case "item/tool/requestUserInput":
			kind = "input"
			title = "Codex has a question"
		default:
			reply, _ := json.Marshal(map[string]any{"id": wire.ID, "error": map[string]any{"code": -32601, "message": "This request is not supported by Kiwi native chat"}})
			go func() { _ = p.writeLine(reply) }()
			return
		}
		p.state.Requests = append(p.state.Requests, chatRequest{ID: wire.ID, Kind: kind, Title: title, Details: strings.TrimSpace(params.Command + "\n" + params.Reason), Questions: params.Questions})
		p.emitLocked("chat_state", p.state)
		return
	}
	switch wire.Method {
	case "item/started", "item/completed":
		item := codexChatItem(params.Item)
		if item.ID != "" {
			p.upsertLocked(item)
		}
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/commandExecution/outputDelta":
		for i := range p.state.Items {
			if p.state.Items[i].ID == params.ItemID {
				p.state.Items[i].Text += params.Delta
				p.emitLocked("chat_item", p.state.Items[i])
				break
			}
		}
	case "turn/started":
		p.turnID = params.Turn.ID
		p.state.Working = true
		p.state.Error = ""
		p.emitLocked("chat_state", p.state)
	case "thread/tokenUsage/updated":
		var usage struct{ TokenUsage struct{ Total chatUsage } }
		if json.Unmarshal(wire.Params, &usage) == nil {
			p.state.Usage = &usage.TokenUsage.Total
			p.emitLocked("chat_usage", usage.TokenUsage.Total)
		}
	case "turn/completed":
		for i := range p.state.Items {
			if p.state.Items[i].Status == "inProgress" {
				p.state.Items[i].Status = "completed"
				if params.Turn.Status != "completed" {
					p.state.Items[i].Status = "interrupted"
				}
			}
		}
		p.turnID = ""
		p.state.Working = false
		p.state.Requests = nil
		if params.Turn.Error != nil {
			p.state.Error = params.Turn.Error.Message
		}
		p.emitLocked("chat_state", p.state)
	case "serverRequest/resolved":
		for i, r := range p.state.Requests {
			if string(r.ID) == string(params.RequestID) {
				p.state.Requests = append(p.state.Requests[:i], p.state.Requests[i+1:]...)
				break
			}
		}
		p.emitLocked("chat_state", p.state)
	case "error":
		var detail struct{ Error struct{ Message string } }
		_ = json.Unmarshal(wire.Params, &detail)
		p.state.Error = detail.Error.Message
		p.emitLocked("chat_state", p.state)
	}
}
func (m *codexNativeManager) getOrStart(item project.Project, thread project.Thread, env []string, arguments ...string) (*codexNativeProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := nativeProcessKey{item.ID, thread.ID}
	if current := m.processes[key]; current != nil && !channelClosed(current.done) {
		return current, nil
	}
	if validPiNativePathSegment(item.ID) != nil || validPiNativePathSegment(thread.ID) != nil {
		return nil, errors.New("invalid thread identity")
	}
	directory := filepath.Join(m.dataDirectory, "codex-native-sessions", item.ID, thread.ID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := m.codexPath
	if path == "" {
		var err error
		path, err = exec.LookPath("codex")
		if err != nil {
			return nil, errors.New("Codex is not installed or not on PATH")
		}
	}
	command := exec.Command(path, append([]string{"app-server"}, arguments...)...)
	command.Dir = thread.Cwd
	command.Env = append(os.Environ(), env...)
	core, stdout, stderr, err := startNativeCommand(key, nativeProcessSpec{displayName: "Codex", endedMessage: "Codex session ended. Reconnect to resume.", unexpectedMessage: "Codex exited. Reconnect to resume.", writeAfterExitError: "Codex has stopped", stopTimeout: 3 * time.Second}, command)
	if err != nil {
		return nil, err
	}
	p := &codexNativeProcess{nativeProcessCore: core, sessionDirectory: directory, pending: make(map[string]chan codexRPC), state: chatState{Items: []chatItem{}, Requests: []chatRequest{}}}
	m.watchActivity(p)
	p.readOutput(stdout, p.receive)
	p.readDiagnostics(stderr)
	p.run(func(message string) {
		p.mu.Lock()
		p.state.Working = false
		p.state.Error = message
		p.emitLocked("chat_state", p.state)
		p.mu.Unlock()
	}, func() {})
	fail := func(err error) (*codexNativeProcess, error) { _ = p.stop(); return nil, err }
	if _, err = p.rpc("initialize", map[string]any{"clientInfo": map[string]string{"name": "kiwi-code", "title": "Kiwi Code", "version": "1.0.0"}, "capabilities": map[string]bool{"experimentalApi": true}}); err != nil {
		return fail(err)
	}
	if err = p.writeLine([]byte(`{"method":"initialized"}`)); err != nil {
		return fail(err)
	}
	params := map[string]any{"cwd": thread.Cwd, "approvalPolicy": "on-request", "sandbox": "workspace-write"}
	method := "thread/start"
	saved, readErr := os.ReadFile(filepath.Join(directory, "thread-id"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fail(readErr)
	}
	_, attemptErr := os.Stat(filepath.Join(directory, "prompt-attempted"))
	if attemptErr != nil && !errors.Is(attemptErr, os.ErrNotExist) {
		return fail(attemptErr)
	}
	p.attempted = attemptErr == nil
	if p.attempted && (readErr != nil || strings.TrimSpace(string(saved)) == "") {
		return fail(errors.New("Saved Codex conversation ID is empty; refusing to replace it"))
	}
	if id := strings.TrimSpace(string(saved)); p.attempted && id != "" {
		method = "thread/resume"
		params["threadId"] = id
	}
	result, err := p.rpc(method, params)
	if err != nil {
		return fail(err)
	}
	var opened struct {
		Model           string
		ReasoningEffort string
		Thread          struct {
			ID    string
			Turns []struct{ Items []json.RawMessage }
		}
	}
	if err = json.Unmarshal(result, &opened); err != nil {
		return fail(err)
	}
	if opened.Thread.ID == "" {
		return fail(errors.New("Codex returned no conversation ID"))
	}
	// Save the exact ID before accepting a prompt. Never silently start a fresh
	// conversation if a saved thread cannot be resumed.
	if err = writeFileAtomically(filepath.Join(directory, "thread-id"), []byte(opened.Thread.ID+"\n"), serverAtomicFileOptions{Mode: 0600, SyncFile: true, SyncDirectory: true}); err != nil {
		return fail(err)
	}
	p.mu.Lock()
	p.threadID = opened.Thread.ID
	p.state.Model = opened.Model
	p.state.Effort = opened.ReasoningEffort
	for _, turn := range opened.Thread.Turns {
		for _, raw := range turn.Items {
			p.upsertLocked(codexChatItem(raw))
		}
	}
	p.mu.Unlock()
	m.processes[key] = p
	return p, nil
}
func (h *terminalHandler) startCodexNativeProcess(item project.Project, thread project.Thread, endpoint string) (*codexNativeProcess, error) {
	if thread.RollbackPending {
		return nil, project.ErrThreadRollbackPending
	}
	if project.EnvironmentSetupBlocksAgent(thread) {
		return nil, errEnvironmentSetupPending
	}
	return withTerminalThreadMutation(h, item, thread, func() (*codexNativeProcess, error) {
		env := kiwiCodeThreadEnvironment(endpoint, item.ID, thread.ID, h.titleGenerationSettings())
		env = append(env, "KIWI_CODE_AGENT_TOKEN_FILE="+h.agentTokenPath, "KIWI_CODE_CODING_AGENT=codex")
		arguments := []string{}
		if h.codexPluginErr != nil {
			return nil, h.codexPluginErr
		}
		if h.agentTokenErr != nil {
			return nil, h.agentTokenErr
		}
		// Configure the thread-scoped MCP server directly, without rewriting the
		// user's Codex profile. This shares Kiwi browser/process tools with the CLI.
		if h.codexPlugin.PluginRoot != "" {
			arguments = append(arguments, "-c", "mcp_servers.kiwi-code-browser.command=\"node\"", "-c", "mcp_servers.kiwi-code-browser.args=["+strconv.Quote(filepath.Join(h.codexPlugin.PluginRoot, "servers", "kiwi-code-browser.mjs"))+"]", "-c", "mcp_servers.kiwi-code-browser.env_vars=[\"KIWI_CODE_THREAD_ENDPOINT\",\"KIWI_CODE_AGENT_TOKEN_FILE\"]")
		}
		if figmaURL := h.figmaMCPURLForProject(item); figmaURL != "" {
			arguments = append(arguments, "-c", "mcp_servers.kiwi-code-figma.url="+strconv.Quote(figmaURL))
		}
		p, err := h.nativeCodex.getOrStart(item, thread, env, arguments...)
		if err != nil {
			return nil, err
		}
		// Native chat does not load the CLI plugin's UserPromptSubmit hooks.
		// Invoke the shared namer ourselves after the first accepted prompt.
		if endpoint != "" && h.codexPlugin.PluginRoot != "" {
			script := filepath.Join(h.codexPlugin.PluginRoot, "scripts", "kiwi-code-hook.mjs")
			titleEnv := append(os.Environ(), env...)
			titleEnv = append(titleEnv, "KIWI_CODE_CODEX_STATE_DIR="+p.sessionDirectory)
			p.mu.Lock()
			p.nameThread = func(sessionID, prompt string) {
				if err := runCodexNativeTitleHook(script, titleEnv, sessionID, prompt); err != nil {
					log.Printf("name native Codex thread: project=%q thread=%q error=%v", item.ID, thread.ID, err)
				}
			}
			p.mu.Unlock()
		}
		return p, nil
	}, func(p *codexNativeProcess) {
		if p != nil {
			_ = p.stop()
		}
	})
}

func runCodexNativeTitleHook(script string, env []string, sessionID, prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input, _ := json.Marshal(map[string]string{"session_id": sessionID, "prompt": prompt})
	command := exec.CommandContext(ctx, "node", script, "title")
	command.Env = env
	command.Dir = os.TempDir()
	command.Stdin = strings.NewReader(string(input))
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		return err
	}
	var result struct{ SystemMessage string }
	if json.Unmarshal(output, &result) == nil && result.SystemMessage != "" {
		return errors.New(result.SystemMessage)
	}
	return nil
}

func (p *codexNativeProcess) prompt(message chatClientMessage) error {
	if strings.TrimSpace(message.Message) == "" && len(message.Images) == 0 {
		return errors.New("Enter a message")
	}
	if len(message.Images) > 20 {
		return errors.New("Too many images")
	}
	options, err := normalizeCodingAgentLaunchOptions(codingAgentCodex, message.Model, message.Effort)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.stopping.Load() || channelClosed(p.done) {
		p.mu.Unlock()
		return errors.New("Codex has stopped. Reconnect to resume.")
	}
	if p.state.Working {
		p.mu.Unlock()
		return errors.New("Wait for Codex to finish or stop the current turn")
	}
	firstPrompt := !p.attempted
	nameThread := p.nameThread
	if firstPrompt {
		if err := writeFileAtomically(filepath.Join(p.sessionDirectory, "prompt-attempted"), []byte("1\n"), serverAtomicFileOptions{Mode: 0600, SyncFile: true, SyncDirectory: true}); err != nil {
			p.mu.Unlock()
			return err
		}
		p.attempted = true
	}
	p.state.Working = true
	p.state.Error = ""
	p.state.Model = options.Model
	p.state.Effort = options.ThinkingLevel
	p.emitLocked("chat_state", p.state)
	p.mu.Unlock()
	input := []map[string]any{{"type": "text", "text": message.Message}}
	for _, image := range message.Images {
		input = append(input, map[string]any{"type": "localImage", "path": image.Path})
	}
	params := map[string]any{"threadId": p.threadID, "input": input}
	if options.Model != "" {
		params["model"] = options.Model
	}
	if options.ThinkingLevel != "" {
		params["effort"] = options.ThinkingLevel
	}
	_, err = p.rpc("turn/start", params)
	if err != nil {
		p.mu.Lock()
		var rejected *codexRequestError
		if firstPrompt && errors.As(err, &rejected) && p.turnID == "" && len(p.state.Items) == 0 {
			// A definite rejection did not create a rollout. A transport failure has
			// unknown delivery and must retain the ID for recovery.
			if removeErr := removeIfExists(filepath.Join(p.sessionDirectory, "prompt-attempted")); removeErr == nil {
				p.attempted = false
			}
		}
		p.state.Working = false
		p.state.Error = err.Error()
		p.emitLocked("chat_state", p.state)
		p.mu.Unlock()
	} else if firstPrompt && nameThread != nil {
		go nameThread(p.threadID, message.Message)
	}
	return err
}
func (p *codexNativeProcess) respond(message chatClientMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, request := range p.state.Requests {
		if string(request.ID) != string(message.RequestID) {
			continue
		}
		var result any
		if request.Kind == "approval" {
			if message.Decision != "accept" && message.Decision != "decline" {
				return errors.New("invalid approval decision")
			}
			result = map[string]string{"decision": message.Decision}
		} else {
			result = map[string]any{"answers": message.Answers}
		}
		payload, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
		if err := p.writeLine(payload); err != nil {
			return err
		}
		p.state.Requests = append(p.state.Requests[:i], p.state.Requests[i+1:]...)
		p.emitLocked("chat_state", p.state)
		return nil
	}
	return errors.New("This request is no longer pending")
}
func (h *terminalHandler) serveCodexNative(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, 400, "Native chat requires a WebSocket connection.")
		return
	}
	item, thread, err := h.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, 404, "Thread not found.")
		return
	}
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1 << 20)
	writer := newWebSocketWriter(connection)
	sendError := func(err error) {
		payload, _ := json.Marshal(map[string]any{"type": "chat_error", "message": err.Error()})
		_ = writer.Write(websocket.TextMessage, payload)
	}
	p, err := h.startCodexNativeProcess(item, thread, threadEndpointURL(r, item.ID, thread.ID))
	if err != nil {
		sendError(err)
		return
	}
	subscription := p.events.Subscribe()
	defer subscription.Close()
	if writer.Write(websocket.TextMessage, p.snapshot()) != nil {
		return
	}
	peer := startWebSocketPeer(connection, writer, rawWebSocketMessage, "native Codex input stalled")
	defer peer.Stop()
	for {
		select {
		case payload, ok := <-subscription.Events():
			if !ok {
				return
			}
			if writer.Write(websocket.TextMessage, payload) != nil {
				return
			}
		case payload := <-peer.messages:
			var message chatClientMessage
			if json.Unmarshal(payload, &message) != nil {
				sendError(errors.New("Invalid chat message"))
				continue
			}
			_, err := withTerminalThreadMutation(h, item, thread, func() (bool, error) {
				switch message.Type {
				case "prompt":
					if len(message.Images) > 20 {
						return false, errors.New("Attach at most 20 images")
					}
					var imageBytes int64
					for _, image := range message.Images {
						contents, err := readPiUploadedImage(image.Path, maxPiImageBytes-imageBytes)
						if err != nil {
							return false, err
						}
						imageBytes += int64(len(contents))
						if _, ok := piImageMIMEType(contents); !ok {
							return false, errors.New("Attach PNG, JPEG, GIF or WebP images")
						}
					}
					if _, err := h.projects.RecordThreadPrompt(item.ID, thread.ID, time.Now().UTC()); err != nil {
						return false, err
					}
					return true, p.prompt(message)
				case "abort":
					p.mu.Lock()
					turnID := p.turnID
					p.mu.Unlock()
					if turnID == "" {
						return false, nil
					}
					_, err := p.rpc("turn/interrupt", map[string]string{"threadId": p.threadID, "turnId": turnID})
					return true, err
				case "respond":
					return true, p.respond(message)
				default:
					return false, errors.New("Unknown chat command")
				}
			}, nil)
			if err != nil {
				sendError(err)
			} else if message.Type == "prompt" {
				_ = writer.Write(websocket.TextMessage, []byte(`{"type":"chat_sent"}`))
			}
		case <-peer.ping.C:
			if err := peer.WritePing(); err != nil {
				return
			}
		case <-peer.done:
			return
		case <-p.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// Keep sidebar activity alive even when Codex is waiting silently on a tool.
// The subscriber runs outside process locks so store callbacks cannot deadlock
// settlement's launch fence.
func (m *codexNativeManager) watchActivity(p *codexNativeProcess) {
	if m.activityReporter == nil && m.usageReporter == nil {
		return
	}
	sub := p.events.Subscribe()
	go func() {
		defer sub.Close()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		working := false
		for {
			select {
			case raw, ok := <-sub.Events():
				if !ok {
					return
				}
				var update struct {
					Type  string
					Value struct{ Working bool }
				}
				if json.Unmarshal(raw, &update) == nil && update.Type == "chat_state" && update.Value.Working != working {
					working = update.Value.Working
					if m.activityReporter != nil {
						m.activityReporter(p.key, working)
					}
				}
				if update.Type == "chat_usage" && m.usageReporter != nil {
					var event struct{ Value chatUsage }
					_ = json.Unmarshal(raw, &event)
					usage := event.Value
					p.mu.Lock()
					sessionID := p.threadID
					p.mu.Unlock()
					m.usageReporter(p.key, "codex-native:"+sessionID, threadUsageTotals{InputTokens: max(0, usage.InputTokens-usage.CachedInputTokens), CacheReadTokens: max(0, usage.CachedInputTokens), OutputTokens: max(0, usage.OutputTokens), TotalTokens: max(0, usage.InputTokens) + max(0, usage.OutputTokens)})
				}
			case <-ticker.C:
				if working && m.activityReporter != nil {
					m.activityReporter(p.key, true)
				}
			case <-p.done:
				if working && m.activityReporter != nil {
					m.activityReporter(p.key, false)
				}
				return
			}
		}
	}()
}
