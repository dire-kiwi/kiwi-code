package project

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultEnvironmentName           = "Local"
	maxEnvironmentNameRunes          = 80
	maxEnvironmentScriptBytes        = 128 << 10
	maxEnvironmentVariables          = 64
	maxEnvironmentVariableBytes      = 32 << 10
	maxEnvironmentActionCommandBytes = 32 << 10
	maxEnvironmentActions            = 16
	maxEnvironmentActionNameRunes    = 40
	maxEnvironmentActionIDBytes      = 128
)

type PlatformScripts struct {
	Default string `json:"default"`
	MacOS   string `json:"macos"`
	Linux   string `json:"linux"`
	Windows string `json:"windows"`
}

type EnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type EnvironmentAction struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Scripts PlatformScripts `json:"scripts"`
}

type LocalEnvironment struct {
	Name           string                `json:"name"`
	SetupScripts   PlatformScripts       `json:"setupScripts"`
	CleanupScripts PlatformScripts       `json:"cleanupScripts"`
	Variables      []EnvironmentVariable `json:"variables"`
	Actions        []EnvironmentAction   `json:"actions"`
}

func defaultLocalEnvironment() LocalEnvironment {
	return LocalEnvironment{
		Name:      defaultEnvironmentName,
		Variables: []EnvironmentVariable{},
		Actions:   []EnvironmentAction{},
	}
}

func localEnvironmentIsZero(environment LocalEnvironment) bool {
	return environment.Name == "" && environment.SetupScripts == (PlatformScripts{}) &&
		environment.CleanupScripts == (PlatformScripts{}) && len(environment.Variables) == 0 && len(environment.Actions) == 0
}

func cloneLocalEnvironment(environment LocalEnvironment) LocalEnvironment {
	cloned := environment
	cloned.Variables = append([]EnvironmentVariable{}, environment.Variables...)
	cloned.Actions = append([]EnvironmentAction{}, environment.Actions...)
	return cloned
}

func equalLocalEnvironment(left, right LocalEnvironment) bool {
	return reflect.DeepEqual(left, right)
}

func normalizeLocalEnvironment(environment LocalEnvironment) (LocalEnvironment, error) {
	environment.Name = strings.TrimSpace(environment.Name)
	if environment.Name == "" {
		return LocalEnvironment{}, errors.New("environment name is required")
	}
	if utf8.RuneCountInString(environment.Name) > maxEnvironmentNameRunes {
		return LocalEnvironment{}, fmt.Errorf("environment name must be %d characters or fewer", maxEnvironmentNameRunes)
	}
	for _, character := range environment.Name {
		if unicode.IsControl(character) {
			return LocalEnvironment{}, errors.New("environment name cannot contain control characters")
		}
	}
	if err := validatePlatformScripts(environment.SetupScripts, "setup script"); err != nil {
		return LocalEnvironment{}, err
	}
	if err := validatePlatformScripts(environment.CleanupScripts, "cleanup script"); err != nil {
		return LocalEnvironment{}, err
	}
	if len(environment.Variables) > maxEnvironmentVariables {
		return LocalEnvironment{}, fmt.Errorf("an environment can define at most %d variables", maxEnvironmentVariables)
	}
	seenVariables := make(map[string]struct{}, len(environment.Variables))
	variables := make([]EnvironmentVariable, 0, len(environment.Variables))
	variableBytes := 0
	for _, variable := range environment.Variables {
		variable.Name = strings.TrimSpace(variable.Name)
		if !validEnvironmentVariableName(variable.Name) {
			return LocalEnvironment{}, fmt.Errorf("invalid environment variable name %q", variable.Name)
		}
		if _, duplicate := seenVariables[variable.Name]; duplicate {
			return LocalEnvironment{}, fmt.Errorf("environment variable %q is defined more than once", variable.Name)
		}
		seenVariables[variable.Name] = struct{}{}
		if strings.ContainsRune(variable.Value, '\x00') {
			return LocalEnvironment{}, fmt.Errorf("environment variable %q cannot contain NUL bytes", variable.Name)
		}
		variableBytes += len(variable.Name) + len(variable.Value)
		if variableBytes > maxEnvironmentVariableBytes {
			return LocalEnvironment{}, fmt.Errorf("environment variables must use %d bytes or fewer", maxEnvironmentVariableBytes)
		}
		variables = append(variables, variable)
	}
	environment.Variables = variables

	if len(environment.Actions) > maxEnvironmentActions {
		return LocalEnvironment{}, fmt.Errorf("an environment can define at most %d actions", maxEnvironmentActions)
	}
	seenActionIDs := make(map[string]struct{}, len(environment.Actions))
	seenActionNames := make(map[string]struct{}, len(environment.Actions))
	actions := make([]EnvironmentAction, 0, len(environment.Actions))
	for _, action := range environment.Actions {
		action.ID = strings.TrimSpace(action.ID)
		action.Name = strings.TrimSpace(action.Name)
		if !validEnvironmentActionID(action.ID) {
			return LocalEnvironment{}, errors.New("environment action IDs may contain only letters, numbers, hyphens, and underscores")
		}
		if action.Name == "" {
			return LocalEnvironment{}, errors.New("environment action name is required")
		}
		if utf8.RuneCountInString(action.Name) > maxEnvironmentActionNameRunes {
			return LocalEnvironment{}, fmt.Errorf("environment action names must be %d characters or fewer", maxEnvironmentActionNameRunes)
		}
		for _, character := range action.Name {
			if unicode.IsControl(character) {
				return LocalEnvironment{}, errors.New("environment action names cannot contain control characters")
			}
		}
		if _, duplicate := seenActionIDs[action.ID]; duplicate {
			return LocalEnvironment{}, fmt.Errorf("environment action ID %q is defined more than once", action.ID)
		}
		foldedName := strings.ToLower(action.Name)
		if _, duplicate := seenActionNames[foldedName]; duplicate {
			return LocalEnvironment{}, fmt.Errorf("environment action %q is defined more than once", action.Name)
		}
		seenActionIDs[action.ID] = struct{}{}
		seenActionNames[foldedName] = struct{}{}
		if err := validatePlatformScripts(action.Scripts, fmt.Sprintf("environment action %q", action.Name)); err != nil {
			return LocalEnvironment{}, err
		}
		for _, command := range []string{action.Scripts.Default, action.Scripts.MacOS, action.Scripts.Linux, action.Scripts.Windows} {
			if len(command) > maxEnvironmentActionCommandBytes {
				return LocalEnvironment{}, fmt.Errorf("environment action %q commands must be %d bytes or fewer", action.Name, maxEnvironmentActionCommandBytes)
			}
		}
		if strings.TrimSpace(action.Scripts.Default) == "" && strings.TrimSpace(action.Scripts.MacOS) == "" &&
			strings.TrimSpace(action.Scripts.Linux) == "" && strings.TrimSpace(action.Scripts.Windows) == "" {
			return LocalEnvironment{}, fmt.Errorf("environment action %q requires a command", action.Name)
		}
		actions = append(actions, action)
	}
	environment.Actions = actions
	return environment, nil
}

func validatePlatformScripts(scripts PlatformScripts, label string) error {
	for platform, script := range map[string]string{
		"default": scripts.Default,
		"macOS":   scripts.MacOS,
		"Linux":   scripts.Linux,
		"Windows": scripts.Windows,
	} {
		if len(script) > maxEnvironmentScriptBytes {
			return fmt.Errorf("%s for %s must be %d bytes or fewer", label, platform, maxEnvironmentScriptBytes)
		}
		if strings.ContainsRune(script, '\x00') {
			return fmt.Errorf("%s for %s cannot contain NUL bytes", label, platform)
		}
	}
	return nil
}

func validEnvironmentVariableName(name string) bool {
	if name == "" || !((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z') || name[0] == '_') {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validEnvironmentActionID(id string) bool {
	if id == "" || len(id) > maxEnvironmentActionIDBytes {
		return false
	}
	for _, character := range id {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func platformScript(scripts PlatformScripts) string {
	var specific string
	switch runtime.GOOS {
	case "darwin":
		specific = scripts.MacOS
	case "linux":
		specific = scripts.Linux
	case "windows":
		specific = scripts.Windows
	}
	if strings.TrimSpace(specific) != "" {
		return specific
	}
	return scripts.Default
}

func runEnvironmentSetup(item Project, thread Thread) error {
	script := platformScript(item.Environment.SetupScripts)
	if strings.TrimSpace(script) == "" {
		return nil
	}
	variables := environmentVariables(item, thread)
	if err := runEnvironmentScript(thread.Cwd, script, variables); err != nil {
		return fmt.Errorf("run environment setup script: %w", err)
	}
	return nil
}

func runEnvironmentScript(cwd, script string, variables []EnvironmentVariable) error {
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd.exe", "/d", "/s", "/c", script)
	} else {
		command = exec.Command("/bin/sh", "-lc", script)
	}
	command.Dir = cwd
	command.Env = mergeEnvironment(os.Environ(), variables)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if len(detail) > 4096 {
		detail = detail[len(detail)-4096:]
	}
	if detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

func mergeEnvironment(base []string, variables []EnvironmentVariable) []string {
	overrides := make(map[string]string, len(variables))
	for _, variable := range variables {
		overrides[variable.Name] = variable.Value
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if _, overridden := overrides[name]; found && overridden {
			continue
		}
		result = append(result, entry)
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}

func environmentVariables(item Project, thread Thread) []EnvironmentVariable {
	variables := append([]EnvironmentVariable{}, item.Environment.Variables...)
	variables = append(variables,
		EnvironmentVariable{Name: "CODEX_PROJECT_ID", Value: item.ID},
		EnvironmentVariable{Name: "CODEX_THREAD_ID", Value: thread.ID},
		EnvironmentVariable{Name: "CODEX_PROJECT_PATH", Value: item.Path},
		EnvironmentVariable{Name: "CODEX_WORKTREE_PATH", Value: thread.WorktreePath},
		EnvironmentVariable{Name: "KIWI_CODE_PROJECT_ID", Value: item.ID},
		EnvironmentVariable{Name: "KIWI_CODE_THREAD_ID", Value: thread.ID},
		EnvironmentVariable{Name: "KIWI_CODE_PROJECT_PATH", Value: item.Path},
		EnvironmentVariable{Name: "KIWI_CODE_WORKTREE_PATH", Value: thread.WorktreePath},
	)
	return variables
}

func ResolveEnvironmentAction(item Project, thread Thread, actionID string) (EnvironmentAction, string, []EnvironmentVariable, error) {
	for _, action := range item.Environment.Actions {
		if action.ID != actionID {
			continue
		}
		command := platformScript(action.Scripts)
		if strings.TrimSpace(command) == "" {
			return EnvironmentAction{}, "", nil, errors.New("this action has no command for the current platform")
		}
		return action, command, environmentVariables(item, thread), nil
	}
	return EnvironmentAction{}, "", nil, errors.New("environment action not found")
}
