package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	runtimeModeProduction  = "production"
	runtimeModeDevelopment = "development"
	productionGitBranch    = "main"
	productionHTTPAddress  = "0.0.0.0:4000"
	productionHTTPPort     = 4000
	productionTmuxSocket   = "dire-mux"
)

type runtimeConfiguration struct {
	Mode                string
	Address             string
	AllowedOriginPort   int
	TmuxSocket          string
	AddCurrentDirectory bool
}

type gitCheckout struct {
	Present bool
	Branch  string
}

func validateRuntimeStartup(configuration runtimeConfiguration) error {
	mode := strings.TrimSpace(configuration.Mode)
	if mode != runtimeModeProduction && mode != runtimeModeDevelopment {
		return fmt.Errorf("mode must be %q or %q", runtimeModeProduction, runtimeModeDevelopment)
	}
	configuration.Mode = mode

	checkout := gitCheckout{}
	if mode == runtimeModeProduction {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("find working directory for production branch check: %w", err)
		}
		checkout, err = gitCheckoutAt(workingDirectory)
		if err != nil {
			return err
		}
	}
	return validateRuntimeConfiguration(configuration, checkout)
}

func validateRuntimeConfiguration(configuration runtimeConfiguration, checkout gitCheckout) error {
	switch configuration.Mode {
	case runtimeModeDevelopment:
		if configuration.TmuxSocket == "" || configuration.TmuxSocket == productionTmuxSocket {
			return fmt.Errorf("development mode may not use production tmux socket %q; set -tmux-socket to an isolated name", productionTmuxSocket)
		}

		port, err := addressPort(configuration.Address)
		if err != nil {
			return fmt.Errorf("parse HTTP listen address: %w", err)
		}
		if port == productionHTTPPort || configuration.AllowedOriginPort == productionHTTPPort {
			return fmt.Errorf("development mode may not use reserved production port %d", productionHTTPPort)
		}
		return nil

	case runtimeModeProduction:
		if configuration.AddCurrentDirectory {
			return errors.New("-add-current-directory is only available in development mode")
		}
		if checkout.Present && checkout.Branch != productionGitBranch {
			branch := checkout.Branch
			if branch == "" {
				branch = "detached HEAD"
			}
			return fmt.Errorf(
				"production mode may only run from git branch %q (current: %s); use development mode with isolated ports and tmux socket instead",
				productionGitBranch,
				branch,
			)
		}
		return nil

	default:
		return fmt.Errorf("mode must be %q or %q", runtimeModeProduction, runtimeModeDevelopment)
	}
}

func addressPort(address string) (int, error) {
	_, value, err := net.SplitHostPort(address)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

func gitCheckoutAt(directory string) (gitCheckout, error) {
	present, err := hasGitMetadata(directory)
	if err != nil || !present {
		return gitCheckout{Present: present}, err
	}

	command := exec.Command("git", "-C", directory, "branch", "--show-current")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return gitCheckout{}, fmt.Errorf("determine production git branch: %s", message)
	}
	return gitCheckout{Present: true, Branch: strings.TrimSpace(string(output))}, nil
}

func hasGitMetadata(directory string) (bool, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return false, fmt.Errorf("resolve working directory for production branch check: %w", err)
	}
	for {
		_, err := os.Lstat(filepath.Join(directory, ".git"))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect git metadata for production branch check: %w", err)
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return false, nil
		}
		directory = parent
	}
}
