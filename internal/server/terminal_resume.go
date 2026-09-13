package server

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// Each terminal agent has its own resume marker. Never use --last/--continue:
// ordinary threads can share a cwd, so those flags can select another thread.
func (h *terminalHandler) terminalAgentResume(item project.Project, thread project.Thread, tool string, args []string) ([]string, []string, error) {
	if h.projects == nil || !isTerminalCodingAgent(tool) {
		return args, nil, nil
	}
	directory := filepath.Join(h.projects.DataDirectory(), "terminal-agent-sessions", item.ID, thread.ID, tool)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, nil, err
	}
	marker := filepath.Join(directory, "session")
	data, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	session := strings.TrimSpace(string(data))
	environment := []string{"KIWI_CODE_AGENT_SESSION_FILE=" + marker}
	if session != "" {
		switch {
		case tool == codingAgentPi:
			args = append(args, "--session", session)
		case tool == codingAgentCodex:
			args = append([]string{"resume", session}, args...)
		case tool == codingAgentGrok || isClaudeCodingAgent(tool):
			args = append(args, "--resume", session)
		}
		return args, environment, nil
	}
	// Existing pre-migration sessions have no marker. Present the agent's resume
	// picker instead of guessing which saved conversation belongs to this thread.
	if thread.UnsettledAt != nil && thread.LastPromptAt != nil && tool != codingAgentGrok {
		switch {
		case tool == codingAgentCodex:
			args = append([]string{"resume"}, args...)
		case tool == codingAgentPi || isClaudeCodingAgent(tool):
			args = append(args, "--resume")
		}
		return args, environment, nil
	}
	if tool == codingAgentPi {
		args = append(args, "--session-dir", directory)
	}
	if tool == codingAgentGrok {
		if thread.UnsettledAt != nil && thread.LastPromptAt != nil {
			return nil, nil, errors.New("this older Grok thread has no saved session ID; resume its conversation by ID from a shell before settling it again")
		}
		// Grok supports an explicit UUID for a new conversation and --resume for
		// subsequent launches, avoiding cwd-based selection entirely.
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, nil, err
		}
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		session = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
		if err := writeFileAtomically(marker, []byte(session+"\n"), serverAtomicFileOptions{Mode: 0600, SyncFile: true}); err != nil {
			return nil, nil, err
		}
		args = append(args, "--session-id", session)
	}
	return args, environment, nil
}
