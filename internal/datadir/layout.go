// Package datadir is the single owner of the persistent data-directory
// layout. The directory and file names below are a compatibility contract:
// they must survive old-binary to new-binary restarts, so they may never
// change. A unit test freezes the exact strings.
package datadir

import "path/filepath"

// Frozen names. Do not change: existing installations have live state under
// these paths and a rename would orphan durable stop markers, mutation locks,
// agent exit tombstones, native agent sessions, and the closure log.
const (
	TerminalStopsDirectoryName        = "terminal-stops-v1"
	TerminalMutationsDirectoryName    = "terminal-mutations-v1"
	CodingAgentExitsDirectoryName     = "coding-agent-exits"
	PiNativeSessionsDirectoryName     = "pi-native-sessions"
	ClaudeNativeSessionsDirectoryName = "claude-native-sessions"
	SessionClosuresFileName           = "tmux-session-closures.json"
	ThreadUsageFileName               = "thread-usage.json"
)

// Layout resolves feature-owned paths inside one data directory root.
type Layout struct {
	root string
}

func NewLayout(root string) Layout {
	return Layout{root: root}
}

func (l Layout) Root() string { return l.root }

// TerminalStops holds durable terminal stop tombstones.
func (l Layout) TerminalStops() string {
	return filepath.Join(l.root, TerminalStopsDirectoryName)
}

// TerminalMutations holds cross-process per-thread mutation locks.
func (l Layout) TerminalMutations() string {
	return filepath.Join(l.root, TerminalMutationsDirectoryName)
}

// CodingAgentExits holds coding-agent exit tombstones.
func (l Layout) CodingAgentExits() string {
	return filepath.Join(l.root, CodingAgentExitsDirectoryName)
}

// PiNativeSessions holds per-thread Pi native session state.
func (l Layout) PiNativeSessions() string {
	return filepath.Join(l.root, PiNativeSessionsDirectoryName)
}

// ClaudeNativeSessions holds per-thread Claude native session state.
func (l Layout) ClaudeNativeSessions() string {
	return filepath.Join(l.root, ClaudeNativeSessionsDirectoryName)
}

// SessionClosures is the tmux session closure log file.
func (l Layout) SessionClosures() string {
	return filepath.Join(l.root, SessionClosuresFileName)
}

// ThreadUsage is the persisted per-thread usage totals file.
func (l Layout) ThreadUsage() string {
	return filepath.Join(l.root, ThreadUsageFileName)
}
