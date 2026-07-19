package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

func TestDiscoverCLIProxyAPIGPTModelsFiltersTheOpenAIModelList(t *testing.T) {
	var authorization string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/gateway/v1/models" {
			t.Errorf("CLIProxyAPI request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		authorization = r.Header.Get("Authorization")
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{
			{"id": "gpt-5.4"},
			{"id": "claude-opus-4-6"},
			{"id": " gpt-5.3-codex "},
			{"id": "gpt-5.4"},
			{"id": "--gpt-invalid"},
		}})
	}))
	defer proxy.Close()

	models, err := discoverCLIProxyAPIGPTModels(
		context.Background(),
		proxy.Client(),
		proxy.URL+"/gateway/",
		"local-client-key",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []codingAgentChoice{
		{ID: "gpt-5.4", Label: "gpt-5.4"},
		{ID: "gpt-5.3-codex", Label: "gpt-5.3-codex"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("CLIProxyAPI GPT models = %#v, want %#v", models, want)
	}
	if authorization != "Bearer local-client-key" {
		t.Fatalf("CLIProxyAPI authorization = %q", authorization)
	}
}

func TestClaudeGPTProfileDirectoryIsPrivateAndRejectsSymlinks(t *testing.T) {
	dataDirectory := t.TempDir()
	profilePath, err := prepareClaudeGPTProfileDirectory(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if profilePath == filepath.Join(dataDirectory, ".claude") || filepath.Base(profilePath) != claudeGPTProfileDirectoryName {
		t.Fatalf("Claude GPT profile path = %q", profilePath)
	}
	info, err := os.Stat(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("Claude GPT profile permissions = %o, want 700", permissions)
	}

	symlinkData := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(symlinkData, claudeGPTProfileDirectoryName)); err != nil {
		t.Skipf("could not create profile symlink: %v", err)
	}
	if _, err := prepareClaudeGPTProfileDirectory(symlinkData); err == nil {
		t.Fatal("symlinked Claude GPT profile was accepted")
	}
}

func TestDiscoverClaudeSandboxPluginPathUsesTheExistingNonGPTProfile(t *testing.T) {
	configDirectory := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDirectory)
	installPath := filepath.Join(t.TempDir(), "sandbox")
	if err := os.MkdirAll(filepath.Join(installPath, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installPath, ".claude-plugin", "plugin.json"), []byte(`{"name":"sandbox-exec"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDirectory, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "settings.json"), []byte(`{"enabledPlugins":{"sandbox-exec@dire-agent-extensions":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := json.Marshal(map[string]any{
		"plugins": map[string]any{
			claudeSandboxPluginID: []map[string]string{{"installPath": installPath}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "plugins", "installed_plugins.json"), registry, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := discoverClaudeSandboxPluginPath()
	if err != nil || got != installPath {
		t.Fatalf("sandbox plugin path = %q, %v; want %q", got, err, installPath)
	}
}

func TestParsePiModels(t *testing.T) {
	output := []byte(`provider      model                context  max-out  thinking  images
openai-codex  gpt-5.6-sol          372K     128K     yes       yes
openai-codex  gpt-5.6-terra        372K     128K     yes       yes
openai-codex  gpt-5.6-sol          372K     128K     yes       yes
warning this line should be ignored
`)
	got := parsePiModels(output)
	want := []codingAgentChoice{
		{ID: "openai-codex/gpt-5.6-sol", Label: "gpt-5.6-sol · openai-codex"},
		{ID: "openai-codex/gpt-5.6-terra", Label: "gpt-5.6-terra · openai-codex"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePiModels() = %#v, want %#v", got, want)
	}
	capabilities := parsePiModelCapabilities(output)
	if len(capabilities) != 2 || !capabilities[0].SupportsThinking || !capabilities[1].SupportsThinking {
		t.Fatalf("parsePiModelCapabilities() = %#v", capabilities)
	}
}

func TestParsePiRPCModelCapabilitiesUsesExactThinkingLevelMap(t *testing.T) {
	output := []byte(`{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"mapped","name":"Mapped model","reasoning":true,"thinkingLevelMap":{"off":null,"minimal":null,"xhigh":"xhigh"}},{"provider":"custom","id":"max-model","name":"Max model","reasoning":true,"thinkingLevelMap":{"max":"max"}},{"provider":"custom","id":"plain","name":"Plain model","reasoning":false}]}}` + "\n")
	models, err := parsePiRPCModelCapabilities(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("RPC models = %#v", models)
	}
	if models[0].ID != "custom/mapped" || models[0].Label != "Mapped model · custom" ||
		!reflect.DeepEqual(models[0].ReasoningLevels, []string{"low", "medium", "high", "xhigh"}) {
		t.Fatalf("mapped RPC model = %#v", models[0])
	}
	if !reflect.DeepEqual(models[1].ReasoningLevels, []string{"off", "minimal", "low", "medium", "high", "max"}) {
		t.Fatalf("max RPC model = %#v", models[1])
	}
	if models[2].SupportsThinking || !reflect.DeepEqual(models[2].ReasoningLevels, []string{"off"}) {
		t.Fatalf("plain RPC model = %#v", models[2])
	}
	if _, err := parsePiRPCModelCapabilities([]byte("not an RPC response\n")); err == nil {
		t.Fatal("expected a missing RPC response to fail")
	}
}

func TestDiscoverPiModels(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, codingAgentPi)
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"model-a","name":"model-a","reasoning":true,"thinkingLevelMap":{"xhigh":"xhigh"}}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	models, err := discoverPiModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []codingAgentChoice{{ID: "custom/model-a", Label: "model-a · custom"}}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("discoverPiModels() = %#v, want %#v", models, want)
	}
}

func TestAvailablePiModelCapabilitiesCachesPerDirectory(t *testing.T) {
	directory := t.TempDir()
	piPath := filepath.Join(directory, "fake-pi")
	script := `#!/bin/sh
model=$(cat "$PWD/model.txt")
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"%s","name":"%s","reasoning":true}]}}\n' "$model" "$model"
      ;;
  esac
done
`
	if err := os.WriteFile(piPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cwdA := filepath.Join(directory, "cwd-a")
	cwdB := filepath.Join(directory, "cwd-b")
	for _, cwd := range []string{cwdA, cwdB} {
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cwdA, "model.txt"), []byte("model-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwdB, "model.txt"), []byte("model-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := &terminalHandler{nativePi: &piNativeManager{piPath: piPath}}
	modelsA, err := handler.availablePiModelCapabilities(context.Background(), cwdA, true)
	if err != nil || len(modelsA) != 1 || modelsA[0].ID != "custom/model-a" {
		t.Fatalf("models for first directory = %#v, error = %v", modelsA, err)
	}
	if err := os.WriteFile(filepath.Join(cwdA, "model.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cachedA, err := handler.availablePiModelCapabilities(context.Background(), cwdA, true)
	if err != nil || len(cachedA) != 1 || cachedA[0].ID != "custom/model-a" {
		t.Fatalf("cached models = %#v, error = %v", cachedA, err)
	}
	modelsB, err := handler.availablePiModelCapabilities(context.Background(), cwdB, true)
	if err != nil || len(modelsB) != 1 || modelsB[0].ID != "custom/model-b" {
		t.Fatalf("models for second directory = %#v, error = %v", modelsB, err)
	}
}

func TestAvailablePiModelCapabilitiesDoesNotSerializeMaximumFanout(t *testing.T) {
	directory := t.TempDir()
	piPath := filepath.Join(directory, "fake-pi")
	script := `#!/bin/sh
sleep 1
model=$(basename "$PWD")
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"%s","name":"%s","reasoning":true}]}}\n' "$model" "$model"
      ;;
  esac
done
`
	if err := os.WriteFile(piPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{nativePi: &piNativeManager{piPath: piPath}}
	type result struct {
		cwd    string
		models []piModelCapability
		err    error
	}
	const fanout = 16
	results := make(chan result, fanout)
	var wait sync.WaitGroup
	started := time.Now()
	for index := 0; index < fanout; index++ {
		cwd := filepath.Join(directory, fmt.Sprintf("case-%02d", index))
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), codingAgentModelDiscoveryTimeout)
			defer cancel()
			models, err := handler.availablePiModelCapabilities(ctx, cwd, true)
			results <- result{cwd: cwd, models: models, err: err}
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("capability probe for %q failed: %v", result.cwd, result.err)
		}
		want := "custom/" + filepath.Base(result.cwd)
		if len(result.models) != 1 || result.models[0].ID != want {
			t.Fatalf("models for %q = %#v, want %q", result.cwd, result.models, want)
		}
	}
	if elapsed := time.Since(started); elapsed >= 4*time.Second {
		t.Fatalf("maximum fanout took %v; distinct directories were serialized", elapsed)
	}
}

func TestDiscoverPiModelCapabilitiesLoadsExtensionsAndFailsClosed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, codingAgentPi)
	extensionPath := filepath.Join(directory, "provider-extension.ts")
	discoveryCwd := filepath.Join(directory, "project")
	if err := os.MkdirAll(filepath.Join(discoveryCwd, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(discoveryCwd, ".pi", "extensions", "provider.ts"), []byte("// project provider"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPECTED_EXTENSION", extensionPath)
	t.Setenv("EXPECTED_CWD", discoveryCwd)
	script := `#!/bin/sh
found_extension=0
found_approve=0
previous=''
for argument in "$@"; do
  if [ "$argument" = "--no-extensions" ]; then
    exit 9
  fi
  if [ "$argument" = "--approve" ]; then
    found_approve=1
  fi
  if [ "$previous" = "--extension" ] && [ "$argument" = "$EXPECTED_EXTENSION" ]; then
    found_extension=1
  fi
  previous="$argument"
done
if [ "$found_extension" != "1" ] || [ "$found_approve" != "1" ] || [ "$PWD" != "$EXPECTED_CWD" ] || [ ! -f .pi/extensions/provider.ts ]; then
  exit 8
fi
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"extension-only","id":"model-a","name":"Extension model","reasoning":true,"thinkingLevelMap":{"max":"max"}}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	models, err := discoverPiModelCapabilitiesAtPathInDirectory(context.Background(), path, discoveryCwd, true, extensionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "extension-only/model-a" ||
		!reflect.DeepEqual(models[0].ReasoningLevels, []string{"off", "minimal", "low", "medium", "high", "max"}) {
		t.Fatalf("extension model capabilities = %#v", models)
	}

	listOnlyScript := `#!/bin/sh
if [ "$1" = "--list-models" ]; then
  printf '%s\n' 'provider model context max-out thinking images' 'fallback model-a 128K 16K yes no'
  exit 0
fi
exit 7
`
	if err := os.WriteFile(path, []byte(listOnlyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverPiModelCapabilitiesAtPath(context.Background(), path); err == nil {
		t.Fatal("RPC failure unexpectedly fell back to --list-models")
	}
}

func TestDiscoverPiModelCapabilitiesLoadsAnApprovedProjectExtension(t *testing.T) {
	piPath, err := exec.LookPath(codingAgentPi)
	if err != nil {
		t.Skip("Pi is not installed")
	}
	projectDirectory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDirectory, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	extension := `export default function (pi) {
  pi.registerProvider("dire-mux-project-test", {
    name: "Dire Mux Project Test",
    baseUrl: "http://127.0.0.1:1",
    apiKey: "test-key",
    api: "openai-completions",
    models: [{
      id: "project-model",
      name: "Project Model",
      reasoning: true,
      thinkingLevelMap: { xhigh: null, max: "max" },
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 128000,
      maxTokens: 4096,
    }],
  });
}
`
	if err := os.WriteFile(filepath.Join(projectDirectory, ".pi", "extensions", "provider.ts"), []byte(extension), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, projectDirectory, "init", "-q")
	serverGit(t, projectDirectory, "config", "user.email", "test@example.com")
	serverGit(t, projectDirectory, "config", "user.name", "Test User")
	serverGit(t, projectDirectory, "add", ".pi/extensions/provider.ts")
	serverGit(t, projectDirectory, "commit", "-q", "-m", "add project provider")
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	serverGit(t, projectDirectory, "worktree", "add", "--detach", worktreePath, "HEAD")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", projectDirectory, "worktree", "remove", "--force", worktreePath).Run()
	})
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	unapprovedModels, err := discoverPiModelCapabilitiesFromRPCInDirectory(ctx, piPath, worktreePath, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range unapprovedModels {
		if model.ID == "dire-mux-project-test/project-model" {
			t.Fatal("unapproved project extension executed during safe model discovery")
		}
	}
	models, err := discoverPiModelCapabilitiesAtPathInDirectory(ctx, piPath, worktreePath, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.ID == "dire-mux-project-test/project-model" {
			if !reflect.DeepEqual(model.ReasoningLevels, []string{"off", "minimal", "low", "medium", "high", "max"}) {
				t.Fatalf("project model reasoning levels = %#v", model.ReasoningLevels)
			}
			return
		}
	}
	t.Fatalf("approved project extension model not discovered: %#v", models)
}

func TestListCodingAgents(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, codingAgentPi)
	script := `#!/bin/sh
for argument in "$@"; do
  if [ "$argument" = "--approve" ]; then exit 9; fi
done
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"model-a","name":"model-a","reasoning":true,"thinkingLevelMap":{"xhigh":"xhigh","max":"max"}},{"provider":"custom","id":"model-b","name":"model-b","reasoning":false}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{
			{"id": "gpt-5.4"},
			{"id": "gpt-5.3-codex"},
			{"id": "claude-opus-4-6"},
		}})
	}))
	defer proxy.Close()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/coding-agents", nil)
	handler := &terminalHandler{
		cliProxyAPIBaseURL:    proxy.URL,
		cliProxyAPIKey:        "test-key",
		cliProxyAPIHTTPClient: proxy.Client(),
	}
	handler.listCodingAgents(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var configs []codingAgentConfig
	if err := json.NewDecoder(response.Body).Decode(&configs); err != nil {
		t.Fatal(err)
	}
	if len(configs) != 3 || configs[0].ID != codingAgentPi || configs[1].ID != codingAgentClaude || configs[2].ID != codingAgentClaudeGPT {
		t.Fatalf("coding agent configs = %#v", configs)
	}
	if len(configs[0].Models) != 3 || configs[0].Models[1].ID != "custom/model-a" || configs[0].Models[2].ID != "custom/model-b" {
		t.Fatalf("Pi models = %#v", configs[0].Models)
	}
	if codingAgentChoiceExists(configs[1].ThinkingLevels, "minimal") {
		t.Fatalf("Claude thinking levels include Pi-only minimal: %#v", configs[1].ThinkingLevels)
	}
	if !codingAgentChoiceExists(configs[1].ThinkingLevels, "ultracode") || !codingAgentChoiceExists(configs[2].ThinkingLevels, "ultracode") {
		t.Fatalf("Claude thinking levels do not expose session-scoped ultracode: %#v / %#v", configs[1].ThinkingLevels, configs[2].ThinkingLevels)
	}
	for _, model := range []string{"sonnet", "opus", "haiku", "fable"} {
		if !codingAgentChoiceExists(configs[1].Models, model) {
			t.Fatalf("Claude models = %#v, missing %q", configs[1].Models, model)
		}
	}
	if !reflect.DeepEqual(configs[2].Models, []codingAgentChoice{
		{ID: "gpt-5.4", Label: "gpt-5.4"},
		{ID: "gpt-5.3-codex", Label: "gpt-5.3-codex"},
	}) {
		t.Fatalf("Claude GPT models = %#v", configs[2].Models)
	}
	if codingAgentChoiceExists(configs[2].Models, "opus") {
		t.Fatalf("Claude GPT models unexpectedly contain Claude aliases: %#v", configs[2].Models)
	}
}

func TestExplicitPiReasoningLevelsNeverSynthesizeFallbacks(t *testing.T) {
	model := piModelCapability{ID: "custom/unknown-levels", SupportsThinking: true}
	if got := explicitPiReasoningLevels(model); len(got) != 0 {
		t.Fatalf("explicitPiReasoningLevels() = %#v, want no selectable levels", got)
	}
	if err := validatePiModelLaunchOptions([]piModelCapability{model}, codingAgentLaunchOptions{
		Model: model.ID, ThinkingLevel: "high",
	}); !errors.Is(err, errPiThinkingLevelUnsupported) {
		t.Fatalf("validatePiModelLaunchOptions() error = %v, want unsupported level", err)
	}
}

func TestNormalizeCodingAgentLaunchOptions(t *testing.T) {
	tests := []struct {
		name     string
		agent    string
		model    string
		thinking string
		want     codingAgentLaunchOptions
		wantErr  bool
	}{
		{name: "Pi defaults", agent: codingAgentPi},
		{
			name: "Pi selection", agent: codingAgentPi, model: "openai-codex/gpt-5.6-sol", thinking: "max",
			want: codingAgentLaunchOptions{Model: "openai-codex/gpt-5.6-sol", ThinkingLevel: "max"},
		},
		{
			name: "Claude selection", agent: codingAgentClaude, model: "sonnet", thinking: "xhigh",
			want: codingAgentLaunchOptions{Model: "sonnet", ThinkingLevel: "xhigh"},
		},
		{
			name: "Claude GPT selection", agent: codingAgentClaudeGPT, model: "gpt-5.4", thinking: "high",
			want: codingAgentLaunchOptions{Model: "gpt-5.4", ThinkingLevel: "high"},
		},
		{name: "Claude GPT allows a server-selected model", agent: codingAgentClaudeGPT},
		{name: "Claude GPT rejects Claude model", agent: codingAgentClaudeGPT, model: "opus", wantErr: true},
		{name: "Claude rejects Pi-only level", agent: codingAgentClaude, thinking: "minimal", wantErr: true},
		{name: "reject option-like model", agent: codingAgentPi, model: "--print", wantErr: true},
		{name: "reject model whitespace", agent: codingAgentPi, model: "model name", wantErr: true},
		{name: "reject removed Dire Agent", agent: "dire", wantErr: true},
		{name: "reject unknown agent", agent: "other", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeCodingAgentLaunchOptions(test.agent, test.model, test.thinking)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeCodingAgentLaunchOptions() = %#v, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("normalizeCodingAgentLaunchOptions() = %#v, %v; want %#v, nil", got, err, test.want)
			}
		})
	}
}

func TestClaudeGPTCommandLoadsItsDefaultModelFromCLIProxyAPI(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{codingAgentPi, codingAgentClaude} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{
			{"id": "gpt-5.4"},
			{"id": "gpt-5.3-codex"},
		}})
	}))
	defer proxy.Close()

	handler := &terminalHandler{
		envPath:                 "/usr/bin/env",
		claudePluginPath:        "/plugin/dire-mux",
		claudeSandboxPluginPath: "/plugin/sandbox-exec",
		claudeGPTProfilePath:    filepath.Join(directory, "profile"),
		cliProxyAPIBaseURL:      proxy.URL,
		cliProxyAPIKey:          "test-key",
		cliProxyAPIHTTPClient:   proxy.Client(),
	}
	_, args, notice, err := handler.commandForCodingAgentPaneWithOptions(
		project.Project{ID: "project"},
		project.Thread{ID: "thread"},
		codingAgentClaudeGPT,
		"",
		"kiwi-code-project-thread-tools",
		codingAgentLaunchOptions{},
	)
	if err != nil || notice != "" {
		t.Fatalf("default Claude GPT command: notice=%q error=%v", notice, err)
	}
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, expected := range []string{
		"--model\ngpt-5.4",
		"ANTHROPIC_MODEL=gpt-5.4",
		"ANTHROPIC_SMALL_FAST_MODEL=gpt-5.6-luna",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-5.6-sol",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=gpt-5.6-terra",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.6-luna",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("default Claude GPT args = %#v, missing %q", args, expected)
		}
	}
}

func TestCodingAgentCommandsUseAgentSpecificModelAndThinkingFlags(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{codingAgentPi, codingAgentClaude} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	profilePath := filepath.Join(directory, "isolated-claude-gpt-profile")
	handler := &terminalHandler{
		envPath:                 "/usr/bin/env",
		claudePluginPath:        "/plugin/dire-mux",
		claudeSandboxPluginPath: "/plugin/sandbox-exec",
		claudeGPTProfilePath:    profilePath,
		cliProxyAPIBaseURL:      "http://127.0.0.1:18317",
		cliProxyAPIKey:          "proxy-client-key",
	}

	tests := []struct {
		agent       string
		options     codingAgentLaunchOptions
		wantCommand string
		wantTail    []string
	}{
		{
			agent: codingAgentPi,
			options: codingAgentLaunchOptions{
				Model: "openai-codex/gpt-5.6-sol", ThinkingLevel: "max", InitialPrompt: "Inspect the repository",
			},
			wantCommand: filepath.Join(directory, codingAgentPi),
			wantTail:    []string{"--model", "openai-codex/gpt-5.6-sol", "--thinking", "max", "Inspect the repository"},
		},
		{
			agent: codingAgentClaude,
			options: codingAgentLaunchOptions{
				Model: "sonnet", ThinkingLevel: "high", InitialPrompt: "Review the repository",
			},
			wantCommand: filepath.Join(directory, codingAgentClaude),
			wantTail:    []string{"--model", "sonnet", "--effort", "high", "Review the repository"},
		},
		{
			agent: codingAgentClaudeGPT,
			options: codingAgentLaunchOptions{
				Model: "gpt-5.4", ThinkingLevel: "xhigh", InitialPrompt: "Use GPT for this review",
			},
			wantCommand: filepath.Join(directory, codingAgentClaude),
			wantTail:    []string{"--model", "gpt-5.4", "--effort", "xhigh", "Use GPT for this review"},
		},
	}

	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			command, args, notice, err := handler.commandForCodingAgentPaneWithOptions(
				project.Project{ID: "project"},
				project.Thread{ID: "thread"},
				test.agent,
				"",
				"kiwi-code-project-thread-tools",
				test.options,
			)
			if err != nil || notice != "" || command != "/usr/bin/env" {
				t.Fatalf("command = %q %#v notice=%q err=%v", command, args, notice, err)
			}
			joined := strings.Join(args, "\n")
			if !strings.Contains(joined, test.wantCommand) {
				t.Fatalf("args %#v do not contain command %q", args, test.wantCommand)
			}
			if len(args) < len(test.wantTail) || !reflect.DeepEqual(args[len(args)-len(test.wantTail):], test.wantTail) {
				t.Fatalf("args tail = %#v, want %#v", args, test.wantTail)
			}
			if test.agent == codingAgentClaudeGPT {
				joined := strings.Join(args, "\n")
				for _, expected := range []string{
					"CLAUDE_CONFIG_DIR=" + profilePath,
					"IS_DEMO=1",
					"ANTHROPIC_BASE_URL=http://127.0.0.1:18317",
					"ANTHROPIC_AUTH_TOKEN=proxy-client-key",
					"ANTHROPIC_MODEL=gpt-5.4",
					"ANTHROPIC_SMALL_FAST_MODEL=gpt-5.6-luna",
					"ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-5.6-sol",
					"ANTHROPIC_DEFAULT_SONNET_MODEL=gpt-5.6-terra",
					"ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.6-luna",
					"DIRE_MUX_CODING_AGENT=" + codingAgentClaudeGPT,
					"/plugin/sandbox-exec",
				} {
					if !strings.Contains(joined, expected) {
						t.Fatalf("Claude GPT args %#v do not contain %q", args, expected)
					}
				}
				for _, name := range claudeGPTUnsetEnvironment {
					found := false
					for index := 0; index+1 < len(args); index++ {
						if args[index] == "-u" && args[index+1] == name {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("Claude GPT args %#v do not unset %q", args, name)
					}
				}
			}
		})
	}
}
