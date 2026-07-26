package server

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	sandboxStateReconcileInterval = 2 * time.Second
	tmuxStateReconcileInterval    = 2 * time.Second
	agentSkillReconcileInterval   = 5 * time.Second
)

func (s *Server) openSettingsTopic(ctx context.Context, channel *stateChannel) error {
	changes, events := s.subscribeStateChanges("", "", stateTopicSettings)
	if changes != nil {
		defer changes.Close()
	}
	snapshot := func() error { return channel.Snapshot(s.projects.GetSettings()) }
	if err := snapshot(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openCodingAgentsTopic(ctx context.Context, projectID string, channel *stateChannel) error {
	if s.terminal == nil {
		return stateTopicFailure("Coding agent discovery is unavailable.")
	}
	changes, events := s.subscribeStateChanges(projectID, "", stateTopicCodingAgents, stateTopicSettings)
	if changes != nil {
		defer changes.Close()
	}
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		configs, err := s.terminal.codingAgentConfigs(ctx, projectID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return stateTopicFailure("Project not found.")
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return stateTopicFailure("Could not discover coding agents.")
		}
		return channel.Snapshot(configs)
	}
	if err := snapshot(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if projectID != "" {
				if _, err := s.projects.Get(projectID); errors.Is(err, project.ErrNotFound) {
					return stateTopicFailure("Project no longer exists.")
				}
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openSandboxConfigTopic(ctx context.Context, params stateSandboxTopic, channel *stateChannel) error {
	changes, events := s.subscribeStateChanges(
		params.ProjectID,
		params.ThreadID,
		stateTopicSandboxConfig,
	)
	if changes != nil {
		defer changes.Close()
	}
	snapshot := func() error {
		var (
			state sandboxConfigState
			err   error
		)
		if params.Scope == "global" {
			state, err = globalSandboxConfigState()
		} else {
			_, thread, threadErr := s.projects.GetThread(params.ProjectID, params.ThreadID)
			if threadErr != nil {
				if errors.Is(threadErr, project.ErrNotFound) || errors.Is(threadErr, project.ErrThreadNotFound) {
					return stateTopicFailure("Thread not found.")
				}
				return stateTopicFailure("Could not load the thread.")
			}
			state, err = threadSandboxConfigState(thread)
		}
		if err != nil {
			return stateTopicFailure("Could not load the sandbox configuration.")
		}
		return channel.Snapshot(state)
	}
	if err := snapshot(); err != nil {
		return err
	}
	ticker := time.NewTicker(sandboxStateReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openCleanupTopic(ctx context.Context, channel *stateChannel) error {
	changes, events := s.subscribeStateChanges("", "", stateTopicCleanup, stateTopicSettings)
	if changes != nil {
		defer changes.Close()
	}
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		overview, err := s.projects.CleanupOverview(time.Now())
		if err != nil {
			log.Printf("build cleanup state snapshot: %v", err)
			return stateTopicFailure("Could not load the cleanup queue.")
		}
		return channel.Snapshot(overview)
	}
	if err := snapshot(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if err := snapshot(); err != nil {
				return err
			}
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openSessionClosuresTopic(ctx context.Context, channel *stateChannel) error {
	if s.sessionClosures == nil {
		return stateTopicFailure("Session closure history is unavailable.")
	}
	changes, events := s.subscribeStateChanges("", "", stateTopicSessionClosures)
	if changes != nil {
		defer changes.Close()
	}
	snapshot := func() error {
		overview, err := s.sessionClosureSnapshot()
		if err != nil {
			return stateTopicFailure("Could not load the tmux session closure log.")
		}
		return channel.Snapshot(overview)
	}
	if err := snapshot(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openGitBranchesTopic(ctx context.Context, projectID string, channel *stateChannel) error {
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		item, err := s.projects.Get(projectID)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				return stateTopicFailure("Project not found.")
			}
			return stateTopicFailure("Could not load the project.")
		}
		state, err := readGitBranchState(ctx, item.Path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return stateTopicFailure("Could not load Git branches.")
		}
		return channel.Snapshot(state)
	}
	if err := snapshot(); err != nil {
		return err
	}
	ticker := time.NewTicker(gitStatusReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if _, err := s.projects.Get(projectID); errors.Is(err, project.ErrNotFound) {
				return stateTopicFailure("Project no longer exists.")
			}
		case <-ticker.C:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openTmuxSessionsTopic(ctx context.Context, channel *stateChannel) error {
	if s.terminal == nil || s.terminal.tmuxPath == "" {
		return stateTopicFailure("tmux is required to inspect sessions.")
	}
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		sessions, err := s.terminal.tmuxBrowserSessionsContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return stateTopicFailure("Could not load tmux sessions.")
		}
		return channel.Snapshot(sessions)
	}
	if err := snapshot(); err != nil {
		return err
	}
	ticker := time.NewTicker(tmuxStateReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if err := snapshot(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openAgentSkillsTopic(ctx context.Context, channel *stateChannel) error {
	if s.agentSkills == nil {
		return stateTopicFailure("Agent skill status is unavailable.")
	}
	changes, events := s.subscribeStateChanges("", "", stateTopicAgentSkills)
	if changes != nil {
		defer changes.Close()
	}
	snapshot := func() error {
		status, err := s.agentSkills.status()
		if err != nil {
			return stateTopicFailure("Could not inspect agent skills.")
		}
		return channel.Snapshot(status)
	}
	if err := snapshot(); err != nil {
		return err
	}
	ticker := time.NewTicker(agentSkillReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) sessionClosureSnapshot() (sessionClosureOverview, error) {
	events, err := s.sessionClosures.list()
	if err != nil {
		return sessionClosureOverview{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].ClosedAt.After(events[j].ClosedAt) })
	return sessionClosureOverview{
		GeneratedAt:     time.Now().UTC(),
		InactivityHours: int(tmuxSessionInactivityLimit / time.Hour),
		Events:          events,
	}, nil
}
