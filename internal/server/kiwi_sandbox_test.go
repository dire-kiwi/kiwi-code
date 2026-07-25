package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestTerminalHandlerAlwaysLoadsKiwiSandbox(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "kcv-sandbox-test")
	if handler.piExtensionErr != nil {
		t.Fatal(handler.piExtensionErr)
	}
	found := false
	for _, path := range handler.piExtensionPaths {
		if filepath.Base(path) == "kiwi-sandbox.ts" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Pi extensions do not include kiwi-sandbox.ts: %#v", handler.piExtensionPaths)
	}
	if len(handler.piSkillPaths) != 1 || filepath.Base(handler.piSkillPaths[0]) != "kiwi-sandbox-config" {
		t.Fatalf("Pi skills do not include kiwi-sandbox-config: %#v", handler.piSkillPaths)
	}
	if handler.claudeSandboxPluginErr != nil {
		t.Fatal(handler.claudeSandboxPluginErr)
	}
	if filepath.Base(handler.claudeSandboxPluginPath) != "claude" {
		t.Fatalf("Claude sandbox plugin path = %q", handler.claudeSandboxPluginPath)
	}
}

func TestMaterializeKiwiSandbox(t *testing.T) {
	dataDirectory := t.TempDir()
	piExtensionPath, claudePluginPath, err := materializeKiwiSandbox(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(piExtensionPath) != "kiwi-sandbox.ts" {
		t.Fatalf("Pi extension path = %q", piExtensionPath)
	}
	shim, err := os.ReadFile(piExtensionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(shim, []byte("../kiwi-sandbox/packages/pi/src/index.ts")) {
		t.Fatalf("Pi extension shim = %q", shim)
	}

	for _, relative := range []string{
		filepath.Join(".claude-plugin", "plugin.json"),
		".mcp.json",
		filepath.Join("hooks", "hooks.json"),
		filepath.Join("src", "mcp.ts"),
		filepath.Join("skills", "enable", "SKILL.md"),
		filepath.Join("skills", "disable", "SKILL.md"),
		filepath.Join("skills", "config", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(claudePluginPath, relative)); err != nil {
			t.Fatalf("materialized Claude plugin file %q: %v", relative, err)
		}
	}
	piSkillPath := kiwiSandboxPiSkillPath(dataDirectory)
	if _, err := os.Stat(filepath.Join(piSkillPath, "SKILL.md")); err != nil {
		t.Fatalf("materialized Pi config skill: %v", err)
	}
	corePath := filepath.Join(dataDirectory, "kiwi-sandbox", "packages", "core", "src", "sandbox.ts")
	if _, err := os.Stat(corePath); err != nil {
		t.Fatalf("materialized shared sandbox library: %v", err)
	}
}
