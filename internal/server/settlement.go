package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const threadSettlementIdleLimit = 3 * 24 * time.Hour

var errThreadWorking = errors.New("wait for the agent to finish before settling this thread")

func (s *Server) setThreadSettlement(projectID, threadID string, settled bool, now time.Time) (project.Thread, error) {
	s.settlementMu.Lock()
	defer s.settlementMu.Unlock()
	return s.setThreadSettlementLocked(projectID, threadID, settled, now)
}

func (s *Server) setThreadSettlementLocked(projectID, threadID string, settled bool, now time.Time, idleBefore ...time.Time) (result project.Thread, resultErr error) {
	item, thread, err := s.projects.GetThreadPersisted(projectID, threadID)
	if err != nil {
		return project.Thread{}, err
	}
	if !settled {
		return s.projects.SetThreadSettled(projectID, threadID, false)
	}
	if s.piActivity != nil {
		for _, activity := range s.piActivity.list(now) {
			if activity.ProjectID == projectID && activity.ThreadID == threadID && activity.State == piActivityWorking {
				return project.Thread{}, errThreadWorking
			}
		}
	}
	settle := func() (project.Thread, error) {
		if len(idleBefore) > 0 {
			return s.projects.SetThreadSettledIfIdle(projectID, threadID, idleBefore[0])
		}
		return s.projects.SetThreadSettled(projectID, threadID, true)
	}
	if s.terminal == nil {
		return settle()
	}
	h := s.terminal
	// Fence all launches before changing durable state. Unlike deletion, release
	// the stop marker afterward: settledAt itself prevents new sessions.
	lease, err := h.stopThreadSessions(item, threadID)
	if err != nil {
		return project.Thread{}, err
	}
	if lease.Adopted() {
		return project.Thread{}, errors.Join(errTerminalStopping, h.retainStopThread(projectID, threadID, lease))
	}
	defer func() { resultErr = errors.Join(resultErr, h.cancelStopThread(projectID, threadID, lease)) }()
	h.sessionMu.Lock()
	mutation, err := h.lockTerminalMutationLocked(projectID, threadID)
	if err != nil {
		h.sessionMu.Unlock()
		return project.Thread{}, err
	}
	if h.nativeThreadWorking(projectID, threadID) {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return project.Thread{}, errors.Join(errThreadWorking, releaseErr)
	}
	var before inactiveThreadSessions
	if h.tmuxPath != "" {
		activities, inspectErr := h.tmuxSessionActivities()
		if inspectErr != nil {
			releaseErr := mutation.Release()
			h.sessionMu.Unlock()
			return project.Thread{}, errors.Join(inspectErr, releaseErr)
		}
		before = inactiveSessionsForThread(item, thread, activities)
	}
	if thread.SettledAt == nil && len(idleBefore) > 0 && before.LastActivityAt.After(idleBefore[0]) {
		releaseErr := mutation.Release()
		h.sessionMu.Unlock()
		return project.Thread{}, errors.Join(project.ErrThreadRecentlyActive, releaseErr)
	}
	thread, err = settle()
	if err == nil && h.tmuxPath != "" {
		err = h.stopThreadSessionsLocked(item, threadID)
	}
	err = errors.Join(err, mutation.Release())
	h.sessionMu.Unlock()
	// Native agents live outside tmux. Stop them too, preserving saved sessions.
	if thread.SettledAt != nil {
		if h.nativePi != nil {
			err = errors.Join(err, h.nativePi.stopThread(projectID, threadID))
		}
		if h.nativeClaude != nil {
			err = errors.Join(err, h.nativeClaude.stopThread(projectID, threadID))
		}
		if h.nativeCodex != nil {
			err = errors.Join(err, h.nativeCodex.stopThread(projectID, threadID))
		}
		if s.piActivity != nil {
			s.piActivity.acknowledge(projectID, threadID)
		}
		if err == nil && len(before.SessionNames) > 0 && s.sessionClosures != nil {
			event, eventErr := newSessionClosureEvent(item, thread, before, now)
			event.Reason = "settlement"
			if eventErr == nil {
				eventErr = s.sessionClosures.append(event)
			}
			err = errors.Join(err, eventErr)
			s.notifyStateChanged(stateTopicSessionClosures, "", "")
		}
		h.wakeThreadTmuxWatchers(projectID, threadID)
		h.notifyThreadStatusChanged(projectID, threadID)
	}
	return thread, err
}

func (s *Server) settleIdleThreads(now time.Time) error {
	s.settlementMu.Lock()
	defer s.settlementMu.Unlock()
	var activities []tmuxSessionActivity
	if s.terminal != nil && s.terminal.tmuxPath != "" {
		var err error
		activities, err = s.terminal.tmuxSessionActivities()
		if err != nil {
			return err
		}
	}
	items, err := s.projects.ListPersisted()
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range items {
		for _, thread := range item.Threads {
			if thread.RollbackPending {
				continue
			}
			latest := inactiveSessionsForThread(item, thread, activities).LastActivityAt
			if thread.UnsettledAt != nil && thread.UnsettledAt.After(latest) {
				latest = *thread.UnsettledAt
			}
			// Reconcile settled threads too, including migrated archives and a previous
			// interrupted shutdown. This is idempotent and never deletes saved history.
			if thread.SettledAt == nil && latest.After(now.Add(-threadSettlementIdleLimit)) {
				continue
			}
			if _, err := s.setThreadSettlementLocked(item.ID, thread.ID, true, now, now.Add(-threadSettlementIdleLimit)); err != nil && !errors.Is(err, errThreadWorking) && !errors.Is(err, project.ErrThreadRecentlyActive) {
				failures = append(failures, fmt.Errorf("settle thread %s/%s: %w", item.ID, thread.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (h *terminalHandler) nativeThreadWorking(projectID, threadID string) bool {
	key := piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}
	if h.nativeCodex != nil {
		h.nativeCodex.mu.Lock()
		p := h.nativeCodex.processes[key]
		h.nativeCodex.mu.Unlock()
		if p != nil {
			p.mu.Lock()
			working := p.state.Working
			p.mu.Unlock()
			if working {
				return true
			}
		}
	}
	if h.nativePi != nil {
		h.nativePi.mu.Lock()
		process := h.nativePi.processes[key]
		h.nativePi.mu.Unlock()
		if process != nil {
			if process.promptPending.Load() {
				return true
			}
			if run, ok := process.latestRunSnapshot(); ok && (run.State == "starting" || run.State == "working") {
				return true
			}
		}
	}
	if h.nativeClaude != nil {
		h.nativeClaude.mu.Lock()
		process := h.nativeClaude.processes[key]
		h.nativeClaude.mu.Unlock()
		if process != nil {
			process.stateMu.Lock()
			working := process.streaming
			process.stateMu.Unlock()
			if working {
				return true
			}
		}
	}
	return false
}
