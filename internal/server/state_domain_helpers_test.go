package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func TestSidebarProcessWebServerCacheRefreshesOnlyDirtyThreads(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := item.Threads[0]
	second, err := store.AddThread(item.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	logPath := filepath.Join(directory, "tmux-calls")
	tmuxPath := filepath.Join(directory, "tmux")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$KIWI_CODE_TMUX_CALL_LOG\"\nexit 1\n"
	if err := os.WriteFile(tmuxPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIWI_CODE_TMUX_CALL_LOG", logPath)
	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "kcv-process-cache")
	terminal.tmuxPath = tmuxPath
	application := &Server{projects: store, terminal: terminal}

	if got := application.processWebServerCache.snapshotContext(context.Background(), application, true); len(got) != 0 {
		t.Fatalf("initial process web servers = %#v", got)
	}
	calls := readProcessCacheCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("initial tmux calls = %#v, want one per thread", calls)
	}

	application.processWebServerCache.markDirty(threadStatusKey{projectID: item.ID, threadID: first.ID})
	terminal.activeSetups.Add(1)
	refreshed := make(chan struct{})
	go func() {
		_ = application.processWebServerCache.snapshotContext(context.Background(), application, false)
		close(refreshed)
	}()
	time.Sleep(30 * time.Millisecond)
	if callsWhileInteractive := readProcessCacheCalls(t, logPath); len(callsWhileInteractive) != 2 {
		t.Fatalf("process projection competed with terminal setup: %#v", callsWhileInteractive)
	}
	terminal.activeSetups.Add(-1)
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("process projection did not resume after terminal setup")
	}
	calls = readProcessCacheCalls(t, logPath)
	if len(calls) != 3 {
		t.Fatalf("targeted tmux calls = %#v, want one additional call", calls)
	}
	if !strings.Contains(calls[2], tmuxSessionName(item.ID, first.ID, "process")) {
		t.Fatalf("targeted call = %q, want first thread", calls[2])
	}

	application.processWebServerCache.markDirty(threadStatusKey{projectID: item.ID, threadID: first.ID})
	application.processWebServerCache.markDirty(threadStatusKey{projectID: item.ID, threadID: second.ID})
	_ = application.processWebServerCache.snapshotContext(context.Background(), application, false)
	calls = readProcessCacheCalls(t, logPath)
	if len(calls) != 5 {
		t.Fatalf("coalesced dirty tmux calls = %#v, want two additional calls", calls)
	}
}

func readProcessCacheCalls(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.FieldsFunc(string(contents), func(character rune) bool {
		return character == '\n' || character == '\r'
	})
}
