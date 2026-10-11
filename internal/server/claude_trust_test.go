package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func readClaudeTestConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("decode Claude config %s: %v", path, err)
	}
	return config
}

func claudeTestProject(t *testing.T, config map[string]any, directory string) map[string]any {
	t.Helper()
	projects, _ := config["projects"].(map[string]any)
	entry, _ := projects[directory].(map[string]any)
	return entry
}

func TestClaudeLaunchesTrustTheThreadDirectoryInTheirOwnConfig(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, codingAgentClaude), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	workConfigDirectory := filepath.Join(t.TempDir(), "claude-work")
	profiles := []project.CodingAgentSetting{{ID: "work", Name: "Work", Kind: project.CodingAgentKindClaude, ConfigDirectory: workConfigDirectory}}
	if _, err := store.UpdateSettingsValues(project.SettingsUpdate{CodingAgents: &profiles}); err != nil {
		t.Fatal(err)
	}
	gptProfilePath := filepath.Join(t.TempDir(), "claude-gpt")
	handler := &terminalHandler{
		projects:             store,
		envPath:              "/usr/bin/env",
		claudePluginPath:     "/plugin/kiwi-code",
		claudeGPTProfilePath: gptProfilePath,
		cliProxyAPIBaseURL:   "http://127.0.0.1:18317",
		cliProxyAPIKey:       "proxy-client-key",
	}
	configureTestClaudeGPTUserConfiguration(t, handler)
	workspace := t.TempDir()

	for _, test := range []struct {
		agent      string
		options    codingAgentLaunchOptions
		configPath string
	}{
		{agent: codingAgentClaude, configPath: filepath.Join(handler.claudeConfigPath, claudeGlobalConfigFileName)},
		{agent: claudeCodeProfileAgentID("work"), configPath: filepath.Join(workConfigDirectory, claudeGlobalConfigFileName)},
		{
			agent:      codingAgentClaudeGPT,
			options:    codingAgentLaunchOptions{Model: "gpt-5.4"},
			configPath: filepath.Join(gptProfilePath, claudeGlobalConfigFileName),
		},
	} {
		t.Run(test.agent, func(t *testing.T) {
			_, _, notice, err := handler.commandForCodingAgentPaneWithOptions(
				project.Project{ID: "project"},
				project.Thread{ID: "thread", Cwd: workspace},
				test.agent,
				"",
				"kiwi-code-project-thread-tools",
				test.options,
			)
			if err != nil || notice != "" {
				t.Fatalf("Claude launch notice=%q err=%v", notice, err)
			}
			entry := claudeTestProject(t, readClaudeTestConfig(t, test.configPath), workspace)
			if entry["hasTrustDialogAccepted"] != true {
				t.Fatalf("%s trust entry for %s = %#v", test.configPath, workspace, entry)
			}
		})
	}
}

func TestClaudeGlobalConfigFileMatchesClaudeCodeLookup(t *testing.T) {
	home := t.TempDir()
	configDirectory := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	handler := &terminalHandler{claudeConfigPath: configDirectory}
	profileDirectory := t.TempDir()

	assertConfigFile := func(configDirectory, want string) {
		t.Helper()
		got, err := handler.claudeGlobalConfigFile(configDirectory)
		if err != nil || got != want {
			t.Fatalf("claudeGlobalConfigFile(%q) = %q, %v; want %q", configDirectory, got, err, want)
		}
	}
	assertConfigFile("", filepath.Join(home, claudeGlobalConfigFileName))
	assertConfigFile(profileDirectory, filepath.Join(profileDirectory, claudeGlobalConfigFileName))

	t.Setenv("CLAUDE_CONFIG_DIR", configDirectory)
	assertConfigFile("", filepath.Join(configDirectory, claudeGlobalConfigFileName))

	legacyPath := filepath.Join(configDirectory, claudeLegacyGlobalConfigFileName)
	if err := os.WriteFile(legacyPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertConfigFile("", legacyPath)

	if _, err := (&terminalHandler{}).claudeGlobalConfigFile(""); err == nil {
		t.Fatal("claudeGlobalConfigFile without a Claude config directory succeeded")
	}
}

func TestTrustClaudeWorkspacePreservesClaudeConfig(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceLink := filepath.Join(root, "workspace-link")
	if err := os.Symlink(workspace, workspaceLink); err != nil {
		t.Fatal(err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspaceLink)
	if err != nil {
		t.Fatal(err)
	}
	configTarget := filepath.Join(root, "dotfiles", claudeGlobalConfigFileName)
	if err := os.Mkdir(filepath.Dir(configTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	encodedWorkspace, err := json.Marshal(resolvedWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	initial := `{"numStartups":12345678901234567890,"oauthAccount":{"displayName":"<Kiwi & Co>"},` +
		`"projects":{` + string(encodedWorkspace) + `:{"allowedTools":["Bash"],"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(configTarget, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, claudeGlobalConfigFileName)
	if err := os.Symlink(configTarget, configPath); err != nil {
		t.Fatal(err)
	}

	if err := trustClaudeWorkspace(configPath, workspaceLink); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(configPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Claude config symlink was replaced: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(configPath + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude config lock remains after trust: %v", err)
	}
	updated, err := os.ReadFile(configTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("<Kiwi & Co>")) {
		t.Fatalf("Claude config escaped unrelated strings:\n%s", updated)
	}
	config := readClaudeTestConfig(t, configTarget)
	if config["numStartups"] != json.Number("12345678901234567890") {
		t.Fatalf("numStartups = %#v", config["numStartups"])
	}
	for _, directory := range []string{workspaceLink, resolvedWorkspace} {
		if entry := claudeTestProject(t, config, directory); entry["hasTrustDialogAccepted"] != true {
			t.Fatalf("trust entry for %s = %#v", directory, entry)
		}
	}
	if tools, _ := claudeTestProject(t, config, resolvedWorkspace)["allowedTools"].([]any); len(tools) != 1 || tools[0] != "Bash" {
		t.Fatalf("existing project settings were not preserved: %#v", claudeTestProject(t, config, resolvedWorkspace))
	}

	if err := trustClaudeWorkspace(configPath, workspaceLink); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := os.ReadFile(configTarget); err != nil || !bytes.Equal(unchanged, updated) {
		t.Fatalf("repeated trust rewrote the Claude config: err=%v\n%s", err, unchanged)
	}
}

func TestTrustClaudeWorkspaceRejectsInvalidConfigWithoutRewriting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), claudeGlobalConfigFileName)
	invalid := []byte(`{"projects":`)
	if err := os.WriteFile(configPath, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trustClaudeWorkspace(configPath, t.TempDir()); err == nil {
		t.Fatal("trustClaudeWorkspace accepted an invalid Claude config")
	}
	if contents, err := os.ReadFile(configPath); err != nil || !bytes.Equal(contents, invalid) {
		t.Fatalf("invalid Claude config was rewritten: err=%v\n%s", err, contents)
	}
}

func TestTrustClaudeWorkspaceWaitsForClaudeConfigLock(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, claudeGlobalConfigFileName)
	if err := os.WriteFile(configPath, []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := configPath + ".lock"
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- trustClaudeWorkspace(configPath, workspace) }()
	select {
	case err := <-done:
		t.Fatalf("trustClaudeWorkspace returned while Claude held the config lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if entry := claudeTestProject(t, readClaudeTestConfig(t, configPath), workspace); entry != nil {
		t.Fatalf("Claude config changed while locked: %#v", entry)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(claudeConfigLockTimeout):
		t.Fatal("trustClaudeWorkspace did not resume after the config lock was released")
	}
	if entry := claudeTestProject(t, readClaudeTestConfig(t, configPath), workspace); entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust entry after lock release = %#v", entry)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude config lock remains after trust: %v", err)
	}
}
