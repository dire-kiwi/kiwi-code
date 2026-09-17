package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// uploadFile keeps each upload in its own private directory so original names
// survive without allowing client filenames to overwrite existing files.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	if _, err := s.projects.Get(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if r.ContentLength > maxPiImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Each file must be 50 MB or smaller.")
		return
	}
	name := filepath.Base(strings.ReplaceAll(r.URL.Query().Get("name"), "\\", "/"))
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	dir, err := os.MkdirTemp("", "kiwi-code-upload-")
	if err != nil {
		writeError(w, 500, "Could not store file.")
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		writeError(w, 500, "Could not store file.")
		return
	}
	_, err = io.Copy(file, http.MaxBytesReader(w, r.Body, maxPiImageBytes))
	closeErr := file.Close()
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "Each file must be 50 MB or smaller.")
		} else {
			writeError(w, 400, "Could not read file.")
		}
		return
	}
	if closeErr != nil {
		writeError(w, 500, "Could not store file.")
		return
	}
	keep = true
	writeJSON(w, http.StatusCreated, map[string]string{"path": path})
}
