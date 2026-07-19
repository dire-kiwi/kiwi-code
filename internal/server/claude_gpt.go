package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultCLIProxyAPIBaseURL     = "http://127.0.0.1:8317"
	defaultCLIProxyAPIKey         = "sk-dummy"
	defaultClaudeGPTOpusModel     = "gpt-5.6-sol"
	defaultClaudeGPTSonnetModel   = "gpt-5.6-terra"
	defaultClaudeGPTHaikuModel    = "gpt-5.6-luna"
	claudeGPTProfileDirectoryName = "claude-code-gpt-profile"
	claudeSandboxPluginID         = "sandbox-exec@dire-agent-extensions"
	maxCLIProxyAPIModelsResponse  = 1 << 20
	cliProxyAPIBaseURLEnvironment = "DIRE_MUX_CLIPROXY_BASE_URL"
	cliProxyAPIKeyEnvironment     = "DIRE_MUX_CLIPROXY_API_KEY"
)

var claudeGPTUnsetEnvironment = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_CUSTOM_HEADERS",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_VERTEX",
}

type claudeInstalledPluginRegistry struct {
	Plugins map[string][]struct {
		InstallPath string `json:"installPath"`
	} `json:"plugins"`
}

func configuredCLIProxyAPI() (string, string, error) {
	baseURL := strings.TrimSpace(os.Getenv(cliProxyAPIBaseURLEnvironment))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("CLIPROXY_API_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = defaultCLIProxyAPIBaseURL
	}
	baseURL, err := normalizeCLIProxyAPIBaseURL(baseURL)
	if err != nil {
		return "", "", err
	}

	apiKey := strings.TrimSpace(os.Getenv(cliProxyAPIKeyEnvironment))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("CLIPROXY_API_KEY"))
	}
	if apiKey == "" {
		apiKey = defaultCLIProxyAPIKey
	}
	if strings.ContainsRune(apiKey, '\x00') || strings.ContainsAny(apiKey, "\r\n") {
		return "", "", errors.New("CLIProxyAPI key contains an invalid character")
	}
	return baseURL, apiKey, nil
}

func normalizeCLIProxyAPIBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("CLIProxyAPI base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("CLIProxyAPI base URL cannot include a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func cliProxyAPIModelsURL(baseURL string) (string, error) {
	normalized, err := normalizeCLIProxyAPIBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(normalized)
	if strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path += "/models"
	} else {
		parsed.Path += "/v1/models"
	}
	return parsed.String(), nil
}

func discoverCLIProxyAPIGPTModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]codingAgentChoice, error) {
	modelsURL, err := cliProxyAPIModelsURL(baseURL)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("CLIProxyAPI model request returned %s", response.Status)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxCLIProxyAPIModelsResponse+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxCLIProxyAPIModelsResponse {
		return nil, errors.New("CLIProxyAPI model response is too large")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(contents, &payload); err != nil {
		return nil, fmt.Errorf("decode CLIProxyAPI models: %w", err)
	}

	models := make([]codingAgentChoice, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, candidate := range payload.Data {
		modelID := strings.TrimSpace(candidate.ID)
		if !isCLIProxyAPIGPTModel(modelID) || !validCodingAgentModel(modelID) {
			continue
		}
		if _, duplicate := seen[modelID]; duplicate {
			continue
		}
		seen[modelID] = struct{}{}
		models = append(models, codingAgentChoice{ID: modelID, Label: modelID})
	}
	if len(models) == 0 {
		return nil, errors.New("CLIProxyAPI reported no GPT models")
	}
	return models, nil
}

func isCLIProxyAPIGPTModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-")
}

func (h *terminalHandler) cliProxyAPIConfiguration() (string, string, error) {
	if h != nil && h.cliProxyAPIErr != nil {
		return "", "", h.cliProxyAPIErr
	}
	if h != nil && h.cliProxyAPIBaseURL != "" {
		return h.cliProxyAPIBaseURL, h.cliProxyAPIKey, nil
	}
	return configuredCLIProxyAPI()
}

func (h *terminalHandler) availableCLIProxyAPIGPTModels(ctx context.Context) ([]codingAgentChoice, error) {
	baseURL, apiKey, err := h.cliProxyAPIConfiguration()
	if err != nil {
		return nil, err
	}
	var client *http.Client
	if h != nil {
		client = h.cliProxyAPIHTTPClient
	}
	return discoverCLIProxyAPIGPTModels(ctx, client, baseURL, apiKey)
}

func prepareClaudeGPTProfileDirectory(dataDirectory string) (string, error) {
	if strings.TrimSpace(dataDirectory) == "" {
		return "", errors.New("Claude GPT profile data directory is unavailable")
	}
	profilePath, err := filepath.Abs(filepath.Join(dataDirectory, claudeGPTProfileDirectoryName))
	if err != nil {
		return "", fmt.Errorf("resolve Claude GPT profile directory: %w", err)
	}
	if err := secureClaudeGPTProfileDirectory(profilePath); err != nil {
		return "", err
	}
	return profilePath, nil
}

func secureClaudeGPTProfileDirectory(profilePath string) error {
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return fmt.Errorf("create Claude GPT profile directory: %w", err)
	}
	info, err := os.Lstat(profilePath)
	if err != nil {
		return fmt.Errorf("inspect Claude GPT profile directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Claude GPT profile path must be a real directory")
	}
	if err := os.Chmod(profilePath, 0o700); err != nil {
		return fmt.Errorf("secure Claude GPT profile directory: %w", err)
	}
	return nil
}

func (h *terminalHandler) claudeGPTProfileDirectory() (string, error) {
	if h == nil {
		return "", errors.New("Claude GPT profile directory is unavailable")
	}
	if h.claudeGPTProfileErr != nil {
		return "", h.claudeGPTProfileErr
	}
	profilePath := h.claudeGPTProfilePath
	if profilePath == "" && h.projects != nil {
		var err error
		profilePath, err = prepareClaudeGPTProfileDirectory(h.projects.DataDirectory())
		if err != nil {
			return "", err
		}
		h.claudeGPTProfilePath = profilePath
	}
	if profilePath == "" {
		return "", errors.New("Claude GPT profile directory is unavailable")
	}
	if err := secureClaudeGPTProfileDirectory(profilePath); err != nil {
		return "", err
	}
	return profilePath, nil
}

func defaultClaudeConfigDirectory() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func discoverClaudeSandboxPluginPath() (string, error) {
	configDirectory, err := defaultClaudeConfigDirectory()
	if err != nil {
		return "", fmt.Errorf("find Claude config directory: %w", err)
	}
	settingsContents, err := os.ReadFile(filepath.Join(configDirectory, "settings.json"))
	if err != nil {
		return "", fmt.Errorf("read Claude settings: %w", err)
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(settingsContents, &settings); err != nil {
		return "", fmt.Errorf("decode Claude settings: %w", err)
	}
	if !settings.EnabledPlugins[claudeSandboxPluginID] {
		return "", fmt.Errorf("Claude plugin %s is not enabled", claudeSandboxPluginID)
	}

	registryContents, err := os.ReadFile(filepath.Join(configDirectory, "plugins", "installed_plugins.json"))
	if err != nil {
		return "", fmt.Errorf("read installed Claude plugins: %w", err)
	}
	var registry claudeInstalledPluginRegistry
	if err := json.Unmarshal(registryContents, &registry); err != nil {
		return "", fmt.Errorf("decode installed Claude plugins: %w", err)
	}
	entries := registry.Plugins[claudeSandboxPluginID]
	for index := len(entries) - 1; index >= 0; index-- {
		installPath := strings.TrimSpace(entries[index].InstallPath)
		if installPath == "" {
			continue
		}
		if !filepath.IsAbs(installPath) {
			installPath = filepath.Join(configDirectory, installPath)
		}
		manifest := filepath.Join(installPath, ".claude-plugin", "plugin.json")
		if info, statErr := os.Stat(manifest); statErr == nil && !info.IsDir() {
			return installPath, nil
		}
	}
	return "", fmt.Errorf("Claude plugin %s is not installed", claudeSandboxPluginID)
}

func claudeGPTProxyEnvironment(profilePath, baseURL, apiKey, model string) []string {
	return []string{
		"CLAUDE_CONFIG_DIR=" + profilePath,
		"IS_DEMO=1",
		"ANTHROPIC_BASE_URL=" + baseURL,
		"ANTHROPIC_AUTH_TOKEN=" + apiKey,
		"ANTHROPIC_MODEL=" + model,
		"ANTHROPIC_SMALL_FAST_MODEL=" + defaultClaudeGPTHaikuModel,
		"ANTHROPIC_DEFAULT_OPUS_MODEL=" + defaultClaudeGPTOpusModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + defaultClaudeGPTSonnetModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + defaultClaudeGPTHaikuModel,
	}
}
