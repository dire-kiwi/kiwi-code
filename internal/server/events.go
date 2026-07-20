package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	globalProjectSnapshotInterval  = projectSnapshotInterval
	globalActivitySnapshotInterval = piActivitySnapshotInterval
	projectsEventName              = "projects"
	profilesEventName              = "profiles"
	piActivityEventName            = "pi-activity"
	threadUsageEventName           = "thread-usage"
	processWebServersEventName     = "processes"
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

// streamEvents is the browser's single global status stream. Project/thread,
// profile, Pi activity, and published process snapshots are named independently
// so terminal bytes can remain on their session-scoped WebSockets.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := prepareEventStream(w, "Global event streaming is unavailable.")
	if !ok {
		return
	}

	projectUpdates, unsubscribeProjects := s.projects.SubscribeChanges()
	defer unsubscribeProjects()
	profileUpdates, unsubscribeProfiles := s.projects.SubscribeProfileChanges()
	defer unsubscribeProfiles()
	activityUpdates, unsubscribeActivity := s.piActivity.subscribe()
	defer unsubscribeActivity()
	var threadStatusEvents <-chan threadStatusKey
	if s.threadStatusChanges != nil {
		statusUpdates := s.threadStatusChanges.Subscribe()
		defer statusUpdates.Close()
		threadStatusEvents = statusUpdates.Events()
	}
	var usageEvents <-chan struct{}
	if s.threadUsage != nil {
		usageUpdates, unsubscribeUsage := s.threadUsage.subscribe()
		defer unsubscribeUsage()
		usageEvents = usageUpdates.Events()
	}

	if err := writeNamedEvent(w, projectsEventName, clientProjects(s.projects.List())); err != nil {
		return
	}
	if err := writeNamedEvent(w, piActivityEventName, s.clientPiActivities(s.piActivity.list(time.Now()))); err != nil {
		return
	}
	if err := writeNamedEvent(w, profilesEventName, s.projects.ListProfiles()); err != nil {
		return
	}
	if s.threadUsage != nil {
		if err := writeNamedEvent(w, threadUsageEventName, s.threadUsage.snapshots(clientProjects(s.projects.List()))); err != nil {
			return
		}
	}

	processWatches := make(map[threadStatusKey]func())
	syncProcessWatches := func() bool {
		if s.terminal == nil {
			return false
		}
		desired := make(map[threadStatusKey]struct{})
		for _, item := range clientProjects(s.projects.List()) {
			for _, thread := range item.Threads {
				desired[threadStatusKey{projectID: item.ID, threadID: thread.ID}] = struct{}{}
			}
		}
		changed := len(desired) != len(processWatches)
		for key, stop := range processWatches {
			if _, exists := desired[key]; exists {
				continue
			}
			stop()
			delete(processWatches, key)
			changed = true
		}
		for key := range desired {
			if _, exists := processWatches[key]; exists {
				continue
			}
			processWatches[key] = s.terminal.watchThreadProcesses(key.projectID, key.threadID)
			changed = true
		}
		return changed
	}
	if s.terminal != nil {
		syncProcessWatches()
	}
	defer func() {
		for _, stop := range processWatches {
			stop()
		}
	}()

	var previousProcessPayload []byte
	writeProcesses := func(force bool) error {
		payload, err := json.Marshal(s.sidebarProcessWebServers())
		if err != nil {
			return err
		}
		if !force && bytes.Equal(payload, previousProcessPayload) {
			return nil
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", processWebServersEventName, payload); err != nil {
			return err
		}
		previousProcessPayload = append(previousProcessPayload[:0], payload...)
		return nil
	}
	if err := writeProcesses(true); err != nil {
		return
	}
	flusher.Flush()

	projectTicker := time.NewTicker(globalProjectSnapshotInterval)
	defer projectTicker.Stop()
	activityTicker := time.NewTicker(globalActivitySnapshotInterval)
	defer activityTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case projects, open := <-projectUpdates:
			if !open || writeNamedEvent(w, projectsEventName, clientProjects(s.projects.ResolveSnapshot(projects))) != nil {
				return
			}
			if syncProcessWatches() {
				if err := writeProcesses(false); err != nil {
					return
				}
			}
			flusher.Flush()
		case profiles, open := <-profileUpdates:
			if !open || writeNamedEvent(w, profilesEventName, profiles) != nil {
				return
			}
			flusher.Flush()
		case activities, open := <-activityUpdates:
			if !open || writeNamedEvent(w, piActivityEventName, s.clientPiActivities(activities)) != nil {
				return
			}
			flusher.Flush()
		case _, open := <-usageEvents:
			if !open || writeNamedEvent(w, threadUsageEventName, s.threadUsage.snapshots(clientProjects(s.projects.List()))) != nil {
				return
			}
			flusher.Flush()
		case _, open := <-threadStatusEvents:
			if !open || writeProcesses(false) != nil {
				return
			}
			flusher.Flush()
		case <-projectTicker.C:
			if err := writeNamedEvent(w, projectsEventName, clientProjects(s.projects.List())); err != nil {
				return
			}
			flusher.Flush()
		case <-activityTicker.C:
			if err := writeNamedEvent(w, piActivityEventName, s.clientPiActivities(s.piActivity.list(time.Now()))); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) sidebarProcessWebServers() []sidebarProcessWebServer {
	result := []sidebarProcessWebServer{}
	if s.terminal == nil || s.terminal.tmuxPath == "" {
		return result
	}
	for _, item := range clientProjects(s.projects.List()) {
		for _, thread := range item.Threads {
			windows, err := s.terminal.processWindows(item, thread)
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
// state for a project or thread that has since been removed. Project snapshots
// remain separate named events, so filtering does not overwrite or reorder the
// project domain.
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

func prepareEventStream(w http.ResponseWriter, unavailableMessage string) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, unavailableMessage)
		return nil, false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return flusher, true
}

func writeNamedEvent(w http.ResponseWriter, name string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
	return err
}
