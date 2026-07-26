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
	Workflows    string `json:"workflows,omitempty"`
	Plans        string `json:"plans,omitempty"`
}

type threadStatusSnapshot struct {
	GitBranches     *gitBranchState               `json:"gitBranches"`
	ContextStatuses map[string]agentContextStatus `json:"contextStatuses"`
	Processes       []processWindow               `json:"processes"`
	ShellWindows    []tmuxWindow                  `json:"shellWindows"`
	Workflows       []workflowRunSnapshot         `json:"workflows"`
	Plans           []threadPlanSnapshot          `json:"plans"`
	Errors          threadStatusErrors            `json:"errors"`
}

// notifyThreadStatusChanged wakes active state channels after a mutation made
// through Kiwi Code or a tmux control-mode notification. Git changes made
// outside Kiwi Code are handled by a separate repository reconciliation.
func (s *Server) notifyThreadStatusChanged(projectID, threadID string) {
	s.notifyStateChanged(stateTopicThreadStatus, projectID, threadID)
	// The sidebar process projection spans every project and thread.
	s.notifyStateChanged(stateTopicProcessWebServers, "", "")
}

func (s *Server) readThreadStatusSnapshot(ctx context.Context, item project.Project, thread project.Thread) threadStatusSnapshot {
	snapshot := threadStatusSnapshot{
		ContextStatuses: s.contextStatuses.forThread(item.ID, thread.ID),
		Processes:       []processWindow{},
		ShellWindows:    []tmuxWindow{},
		Workflows:       []workflowRunSnapshot{},
		Plans:           []threadPlanSnapshot{},
	}
	snapshot.GitBranches, snapshot.Errors.GitBranches = readThreadGitStatus(ctx, thread)
	if s.workflows != nil {
		if records, err := s.workflows.list(item.ID, thread.ID); err != nil {
			snapshot.Errors.Workflows = "Could not load workflows."
		} else {
			for _, record := range records {
				snapshot.Workflows = append(snapshot.Workflows, workflowSummarySnapshot(record))
			}
		}
	}
	if s.plans != nil {
		owner, err := threadPlanOwner(item, thread)
		if err != nil {
			snapshot.Errors.Plans = "Could not resolve the thread's plans."
		} else if plans, err := s.plans.list(item.ID, owner.ID); err != nil {
			snapshot.Errors.Plans = "Could not load the thread's plans."
		} else {
			snapshot.Plans = plans
		}
	}

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
