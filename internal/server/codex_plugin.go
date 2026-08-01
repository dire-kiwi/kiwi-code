package server

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	codexPluginName               = "kiwi-code"
	codexPluginVersion            = "1.0.0"
	codexPluginMarketplacePrefix  = "kiwi-code-managed"
	codexManagedProfileMarker     = "# Managed by Kiwi Code. Changes will be overwritten.\n"
	codexManagedMarketplaceUpdate = "2026-07-27T00:00:00Z"
)

//go:embed codex-plugin/.codex-plugin/plugin.json
var codexPluginManifest []byte

//go:embed codex-plugin/.mcp.json
var codexPluginMCPConfig []byte

//go:embed codex-plugin/hooks.json
var codexPluginHooks []byte

//go:embed codex-plugin/marketplace.json
var codexPluginMarketplace []byte

//go:embed codex-plugin/skills/kiwi-code-in-app-browser/SKILL.md
var codexPluginBrowserSkill []byte

//go:embed codex-plugin/skills/kiwi-code-in-app-browser/agents/openai.yaml
var codexPluginBrowserAgent []byte

//go:embed codex-plugin/skills/kiwi-code-processes/SKILL.md
var codexPluginProcessSkill []byte

//go:embed codex-plugin/skills/kiwi-code-processes/agents/openai.yaml
var codexPluginProcessAgent []byte

//go:embed codex-plugin/LICENSE
var codexPluginLicense []byte

type codexPluginFile struct {
	path     string
	contents []byte
}

type codexPluginInstallation struct {
	MarketplaceRoot string
	MarketplaceName string
	PluginRoot      string
	Version         string
}

func codexPluginFiles() ([]codexPluginFile, error) {
	files := []codexPluginFile{
		{path: filepath.Join(".codex-plugin", "plugin.json"), contents: codexPluginManifest},
		{path: ".mcp.json", contents: codexPluginMCPConfig},
		{path: "hooks.json", contents: codexPluginHooks},
		{path: filepath.Join("scripts", "kiwi-code-hook.mjs"), contents: claudePluginHookScript},
		{path: filepath.Join("servers", "kiwi-code-browser.mjs"), contents: codexBrowserServerContents()},
		{path: filepath.Join("skills", "kiwi-code-in-app-browser", "SKILL.md"), contents: codexPluginBrowserSkill},
		{path: filepath.Join("skills", "kiwi-code-in-app-browser", "agents", "openai.yaml"), contents: codexPluginBrowserAgent},
		{path: filepath.Join("skills", "kiwi-code-processes", "SKILL.md"), contents: codexPluginProcessSkill},
		{path: filepath.Join("skills", "kiwi-code-processes", "agents", "openai.yaml"), contents: codexPluginProcessAgent},
		{path: "LICENSE", contents: codexPluginLicense},
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
		files = append(files, codexPluginFile{
			path:     filepath.Join("skills", "kiwi-code-processes", "scripts", relative),
			contents: contents,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load Codex process skill scripts: %w", err)
	}
	return files, nil
}

func codexBrowserServerContents() []byte {
	replacements := []struct{ old, new string }{
		{"Kiwi Code-managed Claude Code session", "Kiwi Code-managed Codex session"},
		{"kiwi-code-claude-browser-", "kiwi-code-codex-browser-"},
		{"Claude Code accepts at most", "Codex accepts at most"},
	}
	contents := append([]byte(nil), claudePluginBrowserServer...)
	for _, replacement := range replacements {
		contents = bytes.ReplaceAll(contents, []byte(replacement.old), []byte(replacement.new))
	}
	return contents
}

func materializeCodexPlugin(dataDirectory string) (codexPluginInstallation, error) {
	marketplaceRoot, err := filepath.Abs(filepath.Join(dataDirectory, "codex-marketplace"))
	if err != nil {
		return codexPluginInstallation{}, fmt.Errorf("resolve Codex marketplace path: %w", err)
	}
	pluginRoot := filepath.Join(marketplaceRoot, "plugins", codexPluginName)
	if err := removeObsoletePluginOrchestration(pluginRoot); err != nil {
		return codexPluginInstallation{}, err
	}
	files, err := codexPluginFiles()
	if err != nil {
		return codexPluginInstallation{}, err
	}
	for _, file := range files {
		if err := materializeCodexPluginFile(pluginRoot, file); err != nil {
			return codexPluginInstallation{}, err
		}
	}
	if err := removeRetiredProcessUpdateHelper(filepath.Join(pluginRoot, "skills", agentSkillName)); err != nil {
		return codexPluginInstallation{}, fmt.Errorf("remove retired Codex process helper: %w", err)
	}
	marketplaceName := managedCodexMarketplaceName(dataDirectory)
	marketplaceContents, err := codexMarketplaceContents(marketplaceName)
	if err != nil {
		return codexPluginInstallation{}, err
	}
	marketplaceFile := codexPluginFile{
		path:     filepath.Join(".agents", "plugins", "marketplace.json"),
		contents: marketplaceContents,
	}
	if err := materializeCodexPluginFile(marketplaceRoot, marketplaceFile); err != nil {
		return codexPluginInstallation{}, err
	}
	return codexPluginInstallation{
		MarketplaceRoot: canonicalCodexPath(marketplaceRoot),
		MarketplaceName: marketplaceName,
		PluginRoot:      pluginRoot,
		Version:         codexPluginVersion,
	}, nil
}

func codexMarketplaceContents(marketplaceName string) ([]byte, error) {
	const embeddedName = `"name": "` + codexPluginMarketplacePrefix + `"`
	if strings.TrimSpace(marketplaceName) == "" || bytes.Count(codexPluginMarketplace, []byte(embeddedName)) != 1 {
		return nil, errors.New("Codex marketplace template is invalid")
	}
	return bytes.Replace(codexPluginMarketplace, []byte(embeddedName), []byte(`"name": "`+marketplaceName+`"`), 1), nil
}

func materializeCodexPluginFile(root string, file codexPluginFile) error {
	path := filepath.Join(root, file.path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Codex plugin directory: %w", err)
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, file.contents) {
		return nil
	}
	if err := writeFileAtomically(path, file.contents, serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("write Codex plugin file: %w", err)
	}
	return nil
}

func defaultCodexConfigDirectory() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// Namespace managed Codex state by Kiwi data directory so isolated development
// stacks can share the user's CODEX_HOME without racing on profiles or caches.
func managedCodexNamespaceSuffix(dataDirectory string) string {
	absolute, err := filepath.Abs(dataDirectory)
	if err != nil {
		absolute = filepath.Clean(dataDirectory)
	}
	digest := sha256.Sum256([]byte(absolute))
	return fmt.Sprintf("%x", digest[:6])
}

func managedCodexProfileName(dataDirectory string) string {
	return "kiwi-code-" + managedCodexNamespaceSuffix(dataDirectory)
}

func managedCodexMarketplaceName(dataDirectory string) string {
	return codexPluginMarketplacePrefix + "-" + managedCodexNamespaceSuffix(dataDirectory)
}

func prepareCodexPluginProfile(configDirectory, profileName string, installation codexPluginInstallation) error {
	if strings.TrimSpace(configDirectory) == "" {
		return errors.New("Codex config directory is unavailable")
	}
	if strings.TrimSpace(profileName) == "" {
		return errors.New("Codex profile name is unavailable")
	}
	if installation.MarketplaceRoot == "" || installation.MarketplaceName == "" ||
		installation.PluginRoot == "" || installation.Version != codexPluginVersion {
		return errors.New("Codex plugin installation is unavailable")
	}
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		return fmt.Errorf("create Codex config directory: %w", err)
	}
	profilePath := filepath.Join(configDirectory, profileName+".config.toml")
	if current, err := os.ReadFile(profilePath); err == nil && !bytes.HasPrefix(current, []byte(codexManagedProfileMarker)) {
		return fmt.Errorf("Codex profile %q already exists and is not managed by Kiwi Code", profileName)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Codex profile: %w", err)
	}

	// A profile can enable a plugin but does not install its marketplace source.
	// Seed Codex's versioned cache so the managed profile can load on first use.
	files, err := codexPluginFiles()
	if err != nil {
		return err
	}
	cacheRoot := filepath.Join(
		configDirectory,
		"plugins", "cache", installation.MarketplaceName, codexPluginName, installation.Version,
	)
	if err := removeObsoletePluginOrchestration(cacheRoot); err != nil {
		return err
	}
	for _, file := range files {
		if err := materializeCodexPluginFile(cacheRoot, file); err != nil {
			return err
		}
	}

	marketplaceRoot := canonicalCodexPath(installation.MarketplaceRoot)
	profile := codexManagedProfileMarker +
		"[features]\n" +
		"hooks = true\n" +
		"plugins = true\n\n" +
		"[marketplaces." + installation.MarketplaceName + "]\n" +
		"last_updated = " + strconv.Quote(codexManagedMarketplaceUpdate) + "\n" +
		"source_type = \"local\"\n" +
		"source = " + strconv.Quote(marketplaceRoot) + "\n\n" +
		"[plugins.\"" + codexPluginName + "@" + installation.MarketplaceName + "\"]\n" +
		"enabled = true\n\n" +
		"# The ChatGPT in-app Browser is not the browser shown by Kiwi Code.\n" +
		"[plugins.\"browser@openai-bundled\"]\n" +
		"enabled = false\n"
	if current, err := os.ReadFile(profilePath); err == nil && string(current) == profile {
		return nil
	}
	if err := writeFileAtomically(profilePath, []byte(profile), serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("write Codex profile: %w", err)
	}
	return nil
}

func canonicalCodexPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
