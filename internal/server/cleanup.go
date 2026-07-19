package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

const defaultCleanupInterval = time.Hour

func (s *Server) getCleanupOverview(w http.ResponseWriter, _ *http.Request) {
	overview, err := s.projects.CleanupOverview(time.Now())
	if err != nil {
		log.Printf("build cleanup overview: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not load the cleanup queue.")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) startCleanupLoop(ctx context.Context, interval time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = defaultCleanupInterval
	}
	go func() {
		if err := s.runCleanupCycle(time.Now()); err != nil {
			log.Printf("automatic cleanup: %v", err)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.runCleanupCycle(now); err != nil {
					log.Printf("automatic cleanup: %v", err)
				}
			}
		}
	}()
}

func (s *Server) runCleanupCycle(now time.Time) error {
	due, err := s.projects.ArchivedThreadsDue(now)
	if err != nil {
		return fmt.Errorf("find archived threads: %w", err)
	}
	var cleanupErrors []error
	for _, ref := range due {
		if err := s.deleteExpiredArchivedThread(ref); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf(
				"delete archived thread project=%q thread=%q: %w",
				ref.ProjectID,
				ref.ThreadID,
				err,
			))
		} else if exists, inspectErr := s.projects.PersistedResourceExists(ref.ProjectID, ref.ThreadID); inspectErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf(
				"verify archived thread cleanup project=%q thread=%q: %w",
				ref.ProjectID,
				ref.ThreadID,
				inspectErr,
			))
		} else if !exists {
			log.Printf("automatic cleanup deleted archived thread: project=%q thread=%q", ref.ProjectID, ref.ThreadID)
		}
	}
	worktrees, worktreeErr := s.projects.CleanupOrphanedWorktrees(now)
	if worktreeErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("clean unattached worktrees: %w", worktreeErr))
	}
	for _, path := range worktrees.Deleted {
		log.Printf("automatic cleanup deleted unattached worktree: path=%q", path)
	}
	return errors.Join(cleanupErrors...)
}

func (s *Server) deleteExpiredArchivedThread(ref project.ArchivedThreadRef) error {
	archivedBefore := ref.ArchivedBefore.UTC()
	failure := s.deleteThreadTree(ref.ProjectID, ref.ThreadID, &archivedBefore)
	if failure == nil || failure.status == http.StatusNotFound || errors.Is(failure, project.ErrThreadNotArchived) {
		return nil
	}
	return failure
}
