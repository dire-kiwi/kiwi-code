package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

const (
	threadStatusEventName        = "thread-status"
	gitStatusReconcileInterval   = 2 * time.Second
	threadEventKeepAliveInterval = 15 * time.Second
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

// notifyThreadStatusChanged wakes active thread streams after a mutation made
// through Dire Mux or a tmux control-mode notification. Git changes made
// outside Dire Mux are handled by a separate repository reconciliation.
func (s *Server) notifyThreadStatusChanged(projectID, threadID string) {
	if s.threadStatusChanges == nil {
		return
	}
	s.threadStatusChanges.Publish(threadStatusKey{projectID: projectID, threadID: threadID})
}

func (s *Server) streamThreadEvents(w http.ResponseWriter, r *http.Request) {
	s.streamThreadEventsWithInterval(w, r, gitStatusReconcileInterval)
}

func (s *Server) streamThreadEventsWithInterval(w http.ResponseWriter, r *http.Request, gitReconcileInterval time.Duration) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Thread not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}

	flusher, ok := prepareEventStream(w, "Thread status streaming is unavailable.")
	if !ok {
		return
	}

	var changes <-chan threadStatusKey
	unsubscribe := func() {}
	if s.threadStatusChanges != nil {
		subscription := s.threadStatusChanges.Subscribe()
		changes = subscription.Events()
		unsubscribe = subscription.Close
	}
	defer unsubscribe()

	stopWatchingTmux := func() {}
	if s.terminal != nil {
		stopWatchingTmux = s.terminal.watchThreadTmux(projectID, threadID)
	}
	defer stopWatchingTmux()

	var currentSnapshot threadStatusSnapshot
	var previousPayload []byte
	writeSnapshot := func(snapshot threadStatusSnapshot, force bool) bool {
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return false
		}
		currentSnapshot = snapshot
		if !force && bytes.Equal(previousPayload, payload) {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", threadStatusEventName, payload); err != nil {
			return false
		}
		previousPayload = append(previousPayload[:0], payload...)
		flusher.Flush()
		return true
	}
	refreshAll := func(force bool) bool {
		item, thread, err := s.projects.GetThread(projectID, threadID)
		if err != nil || r.Context().Err() != nil {
			return false
		}
		snapshot := s.readThreadStatusSnapshot(r.Context(), item, thread)
		if r.Context().Err() != nil {
			return false
		}
		return writeSnapshot(snapshot, force)
	}
	refreshGit := func() bool {
		_, thread, err := s.projects.GetThread(projectID, threadID)
		if err != nil || r.Context().Err() != nil {
			return false
		}
		branches, gitError := readThreadGitStatus(r.Context(), thread)
		if r.Context().Err() != nil {
			return false
		}
		next := currentSnapshot
		next.GitBranches = branches
		next.Errors.GitBranches = gitError
		return writeSnapshot(next, false)
	}

	if !refreshAll(true) {
		return
	}
	if gitReconcileInterval <= 0 {
		gitReconcileInterval = gitStatusReconcileInterval
	}
	gitTicker := time.NewTicker(gitReconcileInterval)
	defer gitTicker.Stop()
	keepAliveTicker := time.NewTicker(threadEventKeepAliveInterval)
	defer keepAliveTicker.Stop()

	key := threadStatusKey{projectID: projectID, threadID: threadID}
	for {
		select {
		case <-r.Context().Done():
			return
		case changed, open := <-changes:
			if !open {
				return
			}
			if changed == key && !refreshAll(false) {
				return
			}
		case <-gitTicker.C:
			// tmux state is intentionally not read here. Structural and command
			// changes arrive through its control-mode client.
			if !refreshGit() {
				return
			}
		case <-keepAliveTicker.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
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
			records = s.reconcileWorkflowProcesses(item, thread, records)
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

	processes, err := s.terminal.processWindows(item, thread)
	if err != nil {
		snapshot.Errors.Processes = "Could not list process shells."
	} else {
		snapshot.Processes = processes
	}
	windows, err := s.terminal.existingShellWindows(item, thread)
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
		return nil, "git is required for branch controls. Install git and restart dire-mux."
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, ""
	}
	return nil, gitErrorMessage(err)
}
