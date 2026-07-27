package server

import (
	"context"
	"time"
)

const (
	stateProjectReconcileInterval  = 30 * time.Second
	stateActivityReconcileInterval = 5 * time.Second
	stateProcessProjectionWarmup   = 50 * time.Millisecond
)

func (s *Server) openProjectsTopic(ctx context.Context, channel *stateChannel) error {
	updates, unsubscribe := s.projects.SubscribeLatestChanges()
	defer unsubscribe()
	if err := channel.Snapshot(clientProjects(s.projects.List())); err != nil {
		return err
	}
	ticker := time.NewTicker(stateProjectReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-updates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if err := channel.Snapshot(clientProjects(s.projects.List())); err != nil {
				return err
			}
		case <-ticker.C:
			if err := channel.Snapshot(clientProjects(s.projects.List())); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := channel.Snapshot(clientProjects(s.projects.List())); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openProfilesTopic(ctx context.Context, channel *stateChannel) error {
	updates, unsubscribe := s.projects.SubscribeLatestProfileChanges()
	defer unsubscribe()
	if err := channel.Snapshot(s.projects.ListProfiles()); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-updates:
			if !open {
				return stateTopicFailure("Profile updates ended.")
			}
			if err := channel.Snapshot(s.projects.ListProfiles()); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := channel.Snapshot(s.projects.ListProfiles()); err != nil {
				return err
			}
		}
	}
}

func (s *Server) openAgentActivityTopic(ctx context.Context, channel *stateChannel) error {
	if s.piActivity == nil {
		return stateTopicFailure("Agent activity is unavailable.")
	}
	updates, unsubscribe := s.piActivity.subscribeLatest()
	defer unsubscribe()
	snapshot := func() error {
		return channel.Snapshot(s.clientPiActivities(s.piActivity.list(time.Now())))
	}
	if err := snapshot(); err != nil {
		return err
	}
	ticker := time.NewTicker(stateActivityReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-updates:
			if !open {
				return stateTopicFailure("Agent activity updates ended.")
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

func (s *Server) openThreadUsageTopic(ctx context.Context, channel *stateChannel) error {
	if s.threadUsage == nil {
		return stateTopicFailure("Thread usage is unavailable.")
	}
	usageSubscription, unsubscribeUsage := s.threadUsage.subscribeLatest()
	defer unsubscribeUsage()
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		return channel.Snapshot(s.threadUsage.snapshots(clientProjects(s.projects.List())))
	}
	if err := snapshot(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-usageSubscription.Events():
			if !open {
				return stateTopicFailure("Thread usage updates ended.")
			}
			if err := snapshot(); err != nil {
				return err
			}
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
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

func (s *Server) openProcessWebServersTopic(ctx context.Context, channel *stateChannel) error {
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	statusChanges, statusEvents := s.subscribeStateChanges("", "", stateTopicProcessWebServers)
	if statusChanges != nil {
		defer statusChanges.Close()
	}

	snapshot := func(refreshAll bool) error {
		value := s.processWebServerCache.snapshotContext(ctx, s, refreshAll)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return channel.Snapshot(value)
	}
	// Terminal panes are mounted immediately after the global state socket.
	// Give their latency-sensitive setup a brief head start before launching a
	// cross-project batch of tmux inspection clients.
	warmup := time.NewTimer(stateProcessProjectionWarmup)
	select {
	case <-ctx.Done():
		warmup.Stop()
		return ctx.Err()
	case <-warmup.C:
	}
	if err := snapshot(true); err != nil {
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
			if err := snapshot(true); err != nil {
				return err
			}
		case <-statusEvents:
			if err := snapshot(false); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(true); err != nil {
				return err
			}
		}
	}
}
