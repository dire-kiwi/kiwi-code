package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSnapshotTopicRefreshesForUpdatesAndResnap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel := newStateTestChannel(ctx)
	updates := make(chan int)
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- runSnapshotTopic(ctx, channel, updates, snapshotTopicOptions{
			updatesEnded: "Fixture updates ended.",
		}, func() error {
			return channel.Snapshot(calls.Add(1))
		})
	}()

	eventuallyStateTest(t, func() bool { return calls.Load() == 1 })
	updates <- 1
	eventuallyStateTest(t, func() bool { return calls.Load() == 2 })
	channel.resnap <- struct{}{}
	eventuallyStateTest(t, func() bool { return calls.Load() == 3 })
	close(updates)

	select {
	case err := <-done:
		var topicErr *stateTopicError
		if !errors.As(err, &topicErr) || err.Error() != "Fixture updates ended." {
			t.Fatalf("closed update source error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot topic did not stop after its update source closed")
	}
}

func TestRunSnapshotTopicReconcilesAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	channel := newStateTestChannel(ctx)
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- runSnapshotTopic(ctx, channel, (<-chan struct{})(nil), snapshotTopicOptions{
			reconcileInterval: 10 * time.Millisecond,
		}, func() error {
			return channel.Snapshot(calls.Add(1))
		})
	}()

	eventuallyStateTest(t, func() bool { return calls.Load() >= 2 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled snapshot topic error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot topic did not stop after cancellation")
	}
}

func TestRunSnapshotTopicReturnsSnapshotError(t *testing.T) {
	want := errors.New("snapshot failed")
	err := runSnapshotTopic(
		context.Background(),
		newStateTestChannel(context.Background()),
		(<-chan struct{})(nil),
		snapshotTopicOptions{},
		func() error { return want },
	)
	if !errors.Is(err, want) {
		t.Fatalf("snapshot error = %v, want %v", err, want)
	}
}

func TestRunSnapshotTopicReturnsRefreshError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan struct{})
	want := errors.New("refresh failed")
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- runSnapshotTopic(
			ctx,
			newStateTestChannel(ctx),
			updates,
			snapshotTopicOptions{updatesEnded: "Fixture updates ended."},
			func() error {
				if calls.Add(1) > 1 {
					return want
				}
				return nil
			},
		)
	}()

	eventuallyStateTest(t, func() bool { return calls.Load() == 1 })
	updates <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("refresh error = %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot topic did not return its refresh error")
	}
}
