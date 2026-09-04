package project

import (
	"errors"
	"strings"
)

// ThreadWorkspaceUpdate patches each field independently so a tab change from
// one client cannot overwrite an agent selection made by another. Initialize
// migrates legacy browser preferences only while the server field is unset.
type ThreadWorkspaceUpdate struct {
	CodingAgent string `json:"codingAgent,omitempty"`
	ActiveTab   string `json:"activeTab,omitempty"`
	Initialize  bool   `json:"initialize,omitempty"`
}

func (u ThreadWorkspaceUpdate) Validate() error {
	if u.CodingAgent == "" && u.ActiveTab == "" {
		return errors.New("a coding agent or active tab is required")
	}
	if u.ActiveTab != "" {
		switch u.ActiveTab {
		case "pi", "terminal", "nvim", "lazygit", "process", "browser":
		default:
			return errors.New("invalid workspace tab")
		}
	}
	if u.CodingAgent != "" {
		switch u.CodingAgent {
		case "pi", "pi-native", "codex", "grok", "claude", "claude-native", "claude-gpt":
		default:
			id := strings.TrimPrefix(strings.TrimPrefix(u.CodingAgent, "claude-gpt-profile-"), "claude-profile-")
			if id == u.CodingAgent || id == "" || len(id) > maxCodingAgentIDLength {
				return errors.New("invalid coding agent")
			}
			for _, c := range id {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
					return errors.New("invalid coding agent")
				}
			}
		}
	}
	return nil
}

func (s *Store) UpdateThreadWorkspace(projectID, threadID string, update ThreadWorkspaceUpdate) (Thread, error) {
	if err := update.Validate(); err != nil {
		return Thread{}, err
	}
	return withProjectMutationResult(s, func() (Thread, error) {
		for projectIndex := range s.projects {
			if s.projects[projectIndex].ID != projectID {
				continue
			}
			for threadIndex := range s.projects[projectIndex].Threads {
				thread := &s.projects[projectIndex].Threads[threadIndex]
				if thread.ID != threadID {
					continue
				}
				if thread.RollbackPending {
					return Thread{}, ErrThreadRollbackPending
				}
				previous := *thread
				if update.CodingAgent != "" && (!update.Initialize || thread.CodingAgent == "") {
					thread.CodingAgent = update.CodingAgent
				}
				if update.ActiveTab != "" && (!update.Initialize || thread.ActiveTab == "") {
					thread.ActiveTab = update.ActiveTab
				}
				if previous.CodingAgent == thread.CodingAgent && previous.ActiveTab == thread.ActiveTab {
					return cloneThread(*thread), nil
				}
				return saveProjectMutationResult(s, cloneThread(*thread), func() { *thread = previous })
			}
			return Thread{}, ErrThreadNotFound
		}
		return Thread{}, ErrNotFound
	})
}
