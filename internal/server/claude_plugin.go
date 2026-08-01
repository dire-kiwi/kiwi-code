package server

import (
	_ "embed"
	"fmt"
	"github.com/dire-kiwi/kiwi-code/internal/agent/assets"
	"io/fs"
	"path/filepath"
)

//go:embed claude-plugin/.claude-plugin/plugin.json
var claudePluginManifest []byte

//go:embed claude-plugin/hooks/hooks.json
var claudePluginHooks []byte

//go:embed claude-plugin/.mcp.json
var claudePluginMCPConfig []byte

//go:embed claude-plugin/scripts/kiwi-code-hook.mjs
var claudePluginHookScript []byte

//go:embed claude-plugin/servers/kiwi-code-browser.mjs
var claudePluginBrowserServer []byte

//go:embed claude-plugin/skills/kiwi-code-in-app-browser/SKILL.md
var claudePluginBrowserSkill []byte

//go:embed claude-plugin/LICENSE
var claudePluginBrowserLicense []byte

//go:embed claude-plugin/skills/kiwi-code-processes/SKILL.md
var claudePluginProcessSkill []byte

type claudePluginFile struct {
	path     string
	contents []byte
}

func materializeClaudePlugin(dataDirectory string) (string, error) {
	root := filepath.Join(dataDirectory, "claude-plugin")
	if err := removeObsoletePluginOrchestration(root); err != nil {
		return "", err
	}
	files, err := claudePluginFiles()
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if err := materializeClaudePluginFile(root, file); err != nil {
			return "", err
		}
	}
	if err := removeRetiredProcessUpdateHelper(filepath.Join(root, "skills", agentSkillName)); err != nil {
		return "", fmt.Errorf("remove retired Claude process helper: %w", err)
	}
	return root, nil
}

func removeObsoletePluginOrchestration(root string) error {
	return assets.RemoveObsolete(
		"plugin orchestration",
		filepath.Join(root, "servers", "kiwi-code-plans.mjs"),
		filepath.Join(root, "skills", "kiwi-code-planner"),
	)
}

func claudePluginFiles() ([]claudePluginFile, error) {
	files := []claudePluginFile{
		{path: filepath.Join(".claude-plugin", "plugin.json"), contents: claudePluginManifest},
		{path: ".mcp.json", contents: claudePluginMCPConfig},
		{path: filepath.Join("hooks", "hooks.json"), contents: claudePluginHooks},
		{path: filepath.Join("scripts", "kiwi-code-hook.mjs"), contents: claudePluginHookScript},
		{path: filepath.Join("servers", "kiwi-code-browser.mjs"), contents: claudePluginBrowserServer},
		{path: filepath.Join("skills", "kiwi-code-in-app-browser", "SKILL.md"), contents: claudePluginBrowserSkill},
		{path: "LICENSE", contents: claudePluginBrowserLicense},
		{path: filepath.Join("skills", agentSkillName, "SKILL.md"), contents: claudePluginProcessSkill},
	}

	const scriptsRoot = embeddedAgentSkillRoot + "/" + agentSkillName + "/scripts"
	err := fs.WalkDir(embeddedAgentSkill, scriptsRoot, func(embeddedPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(scriptsRoot, embeddedPath)
		if err != nil {
			return err
		}
		contents, err := embeddedAgentSkill.ReadFile(embeddedPath)
		if err != nil {
			return err
		}
		files = append(files, claudePluginFile{
			path:     filepath.Join("skills", agentSkillName, "scripts", relative),
			contents: contents,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load Claude process skill scripts: %w", err)
	}
	return files, nil
}

func materializeClaudePluginFile(root string, file claudePluginFile) error {
	_, err := assets.EnsureFile(root, file.path, file.contents, "Claude plugin file")
	return err
}
