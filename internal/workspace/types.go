package workspace

import (
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/tmux"
)

type Window struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type AgentPane struct {
	ID     string
	Agent  string
	Active bool
}

type ViewSession struct {
	Name          string
	Attached      bool
	SourceSession string
}

type DetailedWindow struct {
	Target       tmux.WindowTarget
	Name         string
	Tool         string
	StartCommand string
}

type PaneExitState struct {
	ServerPID string
	Dead      bool
	Status    string
	Signal    string
	ExitedAt  string
	Found     bool
}

func FixedTool(window DetailedWindow) string {
	for _, tool := range []string{"nvim", "lazygit", "pi"} {
		if window.Tool == tool || (window.Tool == "" && window.Name == tool) {
			return tool
		}
	}
	return ""
}

func SessionFromStartCommand(command string) string {
	const marker = "KIWI_CODE_TMUX_SESSION="
	index := strings.Index(command, marker)
	if index < 0 {
		return ""
	}
	value := command[index+len(marker):]
	end := 0
	for end < len(value) {
		character := value[end]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			end++
			continue
		}
		break
	}
	return value[:end]
}
