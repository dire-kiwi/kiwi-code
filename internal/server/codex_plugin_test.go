package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMaterializeCodexPlugin(t *testing.T) {
	dataDirectory := t.TempDir()
	obsolete := []string{
		filepath.Join(dataDirectory, "codex-marketplace", "plugins", codexPluginName, "servers", "kiwi-code-plans.mjs"),
		filepath.Join(dataDirectory, "codex-marketplace", "plugins", codexPluginName, "skills", "kiwi-code-planner", "SKILL.md"),
	}
	for _, path := range obsolete {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("obsolete"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installation, err := materializeCodexPlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range obsolete {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete plugin artifact still exists at %q: %v", path, err)
		}
	}
	if installation.Version != codexPluginVersion || installation.PluginRoot == "" ||
		installation.MarketplaceRoot == "" || installation.MarketplaceName != managedCodexMarketplaceName(dataDirectory) {
		t.Fatalf("Codex plugin installation = %#v", installation)
	}

	files, err := codexPluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(installation.PluginRoot, file.path))
		if err != nil {
			t.Fatalf("read materialized %s: %v", file.path, err)
		}
		if !bytes.Equal(contents, file.contents) {
			t.Fatalf("materialized %s differs from embedded source", file.path)
		}
	}

	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Skills      string `json:"skills"`
		MCPServers  string `json:"mcpServers"`
	}
	if err := json.Unmarshal(codexPluginManifest, &manifest); err != nil {
		t.Fatalf("parse Codex plugin manifest: %v", err)
	}
	if manifest.Name != codexPluginName || manifest.Version != codexPluginVersion ||
		manifest.Skills != "./skills/" || manifest.MCPServers != "./.mcp.json" {
		t.Fatalf("Codex plugin manifest = %#v", manifest)
	}
	for _, capability := range []string{"activity", "browser", "process", "thread"} {
		if !strings.Contains(strings.ToLower(manifest.Description), capability) {
			t.Fatalf("Codex plugin description %q does not mention %q", manifest.Description, capability)
		}
	}
	if bytes.Contains(codexPluginManifest, []byte(`"hooks"`)) {
		t.Fatal("Codex plugin manifest declares unsupported hooks instead of using root discovery")
	}

	var mcpConfig struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Cwd     string   `json:"cwd"`
			EnvVars []string `json:"env_vars"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(codexPluginMCPConfig, &mcpConfig); err != nil {
		t.Fatalf("parse Codex MCP config: %v", err)
	}
	if len(mcpConfig.MCPServers) != 1 {
		t.Fatalf("Codex MCP servers = %#v", mcpConfig.MCPServers)
	}
	for _, name := range []string{"kiwi-code-browser"} {
		server, ok := mcpConfig.MCPServers[name]
		// Codex resolves a relative MCP cwd against the plugin root; it does not
		// expand ${PLUGIN_ROOT} placeholders inside stdio argument strings.
		if !ok || server.Command != "node" || len(server.Args) != 1 ||
			!strings.HasPrefix(server.Args[0], "./servers/") || server.Cwd != "." {
			t.Fatalf("Codex %s MCP server = %#v", name, server)
		}
		joinedEnvironment := strings.Join(server.EnvVars, "\n")
		for _, variable := range []string{"KIWI_CODE_THREAD_ENDPOINT", "KIWI_CODE_AGENT_TOKEN_FILE"} {
			if !strings.Contains(joinedEnvironment, variable) {
				t.Fatalf("Codex %s MCP environment = %#v, missing %s", name, server.EnvVars, variable)
			}
		}
	}
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionEnd"} {
		if !bytes.Contains(codexPluginHooks, []byte(`"`+event+`"`)) {
			t.Fatalf("Codex hooks do not contain %s", event)
		}
	}
	if !bytes.Contains(codexPluginHooks, []byte("$PLUGIN_ROOT/scripts/kiwi-code-hook.mjs")) {
		t.Fatal("Codex hooks do not use the plugin-owned lifecycle script")
	}
	browserServer := codexBrowserServerContents()
	if bytes.Contains(bytes.ToLower(browserServer), []byte("claude")) ||
		bytes.Contains(bytes.ToLower(browserServer), []byte("context: fork")) {
		t.Fatal("Codex browser MCP server retains Claude-specific runtime behavior")
	}

	marketplaceContents, err := os.ReadFile(filepath.Join(
		installation.MarketplaceRoot, ".agents", "plugins", "marketplace.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	expectedMarketplace, err := codexMarketplaceContents(installation.MarketplaceName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marketplaceContents, expectedMarketplace) {
		t.Fatal("materialized Codex marketplace differs from its managed source")
	}
	for _, skill := range []struct {
		name     string
		contents []byte
	}{
		{name: "kiwi-code-in-app-browser", contents: codexPluginBrowserSkill},
		{name: "kiwi-code-processes", contents: codexPluginProcessSkill},
	} {
		if !bytes.Contains(skill.contents, []byte("\nname: "+skill.name+"\n")) {
			t.Fatalf("Codex skill %s has invalid frontmatter", skill.name)
		}
	}
	for _, script := range []string{
		"common.mjs", "interrupt-process.mjs", "list-processes.mjs", "read-logs.mjs",
		"send-input.mjs", "start-process.mjs", "stop-process.mjs",
	} {
		if _, err := os.Stat(filepath.Join(
			installation.PluginRoot, "skills", "kiwi-code-processes", "scripts", script,
		)); err != nil {
			t.Fatalf("materialized Codex process helper %q: %v", script, err)
		}
	}
	retiredHelper := filepath.Join(installation.PluginRoot, "skills", agentSkillName, "scripts", retiredProcessUpdateHelperName)
	if err := os.WriteFile(retiredHelper, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeCodexPlugin(dataDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retiredHelper); !os.IsNotExist(err) {
		t.Fatalf("retired Codex process helper remains after materialization: %v", err)
	}
}

func TestDefaultCodexConfigDirectoryHonorsCodexHome(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "codex-home")
	t.Setenv("CODEX_HOME", configured)
	got, err := defaultCodexConfigDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if got != configured {
		t.Fatalf("Codex config directory = %q, want %q", got, configured)
	}
}

func TestManagedCodexNamespacesIsolateDataDirectories(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if managedCodexProfileName(first) == managedCodexProfileName(second) {
		t.Fatal("managed Codex profiles collide across data directories")
	}
	if managedCodexMarketplaceName(first) == managedCodexMarketplaceName(second) {
		t.Fatal("managed Codex marketplaces collide across data directories")
	}
	if managedCodexProfileName(first) != managedCodexProfileName(first) ||
		managedCodexMarketplaceName(first) != managedCodexMarketplaceName(first) {
		t.Fatal("managed Codex namespaces are not stable")
	}
}

func TestPrepareCodexPluginProfileUsesNormalCodexHome(t *testing.T) {
	dataDirectory := t.TempDir()
	installation, err := materializeCodexPlugin(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := filepath.Join(t.TempDir(), "codex")
	profileName := managedCodexProfileName(dataDirectory)
	if !strings.HasPrefix(profileName, "kiwi-code-") || profileName != managedCodexProfileName(dataDirectory) {
		t.Fatalf("managed Codex profile name = %q", profileName)
	}
	// Preparation is idempotent and preserves the user's existing base configuration.
	baseConfig := []byte("model = \"user-model\"\n")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.toml"), baseConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexPluginProfile(configDirectory, profileName, installation); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexPluginProfile(configDirectory, profileName, installation); err != nil {
		t.Fatal(err)
	}
	currentBase, err := os.ReadFile(filepath.Join(configDirectory, "config.toml"))
	if err != nil || !bytes.Equal(currentBase, baseConfig) {
		t.Fatalf("base Codex config = %q, err=%v", currentBase, err)
	}

	profile, err := os.ReadFile(filepath.Join(configDirectory, profileName+".config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		codexManagedProfileMarker,
		"[features]\nhooks = true\nplugins = true",
		"[marketplaces." + installation.MarketplaceName + "]",
		canonicalCodexPath(installation.MarketplaceRoot),
		`[plugins."kiwi-code@` + installation.MarketplaceName + "\"]\nenabled = true",
		"[plugins.\"browser@openai-bundled\"]\nenabled = false",
	} {
		if !strings.Contains(string(profile), expected) {
			t.Fatalf("managed Codex profile %q does not contain %q", profile, expected)
		}
	}
	if strings.Count(string(profile), "enabled = false") != 1 {
		t.Fatalf("managed Codex profile disables unexpected plugins: %q", profile)
	}

	files, err := codexPluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(
		configDirectory, "plugins", "cache", installation.MarketplaceName, codexPluginName, codexPluginVersion,
	)
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(cacheRoot, file.path))
		if err != nil {
			t.Fatalf("read cached Codex plugin file %s: %v", file.path, err)
		}
		if !bytes.Equal(contents, file.contents) {
			t.Fatalf("cached Codex plugin file %s differs from source", file.path)
		}
	}
}

func TestPrepareCodexPluginProfileRefusesAnUnmanagedCollision(t *testing.T) {
	installation, err := materializeCodexPlugin(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := t.TempDir()
	profileName := "kiwi-code-collision"
	profilePath := filepath.Join(configDirectory, profileName+".config.toml")
	if err := os.WriteFile(profilePath, []byte("model = \"personal\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexPluginProfile(configDirectory, profileName, installation); err == nil ||
		!strings.Contains(err.Error(), "not managed") {
		t.Fatalf("unmanaged Codex profile collision error = %v", err)
	}
	contents, err := os.ReadFile(profilePath)
	if err != nil || string(contents) != "model = \"personal\"\n" {
		t.Fatalf("unmanaged Codex profile was changed: %q, err=%v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(configDirectory, "plugins", "cache")); !os.IsNotExist(err) {
		t.Fatalf("unmanaged Codex profile collision populated the plugin cache: %v", err)
	}
}

func TestCodexPluginHookReportsActivityAndNamesTheThread(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	installation, err := materializeCodexPlugin(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	type activityUpdate struct {
		State string `json:"state"`
		Agent string `json:"agent"`
	}
	updates := make(chan activityUpdate, 16)
	var mu sync.Mutex
	patchedTitle := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"title": "New thread", "autoNamed": false})
		case http.MethodPatch:
			var input struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode Codex title update: %v", err)
			}
			mu.Lock()
			patchedTitle = input.Title
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"title": input.Title, "autoNamed": true})
		case http.MethodPut:
			if r.URL.Path != "/codex/activity" {
				t.Errorf("Codex activity path = %q", r.URL.Path)
			}
			var input activityUpdate
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode Codex activity: %v", err)
			}
			updates <- input
			writeJSON(w, http.StatusOK, map[string]any{"state": input.State})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	fakePi := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(fakePi, []byte("#!/bin/sh\nprintf 'Create Codex Plugin Integration\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDirectory := t.TempDir()
	environment := testEnvironmentWithoutColorConflicts()
	environment = append(environment,
		"KIWI_CODE_THREAD_ENDPOINT="+server.URL,
		"KIWI_CODE_PROJECT_ID=project",
		"KIWI_CODE_THREAD_ID=thread",
		"KIWI_CODE_CODING_AGENT=codex",
		"KIWI_CODE_PI_PATH="+fakePi,
		"KIWI_CODE_CODEX_STATE_DIR="+stateDirectory,
	)
	scriptPath := filepath.Join(installation.PluginRoot, "scripts", "kiwi-code-hook.mjs")
	input := `{"session_id":"codex-session","turn_id":"turn-1","hook_event_name":"UserPromptSubmit","prompt":"create a Codex plugin"}`
	run := func(action string) []byte {
		t.Helper()
		command := exec.Command(nodePath, scriptPath, action)
		command.Stdin = strings.NewReader(input)
		command.Env = environment
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run Codex %s hook: %v: %s", action, err, output)
		}
		return output
	}

	if output := run("title"); len(bytes.TrimSpace(output)) != 0 {
		t.Fatalf("Codex title hook emitted unsupported hook output: %q", output)
	}
	mu.Lock()
	if patchedTitle != "Create Codex Plugin Integration" {
		t.Fatalf("Codex-generated thread title = %q", patchedTitle)
	}
	mu.Unlock()

	if err := os.WriteFile(fakePi, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	input = `{"session_id":"codex-failing-title","turn_id":"turn-2","hook_event_name":"UserPromptSubmit","prompt":"this title call fails"}`
	output := run("title")
	var hookFailure struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &hookFailure); err != nil ||
		!strings.Contains(hookFailure.SystemMessage, "Could not name Kiwi Code thread") {
		t.Fatalf("failed Codex title hook output = %q, err=%v", output, err)
	}

	run("start")
	workingDeadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.State == "working" {
				if update.Agent != codingAgentCodex {
					t.Fatalf("Codex working activity agent = %q", update.Agent)
				}
				goto workingSeen
			}
		case <-workingDeadline:
			t.Fatal("Codex hook did not report working activity")
		}
	}

workingSeen:
	run("finished")
	finishedDeadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.State == "finished" {
				if update.Agent != codingAgentCodex {
					t.Fatalf("Codex finished activity agent = %q", update.Agent)
				}
				return
			}
		case <-finishedDeadline:
			t.Fatal("Codex hook did not report finished activity")
		}
	}
}
