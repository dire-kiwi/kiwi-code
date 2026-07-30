package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTmuxControlNotificationClassification(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{line: "%window-add @1", want: true},
		{line: "%window-close @1", want: true},
		{line: "%window-renamed @1 server", want: true},
		{line: "%window-pane-changed @1 %2", want: true},
		{line: "%session-window-changed $1 @2", want: true},
		{line: "%subscription-changed kiwi-code-status $1 @2 0 %3 : node", want: true},
		{line: "%sessions-changed", want: false},
		{line: "%output %3 ignored", want: false},
		{line: "%begin 1 2 0", want: false},
		{line: "", want: false},
	}
	for _, test := range tests {
		if got := isTmuxThreadStatusNotification(test.line); got != test.want {
			t.Errorf("isTmuxThreadStatusNotification(%q) = %t, want %t", test.line, got, test.want)
		}
	}
}

func TestServerCloseTerminatesTmuxClientsAndPreventsRecreation(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input")
	t.Setenv("TMUX_WATCH_INPUT_FILE", inputPath)

	fakeTmux := filepath.Join(directory, "tmux")
	script := `#!/bin/sh
case "$*" in
  *"has-session"*) exit 0 ;;
  *"-C attach-session"*)
    if IFS= read -r line; then
      printf '%s\n' "$line" >> "$TMUX_WATCH_INPUT_FILE"
    fi
    while IFS= read -r line; do :; done
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(fakeTmux, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	handler := &terminalHandler{tmuxPath: fakeTmux, tmuxSocket: "watch-stop-test"}
	cleanupContext, cancelCleanup := context.WithCancel(context.Background())
	handler.stopTmuxWatchesOnContext(cleanupContext)
	stopReference := handler.watchThreadTmux("project", "thread")
	waitForTmuxControlSubscriptions(t, inputPath, 2)

	handler.tmuxWatchMu.Lock()
	watches := make([]*tmuxSessionWatch, 0, len(handler.tmuxWatches))
	for _, watch := range handler.tmuxWatches {
		watches = append(watches, watch)
	}
	handler.tmuxWatchMu.Unlock()

	stopContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	application := &Server{terminal: handler}
	cancelCleanup()
	if err := application.Close(stopContext); err != nil {
		t.Fatal(err)
	}
	for _, watch := range watches {
		select {
		case <-watch.done:
		default:
			t.Fatalf("tmux watch %q was not stopped", watch.sessionName)
		}
	}
	handler.tmuxWatchMu.Lock()
	if !handler.tmuxWatchesStopped || len(handler.tmuxWatches) != 0 {
		handler.tmuxWatchMu.Unlock()
		t.Fatalf("stopped tmux watches = stopped %t, count %d",
			handler.tmuxWatchesStopped, len(handler.tmuxWatches))
	}
	handler.tmuxWatchMu.Unlock()

	// Outstanding state-channel cleanup remains safe after application close,
	// and a late resnapshot cannot recreate control clients.
	stopReference()
	stopLate := handler.watchThreadTmux("project", "thread")
	stopLate()
	handler.tmuxWatchMu.Lock()
	defer handler.tmuxWatchMu.Unlock()
	if len(handler.tmuxWatches) != 0 {
		t.Fatalf("late tmux watches = %d, want 0", len(handler.tmuxWatches))
	}
}

func waitForTmuxControlSubscriptions(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		input, err := os.ReadFile(path)
		if err == nil && strings.Count(string(input), "refresh-client -B 'kiwi-code-status:%*:") >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	input, _ := os.ReadFile(path)
	t.Fatalf("format subscriptions did not reach %d; input:\n%s", want, input)
}
