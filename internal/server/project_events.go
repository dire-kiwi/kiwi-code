package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const projectSnapshotInterval = 30 * time.Second

func (s *Server) streamProjects(w http.ResponseWriter, r *http.Request) {
	s.streamProjectsWithInterval(w, r, projectSnapshotInterval)
}

func (s *Server) streamProjectsWithInterval(w http.ResponseWriter, r *http.Request, snapshotInterval time.Duration) {
	flusher, ok := prepareEventStream(w, "Project update streaming is unavailable.")
	if !ok {
		return
	}

	updates, unsubscribe := s.projects.SubscribeChanges()
	defer unsubscribe()
	if err := writeProjectsEvent(w, clientProjects(s.projects.List())); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case projects, open := <-updates:
			if !open || writeProjectsEvent(w, clientProjects(s.projects.ResolveSnapshot(projects))) != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			// Full snapshots reconcile a browser after a dropped notification and
			// also pick up repository state that can change outside Kiwi Code.
			if err := writeProjectsEvent(w, clientProjects(s.projects.List())); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeProjectsEvent(w http.ResponseWriter, projects []project.Project) error {
	payload, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}
