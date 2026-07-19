// Package broadcast provides ordered, in-process fan-out subscriptions.
package broadcast

import "sync"

// DefaultMaxPending is the maximum number of queued snapshots retained for a
// subscriber that is not consuming them. A subscription that exceeds this
// limit is closed so an SSE client can reconnect to an authoritative snapshot.
const DefaultMaxPending = 256

// Broker publishes every value to every subscription that is open when the
// value is published. A consumer never blocks publishers or other consumers.
type Broker[T any] struct {
	mu          sync.Mutex
	subscribers map[*Subscription[T]]struct{}
	maxPending  int
}

// Subscription is an ordered stream of values from a Broker.
type Subscription[T any] struct {
	broker *Broker[T]

	mu         sync.Mutex
	queue      []T
	head       int
	maxPending int
	closed     bool
	wake       chan struct{}
	done       chan struct{}
	events     chan T
}

// NewBroker creates a broker with a bounded backlog for each subscriber.
// Values at or below zero select DefaultMaxPending.
func NewBroker[T any](maxPending int) *Broker[T] {
	if maxPending <= 0 {
		maxPending = DefaultMaxPending
	}
	return &Broker[T]{
		subscribers: make(map[*Subscription[T]]struct{}),
		maxPending:  maxPending,
	}
}

// Subscribe registers a new subscription. Call Close when it is no longer
// needed.
func (b *Broker[T]) Subscribe() *Subscription[T] {
	subscription := &Subscription[T]{
		broker:     b,
		maxPending: b.maxPending,
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		events:     make(chan T),
	}

	b.mu.Lock()
	if b.subscribers == nil {
		b.subscribers = make(map[*Subscription[T]]struct{})
	}
	if subscription.maxPending <= 0 {
		subscription.maxPending = DefaultMaxPending
	}
	b.subscribers[subscription] = struct{}{}
	b.mu.Unlock()

	go subscription.run()
	return subscription
}

// Publish queues value for every current subscriber. Values are delivered in
// publish order. A subscriber whose bounded queue overflows is disconnected;
// it cannot delay this publisher or any other subscriber.
func (b *Broker[T]) Publish(value T) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for subscription := range b.subscribers {
		if !subscription.enqueue(value) {
			delete(b.subscribers, subscription)
		}
	}
}

// Events returns the subscription's ordered event channel.
func (s *Subscription[T]) Events() <-chan T {
	return s.events
}

// Close removes the subscription and releases its delivery goroutine. It is
// safe to call after an automatic slow-consumer disconnect.
func (s *Subscription[T]) Close() {
	s.broker.mu.Lock()
	delete(s.broker.subscribers, s)
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
	s.broker.mu.Unlock()
}

func (s *Subscription[T]) enqueue(value T) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if len(s.queue)-s.head >= s.maxPending {
		s.queue = nil
		s.head = 0
		s.closeLocked()
		return false
	}
	s.queue = append(s.queue, value)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *Subscription[T]) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
}

func (s *Subscription[T]) run() {
	defer close(s.events)
	for {
		value, ok := s.next()
		if ok {
			select {
			case s.events <- value:
			case <-s.done:
				return
			}
			continue
		}

		select {
		case <-s.wake:
		case <-s.done:
			return
		}
	}
}

func (s *Subscription[T]) next() (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head >= len(s.queue) {
		var zero T
		return zero, false
	}

	value := s.queue[s.head]
	s.head++
	if s.head == len(s.queue) {
		s.queue = nil
		s.head = 0
	}
	return value, true
}
