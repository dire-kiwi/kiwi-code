package datadir

import (
	"path/filepath"
	"testing"
)

// TestLayoutNamesAreFrozen pins the exact on-disk names. These are a
// persistence compatibility contract; if this test fails, the change would
// orphan live state from existing installations and must not ship without an
// explicit migration.
func TestLayoutNamesAreFrozen(t *testing.T) {
	l := NewLayout("root")
	expected := map[string]string{
		"terminal stops":         filepath.Join("root", "terminal-stops-v1"),
		"terminal mutations":     filepath.Join("root", "terminal-mutations-v1"),
		"coding agent exits":     filepath.Join("root", "coding-agent-exits"),
		"pi native sessions":     filepath.Join("root", "pi-native-sessions"),
		"claude native sessions": filepath.Join("root", "claude-native-sessions"),
		"session closures":       filepath.Join("root", "tmux-session-closures.json"),
		"thread usage":           filepath.Join("root", "thread-usage.json"),
	}
	actual := map[string]string{
		"terminal stops":         l.TerminalStops(),
		"terminal mutations":     l.TerminalMutations(),
		"coding agent exits":     l.CodingAgentExits(),
		"pi native sessions":     l.PiNativeSessions(),
		"claude native sessions": l.ClaudeNativeSessions(),
		"session closures":       l.SessionClosures(),
		"thread usage":           l.ThreadUsage(),
	}
	for name, want := range expected {
		if actual[name] != want {
			t.Fatalf("%s path = %q, want %q", name, actual[name], want)
		}
	}
	if l.Root() != "root" {
		t.Fatalf("root = %q, want %q", l.Root(), "root")
	}
}
