package server

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps every test away from the user's real Claude Code config.
// Claude launches record workspace trust in the global config of the inherited
// CLAUDE_CONFIG_DIR, which would otherwise be ~/.claude.json.
func TestMain(m *testing.M) {
	configDirectory, err := os.MkdirTemp("", "kiwi-code-test-claude-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated Claude config directory:", err)
		os.Exit(1)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", configDirectory); err != nil {
		fmt.Fprintln(os.Stderr, "isolate Claude config directory:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(configDirectory)
	os.Exit(code)
}
