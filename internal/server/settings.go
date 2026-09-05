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
		NewThreadSelection            *project.NewThreadSelection   `json:"newThreadSelection"`
		WorktreeBasePath              *string                       `json:"worktreeBasePath"`
		ArchivedThreadRetentionDays   *int                          `json:"archivedThreadRetentionDays"`
		OrphanedWorktreeRetentionDays *int                          `json:"orphanedWorktreeRetentionDays"`
		CodingAgents                  *[]project.CodingAgentSetting `json:"codingAgents"`
		TitleModel                    *string                       `json:"titleModel"`
		TitleThinking                 *string                       `json:"titleThinking"`
		Theme                         *project.Theme                `json:"theme"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.WorktreeBasePath == nil &&
		input.ArchivedThreadRetentionDays == nil &&
		input.OrphanedWorktreeRetentionDays == nil && input.CodingAgents == nil &&
		input.TitleModel == nil && input.TitleThinking == nil && input.Theme == nil && input.NewThreadSelection == nil) {
		writeError(w, http.StatusBadRequest, "Invalid settings.")
		return
	}
	settings, err := s.projects.UpdateSettingsFields(project.SettingsUpdate{
		NewThreadSelection:            input.NewThreadSelection,
		WorktreeBasePath:              input.WorktreeBasePath,
		ArchivedThreadRetentionDays:   input.ArchivedThreadRetentionDays,
		OrphanedWorktreeRetentionDays: input.OrphanedWorktreeRetentionDays,
		CodingAgents:                  input.CodingAgents,
		TitleModel:                    input.TitleModel,
		TitleThinking:                 input.TitleThinking,
		Theme:                         input.Theme,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyStateChanged(stateTopicSettings, "", "")
	s.notifyStateChanged(stateTopicCodingAgents, "", "")
	s.notifyStateChanged(stateTopicCleanup, "", "")
	writeJSON(w, http.StatusOK, settings)
}
