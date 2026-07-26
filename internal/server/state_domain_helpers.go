package server

import "context"

type sidebarProcessWebServer struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ThreadID    string `json:"threadId"`
	ThreadTitle string `json:"threadTitle"`
	ProcessID   string `json:"processId"`
	ProcessName string `json:"processName"`
	URL         string `json:"url"`
}

func (s *Server) sidebarProcessWebServers() []sidebarProcessWebServer {
	return s.sidebarProcessWebServersContext(context.Background())
}

func (s *Server) sidebarProcessWebServersContext(ctx context.Context) []sidebarProcessWebServer {
	result := []sidebarProcessWebServer{}
	if s.terminal == nil || s.terminal.tmuxPath == "" {
		return result
	}
	for _, item := range clientProjects(s.projects.List()) {
		for _, thread := range item.Threads {
			if ctx.Err() != nil {
				return result
			}
			windows, err := s.terminal.processWindowsContext(ctx, item, thread)
			if err != nil {
				continue
			}
			for _, window := range windows {
				for _, webServerURL := range window.WebServers {
					result = append(result, sidebarProcessWebServer{
						ProjectID:   item.ID,
						ProjectName: item.Name,
						ThreadID:    thread.ID,
						ThreadTitle: thread.Title,
						ProcessID:   window.ID,
						ProcessName: window.Name,
						URL:         webServerURL,
					})
				}
			}
		}
	}
	return result
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
