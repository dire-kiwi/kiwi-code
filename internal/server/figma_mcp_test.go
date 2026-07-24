package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestFigmaMCPConfigArgumentDeclaresHTTPTransport(t *testing.T) {
	argument, err := figmaMCPConfigArgument("http://127.0.0.1:3845/mcp")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(argument), &decoded); err != nil {
		t.Fatalf("decode %q: %v", argument, err)
	}
	server, found := decoded.MCPServers[figmaMCPServerName]
	if !found {
		t.Fatalf("config %q has no %q server", argument, figmaMCPServerName)
	}
	if server.Type != "http" || server.URL != "http://127.0.0.1:3845/mcp" {
		t.Fatalf("server = %#v", server)
	}
}

func TestFigmaMCPURLForProjectFollowsProjectToggle(t *testing.T) {
	handler := &terminalHandler{}

	if url := handler.figmaMCPURLForProject(project.Project{ID: "project"}); url != "" {
		t.Fatalf("disabled project URL = %q, want empty", url)
	}
	enabled := project.Project{ID: "project", FigmaMCPEnabled: true}
	if url := handler.figmaMCPURLForProject(enabled); url != project.DefaultFigmaMCPURL {
		t.Fatalf("enabled project URL = %q, want the default", url)
	}
}

func TestPiNativeArgumentsLoadFigmaBridgeOnlyWhenEnabled(t *testing.T) {
	without := piNativeArguments("/tmp/sessions", nil, "/tmp/figma.ts", codingAgentLaunchOptions{})
	if slices.Contains(without, "/tmp/figma.ts") {
		t.Fatalf("disabled arguments loaded the Figma bridge: %#v", without)
	}

	with := piNativeArguments("/tmp/sessions", nil, "/tmp/figma.ts", codingAgentLaunchOptions{
		FigmaMCPURL: project.DefaultFigmaMCPURL,
	})
	index := slices.Index(with, "/tmp/figma.ts")
	if index <= 0 || with[index-1] != "--extension" {
		t.Fatalf("enabled arguments = %#v, want --extension /tmp/figma.ts", with)
	}
}

func TestClaudeNativeArgumentsPassFigmaMCPConfig(t *testing.T) {
	without, err := claudeNativeArguments("", "", codingAgentLaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(without, "--mcp-config") {
		t.Fatalf("disabled arguments included --mcp-config: %#v", without)
	}

	with, err := claudeNativeArguments("", "", codingAgentLaunchOptions{
		FigmaMCPURL: project.DefaultFigmaMCPURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	index := slices.Index(with, "--mcp-config")
	if index < 0 || index+1 >= len(with) {
		t.Fatalf("enabled arguments = %#v, want --mcp-config", with)
	}
	if !strings.Contains(with[index+1], project.DefaultFigmaMCPURL) {
		t.Fatalf("--mcp-config value = %q, want the Figma endpoint", with[index+1])
	}
	// --mcp-config is variadic in Claude Code, so a flag must follow it rather
	// than a bare value that would be swallowed as another config.
	if index+2 < len(with) && !strings.HasPrefix(with[index+2], "--") {
		t.Fatalf("argument after the Figma config = %q, want a flag", with[index+2])
	}
}

func TestTmuxCodingAgentLaunchWiresFigmaMCPPerAgent(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{codingAgentPi, codingAgentClaude} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := &terminalHandler{
		projects:             store,
		envPath:              "/usr/bin/env",
		claudePluginPath:     "/plugin/kiwi-code",
		piExtensionPaths:     []string{"/extensions/activity.ts"},
		piFigmaExtensionPath: "/extensions/figma.ts",
	}

	launch := func(item project.Project, agent string) []string {
		t.Helper()
		_, args, notice, err := handler.commandForCodingAgentPaneWithOptions(
			item,
			project.Thread{ID: "thread"},
			agent,
			"",
			"kiwi-code-project-thread-tools",
			codingAgentLaunchOptions{},
		)
		if err != nil || notice != "" {
			t.Fatalf("launch %s: notice=%q err=%v", agent, notice, err)
		}
		return args
	}

	disabled := project.Project{ID: "project"}
	enabled := project.Project{ID: "project", FigmaMCPEnabled: true}

	piDisabled := launch(disabled, codingAgentPi)
	if slices.Contains(piDisabled, "/extensions/figma.ts") ||
		slices.ContainsFunc(piDisabled, func(argument string) bool {
			return strings.HasPrefix(argument, figmaMCPEnvironmentName+"=")
		}) {
		t.Fatalf("disabled Pi launch referenced Figma: %#v", piDisabled)
	}

	piEnabled := launch(enabled, codingAgentPi)
	if !slices.Contains(piEnabled, "/extensions/figma.ts") {
		t.Fatalf("enabled Pi launch did not load the Figma bridge: %#v", piEnabled)
	}
	if !slices.Contains(piEnabled, figmaMCPEnvironmentName+"="+project.DefaultFigmaMCPURL) {
		t.Fatalf("enabled Pi launch did not export the Figma endpoint: %#v", piEnabled)
	}

	claudeDisabled := launch(disabled, codingAgentClaude)
	if slices.Contains(claudeDisabled, "--mcp-config") {
		t.Fatalf("disabled Claude launch included --mcp-config: %#v", claudeDisabled)
	}

	claudeEnabled := launch(enabled, codingAgentClaude)
	index := slices.Index(claudeEnabled, "--mcp-config")
	if index < 0 || !strings.Contains(claudeEnabled[index+1], project.DefaultFigmaMCPURL) {
		t.Fatalf("enabled Claude launch did not pass the Figma MCP config: %#v", claudeEnabled)
	}
	// Claude must not receive the Pi-only bridge extension.
	if slices.Contains(claudeEnabled, "/extensions/figma.ts") {
		t.Fatalf("enabled Claude launch loaded the Pi bridge: %#v", claudeEnabled)
	}
}
