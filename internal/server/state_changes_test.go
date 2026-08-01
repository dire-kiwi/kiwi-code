package server

import (
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/events"
	"github.com/dire-kiwi/kiwi-code/internal/thread"
)

func TestStateChangeBrokerCoalescesMutationBursts(t *testing.T) {
	broker := newStateChangeBroker()
	subscription := broker.Subscribe(
		thread.Key{ProjectID: "project", ThreadID: "thread"},
		stateTopicBrowserStatus,
	)
	defer subscription.Close()

	for range 10_000 {
		broker.Publish(events.Invalidation{
			Topic: stateTopicBrowserStatus,
			Key:   thread.Key{ProjectID: "project", ThreadID: "thread"},
		})
	}
	if got := len(subscription.Events()); got != 1 {
		t.Fatalf("coalesced wake depth = %d, want 1", got)
	}
	<-subscription.Events()

	broker.Publish(events.Invalidation{
		Topic: stateTopicBrowserStatus,
		Key:   thread.Key{ProjectID: "other-project", ThreadID: "thread"},
	})
	broker.Publish(events.Invalidation{Topic: stateTopicSettings})
	select {
	case <-subscription.Events():
		t.Fatal("unrelated mutation woke the subscription")
	default:
	}

	// An unscoped invalidation reaches every matching scoped subscriber.
	broker.Publish(events.Invalidation{Topic: stateTopicBrowserStatus})
	select {
	case <-subscription.Events():
	case <-time.After(time.Second):
		t.Fatal("global browser invalidation did not wake the subscription")
	}
}

func TestNotifyBrowserStateChangedPublishesStatusInvalidation(t *testing.T) {
	server := &Server{stateChanges: newStateChangeBroker()}
	subscription := server.stateChanges.Subscribe(
		thread.Key{ProjectID: "project", ThreadID: "thread"},
		stateTopicBrowserStatus,
	)
	defer subscription.Close()
	server.notifyBrowserStateChanged("project", "thread")
	if got := len(subscription.Events()); got != 1 {
		t.Fatalf("browser mutation queued %d wakes, want 1", got)
	}
}

func TestThreadStateInvalidationsCoalescePerKey(t *testing.T) {
	server := &Server{stateChanges: newStateChangeBroker()}
	first := server.stateChanges.Subscribe(thread.Key{ProjectID: "project", ThreadID: "first"}, stateTopicThreadStatus)
	defer first.Close()
	second := server.stateChanges.Subscribe(thread.Key{ProjectID: "project", ThreadID: "second"}, stateTopicThreadStatus)
	defer second.Close()

	for range 1_000 {
		server.notifyThreadStatusChanged("project", "first")
		server.notifyThreadStatusChanged("project", "second")
	}
	if got := len(first.Events()); got != 1 {
		t.Fatalf("first thread wake depth = %d, want 1", got)
	}
	if got := len(second.Events()); got != 1 {
		t.Fatalf("second thread wake depth = %d, want 1", got)
	}
}

func TestBrowserStreamStateInvalidationsAreThrottledPerThread(t *testing.T) {
	server := &Server{stateChanges: newStateChangeBroker()}
	subscription := server.stateChanges.Subscribe(
		thread.Key{ProjectID: "project", ThreadID: "thread"},
		stateTopicBrowserStatus,
	)
	defer subscription.Close()

	for range 10_000 {
		server.scheduleBrowserStateChanged("project", "thread")
	}
	server.browserStateInvalidations.mu.Lock()
	pending := len(server.browserStateInvalidations.pending)
	server.browserStateInvalidations.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending browser refreshes = %d, want 1", pending)
	}
	select {
	case <-subscription.Events():
	case <-time.After(time.Second):
		t.Fatal("throttled browser refresh did not fire")
	}

	server.scheduleBrowserStateChanged("project", "thread")
	select {
	case <-subscription.Events():
	case <-time.After(time.Second):
		t.Fatal("browser refresh did not re-arm after the throttle interval")
	}
}
