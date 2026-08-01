package server

import (
	"context"
	"time"
)

const (
	stateProjectReconcileInterval  = 30 * time.Second
	stateActivityReconcileInterval = 5 * time.Second
)

func (s *Server) openProjectsTopic(ctx context.Context, channel *stateChannel) error {
	updates, unsubscribe := s.projects.SubscribeLatestChanges()
	defer unsubscribe()
	return runSnapshotTopic(ctx, channel, updates, snapshotTopicOptions{
		updatesEnded:      "Project updates ended.",
		reconcileInterval: stateProjectReconcileInterval,
	}, func() error {
		return channel.Snapshot(clientProjects(s.projects.List()))
	})
}

func (s *Server) openProfilesTopic(ctx context.Context, channel *stateChannel) error {
	updates, unsubscribe := s.projects.SubscribeLatestProfileChanges()
	defer unsubscribe()
	return runSnapshotTopic(ctx, channel, updates, snapshotTopicOptions{
		updatesEnded: "Profile updates ended.",
	}, func() error {
		return channel.Snapshot(s.projects.ListProfiles())
	})
}

func (s *Server) openAgentActivityTopic(ctx context.Context, channel *stateChannel) error {
	if s.piActivity == nil {
		return stateTopicFailure("Agent activity is unavailable.")
	}
	updates, unsubscribe := s.piActivity.SubscribeLatest()
	defer unsubscribe()
	return runSnapshotTopic(ctx, channel, updates, snapshotTopicOptions{
		updatesEnded:      "Agent activity updates ended.",
		reconcileInterval: stateActivityReconcileInterval,
	}, func() error {
		return channel.Snapshot(s.clientPiActivities(s.piActivity.List(time.Now())))
	})
}

func (s *Server) openThreadUsageTopic(ctx context.Context, channel *stateChannel) error {
	if s.threadUsage == nil {
		return stateTopicFailure("Thread usage is unavailable.")
	}
	usageSubscription, unsubscribeUsage := s.threadUsage.SubscribeLatest()
	defer unsubscribeUsage()
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()
	snapshot := func() error {
		return channel.Snapshot(s.threadUsage.Snapshots(clientProjects(s.projects.List())))
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
