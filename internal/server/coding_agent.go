package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	codingAgentModelDiscoveryTimeout = 5 * time.Second
	codingAgentModelCacheDuration    = 15 * time.Second
)

type codingAgentChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type codingAgentConfig struct {
	ID             string              `json:"id"`
	Label          string              `json:"label"`
	Models         []codingAgentChoice `json:"models"`
	ThinkingLevels []codingAgentChoice `json:"thinkingLevels"`
}

type piModelCapability struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	SupportsThinking bool     `json:"supportsThinking"`
	ReasoningLevels  []string `json:"reasoningLevels"`
}

type codingAgentLaunchOptions struct {
	Model                string
	ThinkingLevel        string
	InitialPrompt        string
	AppendSystemPrompt   string
	AllowPendingCreation bool
}

type piModelCapabilityCacheEntry struct {
	models   []piModelCapability
	cachedAt time.Time
}

type piModelCapabilityInflight struct {
	done   chan struct{}
	models []piModelCapability
	err    error
}

var supportedThinkingLevels = []codingAgentChoice{
	{ID: "off", Label: "Off"},
	{ID: "minimal", Label: "Minimal"},
	{ID: "low", Label: "Low"},
	{ID: "medium", Label: "Medium"},
	{ID: "high", Label: "High"},
	{ID: "xhigh", Label: "Extra high"},
	{ID: "max", Label: "Maximum"},
}

func codingAgentThinkingLevels(defaultLabel string, includeOffAndMinimal bool) []codingAgentChoice {
	first := 0
	if !includeOffAndMinimal {
		first = 2
	}
	levels := make([]codingAgentChoice, 1, 1+len(supportedThinkingLevels)-first)
	levels[0] = codingAgentChoice{ID: "", Label: defaultLabel}
	return append(levels, supportedThinkingLevels[first:]...)
}

var piThinkingLevels = codingAgentThinkingLevels("Use Pi default", true)

// Claude's ultracode effort is passed through to Claude Code itself. It is
// independent from the Pi-only Kiwi Code workflow integration.
var claudeThinkingLevels = append(codingAgentThinkingLevels("Use Claude default", false), codingAgentChoice{ID: "ultracode", Label: "Ultracode (Claude built-in)"})
var claudeGPTThinkingLevels = append(codingAgentThinkingLevels("Use model default", false), codingAgentChoice{ID: "ultracode", Label: "Ultracode (Claude built-in)"})

var claudeModels = []codingAgentChoice{
	{ID: "", Label: "Use Claude default"},
	{ID: "sonnet", Label: "Claude Sonnet (latest)"},
	{ID: "opus", Label: "Claude Opus (latest)"},
	{ID: "haiku", Label: "Claude Haiku (latest)"},
	{ID: "fable", Label: "Claude Fable (latest)"},
}

func defaultPiReasoningLevels(supportsThinking bool) []string {
	if !supportsThinking {
		return []string{"off"}
	}
	levels := make([]string, 0, len(supportedThinkingLevels))
	for _, level := range supportedThinkingLevels {
		levels = append(levels, level.ID)
	}
	return levels
}

func explicitPiReasoningLevels(model piModelCapability) []string {
	return append([]string(nil), model.ReasoningLevels...)
}

func (h *terminalHandler) listCodingAgents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), codingAgentModelDiscoveryTimeout)
	defer cancel()

	discoveryCwd := ""
	if projectID := strings.TrimSpace(r.URL.Query().Get("projectId")); projectID != "" {
		if h.projects == nil {
			writeError(w, http.StatusNotFound, "Project not found.")
			return
		}
		item, err := h.projects.Get(projectID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Project not found.")
			return
		}
		discoveryCwd = item.Path
	}

	type modelDiscoveryResult struct {
		piModels  []piModelCapability
		gptModels []codingAgentChoice
	}
	piResult := make(chan []piModelCapability, 1)
	claudeGPTResult := make(chan []codingAgentChoice, 1)
	go func() {
		discovered, _ := h.availablePiModelCapabilities(ctx, discoveryCwd, false)
		piResult <- discovered
	}()
	go func() {
		discovered, _ := h.availableCLIProxyAPIGPTModels(ctx)
		claudeGPTResult <- discovered
	}()
	result := modelDiscoveryResult{
		piModels:  <-piResult,
		gptModels: <-claudeGPTResult,
	}

	piModels := []codingAgentChoice{{ID: "", Label: "Use Pi default"}}
	for _, model := range result.piModels {
		piModels = append(piModels, codingAgentChoice{ID: model.ID, Label: model.Label})
	}
	claudeGPTModels := result.gptModels
	if claudeGPTModels == nil {
		claudeGPTModels = []codingAgentChoice{}
	}

	writeJSON(w, http.StatusOK, []codingAgentConfig{
		{
			ID:             codingAgentPi,
			Label:          "Pi",
			Models:         piModels,
			ThinkingLevels: piThinkingLevels,
		},
		{
			ID:             codingAgentClaude,
			Label:          "Claude Code",
			Models:         claudeModels,
			ThinkingLevels: claudeThinkingLevels,
		},
		{
			ID:             codingAgentClaudeGPT,
			Label:          "Claude Code (with gpt)",
			Models:         claudeGPTModels,
			ThinkingLevels: claudeGPTThinkingLevels,
		},
	})
}

func clonePiModelCapabilities(models []piModelCapability) []piModelCapability {
	cloned := make([]piModelCapability, len(models))
	for index, model := range models {
		cloned[index] = model
		cloned[index].ReasoningLevels = append([]string(nil), model.ReasoningLevels...)
	}
	return cloned
}

func (h *terminalHandler) availablePiModelCapabilities(ctx context.Context, discoveryCwd string, approveProjectExtensions bool) ([]piModelCapability, error) {
	if h.piExtensionErr != nil {
		return nil, h.piExtensionErr
	}
	path := ""
	if h.nativePi != nil {
		path = h.nativePi.piPath
	}
	if path == "" {
		var err error
		path, err = exec.LookPath(codingAgentPi)
		if err != nil {
			return nil, err
		}
	}
	key := path + "\x00" + discoveryCwd + "\x00" + strconv.FormatBool(approveProjectExtensions)

	h.piModelMu.Lock()
	now := time.Now()
	for candidate, entry := range h.piModelCache {
		if now.Sub(entry.cachedAt) >= codingAgentModelCacheDuration {
			delete(h.piModelCache, candidate)
		}
	}
	if entry, found := h.piModelCache[key]; found && len(entry.models) > 0 {
		models := clonePiModelCapabilities(entry.models)
		h.piModelMu.Unlock()
		return models, nil
	}
	if inflight := h.piModelInflight[key]; inflight != nil {
		done := inflight.done
		h.piModelMu.Unlock()
		select {
		case <-done:
			if inflight.err != nil {
				return nil, inflight.err
			}
			return clonePiModelCapabilities(inflight.models), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if h.piModelInflight == nil {
		h.piModelInflight = make(map[string]*piModelCapabilityInflight)
	}
	inflight := &piModelCapabilityInflight{done: make(chan struct{})}
	h.piModelInflight[key] = inflight
	h.piModelMu.Unlock()

	models, err := discoverPiModelCapabilitiesAtPathInDirectory(ctx, path, discoveryCwd, approveProjectExtensions, h.piExtensionPaths...)
	result := clonePiModelCapabilities(models)

	h.piModelMu.Lock()
	if err == nil {
		if h.piModelCache == nil {
			h.piModelCache = make(map[string]piModelCapabilityCacheEntry)
		}
		h.piModelCache[key] = piModelCapabilityCacheEntry{models: result, cachedAt: time.Now()}
		inflight.models = result
	}
	inflight.err = err
	delete(h.piModelInflight, key)
	close(inflight.done)
	h.piModelMu.Unlock()

	if err != nil {
		return nil, err
	}
	return clonePiModelCapabilities(result), nil
}

func discoverPiModels(ctx context.Context) ([]codingAgentChoice, error) {
	path, err := exec.LookPath(codingAgentPi)
	if err != nil {
		return nil, err
	}
	capabilities, err := discoverPiModelCapabilitiesAtPath(ctx, path)
	if err != nil {
		return nil, err
	}
	models := make([]codingAgentChoice, 0, len(capabilities))
	for _, capability := range capabilities {
		models = append(models, codingAgentChoice{ID: capability.ID, Label: capability.Label})
	}
	return models, nil
}

func discoverPiModelCapabilitiesAtPath(ctx context.Context, path string, extensionPaths ...string) ([]piModelCapability, error) {
	return discoverPiModelCapabilitiesAtPathInDirectory(ctx, path, "", false, extensionPaths...)
}

func discoverPiModelCapabilitiesAtPathInDirectory(ctx context.Context, path, cwd string, approveProjectExtensions bool, extensionPaths ...string) ([]piModelCapability, error) {
	models, err := discoverPiModelCapabilitiesFromRPCInDirectory(ctx, path, cwd, approveProjectExtensions, extensionPaths...)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, errors.New("Pi RPC reported no available models")
	}
	return models, nil
}

func discoverPiModelCapabilitiesFromRPC(ctx context.Context, path string, extensionPaths ...string) ([]piModelCapability, error) {
	return discoverPiModelCapabilitiesFromRPCInDirectory(ctx, path, "", false, extensionPaths...)
}

func discoverPiModelCapabilitiesFromRPCInDirectory(ctx context.Context, path, cwd string, approveProjectExtensions bool, extensionPaths ...string) ([]piModelCapability, error) {
	arguments := []string{
		"--mode", "rpc",
		"--no-session",
		"--no-skills",
		"--no-prompt-templates",
		"--no-context-files",
		"--no-tools",
		"--offline",
	}
	if approveProjectExtensions {
		arguments = append(arguments, "--approve")
	}
	for _, extensionPath := range extensionPaths {
		arguments = append(arguments, "--extension", extensionPath)
	}
	command := exec.CommandContext(ctx, path, arguments...)
	if cwd != "" {
		command.Dir = cwd
	}
	command.Env = append(os.Environ(), "NO_COLOR=1", "PI_SKIP_VERSION_CHECK=1")
	command.Stdin = strings.NewReader("{\"type\":\"get_available_models\"}\n")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return parsePiRPCModelCapabilities(output)
}

func parsePiRPCModelCapabilities(output []byte) ([]piModelCapability, error) {
	var response struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Success bool   `json:"success"`
		Data    struct {
			Models []struct {
				ID               string             `json:"id"`
				Name             string             `json:"name"`
				Provider         string             `json:"provider"`
				Reasoning        bool               `json:"reasoning"`
				ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
			} `json:"models"`
		} `json:"data"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		var candidate = response
		if err := json.Unmarshal(scanner.Bytes(), &candidate); err != nil ||
			candidate.Type != "response" || candidate.Command != "get_available_models" || !candidate.Success {
			continue
		}
		models := make([]piModelCapability, 0, len(candidate.Data.Models))
		seen := make(map[string]struct{}, len(candidate.Data.Models))
		for _, model := range candidate.Data.Models {
			provider := strings.TrimSpace(model.Provider)
			modelID := strings.TrimSpace(model.ID)
			if provider == "" || modelID == "" {
				continue
			}
			id := provider + "/" + modelID
			if !validCodingAgentModel(id) {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			label := strings.TrimSpace(model.Name)
			if label == "" {
				label = modelID
			}
			models = append(models, piModelCapability{
				ID:               id,
				Label:            label + " · " + provider,
				SupportsThinking: model.Reasoning,
				ReasoningLevels:  piRPCReasoningLevels(model.Reasoning, model.ThinkingLevelMap),
			})
		}
		return models, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Pi RPC did not return available models")
}

func piRPCReasoningLevels(reasoning bool, levelMap map[string]*string) []string {
	if !reasoning {
		return []string{"off"}
	}
	levels := make([]string, 0, len(supportedThinkingLevels))
	for _, level := range supportedThinkingLevels {
		mapped, configured := levelMap[level.ID]
		if configured {
			if mapped != nil {
				levels = append(levels, level.ID)
			}
			continue
		}
		if level.ID != "xhigh" && level.ID != "max" {
			levels = append(levels, level.ID)
		}
	}
	return levels
}

func parsePiModels(output []byte) []codingAgentChoice {
	capabilities := parsePiModelCapabilities(output)
	models := make([]codingAgentChoice, 0, len(capabilities))
	for _, capability := range capabilities {
		models = append(models, codingAgentChoice{ID: capability.ID, Label: capability.Label})
	}
	return models
}

func parsePiModelCapabilities(output []byte) []piModelCapability {
	models := make([]piModelCapability, 0)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || (fields[4] != "yes" && fields[4] != "no") {
			continue
		}
		id := fields[0] + "/" + fields[1]
		if !validCodingAgentModel(id) {
			continue
		}
		if _, found := seen[id]; found {
			continue
		}
		seen[id] = struct{}{}
		supportsThinking := fields[4] == "yes"
		models = append(models, piModelCapability{
			ID:               id,
			Label:            fields[1] + " · " + fields[0],
			SupportsThinking: supportsThinking,
			ReasoningLevels:  defaultPiReasoningLevels(supportsThinking),
		})
	}
	return models
}

var (
	errPiModelUnavailable         = errors.New("the requested Pi model is not available")
	errPiModelRequiredForThinking = errors.New("choose an explicit Pi model when setting a reasoning level")
	errPiThinkingLevelUnsupported = errors.New("the requested Pi model does not support that reasoning level")
)

func validatePiModelLaunchOptions(models []piModelCapability, options codingAgentLaunchOptions) error {
	if options.Model == "" {
		if options.ThinkingLevel != "" {
			return errPiModelRequiredForThinking
		}
		return nil
	}
	var selected *piModelCapability
	for index := range models {
		if models[index].ID == options.Model {
			selected = &models[index]
			break
		}
	}
	if selected == nil {
		return errPiModelUnavailable
	}
	if options.ThinkingLevel == "" {
		return nil
	}
	for _, level := range explicitPiReasoningLevels(*selected) {
		if level == options.ThinkingLevel {
			return nil
		}
	}
	return errPiThinkingLevelUnsupported
}

func normalizeCodingAgentLaunchOptions(agent, model, thinkingLevel string) (codingAgentLaunchOptions, error) {
	agent, err := normalizeCodingAgent(agent)
	if err != nil {
		return codingAgentLaunchOptions{}, err
	}
	model = strings.TrimSpace(model)
	if model != "" && !validCodingAgentModel(model) {
		return codingAgentLaunchOptions{}, errors.New("invalid coding agent model")
	}
	thinkingLevel = strings.TrimSpace(thinkingLevel)
	levels := piThinkingLevels
	if agent == codingAgentClaude {
		levels = claudeThinkingLevels
	}
	if agent == codingAgentClaudeGPT {
		if model != "" && !isCLIProxyAPIGPTModel(model) {
			return codingAgentLaunchOptions{}, errors.New("Claude Code (with gpt) requires a GPT model from CLIProxyAPI")
		}
		levels = claudeGPTThinkingLevels
	}
	if !codingAgentChoiceExists(levels, thinkingLevel) {
		return codingAgentLaunchOptions{}, errors.New("unknown coding agent thinking level")
	}
	return codingAgentLaunchOptions{Model: model, ThinkingLevel: thinkingLevel}, nil
}

func validCodingAgentModel(model string) bool {
	if model == "" || len(model) > 256 || !utf8.ValidString(model) || strings.HasPrefix(model, "-") {
		return false
	}
	for _, character := range model {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func codingAgentChoiceExists(choices []codingAgentChoice, id string) bool {
	for _, choice := range choices {
		if choice.ID == id {
			return true
		}
	}
	return false
}
