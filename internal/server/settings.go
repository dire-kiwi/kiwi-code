package server

import (
	"encoding/json"
	"net/http"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.projects.GetSettings())
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorktreeBasePath              *string                      `json:"worktreeBasePath"`
		ArchivedThreadRetentionDays   *int                         `json:"archivedThreadRetentionDays"`
		OrphanedWorktreeRetentionDays *int                         `json:"orphanedWorktreeRetentionDays"`
		SubAgentNestingDepth          *int                         `json:"subAgentNestingDepth"`
		DisableWorkflows              *bool                        `json:"disableWorkflows"`
		WorkflowKeywordTrigger        *bool                        `json:"workflowKeywordTriggerEnabled"`
		WorkflowSizeGuideline         *string                      `json:"workflowSizeGuideline"`
		ClaudeCodeProfiles            *[]project.ClaudeCodeProfile `json:"claudeCodeProfiles"`
		Theme                         *project.Theme               `json:"theme"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.WorktreeBasePath == nil &&
		input.ArchivedThreadRetentionDays == nil &&
		input.OrphanedWorktreeRetentionDays == nil && input.SubAgentNestingDepth == nil &&
		input.DisableWorkflows == nil && input.WorkflowKeywordTrigger == nil &&
		input.WorkflowSizeGuideline == nil && input.ClaudeCodeProfiles == nil && input.Theme == nil) {
		writeError(w, http.StatusBadRequest, "Invalid settings.")
		return
	}
	settings, err := s.projects.UpdateSettingsFields(project.SettingsUpdate{
		WorktreeBasePath:              input.WorktreeBasePath,
		ArchivedThreadRetentionDays:   input.ArchivedThreadRetentionDays,
		OrphanedWorktreeRetentionDays: input.OrphanedWorktreeRetentionDays,
		SubAgentNestingDepth:          input.SubAgentNestingDepth,
		DisableWorkflows:              input.DisableWorkflows,
		WorkflowKeywordTrigger:        input.WorkflowKeywordTrigger,
		WorkflowSizeGuideline:         input.WorkflowSizeGuideline,
		ClaudeCodeProfiles:            input.ClaudeCodeProfiles,
		Theme:                         input.Theme,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
