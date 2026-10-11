package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	claudeGlobalConfigFileName       = ".claude.json"
	claudeLegacyGlobalConfigFileName = ".config.json"
	claudeConfigLockTimeout          = 5 * time.Second
	claudeConfigLockRetryInterval    = 25 * time.Millisecond
)

// claudeWorkspaceTrustMu serializes Kiwi Code's own config updates; the lock
// directory coordinates them with running Claude processes.
var claudeWorkspaceTrustMu sync.Mutex

// trustClaudeLaunchWorkspace pre-accepts Claude Code's workspace trust dialog
// for a launch. An empty configDirectory means the launch inherits Kiwi Code's
// own CLAUDE_CONFIG_DIR. Failure only leaves Claude's dialog in place, so it
// never blocks the launch.
func (h *terminalHandler) trustClaudeLaunchWorkspace(configDirectory, workspace string) {
	configPath, err := h.claudeGlobalConfigFile(configDirectory)
	if err == nil {
		err = trustClaudeWorkspace(configPath, workspace)
	}
	if err != nil {
		log.Printf("trust Claude workspace: directory=%q error=%v", workspace, err)
	}
}

// claudeGlobalConfigFile mirrors where Claude Code keeps its global config,
// which records accepted workspace trust: a legacy .config.json in the config
// directory, otherwise .claude.json in CLAUDE_CONFIG_DIR when that is set or in
// the home directory when it is not.
func (h *terminalHandler) claudeGlobalConfigFile(configDirectory string) (string, error) {
	globalDirectory := configDirectory
	if configDirectory == "" {
		if h.claudeConfigErr != nil {
			return "", h.claudeConfigErr
		}
		if h.claudeConfigPath == "" {
			return "", errors.New("Claude config directory is unavailable")
		}
		configDirectory = h.claudeConfigPath
		globalDirectory = configDirectory
		if strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")) == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve Claude home directory: %w", err)
			}
			globalDirectory = home
		}
	}
	legacyPath := filepath.Join(configDirectory, claudeLegacyGlobalConfigFileName)
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}
	return filepath.Join(globalDirectory, claudeGlobalConfigFileName), nil
}

// trustClaudeWorkspace records the same projects[<directory>] trust entry that
// Claude Code saves when the user accepts its workspace trust dialog.
func trustClaudeWorkspace(configPath, workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return errors.New("workspace directory is unavailable")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace directory: %w", err)
	}
	keys := []string{workspace}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil && resolved != workspace {
		keys = append(keys, resolved)
	}

	claudeWorkspaceTrustMu.Lock()
	defer claudeWorkspaceTrustMu.Unlock()
	unlock, err := lockClaudeGlobalConfig(configPath)
	if err != nil {
		return err
	}
	defer unlock()

	// Update a symlinked config (for example, one managed by dotfiles) in place
	// instead of replacing the link with a regular file.
	targetPath := configPath
	if resolved, err := filepath.EvalSymlinks(configPath); err == nil {
		targetPath = resolved
	}
	config := map[string]json.RawMessage{}
	contents, err := os.ReadFile(targetPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(contents, &config); err != nil {
			return fmt.Errorf("decode Claude config: %w", err)
		}
		if config == nil {
			return errors.New("decode Claude config: expected a JSON object")
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read Claude config: %w", err)
	}

	projects := map[string]json.RawMessage{}
	if raw, ok := config["projects"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return fmt.Errorf("decode Claude config projects: %w", err)
		}
		if projects == nil {
			projects = map[string]json.RawMessage{}
		}
	}
	changed := false
	for _, key := range keys {
		entry := map[string]json.RawMessage{}
		if raw, ok := projects[key]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &entry); err != nil {
				return fmt.Errorf("decode Claude config project %q: %w", key, err)
			}
			if entry == nil {
				entry = map[string]json.RawMessage{}
			}
		}
		if string(entry["hasTrustDialogAccepted"]) == "true" {
			continue
		}
		entry["hasTrustDialogAccepted"] = json.RawMessage("true")
		encoded, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("encode Claude config project %q: %w", key, err)
		}
		projects[key] = encoded
		changed = true
	}
	if !changed {
		return nil
	}
	encodedProjects, err := json.Marshal(projects)
	if err != nil {
		return fmt.Errorf("encode Claude config projects: %w", err)
	}
	config["projects"] = encodedProjects

	var updated bytes.Buffer
	encoder := json.NewEncoder(&updated)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("encode Claude config: %w", err)
	}
	if err := writeFileAtomically(
		targetPath,
		updated.Bytes(),
		serverAtomicFileOptions{Mode: 0o600, SyncFile: true},
	); err != nil {
		return fmt.Errorf("write Claude config: %w", err)
	}
	return nil
}

// lockClaudeGlobalConfig takes the <config>.lock directory that Claude Code
// holds (through proper-lockfile) while it rewrites its global config, so a
// running Claude session cannot overwrite the trust entry with a stale copy.
// A lock left by a crashed process is never removed here; Claude reclaims it.
func lockClaudeGlobalConfig(configPath string) (func(), error) {
	lockPath := configPath + ".lock"
	deadline := time.Now().Add(claudeConfigLockTimeout)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock Claude config: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock Claude config: %s is still held", lockPath)
		}
		time.Sleep(claudeConfigLockRetryInterval)
	}
}
