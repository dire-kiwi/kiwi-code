package agent

import (
	"errors"
	"os/exec"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// Claude launches Claude Code, covering the base agent, the CLIProxyAPI GPT
// variant, and user-configured profile instances of both.
type Claude struct {
	AgentID    string
	GPT        bool
	Configured bool
	Profile    project.CodingAgentSetting

	PluginPath     string
	PluginErr      error
	PluginRootPath string
	PluginRootErr  error
	ConfigPath     string
	ConfigErr      error

	// LaunchSettings is the JSON passed via --settings.
	LaunchSettings string
	// SyncProfileSettings mirrors the default profile's settings into a named
	// profile's config directory.
	SyncProfileSettings func(profileDirectory, configPath string) error
	// FigmaConfigArgument builds the --mcp-config value for a Figma URL.
	FigmaConfigArgument func(url string) (string, error)

	// GPT-variant configuration.
	UnsetEnvironment    []string
	GPTProfileDirectory func() (string, error)
	ProxyConfiguration  func() (baseURL, apiKey string, err error)
	ProxyEnvironment    func(profilePath, pluginRootPath, baseURL, apiKey, model string) []string
	IsGPTModel          func(model string) bool
}

func (c Claude) ID() string { return c.AgentID }

func (c Claude) TerminalCommand(lc LaunchContext, opts LaunchOptions) (Command, error) {
	profileAgent := c.Configured && ValidConfiguredClaudeID(c.AgentID)
	if ValidConfiguredClaudeID(c.AgentID) && !c.Configured {
		return Command{}, ErrNotConfigured
	}

	program, baseArgs, notice := resolveProgram(IDClaude, "Claude Code")
	command := Command{Program: program, BaseArgs: baseArgs, Notice: notice}

	if notice == "" {
		if c.PluginErr != nil {
			return Command{}, c.PluginErr
		}
		if c.PluginPath == "" {
			return Command{}, errors.New("Claude plugin path is unavailable")
		}
		if c.GPT || profileAgent {
			if c.PluginRootErr != nil {
				return Command{}, c.PluginRootErr
			}
			if c.PluginRootPath == "" {
				return Command{}, errors.New("Claude plugin root is unavailable")
			}
		}
		if profileAgent && !c.GPT {
			// A named profile isolates Claude's account and session state, not its
			// launch configuration. Mirror the default settings and use the default
			// plugin registry below so installed-plugin skills and MCP servers load
			// exactly as they do for the default Claude profile.
			if c.ConfigErr != nil {
				return Command{}, c.ConfigErr
			}
			if c.ConfigPath == "" {
				return Command{}, errors.New("Claude config directory is unavailable")
			}
			if err := c.SyncProfileSettings(c.Profile.ConfigDirectory, c.ConfigPath); err != nil {
				return Command{}, err
			}
		}
		command.Prefix = []string{"--plugin-dir", c.PluginPath}
		relatedDirectories, err := lc.RelatedDirectories()
		if err != nil {
			return Command{}, err
		}
		if len(relatedDirectories) > 0 {
			command.Prefix = append(command.Prefix, "--add-dir")
			command.Prefix = append(command.Prefix, relatedDirectories...)
		}
		if c.GPT {
			if opts.Model == "" || !c.IsGPTModel(opts.Model) {
				return Command{}, errors.New("Claude Code (with gpt) requires a CLIProxyAPI GPT model")
			}
		}
		if lc.FigmaMCPURL != "" {
			figmaConfig, err := c.FigmaConfigArgument(lc.FigmaMCPURL)
			if err != nil {
				return Command{}, err
			}
			// --mcp-config is variadic, so it must stay ahead of another flag and
			// never trail the positional initial prompt appended at the end.
			command.Prefix = append(command.Prefix, "--mcp-config", figmaConfig)
		}
		command.Prefix = append(command.Prefix,
			"--dangerously-skip-permissions",
			"--settings", c.LaunchSettings,
		)

		if profileAgent && !c.GPT {
			command.Env = append(command.Env,
				"CLAUDE_CONFIG_DIR="+c.Profile.ConfigDirectory,
				"CLAUDE_CODE_PLUGIN_CACHE_DIR="+c.PluginRootPath,
			)
		}
		piPath := IDPi
		if resolvedPiPath, err := exec.LookPath(IDPi); err == nil {
			piPath = resolvedPiPath
		}
		command.Env = append(command.Env,
			"KIWI_CODE_PI_PATH="+piPath,
			"KIWI_CODE_CODING_AGENT="+c.AgentID,
		)

		if c.GPT {
			profilePath, err := c.GPTProfileDirectory()
			if err != nil {
				return Command{}, err
			}
			baseURL, apiKey, err := c.ProxyConfiguration()
			if err != nil {
				return Command{}, err
			}
			command.Unset = append(command.Unset, c.UnsetEnvironment...)
			command.Env = append(command.Env, c.ProxyEnvironment(
				profilePath,
				c.PluginRootPath,
				baseURL,
				apiKey,
				opts.Model,
			)...)
		}
	}

	if opts.Model != "" {
		command.Suffix = append(command.Suffix, "--model", opts.Model)
	}
	if opts.ThinkingLevel != "" {
		command.Suffix = append(command.Suffix, "--effort", opts.ThinkingLevel)
	}
	if opts.InitialPrompt != "" {
		command.Suffix = append(command.Suffix, opts.InitialPrompt)
	}
	return command, nil
}
