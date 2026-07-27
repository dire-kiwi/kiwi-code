package server

import (
	"context"
	"errors"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const stateThreadStatusWarmup = 50 * time.Millisecond

func (s *Server) openThreadStatusTopic(ctx context.Context, projectID, threadID string, channel *stateChannel) error {
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			return stateTopicFailure("Thread not found.")
		}
		return stateTopicFailure("Could not load the thread.")
	}

	changes, changeEvents := s.subscribeStateChanges(
		projectID,
		threadID,
		stateTopicThreadStatus,
	)
	if changes != nil {
		defer changes.Close()
	}
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	stopWatchingTmux := func() {}
	if s.terminal != nil {
		stopWatchingTmux = s.terminal.watchThreadTmux(projectID, threadID)
	}
	defer stopWatchingTmux()

	var current threadStatusSnapshot
	refreshAll := func() error {
		item, thread, err := s.projects.GetThread(projectID, threadID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
				return stateTopicFailure("Thread no longer exists.")
			}
			return stateTopicFailure("Could not load the thread.")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current = s.readThreadStatusSnapshot(ctx, item, thread)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return channel.Snapshot(current)
	}
	refreshGit := func() error {
		_, thread, err := s.projects.GetThread(projectID, threadID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
				return stateTopicFailure("Thread no longer exists.")
			}
			return stateTopicFailure("Could not load the thread.")
		}
		if s.terminal != nil {
			s.terminal.yieldToInteractiveTerminalSetup(ctx, processProjectionInteractiveYieldLimit)
		}
		branches, gitError := readThreadGitStatus(ctx, thread)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current.GitBranches = branches
		current.Errors.GitBranches = gitError
		return channel.Snapshot(current)
	}

	// The terminal component starts on the next animation frame. Avoid racing
	// its first attachment with Git and tmux status subprocesses from this
	// channel's initial authoritative snapshot.
	warmup := time.NewTimer(stateThreadStatusWarmup)
	select {
	case <-ctx.Done():
		warmup.Stop()
		return ctx.Err()
	case <-warmup.C:
	}
	if err := refreshAll(); err != nil {
		return err
	}
	gitTicker := time.NewTicker(gitStatusReconcileInterval)
	defer gitTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changeEvents:
			if err := refreshAll(); err != nil {
				return err
			}
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
				if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
					return stateTopicFailure("Thread no longer exists.")
				}
				return stateTopicFailure("Could not load the thread.")
			}
		case <-gitTicker.C:
			if err := refreshGit(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := refreshAll(); err != nil {
				return err
			}
		}
	}
}
