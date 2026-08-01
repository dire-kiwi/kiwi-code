package agent

import (
	"errors"
	"os/exec"
	"strconv"
)

// Codex launches the Codex CLI with the managed Kiwi Code plugin profile.
type Codex struct {
	AgentToken     string
	AgentTokenPath string
	AgentTokenErr  error
	ProfileName    string
	ConfigPath     string
	// Prepare materializes the managed Codex plugin profile once.
	Prepare func() error
}

func (c Codex) ID() string { return IDCodex }

func (c Codex) TerminalCommand(lc LaunchContext, opts LaunchOptions) (Command, error) {
	program, baseArgs, notice := resolveProgram(IDCodex, "Codex CLI")
	command := Command{Program: program, BaseArgs: baseArgs, Notice: notice}

	if notice == "" {
		if c.AgentTokenErr != nil {
			return Command{}, c.AgentTokenErr
		}
		if c.AgentToken == "" || c.AgentTokenPath == "" {
			return Command{}, errors.New("Codex plugin capability is unavailable")
		}
		if err := c.Prepare(); err != nil {
			return Command{}, err
		}
		command.Prefix = []string{
			"--profile", c.ProfileName,
			"--dangerously-bypass-approvals-and-sandbox",
			"--dangerously-bypass-hook-trust",
		}
		relatedDirectories, err := lc.RelatedDirectories()
		if err != nil {
			return Command{}, err
		}
		for _, directory := range relatedDirectories {
			command.Prefix = append(command.Prefix, "--add-dir", directory)
		}
		if lc.FigmaMCPURL != "" {
			command.Prefix = append(
				command.Prefix,
				"--config", "mcp_servers.kiwi-code-figma.url="+strconv.Quote(lc.FigmaMCPURL),
			)
		}

		piPath := IDPi
		if resolvedPiPath, err := exec.LookPath(IDPi); err == nil {
			piPath = resolvedPiPath
		}
		command.Env = append(command.Env,
			"CODEX_HOME="+c.ConfigPath,
			"KIWI_CODE_AGENT_TOKEN_FILE="+c.AgentTokenPath,
			"KIWI_CODE_CODING_AGENT="+IDCodex,
			"KIWI_CODE_PI_PATH="+piPath,
		)
	}

	if opts.Model != "" {
		command.Suffix = append(command.Suffix, "--model", opts.Model)
	}
	if opts.ThinkingLevel != "" {
		command.Suffix = append(command.Suffix, "--config", `model_reasoning_effort="`+opts.ThinkingLevel+`"`)
	}
	if opts.InitialPrompt != "" {
		command.Suffix = append(command.Suffix, opts.InitialPrompt)
	}
	return command, nil
}
