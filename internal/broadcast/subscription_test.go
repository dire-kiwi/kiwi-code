package broadcast

import (
	"testing"
	"time"
)

func TestBrokerPreservesOrderForEverySubscriber(t *testing.T) {
	broker := NewBroker[int](512)
	first := broker.Subscribe()
	defer first.Close()
	second := broker.Subscribe()
	defer second.Close()

	for value := 0; value < 250; value++ {
		broker.Publish(value)
	}

	for subscriberIndex, subscription := range []*Subscription[int]{first, second} {
		for want := 0; want < 250; want++ {
			select {
			case got, ok := <-subscription.Events():
				if !ok {
					t.Fatalf("subscriber %d closed before event %d", subscriberIndex, want)
				}
				if got != want {
					t.Fatalf("subscriber %d event %d = %d, want %d", subscriberIndex, want, got, want)
				}
			case <-time.After(time.Second):
				t.Fatalf("subscriber %d timed out at event %d", subscriberIndex, want)
			}
		}
	}
}

func TestLatestSubscriptionCoalescesWithoutDisconnecting(t *testing.T) {
	broker := NewBroker[int](1)
	subscription := broker.SubscribeLatest()
	defer subscription.Close()

	for value := 0; value < 10_000; value++ {
		broker.Publish(value)
	}

	broker.mu.Lock()
	_, stillSubscribed := broker.subscribers[subscription]
	broker.mu.Unlock()
	if !stillSubscribed {
		t.Fatal("latest subscription disconnected under a snapshot burst")
	}

	deadline := time.After(time.Second)
	for {
		select {
		case value, open := <-subscription.Events():
			if !open {
				t.Fatal("latest subscription closed under a snapshot burst")
			}
			if value == 9_999 {
				return
			}
		case <-deadline:
			t.Fatal("latest subscription did not converge to the newest value")
		}
	}
}

func TestSlowSubscriberDisconnectsWithoutBlockingOthers(t *testing.T) {
	broker := NewBroker[int](4)
	slow := broker.Subscribe()
	defer slow.Close()
	fast := broker.Subscribe()
	defer fast.Close()

	fastValues := make(chan int)
	go func() {
		for value := range fast.Events() {
			fastValues <- value
		}
	}()

	for value := 0; value < 20; value++ {
		broker.Publish(value)
		select {
		case got := <-fastValues:
			if got != value {
				t.Fatalf("fast event %d = %d", value, got)
			}
		case <-time.After(time.Second):
			t.Fatal("publishing stalled behind the slow subscriber")
		}
	}

	select {
	case _, ok := <-slow.Events():
		if ok {
			// The delivery goroutine may already have handed out one pending value;
			// the next read must observe the overflow disconnect.
			select {
			case _, ok = <-slow.Events():
				if ok {
					t.Fatal("slow subscription remained open after overflow")
				}
			case <-time.After(time.Second):
				t.Fatal("slow subscription did not close after overflow")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscription did not close after overflow")
	}
}

func TestCloseIsIdempotentAfterOverflow(t *testing.T) {
	broker := NewBroker[int](1)
	subscription := broker.Subscribe()
	broker.Publish(1)
	broker.Publish(2)
	broker.Publish(3)
	subscription.Close()
	subscription.Close()

	select {
	case _, ok := <-subscription.Events():
		if ok {
			t.Fatal("events remained open after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("events did not close")
	}
}
