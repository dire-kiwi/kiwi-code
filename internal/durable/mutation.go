package durable

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/dire-kiwi/kiwi-code/internal/datadir"
)

const mutationDirectoryName = datadir.TerminalMutationsDirectoryName

var (
	errMutationLeaseReleased = errors.New("terminal mutation lease is already released")

	mutationLocalLocks = struct {
		sync.Mutex
		locks map[string]*sync.Mutex
	}{locks: make(map[string]*sync.Mutex)}
)

// MutationManager serializes mutations of one thread's tmux state.
// The package-local mutex covers independent handlers in this process, while
// the persistent flock covers handlers in overlapping backend processes.
type MutationManager struct {
	root string
}

type MutationLease struct {
	mu       sync.Mutex
	file     *os.File
	local    *sync.Mutex
	released bool
}

func NewMutationManager(dataDirectory string) *MutationManager {
	cleanDirectory := filepath.Clean(dataDirectory)
	if absoluteDirectory, err := filepath.Abs(cleanDirectory); err == nil {
		cleanDirectory = absoluteDirectory
	}
	return &MutationManager{
		root: filepath.Join(cleanDirectory, mutationDirectoryName),
	}
}

func (m *MutationManager) LockThread(projectID, threadID string) (*MutationLease, error) {
	lease, _, err := m.lockThread(projectID, threadID, true)
	return lease, err
}

// TryLockThread attempts to take the thread's mutation lease without
// blocking. ok=false with a nil error means another holder currently owns
// the lease.
func (m *MutationManager) TryLockThread(projectID, threadID string) (lease *MutationLease, ok bool, err error) {
	return m.lockThread(projectID, threadID, false)
}

func (m *MutationManager) lockThread(projectID, threadID string, block bool) (*MutationLease, bool, error) {
	if projectID == "" {
		return nil, false, errors.New("terminal mutation project ID is required")
	}
	if threadID == "" {
		return nil, false, errors.New("terminal mutation thread ID is required")
	}

	path := m.threadPath(projectID, threadID)
	local := mutationLocalLock(path)
	if block {
		local.Lock()
	} else if !local.TryLock() {
		return nil, false, nil
	}

	if err := m.ensureThreadDirectory(projectID); err != nil {
		local.Unlock()
		return nil, false, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		local.Unlock()
		return nil, false, fmt.Errorf("open terminal mutation lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		local.Unlock()
		return nil, false, errors.Join(
			fmt.Errorf("set terminal mutation lock permissions: %w", err),
			closeErr,
		)
	}
	operation := syscall.LOCK_EX
	if !block {
		operation |= syscall.LOCK_NB
	}
	if err := flockMutationFile(file, operation); err != nil {
		closeErr := file.Close()
		local.Unlock()
		if !block && errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, closeErr
		}
		return nil, false, errors.Join(
			fmt.Errorf("lock terminal mutation file: %w", err),
			closeErr,
		)
	}

	return &MutationLease{file: file, local: local}, true, nil
}

func (m *MutationManager) threadPath(projectID, threadID string) string {
	return filepath.Join(
		m.root,
		"projects",
		mutationPathComponent(projectID),
		"threads",
		mutationPathComponent(threadID)+".lock",
	)
}

func (m *MutationManager) ensureThreadDirectory(projectID string) error {
	directories := []string{
		m.root,
		filepath.Join(m.root, "projects"),
		filepath.Join(m.root, "projects", mutationPathComponent(projectID)),
		filepath.Join(m.root, "projects", mutationPathComponent(projectID), "threads"),
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

func mutationPathComponent(identity string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(identity))
}

func mutationLocalLock(path string) *sync.Mutex {
	mutationLocalLocks.Lock()
	defer mutationLocalLocks.Unlock()

	lock := mutationLocalLocks.locks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		mutationLocalLocks.locks[path] = lock
	}
	return lock
}

func flockMutationFile(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func (l *MutationLease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.released {
		return errMutationLeaseReleased
	}
	l.released = true

	unlockErr := flockMutationFile(l.file, syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.local.Unlock()
	l.file = nil
	l.local = nil

	return errors.Join(
		wrapMutationReleaseError("unlock terminal mutation file", unlockErr),
		wrapMutationReleaseError("close terminal mutation file", closeErr),
	)
}

func wrapMutationReleaseError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
