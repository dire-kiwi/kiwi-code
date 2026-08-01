// Package tmuxpane holds the tmux-pane runtime pieces for coding agents.
// ExitStore is the durable exit-tombstone store: when an agent pane dies, a
// marker written here makes the exit sticky across backend restarts until an
// explicit restart clears it. The on-disk layout (base64url path components
// under coding-agent-exits/, flock'd sidecars, atomic JSON markers) is a
// persistence compatibility contract.
package tmuxpane

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/dire-kiwi/kiwi-code/internal/workspace"
)

// ExitMarker is the persisted record of a dead agent pane.
type ExitMarker struct {
	ProjectID string `json:"projectId"`
	ThreadID  string `json:"threadId"`
	Agent     string `json:"agent"`
	PaneID    string `json:"paneId,omitempty"`
	ServerPID string `json:"serverPid,omitempty"`
	Status    string `json:"status,omitempty"`
	Signal    string `json:"signal,omitempty"`
	ExitedAt  string `json:"exitedAt,omitempty"`
}

// ExitMarkerFromState builds a marker from an observed dead pane.
func ExitMarkerFromState(projectID, threadID, agent, paneID string, state workspace.PaneExitState) ExitMarker {
	return ExitMarker{
		ProjectID: projectID,
		ThreadID:  threadID,
		Agent:     agent,
		PaneID:    paneID,
		ServerPID: state.ServerPID,
		Status:    state.Status,
		Signal:    state.Signal,
		ExitedAt:  state.ExitedAt,
	}
}

// ExitStore reads and writes exit markers under one directory. A process
// mutex serializes in-process access; a per-marker flock sidecar covers
// overlapping backend processes.
type ExitStore struct {
	directory func() string
	mu        sync.Mutex
}

// NewExitStore builds a store over a directory resolver. The resolver runs
// per operation so callers whose data directory is configured late (tests)
// stay correct.
func NewExitStore(directory func() string) *ExitStore {
	return &ExitStore{directory: directory}
}

// MarkerPath is the marker file for one (project, thread, agent). Empty when
// no directory is available.
func (s *ExitStore) MarkerPath(projectID, threadID, agent string) string {
	root := s.directory()
	if root == "" {
		return ""
	}
	component := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(value))
	}
	return filepath.Join(root, component(projectID), component(threadID), component(agent)+".json")
}

// WithLock runs operation while holding the marker's in-process mutex and
// cross-process flock.
func (s *ExitStore) WithLock(projectID, threadID, agent string, operation func(path string) error) error {
	path := s.MarkerPath(projectID, threadID, agent)
	if path == "" {
		return errors.New("coding agent exit marker directory is unavailable")
	}
	directory := filepath.Dir(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create marker directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure marker directory: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open marker lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0o600); err != nil {
		return fmt.Errorf("secure marker lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock marker: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return operation(path)
}

// WriteMarker persists a marker atomically (write temp, fsync, rename).
func WriteMarker(path string, marker ExitMarker) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode marker: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".coding-agent-exit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary marker: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace marker: %w", err)
	}
	return nil
}

// ReadMarkerFile loads a marker and validates it against its path identity.
// found=false with a nil error means no marker exists.
func ReadMarkerFile(path, projectID, threadID, agent string) (ExitMarker, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ExitMarker{}, false, nil
	}
	if err != nil {
		return ExitMarker{}, false, err
	}
	var marker ExitMarker
	if err := json.Unmarshal(contents, &marker); err != nil {
		return ExitMarker{}, false, fmt.Errorf("decode marker: %w", err)
	}
	if marker.ProjectID != projectID || marker.ThreadID != threadID || marker.Agent != agent {
		return ExitMarker{}, false, errors.New("marker identity does not match its path")
	}
	return marker, true, nil
}

// SyncDirectory fsyncs a directory so a renamed marker is durable.
func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
