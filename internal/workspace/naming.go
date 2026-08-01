// Package workspace owns Kiwi Code's tmux topology conventions, layered
// above the product-agnostic internal/tmux client.
//
// The names in this file are a persistent-data compatibility contract (see
// AGENTS.md): Kiwi Code finds the same tmux server, sessions, and windows
// after browser and backend restarts by these exact names. Do not change
// them. Contract tests assert the exact strings here and in
// internal/server/terminal_test.go.
package workspace

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// SocketName is the production tmux socket (`tmux -L kiwi-code`).
	// Reserved exclusively for the user's production environment; tests and
	// development stacks must always use an isolated socket.
	SocketName = "kiwi-code"

	// SessionPrefix starts every canonical thread session name.
	SessionPrefix = "kiwi-code-"

	// ViewSessionPrefix starts every temporary per-browser view session.
	ViewSessionPrefix = "kiwi-code-view-"
)

// tmux user options carrying Kiwi Code metadata on sessions, windows, and
// panes. Live sessions persist these across backend restarts.
const (
	OptionTool             = "@kiwi-code-tool"
	OptionAgent            = "@kiwi-code-agent"
	OptionProcessID        = "@kiwi-code-process-id"
	OptionSourceSession    = "@kiwi-code-source-session"
	OptionOwnerPID         = "@kiwi-code-owner-pid"
	OptionLastUsed         = "@kiwi-code-last-used"
	OptionLastStatusChange = "@kiwi-code-last-status-change"
)

// SessionName is the canonical tmux session for a thread's tool. The shell
// terminal lives in `kiwi-code-<project>-<thread>-terminal`; nvim, lazygit,
// the coding-agent window, and process windows share
// `kiwi-code-<project>-<thread>-tools`.
func SessionName(projectID, threadID, tool string) string {
	return SessionPrefix + projectID + "-" + threadID + "-" + SessionSuffix(tool)
}

// SessionSuffix maps a terminal tool to its session suffix. Callers must
// have normalized the tool name first; unknown tools pass through so a
// malformed name can never silently alias a canonical session.
func SessionSuffix(tool string) string {
	switch tool {
	case "", "terminal":
		return "terminal"
	case "nvim", "lazygit", "pi", "process":
		return "tools"
	default:
		return tool
	}
}

// ViewSessionName builds the name of a temporary view session. The owner PID
// and creation time are recoverable via ParseViewIdentity so other backend
// processes can honor a creation grace period before adopting or reaping.
func ViewSessionName(ownerPID int, createdAt time.Time, counter uint64) string {
	return fmt.Sprintf("%s%d-%x-%d", ViewSessionPrefix, ownerPID, createdAt.UnixNano(), counter)
}

// ParseViewIdentity recovers the owning process and creation time from a
// view session name produced by ViewSessionName.
func ParseViewIdentity(sessionName string) (pid int, createdAt time.Time, ok bool) {
	rest, found := strings.CutPrefix(sessionName, ViewSessionPrefix)
	if !found {
		return 0, time.Time{}, false
	}
	parts := strings.SplitN(rest, "-", 3)
	if len(parts) != 3 {
		return 0, time.Time{}, false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return 0, time.Time{}, false
	}
	nanos, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil || nanos <= 0 {
		return 0, time.Time{}, false
	}
	return pid, time.Unix(0, nanos), true
}
