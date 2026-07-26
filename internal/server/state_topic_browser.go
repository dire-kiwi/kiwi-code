package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	browserStateReconcileInterval = 5 * time.Second
	maxBrowserStatePages          = 128
	maxBrowserStateRecordings     = 512
	maxBrowserStateIDBytes        = 512
	maxBrowserStateTitleBytes     = 4 << 10
	maxBrowserStateURLBytes       = 64 << 10
	maxBrowserStateMetadataBytes  = 4 << 10
)

type browserStateCapabilities struct {
	NativeView        *bool `json:"nativeView,omitempty"`
	InteractiveStream *bool `json:"interactiveStream,omitempty"`
	Preview           *bool `json:"preview,omitempty"`
	Recording         *bool `json:"recording,omitempty"`
}

type browserStatePage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type browserStateCurrentPage struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	CanGoBack    *bool  `json:"canGoBack,omitempty"`
	CanGoForward *bool  `json:"canGoForward,omitempty"`
	Loading      *bool  `json:"loading,omitempty"`
}

type browserStateRecording struct {
	ID             string   `json:"id"`
	State          string   `json:"state"`
	TargetID       string   `json:"targetId"`
	Title          string   `json:"title"`
	StartedAt      string   `json:"startedAt"`
	FinishedAt     string   `json:"finishedAt,omitempty"`
	DurationMS     *float64 `json:"durationMs,omitempty"`
	Bytes          *float64 `json:"bytes,omitempty"`
	MIMEType       string   `json:"mimeType,omitempty"`
	Filename       string   `json:"filename,omitempty"`
	IdleTimeoutMS  *float64 `json:"idleTimeoutMs,omitempty"`
	IdleDeadlineAt string   `json:"idleDeadlineAt,omitempty"`
}

type browserStateSnapshot struct {
	Backend         string                   `json:"backend"`
	Presentation    string                   `json:"presentation"`
	Capabilities    browserStateCapabilities `json:"capabilities"`
	Reachable       *bool                    `json:"reachable,omitempty"`
	Running         *bool                    `json:"running,omitempty"`
	Pages           []browserStatePage       `json:"pages"`
	CurrentTargetID *string                  `json:"currentTargetId"`
	Current         *browserStateCurrentPage `json:"current,omitempty"`
	Recording       *browserStateRecording   `json:"recording"`
	Recordings      []browserStateRecording  `json:"recordings"`
	Error           string                   `json:"error,omitempty"`
}

func emptyBrowserStateSnapshot() browserStateSnapshot {
	return browserStateSnapshot{
		Pages:      make([]browserStatePage, 0),
		Recordings: make([]browserStateRecording, 0),
	}
}

func (s *Server) openBrowserStatusTopic(
	ctx context.Context,
	projectID string,
	threadID string,
	protectedOrigins []string,
	channel *stateChannel,
) error {
	return s.openBrowserStateTopic(ctx, projectID, threadID, protectedOrigins, channel, func(snapshot browserStateSnapshot) any {
		return snapshot
	})
}

func (s *Server) openBrowserRecordingsTopic(
	ctx context.Context,
	projectID string,
	threadID string,
	protectedOrigins []string,
	channel *stateChannel,
) error {
	return s.openBrowserStateTopic(ctx, projectID, threadID, protectedOrigins, channel, func(snapshot browserStateSnapshot) any {
		return browserStateRecordingList(snapshot)
	})
}

func (s *Server) openBrowserStateTopic(
	ctx context.Context,
	projectID string,
	threadID string,
	protectedOrigins []string,
	channel *stateChannel,
	projectSnapshot func(browserStateSnapshot) any,
) error {
	changes, events := s.subscribeStateChanges(
		projectID,
		threadID,
		stateTopicBrowserStatus,
		stateTopicBrowserRecordings,
	)
	if changes != nil {
		defer changes.Close()
	}
	projectUpdates, unsubscribeProjects := s.projects.SubscribeLatestChanges()
	defer unsubscribeProjects()

	snapshot := func() error {
		value, err := s.readBrowserStateSnapshot(ctx, projectID, threadID, protectedOrigins)
		if err != nil {
			return err
		}
		return channel.Snapshot(projectSnapshot(value))
	}
	if err := snapshot(); err != nil {
		return err
	}

	ticker := time.NewTicker(browserStateReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-events:
			if err := snapshot(); err != nil {
				return err
			}
		case _, open := <-projectUpdates:
			if !open {
				return stateTopicFailure("Project updates ended.")
			}
			if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
				if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
					return stateTopicFailure("Thread no longer exists.")
				}
				return stateTopicFailure("Could not load the thread.")
			}
		case <-ticker.C:
			if err := snapshot(); err != nil {
				return err
			}
		case <-channel.Resnap():
			if err := snapshot(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) readBrowserStateSnapshot(
	ctx context.Context,
	projectID string,
	threadID string,
	protectedOrigins []string,
) (browserStateSnapshot, error) {
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			return browserStateSnapshot{}, stateTopicFailure("Thread not found.")
		}
		return browserStateSnapshot{}, stateTopicFailure("Could not load the thread.")
	}

	snapshot := emptyBrowserStateSnapshot()
	if s.browser == nil {
		snapshot.Error = "Browser provider is unavailable."
		return snapshot, nil
	}
	result, err := s.browser.Action(ctx, browsercontrol.Request{
		ProjectID:        projectID,
		ThreadID:         threadID,
		Operation:        "session.status",
		Params:           json.RawMessage(`{}`),
		ProtectedOrigins: append([]string(nil), protectedOrigins...),
	})
	if err != nil {
		if ctx.Err() != nil {
			return browserStateSnapshot{}, ctx.Err()
		}
		snapshot.Error = browserStateErrorMessage(err)
		return snapshot, nil
	}
	return normalizeBrowserStateSnapshot(result), nil
}

func normalizeBrowserStateSnapshot(raw json.RawMessage) browserStateSnapshot {
	snapshot := emptyBrowserStateSnapshot()
	result, ok := browserStateObject(raw)
	if !ok {
		snapshot.Error = "Browser provider returned an invalid status."
		return snapshot
	}
	status, _ := browserStateObject(result["status"])

	snapshot.Backend, _ = browserStateString(result["backend"], maxBrowserStateMetadataBytes)
	snapshot.Presentation, _ = browserStateString(result["presentation"], maxBrowserStateMetadataBytes)
	if snapshot.Presentation == "" {
		snapshot.Presentation, _ = browserStateString(status["presentation"], maxBrowserStateMetadataBytes)
	}
	capabilities, ok := browserStateObject(result["capabilities"])
	if !ok {
		capabilities, _ = browserStateObject(status["capabilities"])
	}
	snapshot.Capabilities = browserStateCapabilities{
		NativeView:        browserStateBool(capabilities["nativeView"]),
		InteractiveStream: browserStateBool(capabilities["interactiveStream"]),
		Preview:           browserStateBool(capabilities["preview"]),
		Recording:         browserStateBool(capabilities["recording"]),
	}
	snapshot.Reachable = browserStateBool(result["reachable"])
	if snapshot.Reachable == nil {
		snapshot.Reachable = browserStateBool(status["reachable"])
	}
	snapshot.Running = browserStateBool(result["running"])

	pageValues, ok := browserStateArray(result["pages"])
	if !ok {
		pageValues, _ = browserStateArray(result["pageList"])
	}
	for _, value := range pageValues {
		if len(snapshot.Pages) >= maxBrowserStatePages {
			break
		}
		if page, valid := normalizeBrowserStatePage(value); valid {
			snapshot.Pages = append(snapshot.Pages, page)
		}
	}

	currentTargetID, hasCurrentTargetID := browserStateString(result["currentTargetId"], maxBrowserStateIDBytes)
	if !hasCurrentTargetID {
		currentTargetID, hasCurrentTargetID = browserStateString(status["currentTargetId"], maxBrowserStateIDBytes)
	}
	if hasCurrentTargetID && strings.TrimSpace(currentTargetID) != "" {
		snapshot.CurrentTargetID = &currentTargetID
	}

	currentDetails, _ := browserStateObject(result["current"])
	if current, valid := normalizeBrowserStateCurrentPage(result["current"]); valid {
		snapshot.Current = &current
	} else {
		if len(currentDetails) == 0 {
			currentDetails, _ = browserStateObject(result["currentPage"])
		}
		for _, page := range snapshot.Pages {
			if snapshot.CurrentTargetID == nil || page.ID != *snapshot.CurrentTargetID {
				continue
			}
			snapshot.Current = &browserStateCurrentPage{
				ID:           page.ID,
				Title:        page.Title,
				URL:          page.URL,
				CanGoBack:    browserStateBool(currentDetails["canGoBack"]),
				CanGoForward: browserStateBool(currentDetails["canGoForward"]),
				Loading:      browserStateBool(currentDetails["loading"]),
			}
			break
		}
	}

	if recording, valid := normalizeBrowserStateRecording(result["recording"]); valid {
		snapshot.Recording = &recording
	}
	recordingValues, _ := browserStateArray(result["recordings"])
	seenRecordings := make(map[string]struct{}, len(recordingValues))
	for _, value := range recordingValues {
		if len(snapshot.Recordings) >= maxBrowserStateRecordings {
			break
		}
		recording, valid := normalizeBrowserStateRecording(value)
		if !valid {
			continue
		}
		if _, exists := seenRecordings[recording.ID]; exists {
			continue
		}
		seenRecordings[recording.ID] = struct{}{}
		snapshot.Recordings = append(snapshot.Recordings, recording)
	}
	return snapshot
}

func browserStateRecordingList(snapshot browserStateSnapshot) []browserStateRecording {
	result := make([]browserStateRecording, 0, len(snapshot.Recordings)+1)
	seen := make(map[string]struct{}, len(snapshot.Recordings)+1)
	if snapshot.Recording != nil {
		result = append(result, *snapshot.Recording)
		seen[snapshot.Recording.ID] = struct{}{}
	}
	for _, recording := range snapshot.Recordings {
		if _, exists := seen[recording.ID]; exists {
			continue
		}
		seen[recording.ID] = struct{}{}
		result = append(result, recording)
	}
	return result
}

func normalizeBrowserStatePage(raw json.RawMessage) (browserStatePage, bool) {
	value, ok := browserStateObject(raw)
	if !ok {
		return browserStatePage{}, false
	}
	id, ok := browserStateString(value["id"], maxBrowserStateIDBytes)
	if !ok || strings.TrimSpace(id) == "" {
		return browserStatePage{}, false
	}
	title, _ := browserStateString(value["title"], maxBrowserStateTitleBytes)
	url, _ := browserStateString(value["url"], maxBrowserStateURLBytes)
	return browserStatePage{ID: id, Title: title, URL: url}, true
}

func normalizeBrowserStateCurrentPage(raw json.RawMessage) (browserStateCurrentPage, bool) {
	value, ok := browserStateObject(raw)
	if !ok {
		return browserStateCurrentPage{}, false
	}
	page, ok := normalizeBrowserStatePage(raw)
	if !ok {
		return browserStateCurrentPage{}, false
	}
	return browserStateCurrentPage{
		ID:           page.ID,
		Title:        page.Title,
		URL:          page.URL,
		CanGoBack:    browserStateBool(value["canGoBack"]),
		CanGoForward: browserStateBool(value["canGoForward"]),
		Loading:      browserStateBool(value["loading"]),
	}, true
}

func normalizeBrowserStateRecording(raw json.RawMessage) (browserStateRecording, bool) {
	value, ok := browserStateObject(raw)
	if !ok {
		return browserStateRecording{}, false
	}
	id, idOK := browserStateString(value["id"], maxBrowserStateIDBytes)
	state, stateOK := browserStateString(value["state"], maxBrowserStateMetadataBytes)
	targetID, targetOK := browserStateString(value["targetId"], maxBrowserStateIDBytes)
	title, titleOK := browserStateString(value["title"], maxBrowserStateTitleBytes)
	startedAt, startedOK := browserStateString(value["startedAt"], maxBrowserStateMetadataBytes)
	if !idOK || strings.TrimSpace(id) == "" || !stateOK || !targetOK || !titleOK || !startedOK {
		return browserStateRecording{}, false
	}
	switch state {
	case "starting", "recording", "finalizing", "completed":
	default:
		return browserStateRecording{}, false
	}
	finishedAt, _ := browserStateString(value["finishedAt"], maxBrowserStateMetadataBytes)
	mimeType, _ := browserStateString(value["mimeType"], maxBrowserStateMetadataBytes)
	filename, _ := browserStateString(value["filename"], maxBrowserStateMetadataBytes)
	idleDeadlineAt, _ := browserStateString(value["idleDeadlineAt"], maxBrowserStateMetadataBytes)
	return browserStateRecording{
		ID:             id,
		State:          state,
		TargetID:       targetID,
		Title:          title,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		DurationMS:     browserStateNonNegativeNumber(value["durationMs"]),
		Bytes:          browserStateNonNegativeNumber(value["bytes"]),
		MIMEType:       mimeType,
		Filename:       filename,
		IdleTimeoutMS:  browserStateNonNegativeNumber(value["idleTimeoutMs"]),
		IdleDeadlineAt: idleDeadlineAt,
	}, true
}

func browserStateObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func browserStateArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value []json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func browserStateString(raw json.RawMessage, maxBytes int) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) > maxBytes {
		return "", false
	}
	return value, true
}

func browserStateBool(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

func browserStateNonNegativeNumber(raw json.RawMessage) *float64 {
	if len(raw) == 0 {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	return &value
}

func browserStateErrorMessage(err error) string {
	if code, ok := browsercontrol.OperationErrorCode(err); ok {
		if response, known := browserProviderErrorResponses[code]; known {
			return response.Message
		}
	}
	switch {
	case errors.Is(err, browsercontrol.ErrSessionNotFound):
		return "Browser session not found."
	case errors.Is(err, browsercontrol.ErrPreviewNotReady):
		return "Browser preview is not ready."
	case errors.Is(err, browsercontrol.ErrRecordingNotFound):
		return "Browser recording not found."
	case errors.Is(err, browsercontrol.ErrRecordingRangeNotSatisfiable):
		return "The requested browser recording range is not available."
	case errors.Is(err, browsercontrol.ErrProvider), errors.Is(err, browsercontrol.ErrRequestTooLarge):
		return "Browser provider returned an error."
	default:
		return "Browser provider is unavailable."
	}
}

func (s *Server) notifyBrowserStateChanged(projectID, threadID string) {
	s.notifyStateChanged(stateTopicBrowserStatus, projectID, threadID)
	s.notifyStateChanged(stateTopicBrowserRecordings, projectID, threadID)
}
