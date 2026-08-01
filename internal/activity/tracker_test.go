package activity

import (
	"fmt"
	"testing"
	"time"
)

func TestPromptOrderingStateExpiresAfterBoundedRetention(t *testing.T) {
	tracker := NewTracker(nil)
	now := time.Now().UTC()
	old := now.Add(-OrderRetention - time.Second)
	for index := 0; index < 100; index++ {
		tracker.UpdateAgentToken(
			"project",
			"thread",
			"claude",
			fmt.Sprintf("session-%d", index),
			fmt.Sprintf("prompt-%d", index),
			StateIdle,
			old,
		)
	}
	if got := len(tracker.promptOrder); got != 100 {
		t.Fatalf("prompt ordering entries = %d, want 100 before expiry", got)
	}
	tracker.List(now)
	if got := len(tracker.promptOrder); got != 0 {
		t.Fatalf("expired prompt ordering entries = %d, want 0", got)
	}
}
