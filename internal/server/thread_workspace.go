package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func (s *Server) updateThreadWorkspace(w http.ResponseWriter, r *http.Request) {
	var input project.ThreadWorkspaceUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid thread workspace.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "Invalid thread workspace.")
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	thread, err := s.projects.UpdateThreadWorkspace(r.PathValue("id"), r.PathValue("threadId"), input)
	switch {
	case errors.Is(err, project.ErrNotFound), errors.Is(err, project.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, "Thread not found.")
	case errors.Is(err, project.ErrThreadRollbackPending):
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "Could not save the thread workspace.")
	default:
		// Store mutations publish the authoritative projects snapshot to all
		// connected state clients, including those not viewing this thread.
		writeJSON(w, http.StatusOK, thread)
	}
}
