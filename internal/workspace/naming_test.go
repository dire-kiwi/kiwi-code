package workspace

import (
	"testing"
	"time"
)

// TestNamingContract pins the exact strings of the tmux persistence contract
// (AGENTS.md). A sibling test in internal/server/terminal_test.go asserts the
// same strings through the server's wrappers; both must always agree. If this
// test fails, live user sessions would be orphaned on upgrade.
func TestNamingContract(t *testing.T) {
	if SocketName != "kiwi-code" {
		t.Fatalf("socket name = %q, want kiwi-code", SocketName)
	}
	if SessionPrefix != "kiwi-code-" {
		t.Fatalf("session prefix = %q", SessionPrefix)
	}
	if ViewSessionPrefix != "kiwi-code-view-" {
		t.Fatalf("view session prefix = %q", ViewSessionPrefix)
	}
	if got := SessionName("abc123", "thread456", "terminal"); got != "kiwi-code-abc123-thread456-terminal" {
		t.Fatalf("terminal session name = %q", got)
	}
	if got := SessionName("abc123", "thread456", ""); got != "kiwi-code-abc123-thread456-terminal" {
		t.Fatalf("default session name = %q", got)
	}
	for _, tool := range []string{"nvim", "lazygit", "pi", "process"} {
		if got := SessionName("abc123", "thread456", tool); got != "kiwi-code-abc123-thread456-tools" {
			t.Fatalf("%s session name = %q", tool, got)
		}
	}

	options := map[string]string{
		"tool":               OptionTool,
		"agent":              OptionAgent,
		"process id":         OptionProcessID,
		"source session":     OptionSourceSession,
		"owner pid":          OptionOwnerPID,
		"last used":          OptionLastUsed,
		"last status change": OptionLastStatusChange,
	}
	expected := map[string]string{
		"tool":               "@kiwi-code-tool",
		"agent":              "@kiwi-code-agent",
		"process id":         "@kiwi-code-process-id",
		"source session":     "@kiwi-code-source-session",
		"owner pid":          "@kiwi-code-owner-pid",
		"last used":          "@kiwi-code-last-used",
		"last status change": "@kiwi-code-last-status-change",
	}
	for name, want := range expected {
		if options[name] != want {
			t.Fatalf("%s option = %q, want %q", name, options[name], want)
		}
	}
}

func TestViewSessionNameRoundTrip(t *testing.T) {
	createdAt := time.Unix(0, 1_700_000_000_123_456_789)
	name := ViewSessionName(4242, createdAt, 7)
	pid, parsedCreatedAt, ok := ParseViewIdentity(name)
	if !ok || pid != 4242 || !parsedCreatedAt.Equal(createdAt) {
		t.Fatalf("round trip = pid %d createdAt %v ok %t from %q", pid, parsedCreatedAt, ok, name)
	}
	for _, invalid := range []string{
		"", "kiwi-code-view-", "kiwi-code-view-x-1-1",
		"kiwi-code-view-0-1-1", "kiwi-code-view-42-zz-1", "other-42-1-1",
	} {
		if _, _, ok := ParseViewIdentity(invalid); ok {
			t.Fatalf("ParseViewIdentity(%q) succeeded", invalid)
		}
	}
}
