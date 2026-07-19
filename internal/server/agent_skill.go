package server

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentSkillName              = "kiwi-code-processes"
	legacyProcessAgentSkillName = "dire-mux-processes"
	embeddedAgentSkillRoot      = "agent-skill"
)

var bundledAgentSkillNames = [...]string{"kiwi-code-processes", "dire-mux-threads", "dire-mux-mermaid"}

//go:generate node agent-skill/generate-common.mjs
//go:embed agent-skill/kiwi-code-processes agent-skill/dire-mux-threads agent-skill/dire-mux-mermaid
var embeddedAgentSkill embed.FS

type agentSkillItemStatus struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	UpToDate  bool   `json:"upToDate"`
}

type agentSkillStatus struct {
	Name      string                 `json:"name"`
	Path      string                 `json:"path"`
	Installed bool                   `json:"installed"`
	UpToDate  bool                   `json:"upToDate"`
	Skills    []agentSkillItemStatus `json:"skills,omitempty"`
}

type agentSkillInstaller struct {
	skillsDirectory string
}

func defaultAgentSkillsDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func newAgentSkillInstaller(skillsDirectory string) *agentSkillInstaller {
	return &agentSkillInstaller{skillsDirectory: skillsDirectory}
}

func (i *agentSkillInstaller) status() (agentSkillStatus, error) {
	status := agentSkillStatus{
		Name:      agentSkillName,
		Path:      filepath.Join(i.skillsDirectory, agentSkillName),
		Installed: true,
		UpToDate:  true,
		Skills:    make([]agentSkillItemStatus, 0, len(bundledAgentSkillNames)),
	}
	for _, name := range bundledAgentSkillNames {
		item, err := i.skillStatus(name)
		if err != nil {
			return agentSkillStatus{}, err
		}
		status.Skills = append(status.Skills, item)
		status.Installed = status.Installed && item.Installed
		status.UpToDate = status.UpToDate && item.UpToDate
	}
	return status, nil
}

func (i *agentSkillInstaller) skillStatus(name string) (agentSkillItemStatus, error) {
	target := filepath.Join(i.skillsDirectory, name)
	status := agentSkillItemStatus{Name: name, Path: target}
	if info, err := os.Stat(filepath.Join(target, "SKILL.md")); err == nil && !info.IsDir() {
		status.Installed = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return agentSkillItemStatus{}, fmt.Errorf("inspect installed agent skill %q: %w", name, err)
	}

	legacyInstalled := false
	if name == agentSkillName {
		legacyManifest := filepath.Join(i.skillsDirectory, legacyProcessAgentSkillName, "SKILL.md")
		if info, err := os.Stat(legacyManifest); err == nil && !info.IsDir() {
			legacyInstalled = true
			status.Installed = true
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return agentSkillItemStatus{}, fmt.Errorf("inspect legacy agent skill %q: %w", legacyProcessAgentSkillName, err)
		}
	}

	upToDate := true
	root := filepath.Join(embeddedAgentSkillRoot, name)
	err := fs.WalkDir(embeddedAgentSkill, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		expected, err := embeddedAgentSkill.ReadFile(path)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join(target, relative))
		if errors.Is(err, os.ErrNotExist) {
			upToDate = false
			return nil
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			upToDate = false
		}
		return nil
	})
	if err != nil {
		return agentSkillItemStatus{}, fmt.Errorf("inspect bundled agent skill %q: %w", name, err)
	}
	status.UpToDate = status.Installed && upToDate && !legacyInstalled
	return status, nil
}

func (i *agentSkillInstaller) install() (agentSkillStatus, error) {
	// Validate every root before writing any skill so a symlink or file at one
	// destination cannot leave a partially updated bundle.
	for _, name := range bundledAgentSkillNames {
		if err := i.validateSkillDestination(name); err != nil {
			return agentSkillStatus{}, err
		}
	}
	if err := i.validateSkillDestination(legacyProcessAgentSkillName); err != nil {
		return agentSkillStatus{}, err
	}
	if err := i.migrateLegacyProcessSkill(); err != nil {
		return agentSkillStatus{}, err
	}

	for _, name := range bundledAgentSkillNames {
		if err := i.installSkill(name); err != nil {
			return agentSkillStatus{}, err
		}
	}
	if err := os.RemoveAll(filepath.Join(i.skillsDirectory, legacyProcessAgentSkillName)); err != nil {
		return agentSkillStatus{}, fmt.Errorf("remove legacy agent skill %q: %w", legacyProcessAgentSkillName, err)
	}
	return i.status()
}

func (i *agentSkillInstaller) validateSkillDestination(name string) error {
	target := filepath.Join(i.skillsDirectory, name)
	info, err := os.Lstat(target)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("agent skill destination %q cannot be a symbolic link", name)
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("agent skill destination %q must be a directory", name)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect agent skill destination %q: %w", name, err)
	}
	return nil
}

func (i *agentSkillInstaller) migrateLegacyProcessSkill() error {
	legacyTarget := filepath.Join(i.skillsDirectory, legacyProcessAgentSkillName)
	if _, err := os.Lstat(legacyTarget); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect legacy agent skill %q: %w", legacyProcessAgentSkillName, err)
	}

	target := filepath.Join(i.skillsDirectory, agentSkillName)
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect agent skill destination %q: %w", agentSkillName, err)
	}
	if err := os.Rename(legacyTarget, target); err != nil {
		return fmt.Errorf("rename agent skill %q to %q: %w", legacyProcessAgentSkillName, agentSkillName, err)
	}
	return nil
}

func (i *agentSkillInstaller) installSkill(name string) error {
	target := filepath.Join(i.skillsDirectory, name)
	if err := ensureSkillDirectory(target); err != nil {
		return fmt.Errorf("create agent skill directory %q: %w", name, err)
	}

	root := filepath.Join(embeddedAgentSkillRoot, name)
	err := fs.WalkDir(embeddedAgentSkill, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return ensureSkillDirectory(destination)
		}
		contents, err := embeddedAgentSkill.ReadFile(path)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(destination, ".mjs") {
			mode = 0o755
		}
		return writeFileAtomically(destination, contents, serverAtomicFileOptions{
			Mode:     mode,
			SyncFile: true,
		})
	})
	if err != nil {
		return fmt.Errorf("install agent skill %q: %w", name, err)
	}
	return nil
}

func ensureSkillDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("agent skill directory cannot be a symbolic link")
	}
	if !info.IsDir() {
		return errors.New("agent skill path must be a directory")
	}
	return nil
}

func (s *Server) getAgentSkillStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.agentSkills.status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not inspect agent skills.")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) installAgentSkill(w http.ResponseWriter, _ *http.Request) {
	status, err := s.agentSkills.install()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not install agent skills.")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
