package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func (s *Server) runEnvironmentAction(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the environment action.")
		return
	}
	action, command, variables, err := project.ResolveEnvironmentAction(item, thread, strings.TrimSpace(r.PathValue("actionId")))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	window, err := s.terminal.newEnvironmentActionProcess(item, thread, action.Name, command, variables)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.terminal.wakeThreadTmuxWatchers(item.ID, thread.ID)
	s.notifyThreadStatusChanged(item.ID, thread.ID)
	writeJSON(w, http.StatusCreated, window)
}
