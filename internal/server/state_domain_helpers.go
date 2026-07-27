package server

import (
	"context"
	"sync"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

type sidebarProcessWebServer struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ThreadID    string `json:"threadId"`
	ThreadTitle string `json:"threadTitle"`
	ProcessID   string `json:"processId"`
	ProcessName string `json:"processName"`
	URL         string `json:"url"`
}

const processProjectionInteractiveYieldLimit = 2 * time.Second

// sidebarProcessWebServerCache keeps global sidebar projection invalidations
// local to the affected thread. Before this cache, every tmux notification or
// terminal mutation synchronously scanned every project and thread.
type sidebarProcessWebServerCache struct {
	refreshMu sync.Mutex
	mu        sync.Mutex
	entries   map[threadStatusKey][]sidebarProcessWebServer
	dirty     map[threadStatusKey]struct{}
}

func (c *sidebarProcessWebServerCache) markDirty(key threadStatusKey) {
	if key.projectID == "" || key.threadID == "" {
		return
	}
	c.mu.Lock()
	if c.dirty == nil {
		c.dirty = make(map[threadStatusKey]struct{})
	}
	c.dirty[key] = struct{}{}
	c.mu.Unlock()
}

func (c *sidebarProcessWebServerCache) snapshotContext(
	ctx context.Context,
	s *Server,
	refreshAll bool,
) []sidebarProcessWebServer {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	items := clientProjects(s.projects.List())
	type threadOwner struct {
		item   project.Project
		thread project.Thread
	}
	owners := make(map[threadStatusKey]threadOwner)
	orderedKeys := make([]threadStatusKey, 0)
	for _, item := range items {
		for _, thread := range item.Threads {
			key := threadStatusKey{projectID: item.ID, threadID: thread.ID}
			owners[key] = threadOwner{item: item, thread: thread}
			orderedKeys = append(orderedKeys, key)
		}
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[threadStatusKey][]sidebarProcessWebServer)
	}
	if c.dirty == nil {
		c.dirty = make(map[threadStatusKey]struct{})
	}
	if refreshAll {
		for key := range owners {
			c.dirty[key] = struct{}{}
		}
	}
	for key := range c.entries {
		if _, exists := owners[key]; !exists {
			delete(c.entries, key)
			delete(c.dirty, key)
		}
	}
	dirty := make([]threadStatusKey, 0, len(c.dirty))
	for key := range c.dirty {
		if _, exists := owners[key]; !exists {
			delete(c.dirty, key)
			continue
		}
		dirty = append(dirty, key)
		delete(c.dirty, key)
	}
	c.mu.Unlock()

	for index, key := range dirty {
		if ctx.Err() != nil {
			c.mu.Lock()
			for _, pending := range dirty[index:] {
				c.dirty[pending] = struct{}{}
			}
			c.mu.Unlock()
			break
		}
		owner := owners[key]
		values := []sidebarProcessWebServer{}
		if s.terminal != nil && s.terminal.tmuxPath != "" {
			s.terminal.yieldToInteractiveTerminalSetup(ctx, processProjectionInteractiveYieldLimit)
			windows, err := s.terminal.processWindowsContext(ctx, owner.item, owner.thread)
			if err == nil {
				for _, window := range windows {
					for _, webServerURL := range window.WebServers {
						values = append(values, sidebarProcessWebServer{
							ProjectID:   owner.item.ID,
							ProjectName: owner.item.Name,
							ThreadID:    owner.thread.ID,
							ThreadTitle: owner.thread.Title,
							ProcessID:   window.ID,
							ProcessName: window.Name,
							URL:         webServerURL,
						})
					}
				}
			}
		}
		c.mu.Lock()
		c.entries[key] = values
		c.mu.Unlock()
	}

	result := []sidebarProcessWebServer{}
	c.mu.Lock()
	for _, key := range orderedKeys {
		result = append(result, c.entries[key]...)
	}
	c.mu.Unlock()
	return result
}

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

func (s *Server) sidebarProcessWebServers() []sidebarProcessWebServer {
	return s.sidebarProcessWebServersContext(context.Background())
}

func (s *Server) sidebarProcessWebServersContext(ctx context.Context) []sidebarProcessWebServer {
	if s == nil || s.projects == nil {
		return []sidebarProcessWebServer{}
	}
	return s.processWebServerCache.snapshotContext(ctx, s, true)
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
