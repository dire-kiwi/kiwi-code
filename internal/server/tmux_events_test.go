package server

import (
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
		{line: "%subscription-changed dire-mux-status $1 @2 0 %3 : node", want: true},
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

func TestTmuxControlWatchIsSharedAndUsesCanonicalSessions(t *testing.T) {
	directory := t.TempDir()
	argumentsPath := filepath.Join(directory, "arguments")
	inputPath := filepath.Join(directory, "input")
	t.Setenv("TMUX_WATCH_ARGS_FILE", argumentsPath)
	t.Setenv("TMUX_WATCH_INPUT_FILE", inputPath)

	fakeTmux := filepath.Join(directory, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_WATCH_ARGS_FILE"
case "$*" in
  *"has-session"*) exit 0 ;;
  *"-C attach-session"*)
    if IFS= read -r line; then
      printf '%s\n' "$line" >> "$TMUX_WATCH_INPUT_FILE"
    fi
    printf '%%window-add @1\n'
    printf '%%subscription-changed dire-mux-status $1 @1 0 %%1 : node\n'
    while IFS= read -r line; do :; done
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(fakeTmux, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	updates := make(chan threadStatusKey, 8)
	handler := &terminalHandler{
		tmuxPath:   fakeTmux,
		tmuxSocket: "watch-test",
		threadStatusChanged: func(projectID, threadID string) {
			updates <- threadStatusKey{projectID: projectID, threadID: threadID}
		},
	}

	stopFirst := handler.watchThreadTmux("project", "thread")
	select {
	case update := <-updates:
		if update != (threadStatusKey{projectID: "project", threadID: "thread"}) {
			t.Fatalf("control update = %#v", update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tmux control notification was not published")
	}

	waitForTmuxControlSubscriptions(t, inputPath, 2)
	stopSecond := handler.watchThreadTmux("project", "thread")
	terminalSession := tmuxSessionName("project", "thread", "terminal")
	toolsSession := tmuxSessionName("project", "thread", "process")
	handler.tmuxWatchMu.Lock()
	if len(handler.tmuxWatches) != 2 {
		handler.tmuxWatchMu.Unlock()
		t.Fatalf("tmux watches = %d, want 2", len(handler.tmuxWatches))
	}
	terminalWatch := handler.tmuxWatches[terminalSession]
	toolsWatch := handler.tmuxWatches[toolsSession]
	if terminalWatch == nil || toolsWatch == nil || terminalWatch.refs != 2 || toolsWatch.refs != 2 {
		handler.tmuxWatchMu.Unlock()
		t.Fatalf("shared tmux watches = terminal %#v, tools %#v", terminalWatch, toolsWatch)
	}
	handler.tmuxWatchMu.Unlock()

	stopFirst()
	handler.tmuxWatchMu.Lock()
	if terminalWatch.refs != 1 || toolsWatch.refs != 1 {
		handler.tmuxWatchMu.Unlock()
		t.Fatalf("tmux watch refs after first unsubscribe = %d, %d", terminalWatch.refs, toolsWatch.refs)
	}
	handler.tmuxWatchMu.Unlock()
	stopSecond()

	for _, watch := range []*tmuxSessionWatch{terminalWatch, toolsWatch} {
		select {
		case <-watch.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("tmux control watch %q did not stop", watch.sessionName)
		}
	}

	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	argumentText := string(arguments)
	for _, sessionName := range []string{terminalSession, toolsSession} {
		want := "-L watch-test -C attach-session -E -f no-output,ignore-size -t " + exactTmuxSessionTarget(sessionName)
		if !strings.Contains(argumentText, want) {
			t.Fatalf("control attachment for %q missing from:\n%s", sessionName, argumentText)
		}
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(input), "refresh-client -B 'dire-mux-status:%*:"); count != 2 {
		t.Fatalf("format subscriptions = %d, want 2; input:\n%s", count, input)
	}
}

func waitForTmuxControlSubscriptions(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		input, err := os.ReadFile(path)
		if err == nil && strings.Count(string(input), "refresh-client -B 'dire-mux-status:%*:") >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	input, _ := os.ReadFile(path)
	t.Fatalf("format subscriptions did not reach %d; input:\n%s", want, input)
}
