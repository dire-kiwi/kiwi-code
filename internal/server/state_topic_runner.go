package server

import (
	"context"
	"time"
)

type snapshotTopicOptions struct {
	updatesEnded      string
	reconcileInterval time.Duration
}

// runSnapshotTopic drives topics whose one update source, optional reconcile
// ticker, and explicit resnap all perform the same authoritative read.
func runSnapshotTopic[T any](
	ctx context.Context,
	channel *stateChannel,
	updates <-chan T,
	options snapshotTopicOptions,
	snapshot func() error,
) error {
	if err := snapshot(); err != nil {
		return err
	}

	var ticker *time.Ticker
	var ticks <-chan time.Time
	if options.reconcileInterval > 0 {
		ticker = time.NewTicker(options.reconcileInterval)
		ticks = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-updates:
			if !open {
				return stateTopicFailure(options.updatesEnded)
			}
		case <-ticks:
		case <-channel.Resnap():
		}
		if err := snapshot(); err != nil {
			return err
		}
	}
}
