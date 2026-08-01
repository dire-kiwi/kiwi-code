// Package events carries state-invalidation wakeups between feature areas.
// The bus is a latest-state wakeup source, not an event log: each subscriber
// owns a depth-one signal, so mutation bursts collapse while a consumer
// recomputes its authoritative snapshot. Lower layers publish invalidations;
// the HTTP state topics subscribe. This replaces direct callback fields
// patched between subsystems.
package events

import (
	"sync"

	"github.com/dire-kiwi/kiwi-code/internal/thread"
)

// Invalidation names changed state. An empty ProjectID or ThreadID in Key is
// a wildcard: the invalidation reaches every subscriber of the topic whose
// scope matches the fields that are set.
type Invalidation struct {
	Topic string
	Key   thread.Key
}

// Bus fans invalidations out to subscribers.
type Bus struct {
	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
}

// Subscription is one subscriber's depth-one wakeup signal.
type Subscription struct {
	bus    *Bus
	topics map[string]struct{}
	key    thread.Key
	wake   chan struct{}
	once   sync.Once
}

func NewBus() *Bus {
	return &Bus{subscribers: make(map[*Subscription]struct{})}
}

// Subscribe registers interest in topics scoped to key. Empty key fields
// subscribe at a broader scope (see Invalidation).
func (b *Bus) Subscribe(key thread.Key, topics ...string) *Subscription {
	subscription := &Subscription{
		bus:    b,
		topics: make(map[string]struct{}, len(topics)),
		key:    key,
		wake:   make(chan struct{}, 1),
	}
	for _, topic := range topics {
		subscription.topics[topic] = struct{}{}
	}
	b.mu.Lock()
	b.subscribers[subscription] = struct{}{}
	b.mu.Unlock()
	return subscription
}

// Publish wakes every matching subscriber. It never blocks: a subscriber
// with a pending wakeup simply keeps it.
func (b *Bus) Publish(change Invalidation) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for subscription := range b.subscribers {
		if !subscription.matches(change) {
			continue
		}
		select {
		case subscription.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Subscription) matches(change Invalidation) bool {
	if _, subscribed := s.topics[change.Topic]; !subscribed {
		return false
	}
	if change.Key.ProjectID != "" && change.Key.ProjectID != s.key.ProjectID {
		return false
	}
	return change.Key.ThreadID == "" || change.Key.ThreadID == s.key.ThreadID
}

func (s *Subscription) Events() <-chan struct{} {
	return s.wake
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subscribers, s)
		s.bus.mu.Unlock()
	})
}
