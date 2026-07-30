package server

import (
	"context"
	"time"
)

const interactiveTerminalSetupYieldLimit = 2 * time.Second

func (h *terminalHandler) yieldToInteractiveTerminalSetup(ctx context.Context, limit time.Duration) {
	if h == nil || h.activeSetups.Load() == 0 || limit <= 0 {
		return
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for h.activeSetups.Load() > 0 {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-ticker.C:
		}
	}
}

// clientPiActivities prevents a queued activity snapshot from resurrecting UI
// state for a project or thread that has since been removed.
func (s *Server) clientPiActivities(activities []piThreadActivity) []piThreadActivity {
	if s.projects == nil {
		return activities
	}
	filtered := make([]piThreadActivity, 0, len(activities))
	for _, activity := range activities {
		if _, thread, err := s.projects.GetThread(activity.ProjectID, activity.ThreadID); err == nil && !thread.RollbackPending {
			filtered = append(filtered, activity)
		}
	}
	return filtered
}
