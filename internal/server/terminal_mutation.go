package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const terminalMutationDirectoryName = "terminal-mutations-v1"

var (
	errTerminalMutationLeaseReleased = errors.New("terminal mutation lease is already released")

	terminalMutationLocalLocks = struct {
		sync.Mutex
		locks map[string]*sync.Mutex
	}{locks: make(map[string]*sync.Mutex)}
)

// terminalMutationManager serializes mutations of one thread's tmux state.
// The package-local mutex covers independent handlers in this process, while
// the persistent flock covers handlers in overlapping backend processes.
type terminalMutationManager struct {
	root string
}

type terminalMutationLease struct {
	mu       sync.Mutex
	file     *os.File
	local    *sync.Mutex
	released bool
}

func newTerminalMutationManager(dataDirectory string) *terminalMutationManager {
	cleanDirectory := filepath.Clean(dataDirectory)
	if absoluteDirectory, err := filepath.Abs(cleanDirectory); err == nil {
		cleanDirectory = absoluteDirectory
	}
	return &terminalMutationManager{
		root: filepath.Join(cleanDirectory, terminalMutationDirectoryName),
	}
}

func (m *terminalMutationManager) LockThread(projectID, threadID string) (*terminalMutationLease, error) {
	if projectID == "" {
		return nil, errors.New("terminal mutation project ID is required")
	}
	if threadID == "" {
		return nil, errors.New("terminal mutation thread ID is required")
	}

	path := m.threadPath(projectID, threadID)
	local := terminalMutationLocalLock(path)
	local.Lock()

	if err := m.ensureThreadDirectory(projectID); err != nil {
		local.Unlock()
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		local.Unlock()
		return nil, fmt.Errorf("open terminal mutation lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		local.Unlock()
		return nil, errors.Join(
			fmt.Errorf("set terminal mutation lock permissions: %w", err),
			closeErr,
		)
	}
	if err := flockTerminalMutationFile(file, syscall.LOCK_EX); err != nil {
		closeErr := file.Close()
		local.Unlock()
		return nil, errors.Join(
			fmt.Errorf("lock terminal mutation file: %w", err),
			closeErr,
		)
	}

	return &terminalMutationLease{file: file, local: local}, nil
}

func (m *terminalMutationManager) threadPath(projectID, threadID string) string {
	return filepath.Join(
		m.root,
		"projects",
		terminalMutationPathComponent(projectID),
		"threads",
		terminalMutationPathComponent(threadID)+".lock",
	)
}

func (m *terminalMutationManager) ensureThreadDirectory(projectID string) error {
	directories := []string{
		m.root,
		filepath.Join(m.root, "projects"),
		filepath.Join(m.root, "projects", terminalMutationPathComponent(projectID)),
		filepath.Join(m.root, "projects", terminalMutationPathComponent(projectID), "threads"),
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create terminal mutation lock directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("set terminal mutation lock directory permissions: %w", err)
		}
	}
	return nil
}

func terminalMutationPathComponent(identity string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(identity))
}

func terminalMutationLocalLock(path string) *sync.Mutex {
	terminalMutationLocalLocks.Lock()
	defer terminalMutationLocalLocks.Unlock()

	lock := terminalMutationLocalLocks.locks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		terminalMutationLocalLocks.locks[path] = lock
	}
	return lock
}

func flockTerminalMutationFile(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func (l *terminalMutationLease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.released {
		return errTerminalMutationLeaseReleased
	}
	l.released = true

	unlockErr := flockTerminalMutationFile(l.file, syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.local.Unlock()
	l.file = nil
	l.local = nil

	return errors.Join(
		wrapTerminalMutationReleaseError("unlock terminal mutation file", unlockErr),
		wrapTerminalMutationReleaseError("close terminal mutation file", closeErr),
	)
}

func wrapTerminalMutationReleaseError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
