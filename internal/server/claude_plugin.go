package server

import (
	"bytes"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
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
	files, err := claudePluginFiles()
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if err := materializeClaudePluginFile(root, file); err != nil {
			return "", err
		}
	}
	return root, nil
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
	path := filepath.Join(root, file.path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Claude plugin directory: %w", err)
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, file.contents) {
		return nil
	}

	if err := writeFileAtomically(path, file.contents, serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("write Claude plugin file: %w", err)
	}
	return nil
}
