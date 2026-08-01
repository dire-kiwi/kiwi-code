package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// testAgents builds one instance of every agent family with stub binaries on
// PATH so command resolution is deterministic.
func testAgents(t *testing.T) []Agent {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"pi", "codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")

	claude := Claude{
		AgentID:             IDClaude,
		Configured:          true,
		PluginPath:          "/plugin",
		PluginRootPath:      "/plugin-root",
		ConfigPath:          "/claude-config",
		LaunchSettings:      `{"skipDangerousModePermissionPrompt":true}`,
		SyncProfileSettings: func(string, string) error { return nil },
		FigmaConfigArgument: func(url string) (string, error) { return "figma:" + url, nil },
		UnsetEnvironment:    []string{"ANTHROPIC_API_KEY"},
		GPTProfileDirectory: func() (string, error) { return "/gpt-profile", nil },
		ProxyConfiguration:  func() (string, string, error) { return "http://proxy", "key", nil },
		ProxyEnvironment: func(profilePath, root, base, key, model string) []string {
			return []string{"PROXY_MODEL=" + model}
		},
		IsGPTModel: func(model string) bool { return strings.HasPrefix(model, "gpt-") },
	}
	gpt := claude
	gpt.AgentID = IDClaudeGPT
	gpt.GPT = true

	return []Agent{
		Pi{ExtensionPaths: []string{"/ext/a.ts"}, AgentToken: "token", FigmaEnvironmentName: "KIWI_CODE_FIGMA_MCP_URL"},
		Codex{
			AgentToken: "token", AgentTokenPath: "/token", ProfileName: "profile",
			ConfigPath: "/codex", Prepare: func() error { return nil },
		},
		claude,
		gpt,
	}
}

func testLaunchContext() LaunchContext {
	return LaunchContext{
		ProjectID:          "project",
		ThreadID:           "thread",
		ThreadEndpoint:     "http://127.0.0.1:4000/api/projects/project/threads/thread",
		SessionName:        "kiwi-code-project-thread-tools",
		WindowName:         "pi",
		RelatedDirectories: func() ([]string, error) { return nil, nil },
	}
}

// TestAgentConformance asserts invariants every agent implementation must
// hold: a stable non-empty ID, deterministic commands, ordering of option
// arguments (initial prompt positional and last), and no option arguments on
// the missing-binary notice path.
func TestAgentConformance(t *testing.T) {
	for _, candidate := range testAgents(t) {
		t.Run(candidate.ID(), func(t *testing.T) {
			if candidate.ID() == "" {
				t.Fatal("agent has an empty ID")
			}
			options := LaunchOptions{Model: "test-model", ThinkingLevel: "high", InitialPrompt: "the prompt"}
			if IsClaudeGPT(candidate.ID()) {
				options.Model = "gpt-test"
			}
			first, err := candidate.TerminalCommand(testLaunchContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			second, err := candidate.TerminalCommand(testLaunchContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(first.Prefix, "\x00") != strings.Join(second.Prefix, "\x00") ||
				strings.Join(first.Suffix, "\x00") != strings.Join(second.Suffix, "\x00") ||
				strings.Join(first.Env, "\x00") != strings.Join(second.Env, "\x00") ||
				first.Program != second.Program {
				t.Fatal("TerminalCommand is not deterministic")
			}
			if first.Notice != "" {
				t.Fatalf("stubbed binary still produced a notice: %q", first.Notice)
			}
			if len(first.Suffix) == 0 || first.Suffix[len(first.Suffix)-1] != "the prompt" {
				t.Fatalf("initial prompt is not the final positional argument: %v", first.Suffix)
			}
			for _, entry := range first.Env {
				if !strings.Contains(entry, "=") {
					t.Fatalf("environment entry %q is not KEY=VALUE", entry)
				}
			}
		})
	}
}

// TestAgentMissingBinaryFallsBackToShell asserts the notice path: with no
// agent binaries on PATH every agent opens a login shell with a notice and
// suppresses agent-specific arguments.
func TestAgentMissingBinaryFallsBackToShell(t *testing.T) {
	agents := testAgents(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, candidate := range agents {
		t.Run(candidate.ID(), func(t *testing.T) {
			options := LaunchOptions{}
			command, err := candidate.TerminalCommand(testLaunchContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			if command.Notice == "" {
				t.Fatal("missing binary did not produce a notice")
			}
			if command.Program != "/bin/sh" {
				t.Fatalf("fallback program = %q, want /bin/sh", command.Program)
			}
			if len(command.Prefix) != 0 {
				t.Fatalf("notice path still carries agent arguments: %v", command.Prefix)
			}
		})
	}
}

// TestRegistryResolution pins registry behavior: built-ins resolve, profile
// IDs resolve against live settings, unconfigured profiles fail with
// ErrNotConfigured, and non-agent tools do not resolve.
func TestRegistryResolution(t *testing.T) {
	profile := project.CodingAgentSetting{
		ID: "work", Name: "Work", Kind: project.CodingAgentKindClaude, ConfigDirectory: "/profiles/work",
	}
	settings := func() project.Settings {
		return project.Settings{CodingAgents: []project.CodingAgentSetting{profile}}
	}
	agents := testAgents(t)
	factory := func(id string, setting project.CodingAgentSetting, configured, gpt bool) Agent {
		claude := agents[2].(Claude)
		claude.AgentID = id
		claude.GPT = gpt
		claude.Configured = configured
		claude.Profile = setting
		return claude
	}
	registry := NewRegistry(agents[0], agents[1], factory, settings)

	for _, id := range []string{IDPi, IDCodex, IDClaude, IDClaudeGPT, "claude-profile-work"} {
		resolved, ok := registry.Resolve(id)
		if !ok || resolved.ID() != id {
			t.Fatalf("Resolve(%q) = %v, %t", id, resolved, ok)
		}
	}
	for _, id := range []string{"", "terminal", "nvim", "lazygit", "process", "gemini", "claude-profile-"} {
		if _, ok := registry.Resolve(id); ok {
			t.Fatalf("Resolve(%q) unexpectedly succeeded", id)
		}
	}

	unconfigured, ok := registry.Resolve("claude-profile-missing")
	if !ok {
		t.Fatal("valid-shaped profile ID did not resolve")
	}
	if _, err := unconfigured.TerminalCommand(testLaunchContext(), LaunchOptions{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured profile error = %v, want ErrNotConfigured", err)
	}
}
