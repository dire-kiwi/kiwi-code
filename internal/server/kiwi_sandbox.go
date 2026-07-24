package server

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed kiwi-sandbox/packages/core/src/*.ts
//go:embed kiwi-sandbox/packages/pi/src/*.ts
//go:embed kiwi-sandbox/packages/pi/skills/*/SKILL.md
//go:embed kiwi-sandbox/packages/claude/.claude-plugin/plugin.json
//go:embed kiwi-sandbox/packages/claude/.mcp.json
//go:embed kiwi-sandbox/packages/claude/hooks/hooks.json
//go:embed kiwi-sandbox/packages/claude/src/*.ts
//go:embed kiwi-sandbox/packages/claude/skills/*/SKILL.md
var embeddedKiwiSandbox embed.FS

const kiwiSandboxEmbeddedRoot = "kiwi-sandbox"

func kiwiSandboxPiSkillPath(dataDirectory string) string {
	return filepath.Join(dataDirectory, "kiwi-sandbox", "packages", "pi", "skills", "kiwi-sandbox-config")
}

func materializeKiwiSandbox(dataDirectory string) (piExtensionPath, claudePluginPath string, err error) {
	root := filepath.Join(dataDirectory, "kiwi-sandbox")
	err = fs.WalkDir(embeddedKiwiSandbox, kiwiSandboxEmbeddedRoot, func(embeddedPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, relativeErr := filepath.Rel(kiwiSandboxEmbeddedRoot, embeddedPath)
		if relativeErr != nil {
			return relativeErr
		}
		contents, readErr := embeddedKiwiSandbox.ReadFile(embeddedPath)
		if readErr != nil {
			return readErr
		}
		if writeErr := materializeKiwiSandboxFile(filepath.Join(root, relative), contents); writeErr != nil {
			return writeErr
		}
		return nil
	})
	if err != nil {
		return "", "", fmt.Errorf("materialize Kiwi Sandbox: %w", err)
	}

	shim := []byte("export { default } from \"../kiwi-sandbox/packages/pi/src/index.ts\";\n")
	piExtensionPath, err = materializePiExtension(dataDirectory, "kiwi-sandbox.ts", shim)
	if err != nil {
		return "", "", err
	}
	return piExtensionPath, filepath.Join(root, "packages", "claude"), nil
}

func materializeKiwiSandboxFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Kiwi Sandbox directory: %w", err)
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, contents) {
		return nil
	}
	if err := writeFileAtomically(path, contents, serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("write Kiwi Sandbox file: %w", err)
	}
	return nil
}
