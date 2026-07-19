package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	root, err := materializeClaudePlugin(t.TempDir())
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
	if manifest.Name != "dire-mux" {
		t.Fatalf("plugin name = %q, want dire-mux", manifest.Name)
	}
	for _, capability := range []string{"browser", "process", "workflow"} {
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
		browserServer.Env["DIRE_MUX_THREAD_ENDPOINT"] != "${DIRE_MUX_THREAD_ENDPOINT}" ||
		browserServer.Env["DIRE_MUX_AGENT_TOKEN_FILE"] != "${CLAUDE_PLUGIN_ROOT}/../"+agentTokenFileName {
		t.Fatalf("browser MCP server config = %#v", browserServer)
	}
	workflowServer, ok := mcpConfig.MCPServers["workflows"]
	if !ok || workflowServer.Command != "node" || len(workflowServer.Args) != 1 || !strings.Contains(workflowServer.Args[0], "dire-mux-workflows.mjs") ||
		workflowServer.Env["DIRE_MUX_THREAD_ENDPOINT"] != "${DIRE_MUX_THREAD_ENDPOINT}" ||
		workflowServer.Env["DIRE_MUX_AGENT_TOKEN_FILE"] != "${CLAUDE_PLUGIN_ROOT}/../"+agentTokenFileName {
		t.Fatalf("workflow MCP server config = %#v", workflowServer)
	}
	if !bytes.Contains(claudePluginBrowserSkill, []byte("ToolSearch")) {
		t.Fatal("Claude browser skill does not explain deferred MCP tool discovery")
	}
	if !bytes.Contains(claudePluginBrowserSkill, []byte("\ncontext: fork\n")) {
		t.Fatal("Claude browser skill does not run in a forked agent context")
	}
	if !bytes.Contains(claudePluginProcessSkill, []byte("${CLAUDE_PLUGIN_ROOT}")) {
		t.Fatal("Claude process skill does not use its materialized plugin path")
	}
	if !bytes.Contains(claudePluginWorkflowSkill, []byte("dire_mux_run_workflow")) ||
		!bytes.Contains(claudePluginWorkflowSkill, []byte("built-in `Workflow`")) ||
		!bytes.Contains(claudePluginWorkflowSkill, []byte("current human-authored prompt")) ||
		!bytes.Contains(claudePluginHooks, []byte("workflow-activation")) {
		t.Fatal("Claude workflow integration does not enforce and explain explicit human activation")
	}
	for _, name := range []string{
		"common.mjs", "interrupt-process.mjs", "list-processes.mjs", "read-logs.mjs",
		"send-input.mjs", "start-process.mjs", "stop-process.mjs",
	} {
		if _, err := os.Stat(filepath.Join(root, "skills", "dire-mux-processes", "scripts", name)); err != nil {
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
	requests := make([]browserRequest, 0, 11)
	png := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	results := map[string]any{
		"session.status": map[string]any{"message": "Browser ready.", "status": map[string]any{"endpoint": "in-app", "owned": true, "pages": 1, "reachable": true}},
		"tabs.list":      map[string]any{"message": "One tab.", "pages": []any{map[string]any{"id": "page-1", "title": "Example", "url": "https://example.com/"}}},
		"navigate.goto":  map[string]any{"action": "goto", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"snapshot":       map[string]any{"includedNodes": 2, "omittedNodes": 0, "refs": 1, "targetId": "page-1", "title": "Example", "url": "https://example.com/", "text": "heading Example\nbutton Continue [ref=e1]"},
		"click":          map[string]any{"clicked": "e1", "newTabs": []any{}, "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"fill":           map[string]any{"filled": "e2", "submitted": false, "targetId": "page-1", "textLength": 5, "title": "Example", "url": "https://example.com/"},
		"key":            map[string]any{"chord": "Enter", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"wait":           map[string]any{"elapsedMs": 10, "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"evaluate":       map[string]any{"result": "Example", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"screenshot":     map[string]any{"data": png, "mimeType": "image/png", "targetId": "page-1", "title": "Example", "url": "https://example.com/"},
		"cdp":            map[string]any{"method": "Network.enable", "result": map[string]any{}, "target": "page", "targetId": "page-1"},
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
	command := exec.CommandContext(ctx, nodePath, filepath.Join(pluginRoot, "servers", "dire-mux-browser.mjs"))
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT="+api.URL,
		"DIRE_MUX_AGENT_TOKEN_FILE="+filepath.Join(pluginRoot, "..", agentTokenFileName),
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
	if !ok || len(listedTools) != 11 {
		t.Fatalf("MCP tools = %#v, want 11", listed["tools"])
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
	if requests[2].Params["url"] != "https://example.com" {
		t.Fatalf("navigate params = %#v", requests[2].Params)
	}
	if requests[10].Params["target"] != "page" {
		t.Fatalf("CDP params = %#v", requests[10].Params)
	}
	if !slices.Equal(toolNames, []string{
		"browser_session", "browser_tabs", "browser_navigate", "browser_snapshot", "browser_click",
		"browser_fill", "browser_key", "browser_wait", "browser_evaluate", "browser_screenshot", "browser_cdp",
	}) {
		t.Fatalf("browser MCP tool names = %#v", toolNames)
	}
}

func TestClaudeWorkflowMCPServer(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dataDirectory := t.TempDir()
	pluginRoot, err := materializeClaudePlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, agentTokenFileName), []byte("claude-workflow-capability\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	basePath := "/api/projects/project/threads/thread/workflows"
	var mu sync.Mutex
	requests := make([]string, 0)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(agentTokenHeader); got != "claude-workflow-capability" {
			t.Errorf("workflow MCP token = %q", got)
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		run := map[string]any{
			"id": "wf-test", "name": "MCP test", "description": "Workflow MCP lifecycle",
			"scriptPath": "/tmp/workflow.js", "agents": []any{},
			"createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == basePath:
			var input struct {
				Script string         `json:"script"`
				Args   map[string]any `json:"args"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode workflow MCP start: %v", err)
			}
			if !strings.Contains(input.Script, "export const meta") || input.Args["scope"] != "test" {
				t.Errorf("workflow MCP start input = %#v", input)
			}
			run["state"] = workflowStateQueued
			writeJSON(w, http.StatusCreated, run)
		case r.Method == http.MethodGet && r.URL.Path == basePath+"/wf-test":
			run["state"] = workflowStateFinished
			run["result"] = map[string]any{"ok": true}
			writeJSON(w, http.StatusOK, run)
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/commands/run/saved-test":
			run["state"] = workflowStateQueued
			writeJSON(w, http.StatusCreated, run)
		case r.Method == http.MethodGet && r.URL.Path == basePath+"/saved":
			writeJSON(w, http.StatusOK, []any{map[string]any{"name": "saved-test", "scope": "project", "path": "/tmp/saved-test.js"}})
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/wf-test/save":
			writeJSON(w, http.StatusCreated, map[string]any{"name": "saved-test", "scope": "project", "path": "/tmp/saved-test.js"})
		case r.Method == http.MethodGet && r.URL.Path == basePath:
			run["state"] = workflowStateFinished
			writeJSON(w, http.StatusOK, []any{run})
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/wf-test/pause":
			run["state"] = workflowStatePaused
			writeJSON(w, http.StatusOK, run)
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/wf-test/resume":
			run["state"] = workflowStateQueued
			writeJSON(w, http.StatusOK, run)
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/wf-test/stop":
			run["state"] = workflowStateStopped
			run["error"] = "Workflow stopped."
			writeJSON(w, http.StatusOK, run)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, filepath.Join(pluginRoot, "servers", "dire-mux-workflows.mjs"))
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT="+api.URL+"/api/projects/project/threads/thread",
		"DIRE_MUX_AGENT_TOKEN_FILE="+filepath.Join(pluginRoot, "..", agentTokenFileName),
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
	nextID := 0
	roundTrip := func(method string, params any) map[string]any {
		t.Helper()
		nextID++
		encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": nextID, "method": method, "params": params})
		if _, err := stdin.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write workflow MCP request: %v; stderr=%s", err, stderr.String())
		}
		if !scanner.Scan() {
			t.Fatalf("read workflow MCP response: %v; stderr=%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("parse workflow MCP response: %v", err)
		}
		if response["error"] != nil || response["id"] != float64(nextID) {
			t.Fatalf("workflow MCP response = %#v", response)
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("workflow MCP result = %#v", response["result"])
		}
		return result
	}

	roundTrip("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	listed := roundTrip("tools/list", map[string]any{})
	listedTools, ok := listed["tools"].([]any)
	if !ok || len(listedTools) != 9 {
		t.Fatalf("workflow MCP tools = %#v", listed["tools"])
	}
	runResult := roundTrip("tools/call", map[string]any{
		"name": "dire_mux_run_workflow",
		"arguments": map[string]any{
			"script": "export const meta = { name: 'test', description: 'test' }\nreturn { ok: true }",
			"args":   map[string]any{"scope": "test"},
			"wait":   true,
		},
	})
	if runResult["isError"] != false || !strings.Contains(fmt.Sprint(runResult["content"]), "finished") {
		t.Fatalf("workflow MCP run result = %#v", runResult)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "dire_mux_run_saved_workflow", args: map[string]any{"name": "saved-test"}},
		{name: "dire_mux_list_saved_workflows", args: map[string]any{}},
		{name: "dire_mux_save_workflow", args: map[string]any{"runId": "wf-test", "name": "saved-test", "scope": "project"}},
		{name: "dire_mux_list_workflows", args: map[string]any{}},
		{name: "dire_mux_wait_workflow", args: map[string]any{"runId": "wf-test"}},
		{name: "dire_mux_pause_workflow", args: map[string]any{"runId": "wf-test"}},
		{name: "dire_mux_resume_workflow", args: map[string]any{"runId": "wf-test"}},
		{name: "dire_mux_stop_workflow", args: map[string]any{"runId": "wf-test"}},
	} {
		result := roundTrip("tools/call", map[string]any{"name": call.name, "arguments": call.args})
		if result["isError"] != false {
			t.Fatalf("%s result = %#v", call.name, result)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, expected := range []string{
		"POST " + basePath,
		"POST " + basePath + "/commands/run/saved-test",
		"GET " + basePath + "/saved",
		"POST " + basePath + "/wf-test/save",
		"GET " + basePath + "/wf-test",
		"GET " + basePath,
		"POST " + basePath + "/wf-test/pause",
		"POST " + basePath + "/wf-test/resume",
		"POST " + basePath + "/wf-test/stop",
	} {
		if !slices.Contains(requests, expected) {
			t.Fatalf("workflow MCP requests %#v do not include %q", requests, expected)
		}
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
	command := exec.CommandContext(ctx, nodePath, filepath.Join(pluginRoot, "scripts", "dire-mux-hook.mjs"), "heartbeat")
	command.Stdin = strings.NewReader(`{"session_id":"session-1","prompt_id":"prompt-1"}`)
	command.Env = append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT="+activityServer.URL,
		"DIRE_MUX_PROJECT_ID=project",
		"DIRE_MUX_THREAD_ID=thread",
		"DIRE_MUX_CLAUDE_STATE_DIR="+t.TempDir(),
		"DIRE_MUX_CODING_AGENT="+codingAgentClaude,
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

func TestClaudePluginHookActivatesOnlyThroughManagedPromptEndpoint(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dataDirectory := t.TempDir()
	pluginRoot, err := materializeClaudePlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, agentTokenFileName), []byte("hook-capability\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var prompt, source, mode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/project/threads/thread/workflows/activation" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get(agentTokenHeader); got != "hook-capability" {
			t.Errorf("hook capability = %q", got)
		}
		var input struct {
			Prompt string `json:"prompt"`
			Source string `json:"source"`
			Mode   string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode activation: %v", err)
		}
		prompt, source, mode = input.Prompt, input.Source, input.Mode
		writeJSON(w, http.StatusOK, workflowActivationSnapshot{Activated: true, Mode: "ultracode"})
	}))
	defer server.Close()
	command := exec.Command(nodePath, filepath.Join(pluginRoot, "scripts", "dire-mux-hook.mjs"), "workflow-activation")
	command.Env = append(os.Environ(),
		"CLAUDE_PLUGIN_ROOT="+pluginRoot,
		"DIRE_MUX_THREAD_ENDPOINT="+server.URL+"/api/projects/project/threads/thread",
	)
	command.Stdin = strings.NewReader(`{"hook_event_name":"UserPromptSubmit","prompt":"use a workflow for this audit","effort":{"level":"ultracode"}}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("activation hook: %v, output=%s", err, output)
	}
	if prompt != "use a workflow for this audit" || source != "claude-hook" || mode != "ultracode" {
		t.Fatalf("activation hook prompt=%q source=%q mode=%q", prompt, source, mode)
	}
	var hookOutput struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output, &hookOutput); err != nil {
		t.Fatalf("decode activation hook output %q: %v", output, err)
	}
	if hookOutput.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
		!strings.Contains(hookOutput.HookSpecificOutput.AdditionalContext, "activated Dire Mux workflows (ultracode)") {
		t.Fatalf("activation hook output = %q", output)
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
	fakePiScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DIRE_MUX_TEST_PI_ARGS\"\nprintf 'Add Claude Status Integration\\n'\n"
	if err := os.WriteFile(fakePi, []byte(fakePiScript), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"session-1","prompt_id":"prompt-1","hook_event_name":"UserPromptSubmit","prompt":"add status and titles"}`
	stateDirectory := t.TempDir()
	hookEnvironment := append(os.Environ(),
		"DIRE_MUX_THREAD_ENDPOINT="+server.URL,
		"DIRE_MUX_PROJECT_ID=project",
		"DIRE_MUX_THREAD_ID=thread",
		"DIRE_MUX_PI_PATH="+fakePi,
		"DIRE_MUX_TEST_PI_ARGS="+piArgsPath,
		"DIRE_MUX_CLAUDE_STATE_DIR="+stateDirectory,
		"DIRE_MUX_CODING_AGENT="+codingAgentClaudeGPT,
	)
	command := exec.Command(nodePath, filepath.Join(pluginRoot, "scripts", "dire-mux-hook.mjs"), "title")
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

	command = exec.Command(nodePath, filepath.Join(pluginRoot, "scripts", "dire-mux-hook.mjs"), "finished")
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
