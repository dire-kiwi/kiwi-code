package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// ExactSessionTarget formats a session name as an exact-match tmux target,
// preventing tmux's prefix matching from resolving a decoy session whose name
// merely starts with the requested one.
func ExactSessionTarget(sessionName string) string {
	return "=" + sessionName
}

// ExactCurrentWindowTarget targets the current window of an exact session.
func ExactCurrentWindowTarget(sessionName string) string {
	return ExactSessionTarget(sessionName) + ":"
}

// ExactWindowTarget targets a window by index in an exact session.
func ExactWindowTarget(sessionName string, index int) string {
	return ExactSessionTarget(sessionName) + ":" + strconv.Itoa(index)
}

// WindowTarget identifies a tmux window together with the server incarnation
// that produced it. ServerPID fences operations against a tmux server that
// restarted between observation and use. ProcessID and Tagged carry
// caller-domain bookkeeping for windows discovered via listing.
type WindowTarget struct {
	Index     int
	ID        string
	ServerPID string
	ProcessID string
	Tagged    bool
}

// ParseWindowTarget parses `#{window_index}\t#{window_id}\t#{pid}` output as
// produced by new-session/new-window with a -F format string.
func ParseWindowTarget(output []byte) (WindowTarget, error) {
	line := strings.TrimSpace(string(output))
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 || parts[1] == "" {
		return WindowTarget{}, fmt.Errorf("parse tmux window target: %q", line)
	}
	index, err := strconv.Atoi(parts[0])
	if err != nil {
		return WindowTarget{}, fmt.Errorf("parse tmux window target index: %w", err)
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil {
		return WindowTarget{}, fmt.Errorf("parse tmux window target server pid: %w", err)
	}
	if pid <= 0 {
		return WindowTarget{}, fmt.Errorf("parse tmux window target server pid: invalid value %q", parts[2])
	}
	return WindowTarget{Index: index, ID: parts[1], ServerPID: parts[2]}, nil
}

// CommandError shapes a failed tmux command into an error carrying tmux's own
// message when it produced one, else the exec error.
func CommandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s", action, message)
}
