package server

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	gitStatusReconcileInterval = 2 * time.Second
)

type threadStatusKey struct {
	projectID string
	threadID  string
}

type threadStatusErrors struct {
	GitBranches  string `json:"gitBranches,omitempty"`
	Processes    string `json:"processes,omitempty"`
	ShellWindows string `json:"shellWindows,omitempty"`
}

type threadStatusSnapshot struct {
	GitBranches     *gitBranchState               `json:"gitBranches"`
	ContextStatuses map[string]agentContextStatus `json:"contextStatuses"`
	Processes       []processWindow               `json:"processes"`
	ShellWindows    []tmuxWindow                  `json:"shellWindows"`
	Errors          threadStatusErrors            `json:"errors"`
}

// notifyThreadStatusChanged wakes active state channels after a mutation made
// through Kiwi Code or a tmux control-mode notification. Git changes made
// outside Kiwi Code are handled by a separate repository reconciliation.
func (s *Server) notifyThreadStatusChanged(projectID, threadID string) {
	s.notifyStateChanged(stateTopicThreadStatus, projectID, threadID)
	// Keep the global sidebar projection cached per thread. The topic still has
	// one latest-state wakeup, while the durable dirty set prevents a burst from
	// losing which thread projections need to be refreshed.
	s.processWebServerCache.markDirty(threadStatusKey{projectID: projectID, threadID: threadID})
	s.notifyStateChanged(stateTopicProcessWebServers, "", "")
}

func (s *Server) readThreadStatusSnapshot(ctx context.Context, item project.Project, thread project.Thread) threadStatusSnapshot {
	if s.terminal != nil {
		s.terminal.yieldToInteractiveTerminalSetup(ctx, processProjectionInteractiveYieldLimit)
	}
	snapshot := threadStatusSnapshot{
		ContextStatuses: s.contextStatuses.forThread(item.ID, thread.ID),
		Processes:       []processWindow{},
		ShellWindows:    []tmuxWindow{},
	}
	snapshot.GitBranches, snapshot.Errors.GitBranches = readThreadGitStatus(ctx, thread)
	if s.terminal == nil || s.terminal.tmuxPath == "" {
		snapshot.Errors.Processes = "tmux is required for process shells."
		snapshot.Errors.ShellWindows = "tmux is required for shell tabs."
		return snapshot
	}

	processes, err := s.terminal.processWindowsContext(ctx, item, thread)
	if err != nil {
		snapshot.Errors.Processes = "Could not list process shells."
	} else {
		snapshot.Processes = processes
	}
	windows, err := s.terminal.existingShellWindowsContext(ctx, item, thread)
	if err != nil {
		snapshot.Errors.ShellWindows = "Could not load shell tabs."
	} else {
		snapshot.ShellWindows = windows
	}
	return snapshot
}

func readThreadGitStatus(ctx context.Context, thread project.Thread) (*gitBranchState, string) {
	branches, err := readGitBranchState(ctx, thread.Cwd)
	if err == nil {
		return &branches, ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, "git is required for branch controls. Install git and restart kiwi-code."
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, ""
	}
	return nil, gitErrorMessage(err)
}
