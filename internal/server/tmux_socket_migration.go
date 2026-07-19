package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	tmuxSocketMigrationMarkerName    = "tmux-socket-migration.json"
	tmuxSocketMigrationMarkerVersion = 1
)

type tmuxServerIdentity struct {
	PID        int
	SocketPath string
}

type tmuxSocketMigrationMarker struct {
	Version             int    `json:"version"`
	CanonicalSocket     string `json:"canonicalSocket"`
	LegacySocket        string `json:"legacySocket"`
	CanonicalSocketPath string `json:"canonicalSocketPath"`
	LegacySocketPath    string `json:"legacySocketPath"`
	ServerPID           int    `json:"serverPid"`
}

// tmuxSocketMigration keeps the new -L name aliased to a live server that was
// started under the previous name. The alias is necessary because tmux cannot
// transfer live sessions between servers, and processes inside existing panes
// retain the original socket path in TMUX.
type tmuxSocketMigration struct {
	mu       sync.Mutex
	tmuxPath string
	path     string
	marker   tmuxSocketMigrationMarker
	retired  bool
}

func prepareDefaultTmuxSocketMigration(dataDirectory, canonicalSocket, legacySocket string) (*tmuxSocketMigration, error) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return nil, nil
	}
	return prepareTmuxSocketMigration(dataDirectory, tmuxPath, canonicalSocket, legacySocket)
}

func prepareTmuxSocketMigration(dataDirectory, tmuxPath, canonicalSocket, legacySocket string) (*tmuxSocketMigration, error) {
	if canonicalSocket == "" || legacySocket == "" || canonicalSocket == legacySocket {
		return nil, nil
	}
	markerPath := filepath.Join(dataDirectory, tmuxSocketMigrationMarkerName)
	marker, markerFound, err := readTmuxSocketMigrationMarker(markerPath)
	if err != nil {
		return nil, err
	}
	if markerFound {
		if err := validateTmuxSocketMigrationMarker(marker, canonicalSocket, legacySocket); err != nil {
			return nil, err
		}
	}

	legacy, legacyFound, err := inspectTmuxServer(tmuxPath, legacySocket)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy tmux server %q: %w", legacySocket, err)
	}
	canonical, canonicalFound, err := inspectTmuxServer(tmuxPath, canonicalSocket)
	if err != nil {
		return nil, fmt.Errorf("inspect canonical tmux server %q: %w", canonicalSocket, err)
	}

	if legacyFound && canonicalFound && legacy.PID != canonical.PID {
		return nil, fmt.Errorf(
			"refuse tmux socket migration: legacy server %q (pid %d) and canonical server %q (pid %d) both contain sessions",
			legacySocket,
			legacy.PID,
			canonicalSocket,
			canonical.PID,
		)
	}

	if legacyFound {
		if filepath.Base(legacy.SocketPath) != legacySocket {
			return nil, fmt.Errorf("legacy tmux server %q reported unexpected socket path %q", legacySocket, legacy.SocketPath)
		}
		canonicalPath := filepath.Join(filepath.Dir(legacy.SocketPath), canonicalSocket)
		marker = tmuxSocketMigrationMarker{
			Version:             tmuxSocketMigrationMarkerVersion,
			CanonicalSocket:     canonicalSocket,
			LegacySocket:        legacySocket,
			CanonicalSocketPath: canonicalPath,
			LegacySocketPath:    legacy.SocketPath,
			ServerPID:           legacy.PID,
		}
		if err := writeTmuxSocketMigrationMarker(markerPath, marker); err != nil {
			return nil, err
		}
		if err := createTmuxSocketAlias(canonicalPath, legacy.SocketPath); err != nil {
			return nil, err
		}
		canonical, canonicalFound, err = inspectTmuxServer(tmuxPath, canonicalSocket)
		if err != nil {
			return nil, fmt.Errorf("verify canonical tmux socket alias %q: %w", canonicalSocket, err)
		}
		if !canonicalFound {
			return nil, fmt.Errorf("canonical tmux socket alias %q did not reach the legacy server", canonicalSocket)
		}
		if canonical.PID != legacy.PID {
			return nil, fmt.Errorf("canonical tmux socket %q reached pid %d, want legacy pid %d", canonicalSocket, canonical.PID, legacy.PID)
		}
		return &tmuxSocketMigration{tmuxPath: tmuxPath, path: markerPath, marker: marker}, nil
	}

	if markerFound {
		if processAlive(marker.ServerPID) {
			return nil, fmt.Errorf(
				"refuse tmux socket migration: legacy socket %q is unavailable while its recorded server pid %d is still alive",
				legacySocket,
				marker.ServerPID,
			)
		}
		if !canonicalFound {
			if err := removeTmuxSocketAlias(marker); err != nil {
				return nil, err
			}
		}
		if err := removeTmuxSocketMigrationMarker(markerPath); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (m *tmuxSocketMigration) active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.retired
}

// prepareServerStart retires a dangling compatibility alias only after the
// exact legacy server incarnation recorded in the durable marker has exited.
// It runs immediately before new-session so a stale alias can never cause tmux
// to silently create a second server while an old process may still exist.
func (m *tmuxSocketMigration) prepareServerStart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.retired {
		return nil
	}

	legacy, legacyFound, err := inspectTmuxServer(m.tmuxPath, m.marker.LegacySocket)
	if err != nil {
		return fmt.Errorf("inspect legacy tmux server before session creation: %w", err)
	}
	canonical, canonicalFound, err := inspectTmuxServer(m.tmuxPath, m.marker.CanonicalSocket)
	if err != nil {
		return fmt.Errorf("inspect canonical tmux server before session creation: %w", err)
	}
	if legacyFound {
		if !canonicalFound {
			return fmt.Errorf("refuse to create a tmux session: canonical socket alias %q disappeared while legacy server pid %d is live", m.marker.CanonicalSocket, legacy.PID)
		}
		if canonical.PID != legacy.PID {
			return fmt.Errorf(
				"refuse to create a tmux session: legacy server %q (pid %d) and canonical server %q (pid %d) differ",
				m.marker.LegacySocket,
				legacy.PID,
				m.marker.CanonicalSocket,
				canonical.PID,
			)
		}
		if legacy.PID != m.marker.ServerPID {
			m.marker.ServerPID = legacy.PID
			m.marker.LegacySocketPath = legacy.SocketPath
			m.marker.CanonicalSocketPath = filepath.Join(filepath.Dir(legacy.SocketPath), m.marker.CanonicalSocket)
			if err := writeTmuxSocketMigrationMarker(m.path, m.marker); err != nil {
				return err
			}
		}
		return nil
	}

	if canonicalFound {
		if processAlive(m.marker.ServerPID) {
			return fmt.Errorf("refuse tmux socket cutover: legacy server pid %d is still alive without its socket", m.marker.ServerPID)
		}
		if err := removeTmuxSocketMigrationMarker(m.path); err != nil {
			return err
		}
		m.retired = true
		return nil
	}
	if processAlive(m.marker.ServerPID) {
		return fmt.Errorf("refuse tmux socket cutover: legacy server pid %d is still alive without its socket", m.marker.ServerPID)
	}
	if err := removeTmuxSocketAlias(m.marker); err != nil {
		return err
	}
	if err := removeTmuxSocketMigrationMarker(m.path); err != nil {
		return err
	}
	m.retired = true
	return nil
}

func inspectTmuxServer(tmuxPath, socketName string) (tmuxServerIdentity, bool, error) {
	command := exec.Command(
		tmuxPath,
		"-L", socketName,
		"list-sessions",
		"-F", "#{pid}\t#{socket_path}",
	)
	command.Env = tmuxEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		if isAbsentTmuxServer(output, err) {
			return tmuxServerIdentity{}, false, nil
		}
		return tmuxServerIdentity{}, false, tmuxCommandError("inspect tmux server", output, err)
	}

	var identity tmuxServerIdentity
	for _, line := range strings.FieldsFunc(string(output), func(character rune) bool { return character == '\n' || character == '\r' }) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return tmuxServerIdentity{}, false, fmt.Errorf("parse tmux server identity: %q", line)
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		socketPath := strings.TrimSpace(parts[1])
		if parseErr != nil || pid <= 0 || !filepath.IsAbs(socketPath) {
			return tmuxServerIdentity{}, false, fmt.Errorf("parse tmux server identity: %q", line)
		}
		if identity.PID == 0 {
			identity = tmuxServerIdentity{PID: pid, SocketPath: socketPath}
			continue
		}
		if identity.PID != pid || identity.SocketPath != socketPath {
			return tmuxServerIdentity{}, false, errors.New("tmux sessions reported inconsistent server identities")
		}
	}
	if identity.PID == 0 {
		return tmuxServerIdentity{}, false, nil
	}
	return identity, true, nil
}

func isAbsentTmuxServer(output []byte, err error) bool {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(string(output)))
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "no sessions") ||
		strings.Contains(message, "no such file or directory")
}

func createTmuxSocketAlias(aliasPath, legacyPath string) error {
	info, err := os.Lstat(aliasPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("refuse tmux socket migration: canonical path %q already exists and is not a compatibility symlink", aliasPath)
		}
		target, readErr := os.Readlink(aliasPath)
		if readErr != nil {
			return fmt.Errorf("read canonical tmux socket alias %q: %w", aliasPath, readErr)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(aliasPath), target)
		}
		if filepath.Clean(target) != filepath.Clean(legacyPath) {
			return fmt.Errorf("refuse tmux socket migration: canonical alias %q points to %q, want %q", aliasPath, target, legacyPath)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect canonical tmux socket path %q: %w", aliasPath, err)
	}
	if err := os.Symlink(legacyPath, aliasPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return createTmuxSocketAlias(aliasPath, legacyPath)
		}
		return fmt.Errorf("create canonical tmux socket alias %q: %w", aliasPath, err)
	}
	return nil
}

func removeTmuxSocketAlias(marker tmuxSocketMigrationMarker) error {
	info, err := os.Lstat(marker.CanonicalSocketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect canonical tmux socket alias %q: %w", marker.CanonicalSocketPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refuse to remove canonical tmux socket path %q because it is not a compatibility symlink", marker.CanonicalSocketPath)
	}
	target, err := os.Readlink(marker.CanonicalSocketPath)
	if err != nil {
		return fmt.Errorf("read canonical tmux socket alias %q: %w", marker.CanonicalSocketPath, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(marker.CanonicalSocketPath), target)
	}
	if filepath.Clean(target) != filepath.Clean(marker.LegacySocketPath) {
		return fmt.Errorf("refuse to remove canonical tmux socket alias %q because it points to %q", marker.CanonicalSocketPath, target)
	}
	if err := os.Remove(marker.CanonicalSocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove canonical tmux socket alias %q: %w", marker.CanonicalSocketPath, err)
	}
	return nil
}

func readTmuxSocketMigrationMarker(path string) (tmuxSocketMigrationMarker, bool, error) {
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return tmuxSocketMigrationMarker{}, false, nil
	}
	if err != nil {
		return tmuxSocketMigrationMarker{}, false, fmt.Errorf("read tmux socket migration marker: %w", err)
	}
	var marker tmuxSocketMigrationMarker
	if err := json.Unmarshal(contents, &marker); err != nil {
		return tmuxSocketMigrationMarker{}, false, fmt.Errorf("parse tmux socket migration marker: %w", err)
	}
	if err := validateTmuxSocketMigrationMarker(marker, marker.CanonicalSocket, marker.LegacySocket); err != nil {
		return tmuxSocketMigrationMarker{}, false, err
	}
	return marker, true, nil
}

func validateTmuxSocketMigrationMarker(marker tmuxSocketMigrationMarker, canonicalSocket, legacySocket string) error {
	if marker.Version != tmuxSocketMigrationMarkerVersion ||
		canonicalSocket == "" || legacySocket == "" || canonicalSocket == legacySocket ||
		marker.CanonicalSocket != canonicalSocket ||
		marker.LegacySocket != legacySocket ||
		marker.ServerPID <= 0 ||
		!filepath.IsAbs(marker.CanonicalSocketPath) ||
		!filepath.IsAbs(marker.LegacySocketPath) ||
		filepath.Base(marker.CanonicalSocketPath) != canonicalSocket ||
		filepath.Base(marker.LegacySocketPath) != legacySocket ||
		filepath.Dir(marker.CanonicalSocketPath) != filepath.Dir(marker.LegacySocketPath) {
		return errors.New("tmux socket migration marker is invalid")
	}
	return nil
}

func writeTmuxSocketMigrationMarker(path string, marker tmuxSocketMigrationMarker) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode tmux socket migration marker: %w", err)
	}
	contents = append(contents, '\n')
	if err := writeFileAtomically(path, contents, serverAtomicFileOptions{Mode: 0o600, SyncFile: true, SyncDirectory: true}); err != nil {
		return fmt.Errorf("write tmux socket migration marker: %w", err)
	}
	return nil
}

func removeTmuxSocketMigrationMarker(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove tmux socket migration marker: %w", err)
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
