package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// The Kiwi Sandbox reads its policy from two JSON files that are also edited by
// hand and by the bundled agent skill: the global file at
// ~/.config/kiwi-sandbox/sandbox.json and a per-directory file at
// <root>/.config/kiwi-sandbox.json. Fields absent from a file inherit from the
// previous layer, so the API keeps the sparse representation (nil = inherit)
// alongside the fully merged effective policy. The shapes and validation rules
// mirror kiwi-sandbox/packages/core/src/config.ts.

type sandboxFileAccess struct {
	Read  []string `json:"read"`
	Write []string `json:"write"`
}

type sandboxCommandRule struct {
	Patterns []string           `json:"patterns"`
	Files    *sandboxFileAccess `json:"files,omitempty"`
	Network  *bool              `json:"network,omitempty"`
}

type sandboxConfig struct {
	Defaults        *sandboxFileAccess    `json:"defaults,omitempty"`
	Commands        *[]sandboxCommandRule `json:"commands,omitempty"`
	Network         *bool                 `json:"network,omitempty"`
	Shell           *string               `json:"shell,omitempty"`
	RelatedProjects *[]string             `json:"relatedProjects,omitempty"`
}

type effectiveSandboxConfig struct {
	Defaults        sandboxFileAccess    `json:"defaults"`
	Commands        []sandboxCommandRule `json:"commands"`
	Network         bool                 `json:"network"`
	Shell           string               `json:"shell"`
	RelatedProjects []string             `json:"relatedProjects"`
}

type sandboxConfigState struct {
	Scope            string        `json:"scope"`
	Path             string        `json:"path"`
	Exists           bool          `json:"exists"`
	ParseError       string        `json:"parseError,omitempty"`
	GlobalParseError string        `json:"globalParseError,omitempty"`
	Config           sandboxConfig `json:"config"`
	// Inherited is the policy before this file is applied: the built-in
	// defaults for the global scope, defaults plus the global file for threads.
	Inherited effectiveSandboxConfig `json:"inherited"`
	Effective effectiveSandboxConfig `json:"effective"`
}

var sandboxRuntimeReadPaths = []string{
	"/bin", "/sbin", "/usr", "/System", "/Library", "/opt/homebrew", "/private/etc", "/dev",
}

func defaultEffectiveSandboxConfig() effectiveSandboxConfig {
	return effectiveSandboxConfig{
		Defaults: sandboxFileAccess{
			Read:  append([]string{"$CWD"}, sandboxRuntimeReadPaths...),
			Write: []string{"$CWD", "$TMPDIR"},
		},
		Commands:        []sandboxCommandRule{},
		Network:         false,
		Shell:           "/bin/zsh",
		RelatedProjects: []string{},
	}
}

func globalSandboxConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "kiwi-sandbox", "sandbox.json"), nil
}

func threadSandboxRoot(thread project.Thread) string {
	if thread.WorktreePath != "" {
		return thread.WorktreePath
	}
	return thread.Cwd
}

func threadSandboxConfigPath(thread project.Thread) string {
	return filepath.Join(threadSandboxRoot(thread), ".config", "kiwi-sandbox.json")
}

func applySandboxConfig(base effectiveSandboxConfig, overlay sandboxConfig) effectiveSandboxConfig {
	if overlay.Defaults != nil {
		base.Defaults = *overlay.Defaults
	}
	if overlay.Commands != nil {
		base.Commands = *overlay.Commands
	}
	if overlay.Network != nil {
		base.Network = *overlay.Network
	}
	if overlay.Shell != nil {
		base.Shell = *overlay.Shell
	}
	if overlay.RelatedProjects != nil {
		base.RelatedProjects = *overlay.RelatedProjects
	}
	return base
}

func sandboxConfigIsEmpty(config sandboxConfig) bool {
	return config.Defaults == nil && config.Commands == nil && config.Network == nil &&
		config.Shell == nil && config.RelatedProjects == nil
}

// readSandboxConfigFile loads a sparse config from disk. A missing file is not
// an error; any other failure (unreadable file, invalid JSON, schema violation)
// is returned so callers can surface it as a parse error without failing the
// whole request.
func readSandboxConfigFile(path string, allowRelatedProjects bool) (config sandboxConfig, exists bool, err error) {
	data, readErr := os.ReadFile(path)
	if errors.Is(readErr, os.ErrNotExist) {
		return sandboxConfig{}, false, nil
	}
	if readErr != nil {
		return sandboxConfig{}, true, readErr
	}
	config, err = parseSandboxConfig(data, allowRelatedProjects)
	return config, true, err
}

func parseSandboxConfig(data []byte, allowRelatedProjects bool) (sandboxConfig, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return sandboxConfig{}, errors.New("configuration must be a JSON object")
	}
	if err := assertKnownSandboxKeys(raw, "defaults", "commands", "network", "shell", "relatedProjects"); err != nil {
		return sandboxConfig{}, err
	}

	var config sandboxConfig
	if value, ok := raw["defaults"]; ok {
		files, err := parseSandboxFileAccess(value, "defaults")
		if err != nil {
			return sandboxConfig{}, err
		}
		config.Defaults = &files
	}
	if value, ok := raw["commands"]; ok {
		rules, err := parseSandboxCommands(value)
		if err != nil {
			return sandboxConfig{}, err
		}
		config.Commands = &rules
	}
	if value, ok := raw["network"]; ok {
		var network bool
		if err := json.Unmarshal(value, &network); err != nil {
			return sandboxConfig{}, errors.New("network must be boolean")
		}
		config.Network = &network
	}
	if value, ok := raw["shell"]; ok {
		var shell string
		if err := json.Unmarshal(value, &shell); err != nil || !filepath.IsAbs(shell) {
			return sandboxConfig{}, errors.New("shell must be an absolute path")
		}
		config.Shell = &shell
	}
	if value, ok := raw["relatedProjects"]; ok {
		if !allowRelatedProjects {
			return sandboxConfig{}, errors.New("relatedProjects is only allowed in the per-directory config")
		}
		var relatedProjects []string
		if err := json.Unmarshal(value, &relatedProjects); err != nil || relatedProjects == nil {
			return sandboxConfig{}, errors.New("relatedProjects must be a string array")
		}
		for _, entry := range relatedProjects {
			if strings.TrimSpace(entry) == "" {
				return sandboxConfig{}, errors.New("relatedProjects must not contain empty paths")
			}
		}
		config.RelatedProjects = &relatedProjects
	}
	return config, nil
}

func parseSandboxFileAccess(data json.RawMessage, label string) (sandboxFileAccess, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return sandboxFileAccess{}, fmt.Errorf("%s must be an object", label)
	}
	if err := assertKnownSandboxKeys(raw, "read", "write"); err != nil {
		return sandboxFileAccess{}, fmt.Errorf("%s: %w", label, err)
	}
	var files sandboxFileAccess
	if err := json.Unmarshal(raw["read"], &files.Read); err != nil || files.Read == nil {
		return sandboxFileAccess{}, fmt.Errorf("%s.read must be a string array", label)
	}
	if err := json.Unmarshal(raw["write"], &files.Write); err != nil || files.Write == nil {
		return sandboxFileAccess{}, fmt.Errorf("%s.write must be a string array", label)
	}
	return files, nil
}

func parseSandboxCommands(data json.RawMessage) ([]sandboxCommandRule, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil || entries == nil {
		return nil, errors.New("commands must be an array")
	}
	rules := make([]sandboxCommandRule, 0, len(entries))
	for index, entry := range entries {
		label := fmt.Sprintf("commands[%d]", index)
		var pattern string
		if err := json.Unmarshal(entry, &pattern); err == nil {
			if strings.TrimSpace(pattern) == "" {
				return nil, fmt.Errorf("%s must be a non-empty string", label)
			}
			rules = append(rules, sandboxCommandRule{Patterns: []string{pattern}})
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(entry, &raw); err != nil || raw == nil {
			return nil, fmt.Errorf("%s must be a string or object", label)
		}
		if err := assertKnownSandboxKeys(raw, "pattern", "files", "network"); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		rule := sandboxCommandRule{}
		var single string
		if err := json.Unmarshal(raw["pattern"], &single); err == nil {
			rule.Patterns = []string{single}
		} else if err := json.Unmarshal(raw["pattern"], &rule.Patterns); err != nil {
			return nil, fmt.Errorf("%s.pattern must be a non-empty string or string array", label)
		}
		if len(rule.Patterns) == 0 {
			return nil, fmt.Errorf("%s.pattern must be a non-empty string or string array", label)
		}
		for _, candidate := range rule.Patterns {
			if strings.TrimSpace(candidate) == "" {
				return nil, fmt.Errorf("%s.pattern must be a non-empty string or string array", label)
			}
		}
		if value, ok := raw["files"]; ok {
			files, err := parseSandboxFileAccess(value, label+".files")
			if err != nil {
				return nil, err
			}
			rule.Files = &files
		}
		if value, ok := raw["network"]; ok {
			var network bool
			if err := json.Unmarshal(value, &network); err != nil {
				return nil, fmt.Errorf("%s.network must be boolean", label)
			}
			rule.Network = &network
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func assertKnownSandboxKeys(raw map[string]json.RawMessage, allowed ...string) error {
	for key := range raw {
		known := false
		for _, candidate := range allowed {
			if key == candidate {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

// normalizeSandboxConfig trims all configured values and rejects entries the
// sandbox itself would refuse to load.
func normalizeSandboxConfig(config sandboxConfig, allowRelatedProjects bool) (sandboxConfig, error) {
	if config.Defaults != nil {
		files := normalizeSandboxFileAccess(*config.Defaults)
		config.Defaults = &files
	}
	if config.Commands != nil {
		rules := make([]sandboxCommandRule, 0, len(*config.Commands))
		for index, rule := range *config.Commands {
			patterns := trimmedSandboxEntries(rule.Patterns)
			if len(patterns) == 0 {
				return sandboxConfig{}, fmt.Errorf("Command rule %d needs at least one pattern.", index+1)
			}
			normalized := sandboxCommandRule{Patterns: patterns, Network: rule.Network}
			if rule.Files != nil {
				files := normalizeSandboxFileAccess(*rule.Files)
				normalized.Files = &files
			}
			rules = append(rules, normalized)
		}
		config.Commands = &rules
	}
	if config.Shell != nil {
		shell := strings.TrimSpace(*config.Shell)
		if !filepath.IsAbs(shell) {
			return sandboxConfig{}, errors.New("The shell must be an absolute path.")
		}
		config.Shell = &shell
	}
	if config.RelatedProjects != nil {
		if !allowRelatedProjects {
			return sandboxConfig{}, errors.New("Related projects are only supported in thread sandbox configs.")
		}
		relatedProjects := trimmedSandboxEntries(*config.RelatedProjects)
		config.RelatedProjects = &relatedProjects
	}
	return config, nil
}

func normalizeSandboxFileAccess(files sandboxFileAccess) sandboxFileAccess {
	return sandboxFileAccess{
		Read:  trimmedSandboxEntries(files.Read),
		Write: trimmedSandboxEntries(files.Write),
	}
}

func trimmedSandboxEntries(entries []string) []string {
	trimmed := make([]string, 0, len(entries))
	for _, entry := range entries {
		if value := strings.TrimSpace(entry); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

// diskSandboxCommandRule matches the on-disk shape, which uses a "pattern"
// field holding either one string or an array.
type diskSandboxCommandRule struct {
	Pattern any                `json:"pattern"`
	Files   *sandboxFileAccess `json:"files,omitempty"`
	Network *bool              `json:"network,omitempty"`
}

type diskSandboxConfig struct {
	Defaults        *sandboxFileAccess        `json:"defaults,omitempty"`
	Commands        *[]diskSandboxCommandRule `json:"commands,omitempty"`
	Network         *bool                     `json:"network,omitempty"`
	Shell           *string                   `json:"shell,omitempty"`
	RelatedProjects *[]string                 `json:"relatedProjects,omitempty"`
}

func writeSandboxConfigFile(path string, config sandboxConfig) error {
	if sandboxConfigIsEmpty(config) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove sandbox config: %w", err)
		}
		return nil
	}
	disk := diskSandboxConfig{
		Defaults:        config.Defaults,
		Network:         config.Network,
		Shell:           config.Shell,
		RelatedProjects: config.RelatedProjects,
	}
	if config.Commands != nil {
		rules := make([]diskSandboxCommandRule, 0, len(*config.Commands))
		for _, rule := range *config.Commands {
			var pattern any = rule.Patterns
			if len(rule.Patterns) == 1 {
				pattern = rule.Patterns[0]
			}
			rules = append(rules, diskSandboxCommandRule{
				Pattern: pattern,
				Files:   rule.Files,
				Network: rule.Network,
			})
		}
		disk.Commands = &rules
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sandbox config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create sandbox config directory: %w", err)
	}
	return writeFileAtomically(path, append(data, '\n'), serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	})
}

func globalSandboxConfigState() (sandboxConfigState, error) {
	path, err := globalSandboxConfigPath()
	if err != nil {
		return sandboxConfigState{}, err
	}
	config, exists, parseErr := readSandboxConfigFile(path, false)
	state := sandboxConfigState{
		Scope:     "global",
		Path:      path,
		Exists:    exists,
		Config:    config,
		Inherited: defaultEffectiveSandboxConfig(),
		Effective: applySandboxConfig(defaultEffectiveSandboxConfig(), config),
	}
	if parseErr != nil {
		state.ParseError = parseErr.Error()
	}
	return state, nil
}

func threadSandboxConfigState(thread project.Thread) (sandboxConfigState, error) {
	globalPath, err := globalSandboxConfigPath()
	if err != nil {
		return sandboxConfigState{}, err
	}
	globalConfig, _, globalParseErr := readSandboxConfigFile(globalPath, false)

	path := threadSandboxConfigPath(thread)
	config, exists, parseErr := readSandboxConfigFile(path, true)
	inherited := applySandboxConfig(defaultEffectiveSandboxConfig(), globalConfig)
	state := sandboxConfigState{
		Scope:     "thread",
		Path:      path,
		Exists:    exists,
		Config:    config,
		Inherited: inherited,
		Effective: applySandboxConfig(inherited, config),
	}
	if parseErr != nil {
		state.ParseError = parseErr.Error()
	}
	if globalParseErr != nil {
		state.GlobalParseError = globalParseErr.Error()
	}
	return state, nil
}

func (s *Server) getGlobalSandboxConfig(w http.ResponseWriter, _ *http.Request) {
	state, err := globalSandboxConfigState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the sandbox configuration.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) updateGlobalSandboxConfig(w http.ResponseWriter, r *http.Request) {
	config, ok := decodeSandboxConfig(w, r)
	if !ok {
		return
	}
	normalized, err := normalizeSandboxConfig(config, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path, err := globalSandboxConfigPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve the sandbox configuration path.")
		return
	}
	if err := writeSandboxConfigFile(path, normalized); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save the sandbox configuration.")
		return
	}
	state, err := globalSandboxConfigState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the sandbox configuration.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) getThreadSandboxConfig(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.resolveSandboxThread(w, r)
	if !ok {
		return
	}
	state, err := threadSandboxConfigState(thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the sandbox configuration.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) updateThreadSandboxConfig(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.resolveSandboxThread(w, r)
	if !ok {
		return
	}
	config, decoded := decodeSandboxConfig(w, r)
	if !decoded {
		return
	}
	normalized, err := normalizeSandboxConfig(config, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root := threadSandboxRoot(thread)
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "The thread directory does not exist.")
		return
	}
	if err := writeSandboxConfigFile(threadSandboxConfigPath(thread), normalized); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save the sandbox configuration.")
		return
	}
	state, err := threadSandboxConfigState(thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the sandbox configuration.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) resolveSandboxThread(w http.ResponseWriter, r *http.Request) (project.Thread, bool) {
	_, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return project.Thread{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return project.Thread{}, false
	}
	return thread, true
}

func decodeSandboxConfig(w http.ResponseWriter, r *http.Request) (sandboxConfig, bool) {
	var config sandboxConfig
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid sandbox configuration.")
		return sandboxConfig{}, false
	}
	return config, true
}
