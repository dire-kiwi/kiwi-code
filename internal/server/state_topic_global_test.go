package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestProcessWebServersTopicDoesNotWatchPublishedServers(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < 20; index++ {
		if _, err := store.AddThread(item.ID, "Idle "+strconv.Itoa(index), false); err != nil {
			t.Fatal(err)
		}
	}
	item, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeThread := item.Threads[0]
	activeSession := tmuxSessionName(item.ID, activeThread.ID, "process")

	directory := t.TempDir()
	t.Setenv("TMUX_ACTIVE_SESSION", activeSession)
	fakeTmux := filepath.Join(directory, "tmux")
	script := `#!/bin/sh
case "$*" in
  *"has-session"*)
    case "$*" in
      *"$TMUX_ACTIVE_SESSION"*) exit 0 ;;
      *) exit 1 ;;
    esac
    ;;
  *"list-windows"*)
    printf '1\t@2\tweb\tprocess\tprocess-1\tnode\t["http://127.0.0.1:5173"]\t421\n'
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(fakeTmux, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	socketName := fmt.Sprintf("kcv-global-%x", time.Now().UnixNano())
	if socketName == "" || socketName == tmuxSocketName {
		t.Fatalf("unsafe tmux socket name %q", socketName)
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, socketName)
	terminal.tmuxPath = fakeTmux
	application := &Server{
		projects:     store,
		terminal:     terminal,
		stateChanges: newStateChangeBroker(),
	}
	terminal.threadStatusChanged = application.notifyThreadStatusChanged

	ctx, cancel := context.WithCancel(context.Background())
	channel := newStateTestChannel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- application.openProcessWebServersTopic(ctx, channel)
	}()

	eventuallyStateTest(t, func() bool {
		terminal.tmuxWatchMu.Lock()
		defer terminal.tmuxWatchMu.Unlock()
		return len(terminal.tmuxWatches) == 0 && stateTestChannelHasSnapshot(channel)
	})

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("process web servers topic returned nil after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process web servers topic did not stop after cancellation")
	}
	eventuallyStateTest(t, func() bool {
		return stateTestTmuxWatchCount(terminal) == 0
	})
}
