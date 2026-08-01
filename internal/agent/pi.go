package agent

import (
	"fmt"
	"os"
	"os/exec"
)

// resolveProgram resolves an agent binary, falling back to a login shell
// with a user-facing notice when the binary is missing.
func resolveProgram(binary, displayName string) (program string, baseArgs []string, notice string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	path, err := exec.LookPath(binary)
	if err == nil {
		return path, nil, ""
	}
	notice = fmt.Sprintf("\r\n\x1b[38;5;214m%s is not installed or not on PATH. Opened a shell instead.\x1b[0m\r\n\r\n", displayName)
	return shell, []string{"-l"}, notice
}

// Pi launches the Pi TUI with the Kiwi Code extensions.
type Pi struct {
	ExtensionPaths     []string
	ExtensionErr       error
	FigmaExtensionPath string
	FigmaExtensionErr  error
	AgentToken         string
	// FigmaEnvironmentName is the env variable carrying the Figma bridge URL
	// (Pi has no built-in MCP support, so the extension reads it from env).
	FigmaEnvironmentName string
}

func (p Pi) ID() string { return IDPi }

func (p Pi) TerminalCommand(lc LaunchContext, opts LaunchOptions) (Command, error) {
	program, baseArgs, notice := resolveProgram(IDPi, IDPi)
	command := Command{Program: program, BaseArgs: baseArgs, Notice: notice}

	if notice == "" {
		if p.ExtensionErr != nil {
			return Command{}, p.ExtensionErr
		}
		extensionPaths := p.ExtensionPaths
		if lc.FigmaMCPURL != "" {
			if p.FigmaExtensionErr != nil {
				return Command{}, p.FigmaExtensionErr
			}
			extensionPaths = append(append([]string(nil), extensionPaths...), p.FigmaExtensionPath)
		}
		for _, extensionPath := range extensionPaths {
			command.Prefix = append(command.Prefix, "--extension", extensionPath)
		}
		if lc.FigmaMCPURL != "" {
			command.Env = append(command.Env, p.FigmaEnvironmentName+"="+lc.FigmaMCPURL)
		}
	}
	if lc.ThreadEndpoint != "" && p.AgentToken != "" {
		command.Env = append(command.Env, "KIWI_CODE_AGENT_TOKEN="+p.AgentToken)
	}

	if opts.Model != "" {
		command.Suffix = append(command.Suffix, "--model", opts.Model)
	}
	if opts.ThinkingLevel != "" {
		command.Suffix = append(command.Suffix, "--thinking", opts.ThinkingLevel)
	}
	if opts.InitialPrompt != "" {
		command.Suffix = append(command.Suffix, opts.InitialPrompt)
	}
	return command, nil
}
