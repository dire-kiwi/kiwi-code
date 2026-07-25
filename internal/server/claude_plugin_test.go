package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMaterializeClaudePlugin(t *testing.T) {
	dataDirectory := t.TempDir()
	root, err := materializeClaudePlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}

	files, err := claudePluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(root, file.path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, file.contents) {
			t.Fatalf("materialized %s differs from the embedded source", file.path)
		}
	}

	var manifest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(claudePluginManifest, &manifest); err != nil {
		t.Fatalf("parse plugin manifest: %v", err)
	}
	if manifest.Name != "kiwi-code" {
		t.Fatalf("plugin name = %q, want kiwi-code", manifest.Name)
	}
	for _, capability := range []string{"browser", "process"} {
		if !strings.Contains(strings.ToLower(manifest.Description), capability) {
			t.Fatalf("plugin description %q does not mention %q support", manifest.Description, capability)
		}
	}
	var hooks map[string]any
	if err := json.Unmarshal(claudePluginHooks, &hooks); err != nil {
		t.Fatalf("parse plugin hooks: %v", err)
	}
	var mcpConfig struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(claudePluginMCPConfig, &mcpConfig); err != nil {
		t.Fatalf("parse plugin MCP config: %v", err)
	}
	browserServer, ok := mcpConfig.MCPServers["browser"]
	if !ok || browserServer.Command != "node" || len(browserServer.Args) != 1 || !strings.Contains(browserServer.Args[0], "${CLAUDE_PLUGIN_ROOT}") ||
		browserServer.Env["KIWI_CODE_THREAD_ENDPOINT"] != "${KIWI_CODE_THREAD_ENDPOINT}" ||
		browserServer.Env["KIWI_CODE_AGENT_TOKEN_FILE"] != "${CLAUDE_PLUGIN_ROOT}/../"+agentTokenFileName {
		t.Fatalf("browser MCP server config = %#v", browserServer)
	}
	if len(mcpConfig.MCPServers) != 1 {
		t.Fatalf("Claude plugin MCP servers = %#v, want browser only", mcpConfig.MCPServers)
	}
	if strings.Contains(strings.ToLower(manifest.Description), "workflow") ||
		bytes.Contains(claudePluginHooks, []byte("workflow-activation")) ||
		bytes.Contains(claudePluginHookScript, []byte("/workflows/activation")) {
		t.Fatal("Claude plugin still advertises or activates Kiwi Code workflows")
	}
	if !bytes.Contains(claudePluginBrowserSkill, []byte("ToolSearch")) {
		t.Fatal("Claude browser skill does not explain deferred MCP tool discovery")
	}
	if !bytes.Contains(claudePluginBrowserSkill, []byte("\ncontext: fork\n")) {
		t.Fatal("Claude browser skill does not run in a forked agent context")
	}
	if !bytes.Contains(claudePluginBrowserSkill, []byte("browser_recording")) || !bytes.Contains(claudePluginBrowserSkill, []byte("inactivity timeout")) {
		t.Fatal("Claude browser skill does not own recording lifecycle and inactivity cleanup")
	}
	if !bytes.Contains(claudePluginProcessSkill, []byte("\nname: kiwi-code-processes\n")) {
		t.Fatal("Claude process skill does not use its Kiwi Code name")
	}
	if !bytes.Contains(claudePluginProcessSkill, []byte("\ncontext: fork\n")) {
		t.Fatal("Claude process skill does not run in a forked agent context")
	}
	if !bytes.Contains(claudePluginProcessSkill, []byte("${CLAUDE_PLUGIN_ROOT}")) {
		t.Fatal("Claude process skill does not use its materialized plugin path")
	}
	for _, name := range []string{
		"common.mjs", "interrupt-process.mjs", "list-processes.mjs", "read-logs.mjs",
		"send-input.mjs", "start-process.mjs", "stop-process.mjs", "update-process.mjs",
	} {
		if _, err := os.Stat(filepath.Join(root, "skills", "kiwi-code-processes", "scripts", name)); err != nil {
			t.Fatalf("materialized Claude process helper %q: %v", name, err)
		}
	}
}

func TestClaudeBrowserMCPServer(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dataDirectory := t.TempDir()
	pluginRoot, err := materializeClaudePlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dataDirectory, agentTokenFileName)
	if err := os.WriteFile(tokenPath, []byte("claude-browser-capability\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type browserRequest struct {
		Operation string         `json:"operation"`
		Params    map[string]any `json:"params"`
	}
	var mu sync.Mutex
	requests := make([]browserRequest, 0, 14)
	png := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	recordingID := "rec-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	activeRecording := map[string]any{"id": recordingID, "state": "recording", "targetId": "page-1", "title": "Demonstrate example navigation", "startedAt": "2026-07-21T12:00:00.000Z", "idleTimeoutMs": 120_000, "idleDeadlineAt": "2026-07-21T12:02:00.000Z"}
	completedRecording := map[string]any{"id": recordingID, "state": "completed", "targetId": "page-1", "title": "Demonstrate example navigation", "startedAt": "2026-07-21T12:00:00.000Z", "finishedAt": "2026-07-21T12:00:10.000Z", "durationMs": 10_000, "bytes": 1024, "mimeType": "video/webm;codecs=vp9", "filename": recordingID + ".webm"}
	results := map[string]any{
		"session.status":   map[string]any{"message": "Browser ready.", "status": map[string]any{"endpoint": "in-app", "owned": true, "pages": 1, "reachable": true}},
		"recording.status": map[string]any{"recording": nil, "recordings": []any{}},
		"recording.start":  activeRecording,
		"recording.stop":   completedRecording,
		"tabs.list":        map[string]any{"message": "One tab.", "pages": []any{map[string]any{"id": "page-1", "title": "Example", "url": "https://example.com/"}}},
		"navigate.goto":    map[string]any{"action": "goto", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"snapshot":         map[string]any{"includedNodes": 2, "omittedNodes": 0, "refs": 1, "targetId": "page-1", "title": "Example", "url": "https://example.com/", "text": "heading Example\nbutton Continue [ref=e1]"},
		"click":            map[string]any{"clicked": "e1", "newTabs": []any{}, "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"fill":             map[string]any{"filled": "e2", "submitted": false, "targetId": "page-1", "textLength": 5, "title": "Example", "url": "https://example.com/"},
		"key":              map[string]any{"chord": "Enter", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"wait":             map[string]any{"elapsedMs": 10, "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"evaluate":         map[string]any{"result": "Example", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"screenshot":       map[string]any{"data": png, "mimeType": "image/png", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"cdp":              map[string]any{"method": "Network.enable", "result": map[string]any{}, "target": "page", "targetId": "page-1"},
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/browser/actions" {
			t.Errorf("browser MCP request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get(agentTokenHeader); got != "claude-browser-capability" {
			t.Errorf("browser MCP agent token = %q", got)
		}
		var request browserRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Errorf("decode browser MCP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result, ok := results[request.Operation]
		if !ok {
			t.Errorf("unexpected browser operation %q", request.Operation)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"result": result})
	}))
	defer api.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, filepath.Join(pluginRoot, "servers", "kiwi-code-browser.mjs"))
	command.Env = append(os.Environ(),
		"KIWI_CODE_THREAD_ENDPOINT="+api.URL,
		"KIWI_CODE_AGENT_TOKEN_FILE="+filepath.Join(pluginRoot, "..", agentTokenFileName),
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	nextID := 0
	roundTrip := func(method string, params any) map[string]any {
		t.Helper()
		nextID++
		request := map[string]any{"jsonrpc": "2.0", "id": nextID, "method": method, "params": params}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stdin.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write MCP request: %v; stderr=%s", err, stderr.String())
		}
		if !scanner.Scan() {
			t.Fatalf("read MCP response: %v; stderr=%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("parse MCP response %q: %v", scanner.Bytes(), err)
		}
		if response["id"] != float64(nextID) {
			t.Fatalf("MCP response ID = %#v, want %d", response["id"], nextID)
		}
		if response["error"] != nil {
			t.Fatalf("MCP response error = %#v", response["error"])
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("MCP response result = %#v", response["result"])
		}
		return result
	}

	initialized := roundTrip("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	if initialized["protocolVersion"] != "2025-06-18" {
		t.Fatalf("MCP protocol version = %#v", initialized["protocolVersion"])
	}
	listed := roundTrip("tools/list", map[string]any{})
	listedTools, ok := listed["tools"].([]any)
	if !ok || len(listedTools) != 12 {
		t.Fatalf("MCP tools = %#v, want 12", listed["tools"])
	}
	toolNames := make([]string, 0, len(listedTools))
	for _, value := range listedTools {
		tool, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("MCP tool = %#v", value)
		}
		name, _ := tool["name"].(string)
		toolNames = append(toolNames, name)
		if !strings.HasPrefix(name, "browser_") {
			t.Fatalf("MCP tool name = %q", name)
		}
	}

	calls := []struct {
		name      string
		arguments map[string]any
		operation string
	}{
		{name: "browser_session", arguments: map[string]any{"action": "status"}, operation: "session.status"},
		{name: "browser_recording", arguments: map[string]any{"action": "status"}, operation: "recording.status"},
		{name: "browser_recording", arguments: map[string]any{"action": "start", "targetId": "page-1", "title": "Demonstrate example navigation", "idleTimeoutSeconds": 120}, operation: "recording.start"},
		{name: "browser_recording", arguments: map[string]any{"action": "stop", "recordingId": recordingID}, operation: "recording.stop"},
		{name: "browser_tabs", arguments: map[string]any{"action": "list"}, operation: "tabs.list"},
		{name: "browser_navigate", arguments: map[string]any{"action": "goto", "url": "https://example.com"}, operation: "navigate.goto"},
		{name: "browser_snapshot", arguments: map[string]any{}, operation: "snapshot"},
		{name: "browser_click", arguments: map[string]any{"ref": "e1"}, operation: "click"},
		{name: "browser_fill", arguments: map[string]any{"ref": "e2", "text": "hello"}, operation: "fill"},
		{name: "browser_key", arguments: map[string]any{"key": "Enter"}, operation: "key"},
		{name: "browser_wait", arguments: map[string]any{"timeMs": 10}, operation: "wait"},
		{name: "browser_evaluate", arguments: map[string]any{"expression": "document.title"}, operation: "evaluate"},
		{name: "browser_screenshot", arguments: map[string]any{"format": "png"}, operation: "screenshot"},
		{name: "browser_cdp", arguments: map[string]any{"method": "Network.enable"}, operation: "cdp"},
	}
	for _, call := range calls {
		result := roundTrip("tools/call", map[string]any{"name": call.name, "arguments": call.arguments})
		if result["isError"] != false {
			t.Fatalf("%s MCP result = %#v", call.name, result)
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("%s MCP content = %#v", call.name, result["content"])
		}
		if call.name == "browser_screenshot" && len(content) != 2 {
			t.Fatalf("screenshot MCP content = %#v, want text and image", content)
		}
	}
	invalid := roundTrip("tools/call", map[string]any{
		"name": "browser_click", "arguments": map[string]any{},
	})
	if invalid["isError"] != true {
		t.Fatalf("invalid MCP tool result = %#v, want tool error", invalid)
	}
	invalidRecording := roundTrip("tools/call", map[string]any{
		"name": "browser_recording", "arguments": map[string]any{"action": "stop"},
	})
	if invalidRecording["isError"] != true {
		t.Fatalf("invalid recording MCP result = %#v, want tool error", invalidRecording)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(calls) {
		t.Fatalf("browser requests = %d, want %d", len(requests), len(calls))
	}
	for index, call := range calls {
		if requests[index].Operation != call.operation {
			t.Fatalf("browser request %d operation = %q, want %q", index, requests[index].Operation, call.operation)
		}
	}
	requestByOperation := make(map[string]browserRequest, len(requests))
	for _, request := range requests {
		requestByOperation[request.Operation] = request
	}
	if requestByOperation["navigate.goto"].Params["url"] != "https://example.com" {
		t.Fatalf("navigate params = %#v", requestByOperation["navigate.goto"].Params)
	}
	if requestByOperation["recording.start"].Params["title"] != "Demonstrate example navigation" || requestByOperation["recording.start"].Params["idleTimeoutMs"] != float64(120_000) {
		t.Fatalf("recording start params = %#v", requestByOperation["recording.start"].Params)
	}
	if requestByOperation["recording.stop"].Params["recordingId"] != recordingID {
		t.Fatalf("recording stop params = %#v", requestByOperation["recording.stop"].Params)
	}
	if requestByOperation["cdp"].Params["target"] != "page" {
		t.Fatalf("CDP params = %#v", requestByOperation["cdp"].Params)
	}
	if !slices.Equal(toolNames, []string{
		"browser_session", "browser_recording", "browser_tabs", "browser_navigate", "browser_snapshot", "browser_click",
		"browser_fill", "browser_key", "browser_wait", "browser_evaluate", "browser_screenshot", "browser_cdp",
	}) {
		t.Fatalf("browser MCP tool names = %#v", toolNames)
	}
}

func TestClaudePluginHeartbeatReportsPromptStart(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	pluginRoot, err := materializeClaudePlugin(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	type activityUpdate struct {
		State           string `json:"state"`
		Agent           string `json:"agent"`
		PromptStartedAt string `json:"promptStartedAt"`
	}
	updates := make(chan activityUpdate, 1)
	activityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/claude/activity" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var update activityUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Errorf("decode Claude activity: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case updates <- update:
		default:
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": update.State})
	}))
	defer activityServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, nodePath, filepath.Join(pluginRoot, "scripts", "kiwi-code-hook.mjs"), "heartbeat")
	command.Stdin = strings.NewReader(`{"session_id":"session-1","prompt_id":"prompt-1"}`)
	command.Env = append(os.Environ(),
		"KIWI_CODE_THREAD_ENDPOINT="+activityServer.URL,
		"KIWI_CODE_PROJECT_ID=project",
		"KIWI_CODE_THREAD_ID=thread",
		"KIWI_CODE_CLAUDE_STATE_DIR="+t.TempDir(),
		"KIWI_CODE_CODING_AGENT="+codingAgentClaude,
	)
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()

	select {
	case update := <-updates:
		if update.State != "working" || update.Agent != codingAgentClaude {
			t.Fatalf("Claude heartbeat = %#v", update)
		}
		if _, err := time.Parse(time.RFC3339Nano, update.PromptStartedAt); err != nil {
			t.Fatalf("Claude prompt start time = %q: %v", update.PromptStartedAt, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Claude heartbeat")
	}
}

func TestClaudePluginNamesThreadWithPiFromFirstPrompt(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	pluginRoot, err := materializeClaudePlugin(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var patchedTitle string
	var autoGenerated bool
	var activityState string
	var activityAgent string
	var activityPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"title": "New thread", "autoNamed": false})
		case http.MethodPatch:
			var input struct {
				Title         string `json:"title"`
				AutoGenerated bool   `json:"autoGenerated"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode title update: %v", err)
			}
			mu.Lock()
			patchedTitle = input.Title
			autoGenerated = input.AutoGenerated
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"title": input.Title, "autoNamed": true})
		case http.MethodPut:
			var input struct {
				State string `json:"state"`
				Agent string `json:"agent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode activity update: %v", err)
			}
			mu.Lock()
			activityState = input.State
			activityAgent = input.Agent
			activityPath = r.URL.Path
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"state": input.State})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	fakePi := filepath.Join(t.TempDir(), "pi")
	piArgsPath := filepath.Join(t.TempDir(), "pi-args")
	fakePiScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$KIWI_CODE_TEST_PI_ARGS\"\nprintf 'Add Claude Status Integration\\n'\n"
	if err := os.WriteFile(fakePi, []byte(fakePiScript), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"session-1","prompt_id":"prompt-1","hook_event_name":"UserPromptSubmit","prompt":"add status and titles"}`
	stateDirectory := t.TempDir()
	hookEnvironment := append(os.Environ(),
		"KIWI_CODE_THREAD_ENDPOINT="+server.URL,
		"KIWI_CODE_PROJECT_ID=project",
		"KIWI_CODE_THREAD_ID=thread",
		"KIWI_CODE_PI_PATH="+fakePi,
		"KIWI_CODE_TEST_PI_ARGS="+piArgsPath,
		"KIWI_CODE_CLAUDE_STATE_DIR="+stateDirectory,
		"KIWI_CODE_CODING_AGENT="+codingAgentClaudeGPT,
	)
	command := exec.Command(nodePath, filepath.Join(pluginRoot, "scripts", "kiwi-code-hook.mjs"), "title")
	command.Stdin = strings.NewReader(input)
	command.Env = hookEnvironment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run Pi title hook: %v: %s", err, output)
	}
	var result struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			SessionTitle  string `json:"sessionTitle"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("parse hook output %q: %v", output, err)
	}
	if result.HookSpecificOutput.HookEventName != "UserPromptSubmit" || result.HookSpecificOutput.SessionTitle != "Add Claude Status Integration" {
		t.Fatalf("unexpected hook output: %#v", result)
	}
	piArgs, err := os.ReadFile(piArgsPath)
	if err != nil {
		t.Fatalf("read Pi title arguments: %v", err)
	}
	for _, expected := range []string{
		"--print\n",
		"--no-session\n",
		"--no-tools\n",
		"--no-extensions\n",
		"--no-skills\n",
		"--no-prompt-templates\n",
		"--no-themes\n",
		"--no-context-files\n",
		"--model\nopenai-codex/gpt-5.6-luna\n",
		"--thinking\nlow\n",
	} {
		if !strings.Contains(string(piArgs), expected) {
			t.Fatalf("Pi title arguments %q do not contain %q", piArgs, expected)
		}
	}
	mu.Lock()
	if patchedTitle != "Add Claude Status Integration" || !autoGenerated {
		t.Fatalf("title update = %q, autoGenerated=%t", patchedTitle, autoGenerated)
	}
	mu.Unlock()

	command = exec.Command(nodePath, filepath.Join(pluginRoot, "scripts", "kiwi-code-hook.mjs"), "finished")
	command.Stdin = strings.NewReader(input)
	command.Env = hookEnvironment
	if output, err = command.CombinedOutput(); err != nil {
		t.Fatalf("run Claude activity hook: %v: %s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if activityState != "finished" || activityAgent != codingAgentClaudeGPT || activityPath != "/claude/activity" {
		t.Fatalf("activity update = %q for %q at %q", activityState, activityAgent, activityPath)
	}
}
