package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	tmuxSessionInactivityLimit = 24 * time.Hour
	sessionClosureLogFileName  = "tmux-session-closures.json"
	maxSessionClosureEvents    = 500
)

type tmuxSessionActivity struct {
	Name           string
	Attached       bool
	CreatedAt      time.Time
	ActivityAt     time.Time
	LastAttachedAt time.Time
	RecordedUseAt  time.Time
	SourceSession  string
}

type inactiveThreadSessions struct {
	SessionNames   []string
	LastActivityAt time.Time
	Attached       bool
}

type sessionClosureEvent struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"projectId"`
	ProjectName    string    `json:"projectName"`
	ThreadID       string    `json:"threadId"`
	ThreadTitle    string    `json:"threadTitle"`
	SessionNames   []string  `json:"sessionNames"`
	LastActivityAt time.Time `json:"lastActivityAt"`
	ClosedAt       time.Time `json:"closedAt"`
	Reason         string    `json:"reason"`
}

type sessionClosureOverview struct {
	GeneratedAt     time.Time             `json:"generatedAt"`
	InactivityHours int                   `json:"inactivityHours"`
	Events          []sessionClosureEvent `json:"events"`
}

type persistedSessionClosureLog struct {
	Version int                   `json:"version"`
	Events  []sessionClosureEvent `json:"events"`
}

type sessionClosureLog struct {
	mu       sync.Mutex
	path     string
	lockPath string
}

func newSessionClosureLog(dataDirectory string) (*sessionClosureLog, error) {
	log := &sessionClosureLog{
		path:     filepath.Join(dataDirectory, sessionClosureLogFileName),
		lockPath: filepath.Join(dataDirectory, sessionClosureLogFileName+".lock"),
	}
	if _, err := log.list(); err != nil {
		return nil, err
	}
	return log, nil
}

func (l *sessionClosureLog) list() ([]sessionClosureEvent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlockSessionClosureLog(file)
	return l.read()
}

func (l *sessionClosureLog) append(event sessionClosureEvent) error {
	if !validSessionClosureEvent(event) {
		return errors.New("invalid tmux session closure event")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.lock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlockSessionClosureLog(file)

	events, err := l.read()
	if err != nil {
		return err
	}
	events = append(events, event)
	if len(events) > maxSessionClosureEvents {
		events = events[len(events)-maxSessionClosureEvents:]
	}
	contents, err := json.MarshalIndent(persistedSessionClosureLog{Version: 1, Events: events}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tmux session closure log: %w", err)
	}
	contents = append(contents, '\n')
	if err := writeFileAtomically(l.path, contents, serverAtomicFileOptions{Mode: 0o600, SyncFile: true}); err != nil {
		return fmt.Errorf("save tmux session closure log: %w", err)
	}
	return nil
}

func (l *sessionClosureLog) lock(operation int) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return nil, fmt.Errorf("create tmux session closure log directory: %w", err)
	}
	file, err := os.OpenFile(l.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open tmux session closure log lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), operation)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock tmux session closure log: %w", err)
	}
	return file, nil
}

func unlockSessionClosureLog(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (l *sessionClosureLog) read() ([]sessionClosureEvent, error) {
	contents, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return []sessionClosureEvent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tmux session closure log: %w", err)
	}
	var persisted persistedSessionClosureLog
	if err := json.Unmarshal(contents, &persisted); err != nil {
		return nil, fmt.Errorf("decode tmux session closure log: %w", err)
	}
	if persisted.Version != 1 || persisted.Events == nil {
		return nil, errors.New("decode tmux session closure log: unsupported format")
	}
	for _, event := range persisted.Events {
		if !validSessionClosureEvent(event) {
			return nil, errors.New("decode tmux session closure log: invalid event")
		}
	}
	return append([]sessionClosureEvent(nil), persisted.Events...), nil
}

func validSessionClosureEvent(event sessionClosureEvent) bool {
	return event.ID != "" && event.ProjectID != "" && event.ThreadID != "" &&
		event.ProjectName != "" && event.ThreadTitle != "" && len(event.SessionNames) > 0 &&
		!event.LastActivityAt.IsZero() && !event.ClosedAt.IsZero() && event.Reason == "inactivity"
}

func newSessionClosureEvent(item project.Project, thread project.Thread, sessions inactiveThreadSessions, closedAt time.Time) (sessionClosureEvent, error) {
	var idBytes [12]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return sessionClosureEvent{}, fmt.Errorf("create tmux session closure event id: %w", err)
	}
	return sessionClosureEvent{
		ID:             hex.EncodeToString(idBytes[:]),
		ProjectID:      item.ID,
		ProjectName:    item.Name,
		ThreadID:       thread.ID,
		ThreadTitle:    thread.Title,
		SessionNames:   append([]string(nil), sessions.SessionNames...),
		LastActivityAt: sessions.LastActivityAt.UTC(),
		ClosedAt:       closedAt.UTC(),
		Reason:         "inactivity",
	}, nil
}

func (s *Server) touchThreadTmuxActivity(w http.ResponseWriter, r *http.Request) {
	item, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if s.terminal.tmuxPath == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.terminal.markThreadTmuxSessionsUsed(item, thread, time.Now()); err != nil {
		if errors.Is(err, errTerminalStopping) {
			writeError(w, http.StatusConflict, "The thread's tmux sessions are stopping.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not record thread activity.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSessionClosureLog(w http.ResponseWriter, _ *http.Request) {
	events, err := s.sessionClosures.list()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the tmux session closure log.")
		return
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].ClosedAt.After(events[j].ClosedAt) })
	writeJSON(w, http.StatusOK, sessionClosureOverview{
		GeneratedAt:     time.Now().UTC(),
		InactivityHours: int(tmuxSessionInactivityLimit / time.Hour),
		Events:          events,
	})
}

func (s *Server) closeInactiveTmuxSessions(now time.Time) error {
	if s.terminal == nil || s.terminal.tmuxPath == "" {
		return nil
	}
	activities, err := s.terminal.tmuxSessionActivities()
	if err != nil {
		return err
	}
	working := make(map[terminalThreadKey]bool)
	for _, activity := range s.piActivity.list(now) {
		if activity.State == piActivityWorking {
			working[terminalThreadKey{ProjectID: activity.ProjectID, ThreadID: activity.ThreadID}] = true
		}
	}

	var closeErrors []error
	for _, item := range s.projects.List() {
		for _, thread := range item.Threads {
			key := terminalThreadKey{ProjectID: item.ID, ThreadID: thread.ID}
			if working[key] {
				continue
			}
			sessions := inactiveSessionsForThread(item, thread, activities)
			if len(sessions.SessionNames) == 0 || sessions.Attached || sessions.LastActivityAt.After(now.Add(-tmuxSessionInactivityLimit)) {
				continue
			}
			closed, closeErr := s.terminal.closeInactiveThreadSessions(item, thread, now, tmuxSessionInactivityLimit)
			if len(closed.SessionNames) > 0 {
				event, eventErr := newSessionClosureEvent(item, thread, closed, now)
				if eventErr == nil {
					eventErr = s.sessionClosures.append(event)
				}
				if eventErr != nil {
					closeErrors = append(closeErrors, eventErr)
				} else {
					logSessionClosure(event)
				}
			}
			if closeErr != nil && !errors.Is(closeErr, errTerminalStopping) {
				closeErrors = append(closeErrors, fmt.Errorf("close inactive tmux sessions project=%q thread=%q: %w", item.ID, thread.ID, closeErr))
			}
		}
	}
	return errors.Join(closeErrors...)
}

func logSessionClosure(event sessionClosureEvent) {
	log.Printf("automatic cleanup closed inactive tmux sessions: project=%q thread=%q sessions=%q last_activity=%s",
		event.ProjectID, event.ThreadID, strings.Join(event.SessionNames, ","), event.LastActivityAt.Format(time.RFC3339))
}

func (h *terminalHandler) closeInactiveThreadSessions(item project.Project, thread project.Thread, now time.Time, limit time.Duration) (inactiveThreadSessions, error) {
	// A temporary durable stop fence prevents another backend from creating or
	// mutating this thread's sessions between the final activity check and the
	// kill. Roll the fence back afterward so opening the retained thread can
	// create fresh sessions. Never adopt a marker owned by a real deletion.
	lease, err := h.stopThreadSessions(item, thread.ID)
	if err != nil {
		return inactiveThreadSessions{}, err
	}
	if lease.Adopted() {
		return inactiveThreadSessions{}, errors.Join(errTerminalStopping, h.retainStopThread(item.ID, thread.ID, lease))
	}

	var closed inactiveThreadSessions
	operationErr := func() (err error) {
		h.sessionMu.Lock()
		defer h.sessionMu.Unlock()
		mutation, err := h.lockTerminalMutationLocked(item.ID, thread.ID)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, mutation.Release()) }()

		activities, err := h.tmuxSessionActivities()
		if err != nil {
			return err
		}
		sessions := inactiveSessionsForThread(item, thread, activities)
		if len(sessions.SessionNames) == 0 || sessions.Attached || sessions.LastActivityAt.After(now.Add(-limit)) {
			return nil
		}
		if err := h.stopNamedTmuxSessionsAndViews(threadTmuxSessionNameSet(item, thread.ID)); err != nil {
			// Record sessions that did close even if another session failed.
			closed = h.closedSessionsFrom(item, thread, sessions)
			return err
		}
		closed = sessions
		return nil
	}()
	rollbackErr := h.cancelStopThread(item.ID, thread.ID, lease)
	if len(closed.SessionNames) > 0 {
		h.wakeThreadTmuxWatchers(item.ID, thread.ID)
		h.notifyThreadStatusChanged(item.ID, thread.ID)
	}
	return closed, errors.Join(operationErr, rollbackErr)
}

func (h *terminalHandler) closedSessionsFrom(item project.Project, thread project.Thread, before inactiveThreadSessions) inactiveThreadSessions {
	closed := inactiveThreadSessions{LastActivityAt: before.LastActivityAt}
	for _, name := range before.SessionNames {
		exists, err := h.tmuxExactSessionExists(name)
		if err == nil && !exists {
			closed.SessionNames = append(closed.SessionNames, name)
		}
	}
	return closed
}

func (h *terminalHandler) markThreadTmuxSessionsUsed(item project.Project, thread project.Thread, usedAt time.Time) (err error) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	mutation, err := h.lockTerminalMutationLocked(item.ID, thread.ID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, mutation.Release()) }()
	if err := h.ensureTerminalThreadActiveLocked(item.ID, thread.ID); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, h.finishTerminalThreadMutationLocked(item, thread)) }()

	for sessionName := range threadTmuxSessionNameSet(item, thread.ID) {
		exists, err := h.tmuxExactSessionExists(sessionName)
		if err != nil {
			return err
		}
		if exists {
			if err := h.markTmuxSessionUsed(sessionName, usedAt); err != nil {
				if stillExists, checkErr := h.tmuxExactSessionExists(sessionName); checkErr == nil && !stillExists {
					continue
				} else {
					return errors.Join(err, checkErr)
				}
			}
		}
	}
	return nil
}

func (h *terminalHandler) markTmuxSessionUsed(sessionName string, usedAt time.Time) error {
	if strings.TrimSpace(sessionName) == "" {
		return errors.New("tmux session name is required")
	}
	output, err := h.tmuxCommand(
		"set-option", "-t", exactTmuxCurrentWindowTarget(sessionName),
		"@kiwi-code-last-used", strconv.FormatInt(usedAt.UTC().Unix(), 10),
	).CombinedOutput()
	if err != nil {
		return tmuxCommandError("record tmux session use", output, err)
	}
	return nil
}

func (h *terminalHandler) tmuxSessionActivities() ([]tmuxSessionActivity, error) {
	output, err := h.tmuxCommand(
		"list-sessions", "-F",
		"#{session_name}\t#{?session_attached,1,0}\t#{session_created}\t#{session_activity}\t#{session_last_attached}\t#{@kiwi-code-last-used}\t#{@kiwi-code-source-session}",
	).CombinedOutput()
	if err != nil {
		if isMissingTmuxServer(output, err) {
			return []tmuxSessionActivity{}, nil
		}
		return nil, tmuxCommandError("list tmux session activity", output, err)
	}
	return parseTmuxSessionActivities(output)
}

func parseTmuxSessionActivities(output []byte) ([]tmuxSessionActivity, error) {
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	activities := make([]tmuxSessionActivity, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 7)
		if len(parts) != 7 || strings.TrimSpace(parts[0]) == "" || (parts[1] != "0" && parts[1] != "1") {
			return nil, fmt.Errorf("parse tmux session activity: %q", line)
		}
		createdAt, err := parseTmuxUnixTime(parts[2], true)
		if err != nil {
			return nil, fmt.Errorf("parse tmux session creation time: %w", err)
		}
		activityAt, err := parseTmuxUnixTime(parts[3], false)
		if err != nil {
			return nil, fmt.Errorf("parse tmux session activity time: %w", err)
		}
		lastAttachedAt, err := parseTmuxUnixTime(parts[4], false)
		if err != nil {
			return nil, fmt.Errorf("parse tmux session attachment time: %w", err)
		}
		recordedUseAt, err := parseTmuxUnixTime(parts[5], false)
		if err != nil {
			return nil, fmt.Errorf("parse recorded tmux session use time: %w", err)
		}
		activities = append(activities, tmuxSessionActivity{
			Name:           strings.TrimSpace(parts[0]),
			Attached:       parts[1] == "1",
			CreatedAt:      createdAt,
			ActivityAt:     activityAt,
			LastAttachedAt: lastAttachedAt,
			RecordedUseAt:  recordedUseAt,
			SourceSession:  strings.TrimSpace(parts[6]),
		})
	}
	return activities, nil
}

func parseTmuxUnixTime(raw string, required bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		if required {
			return time.Time{}, errors.New("missing timestamp")
		}
		return time.Time{}, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, fmt.Errorf("invalid timestamp %q", raw)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func inactiveSessionsForThread(item project.Project, thread project.Thread, activities []tmuxSessionActivity) inactiveThreadSessions {
	names := threadTmuxSessionNameSet(item, thread.ID)
	result := inactiveThreadSessions{LastActivityAt: thread.CreatedAt.UTC()}
	if thread.LastPromptAt != nil && thread.LastPromptAt.After(result.LastActivityAt) {
		result.LastActivityAt = thread.LastPromptAt.UTC()
	}
	for _, activity := range activities {
		_, canonical := names[activity.Name]
		_, linkedView := names[activity.SourceSession]
		if !canonical && !linkedView {
			continue
		}
		if canonical {
			result.SessionNames = append(result.SessionNames, activity.Name)
		}
		result.Attached = result.Attached || activity.Attached
		for _, timestamp := range []time.Time{activity.CreatedAt, activity.ActivityAt, activity.LastAttachedAt, activity.RecordedUseAt} {
			if timestamp.After(result.LastActivityAt) {
				result.LastActivityAt = timestamp.UTC()
			}
		}
	}
	sort.Strings(result.SessionNames)
	return result
}
