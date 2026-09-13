package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
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
	defer s.notifyStateChanged(stateTopicCleanup, "", "")
	var cleanupErrors []error
	if err := s.settleIdleThreads(now); err != nil {
		cleanupErrors = append(cleanupErrors, err)
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
