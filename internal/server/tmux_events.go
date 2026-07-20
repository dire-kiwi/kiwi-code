package server

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	tmuxControlReconnectDelay    = 250 * time.Millisecond
	tmuxControlMaxReconnectDelay = 5 * time.Second
	tmuxControlStableDuration    = 5 * time.Second
)

// tmuxSessionWatch is shared by every browser subscribed to the same thread.
// It is a real tmux client, but no-output and ignore-size keep it from
// consuming terminal bytes or changing pane dimensions.
type tmuxSessionWatch struct {
	projectID   string
	threadID    string
	sessionName string
	refs        int
	wake        chan struct{}
	stop        chan struct{}
	done        chan struct{}
}

// watchThreadTmux starts control-mode clients for the thread's two canonical
// sessions. Missing sessions do not get polled: their watcher sleeps until a
// Dire Mux session creation or a %sessions-changed notification wakes it.
func (h *terminalHandler) watchThreadTmux(projectID, threadID string) func() {
	return h.watchThreadTmuxSessions(projectID, threadID, []string{
		tmuxSessionName(projectID, threadID, "terminal"),
		tmuxSessionName(projectID, threadID, "process"),
	})
}

func (h *terminalHandler) watchThreadProcesses(projectID, threadID string) func() {
	return h.watchThreadTmuxSessions(projectID, threadID, []string{
		tmuxSessionName(projectID, threadID, "process"),
	})
}

func (h *terminalHandler) watchThreadTmuxSessions(projectID, threadID string, sessionNames []string) func() {
	if h == nil || h.tmuxPath == "" {
		return func() {}
	}

	created := make([]*tmuxSessionWatch, 0, len(sessionNames))
	h.tmuxWatchMu.Lock()
	if h.tmuxWatches == nil {
		h.tmuxWatches = make(map[string]*tmuxSessionWatch)
	}
	for _, sessionName := range sessionNames {
		watch := h.tmuxWatches[sessionName]
		if watch == nil {
			watch = &tmuxSessionWatch{
				projectID:   projectID,
				threadID:    threadID,
				sessionName: sessionName,
				wake:        make(chan struct{}, 1),
				stop:        make(chan struct{}),
				done:        make(chan struct{}),
			}
			h.tmuxWatches[sessionName] = watch
			created = append(created, watch)
		}
		watch.refs++
	}
	h.tmuxWatchMu.Unlock()
	for _, watch := range created {
		go h.runTmuxSessionWatch(watch)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			h.tmuxWatchMu.Lock()
			for _, sessionName := range sessionNames {
				watch := h.tmuxWatches[sessionName]
				if watch == nil {
					continue
				}
				watch.refs--
				if watch.refs == 0 {
					delete(h.tmuxWatches, sessionName)
					close(watch.stop)
				}
			}
			h.tmuxWatchMu.Unlock()
		})
	}
}

// wakeThreadTmuxWatchers is called when Dire Mux may have created one of the
// sessions. An attached watcher simply consumes this signal; a watcher waiting
// for a missing session attempts its control-mode attachment immediately.
func (h *terminalHandler) wakeThreadTmuxWatchers(projectID, threadID string) {
	h.tmuxWatchMu.Lock()
	defer h.tmuxWatchMu.Unlock()
	for _, sessionName := range []string{
		tmuxSessionName(projectID, threadID, "terminal"),
		tmuxSessionName(projectID, threadID, "process"),
	} {
		if watch := h.tmuxWatches[sessionName]; watch != nil {
			wakeTmuxSessionWatch(watch)
		}
	}
}

func (h *terminalHandler) wakeAllTmuxWatchers() {
	h.tmuxWatchMu.Lock()
	defer h.tmuxWatchMu.Unlock()
	for _, watch := range h.tmuxWatches {
		wakeTmuxSessionWatch(watch)
	}
}

func wakeTmuxSessionWatch(watch *tmuxSessionWatch) {
	select {
	case watch.wake <- struct{}{}:
	default:
	}
}

func (h *terminalHandler) runTmuxSessionWatch(watch *tmuxSessionWatch) {
	defer close(watch.done)
	reconnectDelay := tmuxControlReconnectDelay
	for {
		if tmuxWatchStopped(watch) {
			return
		}
		exists, err := h.tmuxSessionExists(watch.sessionName)
		if err != nil {
			if !waitForTmuxWatch(watch, reconnectDelay) {
				return
			}
			reconnectDelay = nextTmuxControlReconnectDelay(reconnectDelay)
			continue
		}
		if !exists {
			reconnectDelay = tmuxControlReconnectDelay
			if !waitForTmuxWatch(watch, 0) {
				return
			}
			continue
		}

		started := time.Now()
		if h.runTmuxControlClient(watch) {
			return
		}
		if time.Since(started) >= tmuxControlStableDuration {
			reconnectDelay = tmuxControlReconnectDelay
		}
		// A control client exiting is itself authoritative evidence that the
		// attached session may have disappeared. Refresh subscribers, then
		// reconnect if the session still exists.
		h.publishThreadStatusChanged(watch.projectID, watch.threadID)
		if !waitForTmuxWatch(watch, reconnectDelay) {
			return
		}
		reconnectDelay = nextTmuxControlReconnectDelay(reconnectDelay)
	}
}

func nextTmuxControlReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > tmuxControlMaxReconnectDelay {
		return tmuxControlMaxReconnectDelay
	}
	return next
}

// runTmuxControlClient returns true when the watch was deliberately stopped.
func (h *terminalHandler) runTmuxControlClient(watch *tmuxSessionWatch) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := h.tmuxCommandContext(
		ctx,
		"-C",
		"attach-session",
		"-E",
		"-f", "no-output,ignore-size",
		"-t", exactTmuxSessionTarget(watch.sessionName),
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return false
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return false
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return false
	}

	// Native notifications cover window lifecycle and selection. The format
	// subscription adds pane_current_command changes, which have no dedicated
	// control notification. tmux evaluates subscriptions internally and emits
	// only changes (at most once per second).
	_, _ = io.WriteString(stdin, "refresh-client -B 'dire-mux-status:%*:#{pane_current_command}|#{window_name}|#{window_active}|#{@dire-mux-process-id}|#{@dire-mux-web-servers}'\n")

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if isTmuxThreadStatusNotification(line) && !tmuxWatchStopped(watch) {
				h.publishThreadStatusChanged(watch.projectID, watch.threadID)
			}
			if strings.HasPrefix(line, "%sessions-changed") {
				h.wakeAllTmuxWatchers()
			}
		}
		_ = command.Wait()
		close(done)
	}()

	for {
		select {
		case <-watch.stop:
			cancel()
			_ = stdin.Close()
			<-done
			return true
		case <-watch.wake:
			// The control client is already attached; consume creation signals
			// so they cannot cause a needless reconnect later.
		case <-done:
			_ = stdin.Close()
			return false
		}
	}
}

func isTmuxThreadStatusNotification(line string) bool {
	name, _, _ := strings.Cut(line, " ")
	switch name {
	case "%window-add",
		"%window-close",
		"%window-renamed",
		"%window-pane-changed",
		"%session-window-changed",
		"%subscription-changed":
		return true
	default:
		return false
	}
}

func waitForTmuxWatch(watch *tmuxSessionWatch, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-watch.stop:
			return false
		case <-watch.wake:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-watch.stop:
		return false
	case <-watch.wake:
		return true
	case <-timer.C:
		return true
	}
}

func tmuxWatchStopped(watch *tmuxSessionWatch) bool {
	select {
	case <-watch.stop:
		return true
	default:
		return false
	}
}

func (h *terminalHandler) publishThreadStatusChanged(projectID, threadID string) {
	if h.threadStatusChanged != nil {
		h.threadStatusChanged(projectID, threadID)
	}
}

func (h *terminalHandler) tmuxCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, h.tmuxPath, h.tmuxCommandArguments(args...)...)
	command.Env = tmuxEnvironment()
	return command
}
