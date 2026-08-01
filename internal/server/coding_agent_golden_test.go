package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// TestCodingAgentLaunchCommandGoldens pins the exact launch command (program,
// argument order, environment assignments, unsets, notices) for every coding
// agent across representative option sets. The agent-plugin refactor must
// reproduce these byte-for-byte: argument order is load-bearing (variadic
// --mcp-config placement, env unsets first, positional prompt last).
//
// Regenerate deliberately with:
//
//	KIWI_CODE_UPDATE_GOLDENS=1 go test ./internal/server/ -run TestCodingAgentLaunchCommandGoldens
func TestCodingAgentLaunchCommandGoldens(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"pi", "codex", "claude"} {
		stub := "#!/bin/sh\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profileDirectory := filepath.Join(t.TempDir(), "claude-profile")
	if err := os.MkdirAll(profileDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	agents := append(store.GetSettings().CodingAgents, project.CodingAgentSetting{
		ID:              "golden-profile",
		Name:            "Golden Profile",
		Kind:            project.CodingAgentKindClaude,
		ConfigDirectory: profileDirectory,
	})
	if _, err := store.UpdateSettingsFields(project.SettingsUpdate{CodingAgents: &agents}); err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]

	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "kcv-golden-test")
	handler.cliProxyAPIBaseURL = "http://127.0.0.1:9999"
	handler.cliProxyAPIKey = "golden-proxy-key"
	handler.cliProxyAPIErr = nil

	sessionName := tmuxSessionName(item.ID, thread.ID, "pi")
	threadEndpoint := "http://127.0.0.1:4000/api/projects/" + item.ID + "/threads/" + thread.ID

	type capture struct {
		Agent   string   `json:"agent"`
		Case    string   `json:"case"`
		Program string   `json:"program"`
		Args    []string `json:"args"`
		Notice  string   `json:"notice,omitempty"`
		Error   string   `json:"error,omitempty"`
	}

	cases := []struct {
		name    string
		agent   string
		options codingAgentLaunchOptions
	}{
		{"default", codingAgentPi, codingAgentLaunchOptions{}},
		{"full", codingAgentPi, codingAgentLaunchOptions{Model: "test-model", ThinkingLevel: "high", InitialPrompt: "hello world"}},
		{"default", codingAgentCodex, codingAgentLaunchOptions{}},
		{"thinking", codingAgentCodex, codingAgentLaunchOptions{ThinkingLevel: "high"}},
		{"default", codingAgentClaude, codingAgentLaunchOptions{}},
		{"full", codingAgentClaude, codingAgentLaunchOptions{Model: "test-opus", ThinkingLevel: "high", InitialPrompt: "hi there"}},
		{"model", codingAgentClaudeGPT, codingAgentLaunchOptions{Model: "gpt-5.6-sol"}},
		{"default", "claude-profile-golden-profile", codingAgentLaunchOptions{}},
	}

	normalizer := strings.NewReplacer(
		handler.agentToken, "<agent-token>",
		store.DataDirectory(), "<data>",
		profileDirectory, "<profile-dir>",
		bin, "<bin>",
		handler.claudeConfigPath, "<claude-config>",
		handler.codexConfigPath, "<codex-config>",
		item.ID, "<project>",
		thread.ID, "<thread>",
		os.Getenv("HOME"), "<home>",
	)
	hashPattern := regexp.MustCompile(`kiwi-code-[0-9a-f]{8,}`)
	normalize := func(value string) string {
		value = normalizer.Replace(value)
		return hashPattern.ReplaceAllString(value, "kiwi-code-<hash>")
	}

	captures := make([]capture, 0, len(cases))
	for _, testCase := range cases {
		program, args, notice, err := handler.commandForCodingAgentPaneWithOptions(
			item, thread, testCase.agent, threadEndpoint, sessionName, testCase.options,
		)
		entry := capture{
			Agent:   testCase.agent,
			Case:    testCase.name,
			Program: normalize(program),
			Notice:  notice,
		}
		if testCase.agent == "claude-profile-golden-profile" {
			entry.Agent = "claude-profile-<id>"
		}
		if err != nil {
			entry.Error = normalize(err.Error())
		}
		for _, arg := range args {
			entry.Args = append(entry.Args, normalize(arg))
		}
		captures = append(captures, entry)
	}

	encoded, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	goldenPath := filepath.Join("testdata", "coding_agent_launch_goldens.json")
	if os.Getenv("KIWI_CODE_UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with KIWI_CODE_UPDATE_GOLDENS=1): %v", err)
	}
	if string(expected) != string(encoded) {
		t.Fatalf("launch commands drifted from golden:\n--- got ---\n%s\n--- want ---\n%s", encoded, expected)
	}
}
