package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	terminalStopDirectoryName = "terminal-stops-v1"
	terminalStopMarkerVersion = 1
	terminalStopTempPrefix    = ".terminal-stop-marker-"

	terminalStopScopeProject terminalStopScope = "project"
	terminalStopScopeThread  terminalStopScope = "thread"
)

var (
	errTerminalStopLeaseClosed     = errors.New("terminal stop lease is closed")
	errTerminalStopLeaseOwnership  = errors.New("terminal stop marker is not owned by this lease")
	errTerminalStopMarkerMalformed = errors.New("terminal stop marker is malformed")
	errTerminalStopMarkerChanged   = errors.New("terminal stop marker changed while opening")
)

type terminalStopScope string

// terminalStopMarker is both the durable stop tombstone and the recovery
// recipe for a final cleanup sweep. SessionNames contains exact tmux session
// identities; callers must never reinterpret them as prefixes. ThreadIDs lets
// non-tmux thread-owned resources (such as browser sessions) retry cleanup.
type terminalStopMarker struct {
	Version      int               `json:"version"`
	Scope        terminalStopScope `json:"scope"`
	ProjectID    string            `json:"projectId"`
	ThreadID     string            `json:"threadId,omitempty"`
	Token        string            `json:"token"`
	SessionNames []string          `json:"sessionNames"`
	ThreadIDs    []string          `json:"threadIds,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	Committed    bool              `json:"committed,omitempty"`
}

// terminalStopManager coordinates terminal deletion between independent
// terminalHandler instances, including handlers in overlapping processes.
// Marker existence is authoritative; the flock only distinguishes an active
// deletion from an unlocked marker that a later DELETE may adopt and resume.
type terminalStopManager struct {
	root string
}

// terminalStopMarkerRef is derived exclusively from the marker's path. The
// marker contents are validated separately after its persistent sidecar lock
// has been acquired.
type terminalStopMarkerRef struct {
	Scope     terminalStopScope
	ProjectID string
	ThreadID  string
}

type terminalStopLease struct {
	mu       sync.Mutex
	manager  *terminalStopManager
	marker   terminalStopMarker
	path     string
	lockPath string
	file     *os.File
	adopted  bool
	closed   bool
}

func newTerminalStopManager(dataDirectory string) *terminalStopManager {
	return &terminalStopManager{
		root: filepath.Join(dataDirectory, terminalStopDirectoryName),
	}
}

func (m *terminalStopManager) beginThread(
	projectID string,
	threadID string,
	sessionNames []string,
) (*terminalStopLease, error) {
	marker, err := newTerminalStopMarker(terminalStopScopeThread, projectID, threadID, sessionNames)
	if err != nil {
		return nil, err
	}

	if _, found, inspectErr := m.readProject(projectID); inspectErr != nil {
		return nil, fmt.Errorf("inspect project terminal stop marker: %w", inspectErr)
	} else if found {
		return nil, fmt.Errorf("%w: project %q", errTerminalStopping, projectID)
	}

	lease, err := m.acquire(marker, m.threadPath(projectID, threadID))
	if err != nil {
		return nil, err
	}

	// A project stop can start after the first check but before the thread
	// marker becomes visible. Rechecking makes the two differently named marker
	// files behave as one ordered stop boundary.
	_, projectFound, inspectErr := m.readProject(projectID)
	if inspectErr == nil && !projectFound {
		return lease, nil
	}
	cleanupErr := lease.abandonAfterConflict()
	if inspectErr != nil {
		return nil, errors.Join(fmt.Errorf("recheck project terminal stop marker: %w", inspectErr), cleanupErr)
	}
	return nil, errors.Join(fmt.Errorf("%w: project %q", errTerminalStopping, projectID), cleanupErr)
}

func (m *terminalStopManager) beginProject(
	projectID string,
	threadIDs []string,
	sessionNames []string,
) (*terminalStopLease, error) {
	marker, err := newTerminalStopMarker(terminalStopScopeProject, projectID, "", sessionNames)
	if err != nil {
		return nil, err
	}
	threadIDs, err = normalizeTerminalStopThreadIDs(threadIDs)
	if err != nil {
		return nil, err
	}
	marker.ThreadIDs = threadIDs

	lease, err := m.acquire(marker, m.projectPath(projectID))
	if err != nil {
		return nil, err
	}

	// A current thread deletion that won its marker first owns the narrower
	// operation. Leave an adopted project marker in place for a later retry; a
	// marker created by this call is rolled back so it does not wedge the thread.
	for _, threadID := range threadIDs {
		_, found, inspectErr := m.readThread(projectID, threadID)
		if inspectErr == nil && !found {
			continue
		}
		cleanupErr := lease.abandonAfterConflict()
		if inspectErr != nil {
			return nil, errors.Join(
				fmt.Errorf("inspect thread %q terminal stop marker: %w", threadID, inspectErr),
				cleanupErr,
			)
		}
		return nil, errors.Join(
			fmt.Errorf("%w: thread %q", errTerminalStopping, threadID),
			cleanupErr,
		)
	}
	return lease, nil
}

// threadStopped checks both scopes. It returns stopped=true on malformed or
// unreadable marker state so callers cannot create terminal state when the
// durable deletion state is unknown.
func (m *terminalStopManager) threadStopped(projectID, threadID string) (bool, error) {
	if _, found, err := m.readProject(projectID); err != nil {
		return true, fmt.Errorf("inspect project terminal stop marker: %w", err)
	} else if found {
		return true, nil
	}
	if _, found, err := m.readThread(projectID, threadID); err != nil {
		return true, fmt.Errorf("inspect thread terminal stop marker: %w", err)
	} else if found {
		return true, nil
	}
	return false, nil
}

// projectStopped returns stopped=true on malformed or unreadable marker state.
func (m *terminalStopManager) projectStopped(projectID string) (bool, error) {
	_, found, err := m.readProject(projectID)
	if err != nil {
		return true, fmt.Errorf("inspect project terminal stop marker: %w", err)
	}
	return found, nil
}

func (m *terminalStopManager) readProject(projectID string) (terminalStopMarker, bool, error) {
	if err := validateTerminalStopIdentity(projectID, ""); err != nil {
		return terminalStopMarker{}, false, err
	}
	return readTerminalStopMarkerFile(
		m.projectPath(projectID),
		terminalStopScopeProject,
		projectID,
		"",
	)
}

func (m *terminalStopManager) readThread(projectID, threadID string) (terminalStopMarker, bool, error) {
	if err := validateTerminalStopIdentity(projectID, threadID); err != nil {
		return terminalStopMarker{}, false, err
	}
	return readTerminalStopMarkerFile(
		m.threadPath(projectID, threadID),
		terminalStopScopeThread,
		projectID,
		threadID,
	)
}

func (m *terminalStopManager) projectPath(projectID string) string {
	return filepath.Join(
		m.root,
		"projects",
		terminalStopPathComponent(projectID),
		"project.json",
	)
}

func (m *terminalStopManager) threadPath(projectID, threadID string) string {
	return filepath.Join(
		m.root,
		"projects",
		terminalStopPathComponent(projectID),
		"threads",
		terminalStopPathComponent(threadID)+".json",
	)
}

func (m *terminalStopManager) acquire(marker terminalStopMarker, path string) (*terminalStopLease, error) {
	if err := secureTerminalStopDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}

	lockPath := terminalStopLockPath(path)
	file, err := openTerminalStopLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open terminal stop marker lock: %w", err)
	}
	if err := lockTerminalStopFile(file); err != nil {
		_ = file.Close()
		if terminalStopLockBusy(err) {
			return nil, fmt.Errorf("%w: %s %q", errTerminalStopping, marker.Scope, marker.ProjectID)
		}
		return nil, fmt.Errorf("lock terminal stop marker: %w", err)
	}
	lease := &terminalStopLease{
		manager:  m,
		marker:   marker,
		path:     path,
		lockPath: lockPath,
		file:     file,
	}

	existing, found, err := readTerminalStopMarkerFile(path, marker.Scope, marker.ProjectID, marker.ThreadID)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("%w: read existing terminal stop marker: %v", errTerminalStopMarkerMalformed, err),
			lease.closePreservingLocked(nil),
		)
	}
	if found {
		lease.marker = existing
		lease.adopted = true
		return lease, nil
	}

	if err := writeTerminalStopMarkerAtomic(path, marker); err != nil {
		return nil, errors.Join(
			fmt.Errorf("create terminal stop marker: %w", err),
			lease.closePreservingLocked(nil),
		)
	}
	return lease, nil
}

// acquireExisting adopts one exact marker without applying normal
// project/thread ordering. Recovery needs this to resolve the crash state in
// which both scopes were durably created before either creator could back off.
// found distinguishes an absent marker from a malformed or actively locked one.
func (m *terminalStopManager) acquireExisting(ref terminalStopMarkerRef) (*terminalStopLease, bool, error) {
	path, err := m.markerPath(ref)
	if err != nil {
		return nil, false, err
	}
	present, err := terminalStopMarkerPathPresent(path)
	if err != nil {
		return nil, true, fmt.Errorf("%w: inspect terminal stop marker: %v", errTerminalStopMarkerMalformed, err)
	}
	if !present {
		return nil, false, nil
	}

	if err := secureTerminalStopDirectory(filepath.Dir(path)); err != nil {
		return nil, true, err
	}
	lockPath := terminalStopLockPath(path)
	file, err := openTerminalStopLockFile(lockPath)
	if err != nil {
		return nil, true, fmt.Errorf("open terminal stop marker lock: %w", err)
	}
	if err := lockTerminalStopFile(file); err != nil {
		_ = file.Close()
		if terminalStopLockBusy(err) {
			return nil, true, fmt.Errorf("%w: %s %q", errTerminalStopping, ref.Scope, ref.ProjectID)
		}
		return nil, true, fmt.Errorf("lock existing terminal stop marker: %w", err)
	}

	marker, found, readErr := readTerminalStopMarkerFile(path, ref.Scope, ref.ProjectID, ref.ThreadID)
	if readErr != nil {
		return nil, true, errors.Join(
			fmt.Errorf("%w: read existing terminal stop marker: %v", errTerminalStopMarkerMalformed, readErr),
			unlockAndCloseTerminalStopFile(file),
		)
	}
	if !found {
		return nil, false, unlockAndCloseTerminalStopFile(file)
	}
	return &terminalStopLease{
		manager:  m,
		marker:   marker,
		path:     path,
		lockPath: lockPath,
		file:     file,
		adopted:  true,
	}, true, nil
}

func (m *terminalStopManager) markerPath(ref terminalStopMarkerRef) (string, error) {
	if err := validateTerminalStopIdentity(ref.ProjectID, ref.ThreadID); err != nil {
		return "", err
	}
	switch ref.Scope {
	case terminalStopScopeProject:
		if ref.ThreadID != "" {
			return "", errors.New("project terminal stop marker ref cannot have a thread ID")
		}
		return m.projectPath(ref.ProjectID), nil
	case terminalStopScopeThread:
		if ref.ThreadID == "" {
			return "", errors.New("thread terminal stop marker ref requires a thread ID")
		}
		return m.threadPath(ref.ProjectID, ref.ThreadID), nil
	default:
		return "", errors.New("terminal stop marker ref has an invalid scope")
	}
}

// listMarkers discovers only the exact marker layout owned by this manager.
// It returns valid path-derived refs even when other entries are malformed so
// recovery can make progress while also surfacing every unsafe entry.
func (m *terminalStopManager) listMarkers() ([]terminalStopMarkerRef, error) {
	projectsRoot := filepath.Join(m.root, "projects")
	entries, err := readTerminalStopDirectory(projectsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect terminal stop projects directory: %v", errTerminalStopMarkerMalformed, err)
	}

	var refs []terminalStopMarkerRef
	var inspectErrors []error
	for _, entry := range entries {
		projectID, decodeErr := decodeTerminalStopPathComponent(entry.Name())
		if decodeErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(entry.Name(), decodeErr))
			continue
		}
		if entryErr := requireTerminalStopEntryMode(entry, true); entryErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(entry.Name(), entryErr))
			continue
		}

		projectDirectory := filepath.Join(projectsRoot, entry.Name())
		projectEntries, readErr := readTerminalStopDirectory(projectDirectory)
		if readErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(projectDirectory, readErr))
			continue
		}
		for _, projectEntry := range projectEntries {
			name := projectEntry.Name()
			switch {
			case name == "project.json":
				ref := terminalStopMarkerRef{Scope: terminalStopScopeProject, ProjectID: projectID}
				path := m.projectPath(projectID)
				if entryErr := requireTerminalStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedTerminalStopPath(path, entryErr))
					continue
				}
				refs = append(refs, ref)
				if _, found, markerErr := readTerminalStopMarkerFile(path, ref.Scope, ref.ProjectID, ref.ThreadID); markerErr != nil {
					inspectErrors = append(inspectErrors, malformedTerminalStopPath(path, markerErr))
				} else if !found {
					refs = refs[:len(refs)-1]
				}
			case name == "project.json.lock":
				if entryErr := requireTerminalStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(projectDirectory, name), entryErr))
				}
			case name == "threads":
				if entryErr := requireTerminalStopEntryMode(projectEntry, true); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(projectDirectory, name), entryErr))
					continue
				}
				threadRefs, threadErr := m.listThreadMarkers(projectID, filepath.Join(projectDirectory, name))
				refs = append(refs, threadRefs...)
				if threadErr != nil {
					inspectErrors = append(inspectErrors, threadErr)
				}
			case isTerminalStopTemporaryName(name):
				if entryErr := requireTerminalStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(projectDirectory, name), entryErr))
				}
			default:
				inspectErrors = append(inspectErrors, malformedTerminalStopPath(
					filepath.Join(projectDirectory, name),
					errors.New("unexpected terminal stop entry"),
				))
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ProjectID != refs[j].ProjectID {
			return refs[i].ProjectID < refs[j].ProjectID
		}
		if refs[i].Scope != refs[j].Scope {
			return refs[i].Scope == terminalStopScopeProject
		}
		return refs[i].ThreadID < refs[j].ThreadID
	})
	return refs, errors.Join(inspectErrors...)
}

func (m *terminalStopManager) listThreadMarkers(projectID, directory string) ([]terminalStopMarkerRef, error) {
	entries, err := readTerminalStopDirectory(directory)
	if err != nil {
		return nil, malformedTerminalStopPath(directory, err)
	}
	var refs []terminalStopMarkerRef
	var inspectErrors []error
	for _, entry := range entries {
		name := entry.Name()
		if isTerminalStopTemporaryName(name) {
			if entryErr := requireTerminalStopEntryMode(entry, false); entryErr != nil {
				inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(directory, name), entryErr))
			}
			continue
		}

		isLock := strings.HasSuffix(name, ".json.lock")
		suffix := ".json"
		if isLock {
			suffix = ".json.lock"
		}
		if !strings.HasSuffix(name, suffix) {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(
				filepath.Join(directory, name),
				errors.New("unexpected terminal stop thread entry"),
			))
			continue
		}
		threadID, decodeErr := decodeTerminalStopPathComponent(strings.TrimSuffix(name, suffix))
		if decodeErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(directory, name), decodeErr))
			continue
		}
		if entryErr := requireTerminalStopEntryMode(entry, false); entryErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(directory, name), entryErr))
			continue
		}
		if isLock {
			continue
		}
		ref := terminalStopMarkerRef{Scope: terminalStopScopeThread, ProjectID: projectID, ThreadID: threadID}
		refs = append(refs, ref)
		if _, found, markerErr := readTerminalStopMarkerFile(m.threadPath(projectID, threadID), ref.Scope, projectID, threadID); markerErr != nil {
			inspectErrors = append(inspectErrors, malformedTerminalStopPath(filepath.Join(directory, name), markerErr))
		} else if !found {
			refs = refs[:len(refs)-1]
		}
	}
	return refs, errors.Join(inspectErrors...)
}

func readTerminalStopDirectory(path string) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("terminal stop path is not a real directory")
	}
	return os.ReadDir(path)
}

func requireTerminalStopEntryMode(entry os.DirEntry, directory bool) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("terminal stop entry is a symbolic link")
	}
	if directory && !info.IsDir() {
		return errors.New("terminal stop entry is not a directory")
	}
	if !directory && !info.Mode().IsRegular() {
		return errors.New("terminal stop entry is not a regular file")
	}
	return nil
}

func decodeTerminalStopPathComponent(component string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(component)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return "", errors.New("terminal stop path component is not valid base64 identity data")
	}
	value := string(decoded)
	if terminalStopPathComponent(value) != component {
		return "", errors.New("terminal stop path component is not canonical")
	}
	return value, nil
}

func malformedTerminalStopPath(path string, err error) error {
	return fmt.Errorf("%w: %s: %v", errTerminalStopMarkerMalformed, path, err)
}

func isTerminalStopTemporaryName(name string) bool {
	return strings.HasPrefix(name, terminalStopTempPrefix) && strings.HasSuffix(name, ".tmp")
}

func (l *terminalStopLease) Marker() terminalStopMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	marker := l.marker
	marker.SessionNames = append([]string(nil), marker.SessionNames...)
	marker.ThreadIDs = append([]string(nil), marker.ThreadIDs...)
	return marker
}

func (l *terminalStopLease) Adopted() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.adopted
}

// UpdateSessionNames retains the existing project-thread cleanup recipe while
// replacing the exact tmux identities. It remains for focused terminal tests
// and callers that do not refresh project membership.
func (l *terminalStopLease) UpdateSessionNames(sessionNames []string) error {
	l.mu.Lock()
	threadIDs := append([]string(nil), l.marker.ThreadIDs...)
	l.mu.Unlock()
	return l.UpdateCleanupRecipe(threadIDs, sessionNames)
}

// UpdateCleanupRecipe atomically replaces the project thread identities and
// exact tmux session names after project deletion refreshes its Store snapshot.
func (l *terminalStopLease) UpdateCleanupRecipe(threadIDs, sessionNames []string) error {
	threadIDs, err := normalizeTerminalStopThreadIDs(threadIDs)
	if err != nil {
		return err
	}
	sessionNames, err = normalizeTerminalStopSessionNames(sessionNames)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errTerminalStopLeaseClosed
	}
	if err := l.verifyOwnershipLocked(); err != nil {
		return l.closePreservingLocked(err)
	}
	if l.marker.Committed {
		return errors.New("committed terminal stop cleanup recipe cannot be changed")
	}
	updated := l.marker
	updated.ThreadIDs = threadIDs
	updated.SessionNames = sessionNames
	if err := writeTerminalStopMarkerAtomic(l.path, updated); err != nil {
		return l.closePreservingLocked(fmt.Errorf("update terminal stop marker: %w", err))
	}
	l.marker = updated
	return nil
}

// Commit durably records that the Store deletion succeeded. Recovery may use
// the persisted Store while this bit is still false (the crash gap between the
// two commits), but it must never roll a committed marker back based on a stale
// Store snapshot from an overlapping backend.
func (l *terminalStopLease) Commit() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errTerminalStopLeaseClosed
	}
	if err := l.verifyOwnershipLocked(); err != nil {
		return l.closePreservingLocked(err)
	}
	if l.marker.Committed {
		return nil
	}
	updated := l.marker
	updated.Committed = true
	if err := writeTerminalStopMarkerAtomic(l.path, updated); err != nil {
		// The old pending marker is still valid and this caller still owns the
		// sidecar lock. Let the caller retain it for durable recovery.
		return fmt.Errorf("commit terminal stop marker: %w", err)
	}
	l.marker = updated
	return nil
}

// RecheckProjectThreads closes the snapshot gap between creating the broad
// project marker and refreshing the Store. Once the project marker exists, no
// new thread marker can successfully begin; therefore any exact current-thread
// marker found here won before the project marker and the project operation
// rolls itself back to let that narrower deletion finish.
func (l *terminalStopLease) RecheckProjectThreads(threadIDs []string) error {
	threadIDs, err := normalizeTerminalStopThreadIDs(threadIDs)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errTerminalStopLeaseClosed
	}
	if l.marker.Scope != terminalStopScopeProject {
		return errors.New("only a project terminal stop lease can recheck threads")
	}
	for _, threadID := range threadIDs {
		_, found, inspectErr := l.manager.readThread(l.marker.ProjectID, threadID)
		if inspectErr != nil {
			return l.closePreservingLocked(
				fmt.Errorf("inspect refreshed thread %q terminal stop marker: %w", threadID, inspectErr),
			)
		}
		if !found {
			continue
		}
		operationErr := fmt.Errorf("%w: thread %q", errTerminalStopping, threadID)
		return errors.Join(operationErr, l.abandonAfterConflictLocked())
	}
	return nil
}

// Retain releases the active deletion lock while preserving its durable stop
// marker. A later DELETE may adopt the unlocked marker for an exact final sweep.
func (l *terminalStopLease) Retain() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errTerminalStopLeaseClosed
	}
	err := unlockAndCloseTerminalStopFile(l.file)
	l.file = nil
	l.closed = true
	return err
}

// Rollback compare-removes only the marker represented by this locked lease.
// A token mismatch or path replacement is preserved and reported fail-closed.
func (l *terminalStopLease) Rollback() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rollbackLocked()
}

func (l *terminalStopLease) rollbackLocked() error {
	if l.closed || l.file == nil {
		return errTerminalStopLeaseClosed
	}

	if err := l.verifyOwnershipLocked(); err != nil {
		return l.closePreservingLocked(err)
	}
	if l.marker.Committed {
		return l.closePreservingLocked(errors.New("committed terminal stop marker cannot be rolled back"))
	}
	if err := os.Remove(l.path); err != nil {
		return l.closePreservingLocked(fmt.Errorf("remove terminal stop marker: %w", err))
	}
	syncErr := syncTerminalStopDirectory(filepath.Dir(l.path))
	closeErr := unlockAndCloseTerminalStopFile(l.file)
	l.file = nil
	l.closed = true
	return errors.Join(syncErr, closeErr)
}

func (l *terminalStopLease) verifyOwnershipLocked() error {
	current, found, err := readTerminalStopMarkerFile(l.path, l.marker.Scope, l.marker.ProjectID, l.marker.ThreadID)
	if err != nil || !found {
		if err == nil {
			err = os.ErrNotExist
		}
		return fmt.Errorf("verify terminal stop marker ownership: %w", err)
	}
	if current.Token != l.marker.Token {
		return fmt.Errorf("%w: token changed", errTerminalStopLeaseOwnership)
	}
	pathInfo, err := os.Lstat(l.lockPath)
	if err != nil {
		return fmt.Errorf("verify terminal stop marker lock path: %w", err)
	}
	fileInfo, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("verify terminal stop marker lock file: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return fmt.Errorf("%w: marker lock path was replaced", errTerminalStopLeaseOwnership)
	}
	return nil
}

func (l *terminalStopLease) closePreservingLocked(operationErr error) error {
	closeErr := unlockAndCloseTerminalStopFile(l.file)
	l.file = nil
	l.closed = true
	return errors.Join(operationErr, closeErr)
}

// abandonAfterConflict rolls back a marker created by this begin call. An
// adopted marker may represent an already committed deletion, so it is
// preserved fail-closed rather than removed based on a possibly stale Store.
func (l *terminalStopLease) abandonAfterConflict() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.abandonAfterConflictLocked()
}

func (l *terminalStopLease) abandonAfterConflictLocked() error {
	if l.adopted {
		return l.closePreservingLocked(nil)
	}
	return l.rollbackLocked()
}

func newTerminalStopMarker(
	scope terminalStopScope,
	projectID string,
	threadID string,
	sessionNames []string,
) (terminalStopMarker, error) {
	if scope != terminalStopScopeProject && scope != terminalStopScopeThread {
		return terminalStopMarker{}, errors.New("invalid terminal stop scope")
	}
	if err := validateTerminalStopIdentity(projectID, threadID); err != nil {
		return terminalStopMarker{}, err
	}
	if scope == terminalStopScopeProject && threadID != "" {
		return terminalStopMarker{}, errors.New("project terminal stop marker cannot have a thread ID")
	}
	if scope == terminalStopScopeThread && threadID == "" {
		return terminalStopMarker{}, errors.New("thread terminal stop marker requires a thread ID")
	}
	sessionNames, err := normalizeTerminalStopSessionNames(sessionNames)
	if err != nil {
		return terminalStopMarker{}, err
	}
	token, err := newTerminalStopToken()
	if err != nil {
		return terminalStopMarker{}, err
	}
	return terminalStopMarker{
		Version:      terminalStopMarkerVersion,
		Scope:        scope,
		ProjectID:    projectID,
		ThreadID:     threadID,
		Token:        token,
		SessionNames: sessionNames,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func validateTerminalStopIdentity(projectID, threadID string) error {
	if projectID == "" {
		return errors.New("terminal stop project ID is required")
	}
	if threadID == "" {
		return nil
	}
	return nil
}

func normalizeTerminalStopThreadIDs(threadIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(threadIDs))
	normalized := make([]string, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if threadID == "" {
			return nil, errors.New("terminal stop thread ID is required")
		}
		if _, found := seen[threadID]; found {
			continue
		}
		seen[threadID] = struct{}{}
		normalized = append(normalized, threadID)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeTerminalStopSessionNames(sessionNames []string) ([]string, error) {
	seen := make(map[string]struct{}, len(sessionNames))
	normalized := make([]string, 0, len(sessionNames))
	for _, sessionName := range sessionNames {
		if sessionName == "" {
			return nil, errors.New("terminal stop session name cannot be empty")
		}
		if _, found := seen[sessionName]; found {
			continue
		}
		seen[sessionName] = struct{}{}
		normalized = append(normalized, sessionName)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func newTerminalStopToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create terminal stop marker token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func terminalStopPathComponent(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func readTerminalStopMarkerFile(
	path string,
	scope terminalStopScope,
	projectID string,
	threadID string,
) (terminalStopMarker, bool, error) {
	var file *os.File
	var err error
	for attempt := 0; attempt < 64; attempt++ {
		file, err = openRegularTerminalStopFile(path)
		if !errors.Is(err, errTerminalStopMarkerChanged) {
			break
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return terminalStopMarker{}, false, nil
	}
	if err != nil {
		// Unknown filesystem state is treated as marker presence by callers.
		return terminalStopMarker{}, true, err
	}
	defer file.Close()
	marker, err := readTerminalStopMarker(file, scope, projectID, threadID)
	if err != nil {
		return terminalStopMarker{}, true, err
	}
	return marker, true, nil
}

func openRegularTerminalStopFile(path string) (*os.File, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, errors.New("terminal stop marker is not a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(pathInfo, fileInfo) {
		_ = file.Close()
		return nil, errTerminalStopMarkerChanged
	}
	return file, nil
}

func readTerminalStopMarker(
	file *os.File,
	scope terminalStopScope,
	projectID string,
	threadID string,
) (terminalStopMarker, error) {
	info, err := file.Stat()
	if err != nil {
		return terminalStopMarker{}, err
	}
	if info.Size() <= 0 || info.Size() > 1<<20 {
		return terminalStopMarker{}, errors.New("terminal stop marker has an invalid size")
	}
	contents := make([]byte, info.Size())
	if _, err := file.ReadAt(contents, 0); err != nil && !errors.Is(err, io.EOF) {
		return terminalStopMarker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var marker terminalStopMarker
	if err := decoder.Decode(&marker); err != nil {
		return terminalStopMarker{}, fmt.Errorf("decode terminal stop marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return terminalStopMarker{}, errors.New("terminal stop marker has trailing data")
		}
		return terminalStopMarker{}, fmt.Errorf("decode terminal stop marker trailing data: %w", err)
	}
	if err := validateTerminalStopMarker(marker, scope, projectID, threadID); err != nil {
		return terminalStopMarker{}, err
	}
	return marker, nil
}

func validateTerminalStopMarker(
	marker terminalStopMarker,
	scope terminalStopScope,
	projectID string,
	threadID string,
) error {
	if marker.Version != terminalStopMarkerVersion {
		return fmt.Errorf("unsupported terminal stop marker version %d", marker.Version)
	}
	if marker.Scope != scope || marker.ProjectID != projectID || marker.ThreadID != threadID {
		return errors.New("terminal stop marker identity does not match its path")
	}
	token, err := hex.DecodeString(marker.Token)
	if err != nil || len(token) != 16 {
		return errors.New("terminal stop marker has an invalid token")
	}
	if marker.CreatedAt.IsZero() {
		return errors.New("terminal stop marker has no creation time")
	}
	if marker.Scope != terminalStopScopeProject && len(marker.ThreadIDs) != 0 {
		return errors.New("thread terminal stop marker cannot contain project thread IDs")
	}
	normalizedThreadIDs, err := normalizeTerminalStopThreadIDs(marker.ThreadIDs)
	if err != nil {
		return err
	}
	if len(normalizedThreadIDs) != len(marker.ThreadIDs) {
		return errors.New("terminal stop marker thread IDs are not unique")
	}
	for index := range normalizedThreadIDs {
		if normalizedThreadIDs[index] != marker.ThreadIDs[index] {
			return errors.New("terminal stop marker thread IDs are not sorted")
		}
	}
	normalized, err := normalizeTerminalStopSessionNames(marker.SessionNames)
	if err != nil {
		return err
	}
	if len(normalized) != len(marker.SessionNames) {
		return errors.New("terminal stop marker session names are not unique")
	}
	for index := range normalized {
		if normalized[index] != marker.SessionNames[index] {
			return errors.New("terminal stop marker session names are not sorted")
		}
	}
	return nil
}

func writeTerminalStopMarkerAtomic(path string, marker terminalStopMarker) error {
	return writeTerminalStopMarkerAtomicWithRename(path, marker, os.Rename)
}

func writeTerminalStopMarkerAtomicWithRename(
	path string,
	marker terminalStopMarker,
	rename func(oldPath, newPath string) error,
) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode terminal stop marker: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, terminalStopTempPrefix+"*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary terminal stop marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary terminal stop marker: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary terminal stop marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary terminal stop marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary terminal stop marker: %w", err)
	}
	if err := rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace terminal stop marker: %w", err)
	}
	if err := syncTerminalStopDirectory(directory); err != nil {
		return fmt.Errorf("sync terminal stop marker directory: %w", err)
	}
	return nil
}

func terminalStopLockPath(markerPath string) string {
	return markerPath + ".lock"
}

func terminalStopMarkerPathPresent(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return true, errors.New("terminal stop marker is not a regular file")
	}
	return true, nil
}

func secureTerminalStopDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create terminal stop marker directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure terminal stop marker directory: %w", err)
	}
	return nil
}

func openTerminalStopLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	closeWithError := func(operationErr error) (*os.File, error) {
		return nil, errors.Join(operationErr, file.Close())
	}
	if err := file.Chmod(0o600); err != nil {
		return closeWithError(fmt.Errorf("secure terminal stop marker lock: %w", err))
	}
	info, err := file.Stat()
	if err != nil {
		return closeWithError(err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return closeWithError(err)
	}
	if !info.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		return closeWithError(errors.New("terminal stop marker lock path changed while opening"))
	}
	if err := file.Sync(); err != nil {
		return closeWithError(fmt.Errorf("sync terminal stop marker lock: %w", err))
	}
	if err := syncTerminalStopDirectory(filepath.Dir(path)); err != nil {
		return closeWithError(fmt.Errorf("sync terminal stop marker lock directory: %w", err))
	}
	return file, nil
}

func lockTerminalStopFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func terminalStopLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func unlockAndCloseTerminalStopFile(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

func syncTerminalStopDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
