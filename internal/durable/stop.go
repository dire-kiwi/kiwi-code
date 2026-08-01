package durable

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

	"github.com/dire-kiwi/kiwi-code/internal/datadir"
)

const (
	stopDirectoryName = datadir.TerminalStopsDirectoryName
	stopMarkerVersion = 1
	stopTempPrefix    = ".terminal-stop-marker-"

	StopScopeProject StopScope = "project"
	StopScopeThread  StopScope = "thread"
)

var (
	// ErrStopping reports that the resource's terminal sessions are inside a
	// durable stop. The message is part of observable behavior (it reaches API
	// error text) and must not change.
	ErrStopping = errors.New("terminal sessions are stopping")

	errStopLeaseClosed     = errors.New("terminal stop lease is closed")
	errStopLeaseOwnership  = errors.New("terminal stop marker is not owned by this lease")
	errStopMarkerMalformed = errors.New("terminal stop marker is malformed")
	errStopMarkerChanged   = errors.New("terminal stop marker changed while opening")
)

type StopScope string

// StopMarker is both the durable stop tombstone and the recovery
// recipe for a final cleanup sweep. SessionNames contains exact tmux session
// identities; callers must never reinterpret them as prefixes. ThreadIDs lets
// non-tmux thread-owned resources (such as browser sessions) retry cleanup.
type StopMarker struct {
	Version      int       `json:"version"`
	Scope        StopScope `json:"scope"`
	ProjectID    string    `json:"projectId"`
	ThreadID     string    `json:"threadId,omitempty"`
	Token        string    `json:"token"`
	SessionNames []string  `json:"sessionNames"`
	ThreadIDs    []string  `json:"threadIds,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	Committed    bool      `json:"committed,omitempty"`
}

// StopManager coordinates terminal deletion between independent
// terminalHandler instances, including handlers in overlapping processes.
// Marker existence is authoritative; the flock only distinguishes an active
// deletion from an unlocked marker that a later DELETE may adopt and resume.
type StopManager struct {
	root string
}

// StopMarkerRef is derived exclusively from the marker's path. The
// marker contents are validated separately after its persistent sidecar lock
// has been acquired.
type StopMarkerRef struct {
	Scope     StopScope
	ProjectID string
	ThreadID  string
}

type StopLease struct {
	mu       sync.Mutex
	manager  *StopManager
	marker   StopMarker
	path     string
	lockPath string
	file     *os.File
	adopted  bool
	closed   bool
}

func NewStopManager(dataDirectory string) *StopManager {
	return &StopManager{
		root: filepath.Join(dataDirectory, stopDirectoryName),
	}
}

func (m *StopManager) BeginThread(
	projectID string,
	threadID string,
	sessionNames []string,
) (*StopLease, error) {
	marker, err := newStopMarker(StopScopeThread, projectID, threadID, sessionNames)
	if err != nil {
		return nil, err
	}

	if _, found, inspectErr := m.ReadProject(projectID); inspectErr != nil {
		return nil, fmt.Errorf("inspect project terminal stop marker: %w", inspectErr)
	} else if found {
		return nil, fmt.Errorf("%w: project %q", ErrStopping, projectID)
	}

	lease, err := m.acquire(marker, m.threadPath(projectID, threadID))
	if err != nil {
		return nil, err
	}

	// A project stop can start after the first check but before the thread
	// marker becomes visible. Rechecking makes the two differently named marker
	// files behave as one ordered stop boundary.
	_, projectFound, inspectErr := m.ReadProject(projectID)
	if inspectErr == nil && !projectFound {
		return lease, nil
	}
	cleanupErr := lease.abandonAfterConflict()
	if inspectErr != nil {
		return nil, errors.Join(fmt.Errorf("recheck project terminal stop marker: %w", inspectErr), cleanupErr)
	}
	return nil, errors.Join(fmt.Errorf("%w: project %q", ErrStopping, projectID), cleanupErr)
}

func (m *StopManager) BeginProject(
	projectID string,
	threadIDs []string,
	sessionNames []string,
) (*StopLease, error) {
	marker, err := newStopMarker(StopScopeProject, projectID, "", sessionNames)
	if err != nil {
		return nil, err
	}
	threadIDs, err = normalizeStopThreadIDs(threadIDs)
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
		_, found, inspectErr := m.ReadThread(projectID, threadID)
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
			fmt.Errorf("%w: thread %q", ErrStopping, threadID),
			cleanupErr,
		)
	}
	return lease, nil
}

// threadStopped checks both scopes. It returns stopped=true on malformed or
// unreadable marker state so callers cannot create terminal state when the
// durable deletion state is unknown.
func (m *StopManager) ThreadStopped(projectID, threadID string) (bool, error) {
	if _, found, err := m.ReadProject(projectID); err != nil {
		return true, fmt.Errorf("inspect project terminal stop marker: %w", err)
	} else if found {
		return true, nil
	}
	if _, found, err := m.ReadThread(projectID, threadID); err != nil {
		return true, fmt.Errorf("inspect thread terminal stop marker: %w", err)
	} else if found {
		return true, nil
	}
	return false, nil
}

// projectStopped returns stopped=true on malformed or unreadable marker state.
func (m *StopManager) ProjectStopped(projectID string) (bool, error) {
	_, found, err := m.ReadProject(projectID)
	if err != nil {
		return true, fmt.Errorf("inspect project terminal stop marker: %w", err)
	}
	return found, nil
}

func (m *StopManager) ReadProject(projectID string) (StopMarker, bool, error) {
	if err := validateStopIdentity(projectID, ""); err != nil {
		return StopMarker{}, false, err
	}
	return readStopMarkerFile(
		m.projectPath(projectID),
		StopScopeProject,
		projectID,
		"",
	)
}

func (m *StopManager) ReadThread(projectID, threadID string) (StopMarker, bool, error) {
	if err := validateStopIdentity(projectID, threadID); err != nil {
		return StopMarker{}, false, err
	}
	return readStopMarkerFile(
		m.threadPath(projectID, threadID),
		StopScopeThread,
		projectID,
		threadID,
	)
}

func (m *StopManager) projectPath(projectID string) string {
	return filepath.Join(
		m.root,
		"projects",
		stopPathComponent(projectID),
		"project.json",
	)
}

func (m *StopManager) threadPath(projectID, threadID string) string {
	return filepath.Join(
		m.root,
		"projects",
		stopPathComponent(projectID),
		"threads",
		stopPathComponent(threadID)+".json",
	)
}

func (m *StopManager) acquire(marker StopMarker, path string) (*StopLease, error) {
	if err := secureStopDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}

	lockPath := stopLockPath(path)
	file, err := openStopLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open terminal stop marker lock: %w", err)
	}
	if err := lockStopFile(file); err != nil {
		_ = file.Close()
		if stopLockBusy(err) {
			return nil, fmt.Errorf("%w: %s %q", ErrStopping, marker.Scope, marker.ProjectID)
		}
		return nil, fmt.Errorf("lock terminal stop marker: %w", err)
	}
	lease := &StopLease{
		manager:  m,
		marker:   marker,
		path:     path,
		lockPath: lockPath,
		file:     file,
	}

	existing, found, err := readStopMarkerFile(path, marker.Scope, marker.ProjectID, marker.ThreadID)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("%w: read existing terminal stop marker: %v", errStopMarkerMalformed, err),
			lease.closePreservingLocked(nil),
		)
	}
	if found {
		lease.marker = existing
		lease.adopted = true
		return lease, nil
	}

	if err := writeStopMarkerAtomic(path, marker); err != nil {
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
func (m *StopManager) AcquireExisting(ref StopMarkerRef) (*StopLease, bool, error) {
	path, err := m.MarkerPath(ref)
	if err != nil {
		return nil, false, err
	}
	present, err := stopMarkerPathPresent(path)
	if err != nil {
		return nil, true, fmt.Errorf("%w: inspect terminal stop marker: %v", errStopMarkerMalformed, err)
	}
	if !present {
		return nil, false, nil
	}

	if err := secureStopDirectory(filepath.Dir(path)); err != nil {
		return nil, true, err
	}
	lockPath := stopLockPath(path)
	file, err := openStopLockFile(lockPath)
	if err != nil {
		return nil, true, fmt.Errorf("open terminal stop marker lock: %w", err)
	}
	if err := lockStopFile(file); err != nil {
		_ = file.Close()
		if stopLockBusy(err) {
			return nil, true, fmt.Errorf("%w: %s %q", ErrStopping, ref.Scope, ref.ProjectID)
		}
		return nil, true, fmt.Errorf("lock existing terminal stop marker: %w", err)
	}

	marker, found, readErr := readStopMarkerFile(path, ref.Scope, ref.ProjectID, ref.ThreadID)
	if readErr != nil {
		return nil, true, errors.Join(
			fmt.Errorf("%w: read existing terminal stop marker: %v", errStopMarkerMalformed, readErr),
			unlockAndCloseStopFile(file),
		)
	}
	if !found {
		return nil, false, unlockAndCloseStopFile(file)
	}
	return &StopLease{
		manager:  m,
		marker:   marker,
		path:     path,
		lockPath: lockPath,
		file:     file,
		adopted:  true,
	}, true, nil
}

func (m *StopManager) MarkerPath(ref StopMarkerRef) (string, error) {
	if err := validateStopIdentity(ref.ProjectID, ref.ThreadID); err != nil {
		return "", err
	}
	switch ref.Scope {
	case StopScopeProject:
		if ref.ThreadID != "" {
			return "", errors.New("project terminal stop marker ref cannot have a thread ID")
		}
		return m.projectPath(ref.ProjectID), nil
	case StopScopeThread:
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
func (m *StopManager) ListMarkers() ([]StopMarkerRef, error) {
	projectsRoot := filepath.Join(m.root, "projects")
	entries, err := readStopDirectory(projectsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect terminal stop projects directory: %v", errStopMarkerMalformed, err)
	}

	var refs []StopMarkerRef
	var inspectErrors []error
	for _, entry := range entries {
		projectID, decodeErr := decodeStopPathComponent(entry.Name())
		if decodeErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(entry.Name(), decodeErr))
			continue
		}
		if entryErr := requireStopEntryMode(entry, true); entryErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(entry.Name(), entryErr))
			continue
		}

		projectDirectory := filepath.Join(projectsRoot, entry.Name())
		projectEntries, readErr := readStopDirectory(projectDirectory)
		if readErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(projectDirectory, readErr))
			continue
		}
		for _, projectEntry := range projectEntries {
			name := projectEntry.Name()
			switch {
			case name == "project.json":
				ref := StopMarkerRef{Scope: StopScopeProject, ProjectID: projectID}
				path := m.projectPath(projectID)
				if entryErr := requireStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedStopPath(path, entryErr))
					continue
				}
				refs = append(refs, ref)
				if _, found, markerErr := readStopMarkerFile(path, ref.Scope, ref.ProjectID, ref.ThreadID); markerErr != nil {
					inspectErrors = append(inspectErrors, malformedStopPath(path, markerErr))
				} else if !found {
					refs = refs[:len(refs)-1]
				}
			case name == "project.json.lock":
				if entryErr := requireStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(projectDirectory, name), entryErr))
				}
			case name == "threads":
				if entryErr := requireStopEntryMode(projectEntry, true); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(projectDirectory, name), entryErr))
					continue
				}
				threadRefs, threadErr := m.listThreadMarkers(projectID, filepath.Join(projectDirectory, name))
				refs = append(refs, threadRefs...)
				if threadErr != nil {
					inspectErrors = append(inspectErrors, threadErr)
				}
			case isStopTemporaryName(name):
				if entryErr := requireStopEntryMode(projectEntry, false); entryErr != nil {
					inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(projectDirectory, name), entryErr))
				}
			default:
				inspectErrors = append(inspectErrors, malformedStopPath(
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
			return refs[i].Scope == StopScopeProject
		}
		return refs[i].ThreadID < refs[j].ThreadID
	})
	return refs, errors.Join(inspectErrors...)
}

func (m *StopManager) listThreadMarkers(projectID, directory string) ([]StopMarkerRef, error) {
	entries, err := readStopDirectory(directory)
	if err != nil {
		return nil, malformedStopPath(directory, err)
	}
	var refs []StopMarkerRef
	var inspectErrors []error
	for _, entry := range entries {
		name := entry.Name()
		if isStopTemporaryName(name) {
			if entryErr := requireStopEntryMode(entry, false); entryErr != nil {
				inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(directory, name), entryErr))
			}
			continue
		}

		isLock := strings.HasSuffix(name, ".json.lock")
		suffix := ".json"
		if isLock {
			suffix = ".json.lock"
		}
		if !strings.HasSuffix(name, suffix) {
			inspectErrors = append(inspectErrors, malformedStopPath(
				filepath.Join(directory, name),
				errors.New("unexpected terminal stop thread entry"),
			))
			continue
		}
		threadID, decodeErr := decodeStopPathComponent(strings.TrimSuffix(name, suffix))
		if decodeErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(directory, name), decodeErr))
			continue
		}
		if entryErr := requireStopEntryMode(entry, false); entryErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(directory, name), entryErr))
			continue
		}
		if isLock {
			continue
		}
		ref := StopMarkerRef{Scope: StopScopeThread, ProjectID: projectID, ThreadID: threadID}
		refs = append(refs, ref)
		if _, found, markerErr := readStopMarkerFile(m.threadPath(projectID, threadID), ref.Scope, projectID, threadID); markerErr != nil {
			inspectErrors = append(inspectErrors, malformedStopPath(filepath.Join(directory, name), markerErr))
		} else if !found {
			refs = refs[:len(refs)-1]
		}
	}
	return refs, errors.Join(inspectErrors...)
}

func readStopDirectory(path string) ([]os.DirEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("terminal stop path is not a real directory")
	}
	return os.ReadDir(path)
}

func requireStopEntryMode(entry os.DirEntry, directory bool) error {
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

func decodeStopPathComponent(component string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(component)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return "", errors.New("terminal stop path component is not valid base64 identity data")
	}
	value := string(decoded)
	if stopPathComponent(value) != component {
		return "", errors.New("terminal stop path component is not canonical")
	}
	return value, nil
}

func malformedStopPath(path string, err error) error {
	return fmt.Errorf("%w: %s: %v", errStopMarkerMalformed, path, err)
}

func isStopTemporaryName(name string) bool {
	return strings.HasPrefix(name, stopTempPrefix) && strings.HasSuffix(name, ".tmp")
}

func (l *StopLease) Marker() StopMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	marker := l.marker
	marker.SessionNames = append([]string(nil), marker.SessionNames...)
	marker.ThreadIDs = append([]string(nil), marker.ThreadIDs...)
	return marker
}

func (l *StopLease) Adopted() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.adopted
}

// UpdateSessionNames retains the existing project-thread cleanup recipe while
// replacing the exact tmux identities. It remains for focused terminal tests
// and callers that do not refresh project membership.
func (l *StopLease) UpdateSessionNames(sessionNames []string) error {
	l.mu.Lock()
	threadIDs := append([]string(nil), l.marker.ThreadIDs...)
	l.mu.Unlock()
	return l.UpdateCleanupRecipe(threadIDs, sessionNames)
}

// UpdateCleanupRecipe atomically replaces the project thread identities and
// exact tmux session names after project deletion refreshes its Store snapshot.
func (l *StopLease) UpdateCleanupRecipe(threadIDs, sessionNames []string) error {
	threadIDs, err := normalizeStopThreadIDs(threadIDs)
	if err != nil {
		return err
	}
	sessionNames, err = normalizeStopSessionNames(sessionNames)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errStopLeaseClosed
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
	if err := writeStopMarkerAtomic(l.path, updated); err != nil {
		return l.closePreservingLocked(fmt.Errorf("update terminal stop marker: %w", err))
	}
	l.marker = updated
	return nil
}

// Commit durably records that the Store deletion succeeded. Recovery may use
// the persisted Store while this bit is still false (the crash gap between the
// two commits), but it must never roll a committed marker back based on a stale
// Store snapshot from an overlapping backend.
func (l *StopLease) Commit() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errStopLeaseClosed
	}
	if err := l.verifyOwnershipLocked(); err != nil {
		return l.closePreservingLocked(err)
	}
	if l.marker.Committed {
		return nil
	}
	updated := l.marker
	updated.Committed = true
	if err := writeStopMarkerAtomic(l.path, updated); err != nil {
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
func (l *StopLease) RecheckProjectThreads(threadIDs []string) error {
	threadIDs, err := normalizeStopThreadIDs(threadIDs)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errStopLeaseClosed
	}
	if l.marker.Scope != StopScopeProject {
		return errors.New("only a project terminal stop lease can recheck threads")
	}
	for _, threadID := range threadIDs {
		_, found, inspectErr := l.manager.ReadThread(l.marker.ProjectID, threadID)
		if inspectErr != nil {
			return l.closePreservingLocked(
				fmt.Errorf("inspect refreshed thread %q terminal stop marker: %w", threadID, inspectErr),
			)
		}
		if !found {
			continue
		}
		operationErr := fmt.Errorf("%w: thread %q", ErrStopping, threadID)
		return errors.Join(operationErr, l.abandonAfterConflictLocked())
	}
	return nil
}

// Retain releases the active deletion lock while preserving its durable stop
// marker. A later DELETE may adopt the unlocked marker for an exact final sweep.
func (l *StopLease) Retain() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return errStopLeaseClosed
	}
	err := unlockAndCloseStopFile(l.file)
	l.file = nil
	l.closed = true
	return err
}

// Rollback compare-removes only the marker represented by this locked lease.
// A token mismatch or path replacement is preserved and reported fail-closed.
func (l *StopLease) Rollback() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rollbackLocked()
}

func (l *StopLease) rollbackLocked() error {
	if l.closed || l.file == nil {
		return errStopLeaseClosed
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
	syncErr := syncStopDirectory(filepath.Dir(l.path))
	closeErr := unlockAndCloseStopFile(l.file)
	l.file = nil
	l.closed = true
	return errors.Join(syncErr, closeErr)
}

func (l *StopLease) verifyOwnershipLocked() error {
	current, found, err := readStopMarkerFile(l.path, l.marker.Scope, l.marker.ProjectID, l.marker.ThreadID)
	if err != nil || !found {
		if err == nil {
			err = os.ErrNotExist
		}
		return fmt.Errorf("verify terminal stop marker ownership: %w", err)
	}
	if current.Token != l.marker.Token {
		return fmt.Errorf("%w: token changed", errStopLeaseOwnership)
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
		return fmt.Errorf("%w: marker lock path was replaced", errStopLeaseOwnership)
	}
	return nil
}

func (l *StopLease) closePreservingLocked(operationErr error) error {
	closeErr := unlockAndCloseStopFile(l.file)
	l.file = nil
	l.closed = true
	return errors.Join(operationErr, closeErr)
}

// abandonAfterConflict rolls back a marker created by this begin call. An
// adopted marker may represent an already committed deletion, so it is
// preserved fail-closed rather than removed based on a possibly stale Store.
func (l *StopLease) abandonAfterConflict() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.abandonAfterConflictLocked()
}

func (l *StopLease) abandonAfterConflictLocked() error {
	if l.adopted {
		return l.closePreservingLocked(nil)
	}
	return l.rollbackLocked()
}

func newStopMarker(
	scope StopScope,
	projectID string,
	threadID string,
	sessionNames []string,
) (StopMarker, error) {
	if scope != StopScopeProject && scope != StopScopeThread {
		return StopMarker{}, errors.New("invalid terminal stop scope")
	}
	if err := validateStopIdentity(projectID, threadID); err != nil {
		return StopMarker{}, err
	}
	if scope == StopScopeProject && threadID != "" {
		return StopMarker{}, errors.New("project terminal stop marker cannot have a thread ID")
	}
	if scope == StopScopeThread && threadID == "" {
		return StopMarker{}, errors.New("thread terminal stop marker requires a thread ID")
	}
	sessionNames, err := normalizeStopSessionNames(sessionNames)
	if err != nil {
		return StopMarker{}, err
	}
	token, err := newStopToken()
	if err != nil {
		return StopMarker{}, err
	}
	return StopMarker{
		Version:      stopMarkerVersion,
		Scope:        scope,
		ProjectID:    projectID,
		ThreadID:     threadID,
		Token:        token,
		SessionNames: sessionNames,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func validateStopIdentity(projectID, threadID string) error {
	if projectID == "" {
		return errors.New("terminal stop project ID is required")
	}
	if threadID == "" {
		return nil
	}
	return nil
}

func normalizeStopThreadIDs(threadIDs []string) ([]string, error) {
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

func normalizeStopSessionNames(sessionNames []string) ([]string, error) {
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

func newStopToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create terminal stop marker token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func stopPathComponent(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func readStopMarkerFile(
	path string,
	scope StopScope,
	projectID string,
	threadID string,
) (StopMarker, bool, error) {
	var file *os.File
	var err error
	for attempt := 0; attempt < 64; attempt++ {
		file, err = openRegularStopFile(path)
		if !errors.Is(err, errStopMarkerChanged) {
			break
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return StopMarker{}, false, nil
	}
	if err != nil {
		// Unknown filesystem state is treated as marker presence by callers.
		return StopMarker{}, true, err
	}
	defer file.Close()
	marker, err := readStopMarker(file, scope, projectID, threadID)
	if err != nil {
		return StopMarker{}, true, err
	}
	return marker, true, nil
}

func openRegularStopFile(path string) (*os.File, error) {
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
		return nil, errStopMarkerChanged
	}
	return file, nil
}

func readStopMarker(
	file *os.File,
	scope StopScope,
	projectID string,
	threadID string,
) (StopMarker, error) {
	info, err := file.Stat()
	if err != nil {
		return StopMarker{}, err
	}
	if info.Size() <= 0 || info.Size() > 1<<20 {
		return StopMarker{}, errors.New("terminal stop marker has an invalid size")
	}
	contents := make([]byte, info.Size())
	if _, err := file.ReadAt(contents, 0); err != nil && !errors.Is(err, io.EOF) {
		return StopMarker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var marker StopMarker
	if err := decoder.Decode(&marker); err != nil {
		return StopMarker{}, fmt.Errorf("decode terminal stop marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return StopMarker{}, errors.New("terminal stop marker has trailing data")
		}
		return StopMarker{}, fmt.Errorf("decode terminal stop marker trailing data: %w", err)
	}
	if err := validateStopMarker(marker, scope, projectID, threadID); err != nil {
		return StopMarker{}, err
	}
	return marker, nil
}

func validateStopMarker(
	marker StopMarker,
	scope StopScope,
	projectID string,
	threadID string,
) error {
	if marker.Version != stopMarkerVersion {
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
	if marker.Scope != StopScopeProject && len(marker.ThreadIDs) != 0 {
		return errors.New("thread terminal stop marker cannot contain project thread IDs")
	}
	normalizedThreadIDs, err := normalizeStopThreadIDs(marker.ThreadIDs)
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
	normalized, err := normalizeStopSessionNames(marker.SessionNames)
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

func writeStopMarkerAtomic(path string, marker StopMarker) error {
	return writeStopMarkerAtomicWithRename(path, marker, os.Rename)
}

func writeStopMarkerAtomicWithRename(
	path string,
	marker StopMarker,
	rename func(oldPath, newPath string) error,
) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode terminal stop marker: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, stopTempPrefix+"*.tmp")
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
	if err := syncStopDirectory(directory); err != nil {
		return fmt.Errorf("sync terminal stop marker directory: %w", err)
	}
	return nil
}

func stopLockPath(markerPath string) string {
	return markerPath + ".lock"
}

func stopMarkerPathPresent(path string) (bool, error) {
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

func secureStopDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create terminal stop marker directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure terminal stop marker directory: %w", err)
	}
	return nil
}

func openStopLockFile(path string) (*os.File, error) {
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
	if err := syncStopDirectory(filepath.Dir(path)); err != nil {
		return closeWithError(fmt.Errorf("sync terminal stop marker lock directory: %w", err))
	}
	return file, nil
}

func lockStopFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func stopLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func unlockAndCloseStopFile(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

func syncStopDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
