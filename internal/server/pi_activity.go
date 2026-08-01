package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/activity"
	"github.com/dire-kiwi/kiwi-code/internal/agent"
	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// Aliases while HTTP and cleanup code migrate to the activity package.
type (
	piActivityState   = activity.State
	piThreadActivity  = activity.ThreadActivity
	piActivityTracker = activity.Tracker
)

const (
	piActivityWorking        = activity.StateWorking
	piActivityFinished       = activity.StateFinished
	piActivityIdle           = activity.StateIdle
	maxPiActivityTokenLength = activity.MaxTokenLength
)

func (s *Server) listPiActivity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.clientPiActivities(s.piActivity.List(time.Now())))
}

func (s *Server) updatePiActivity(w http.ResponseWriter, r *http.Request) {
	s.updateAgentActivity(w, r, codingAgentPi, "Pi")
}

// agentActivityHandler adapts one registry activity route to the shared
// activity update handler.
func (s *Server) agentActivityHandler(route agent.ActivityRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.updateAgentActivity(w, r, route.AgentID, route.Label)
	}
}

func (s *Server) updateAgentActivity(w http.ResponseWriter, r *http.Request, agent, label string) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	_, thread, err := s.projects.GetThread(projectID, threadID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Thread not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}

	var input struct {
		State           piActivityState `json:"state"`
		Agent           string          `json:"agent,omitempty"`
		Session         string          `json:"session,omitempty"`
		Token           string          `json:"token,omitempty"`
		PromptStartedAt *time.Time      `json:"promptStartedAt,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid "+label+" activity.")
		return
	}
	if input.State != piActivityWorking && input.State != piActivityFinished && input.State != piActivityIdle {
		writeError(w, http.StatusBadRequest, "Unknown "+label+" activity state.")
		return
	}
	if len(input.Token) > maxPiActivityTokenLength || len(input.Session) > maxPiActivityTokenLength {
		writeError(w, http.StatusBadRequest, "The "+label+" activity identifiers are too long.")
		return
	}
	activityAgent := agent
	if input.Agent != "" {
		requestedAgent, normalizeErr := normalizeCodingAgent(input.Agent)
		validAgent := false
		switch agent {
		case codingAgentCodex:
			validAgent = normalizeErr == nil && requestedAgent == codingAgentCodex
		case codingAgentClaude:
			validAgent = normalizeErr == nil && isClaudeCodingAgent(requestedAgent)
		}
		if !validAgent {
			writeError(w, http.StatusBadRequest, "Unknown "+label+" coding agent.")
			return
		}
		activityAgent = requestedAgent
	}
	now := time.Now().UTC()
	promptStartedAt := input.PromptStartedAt
	if promptStartedAt != nil {
		normalized := promptStartedAt.UTC()
		if normalized.After(now) {
			normalized = now
		}
		promptStartedAt = &normalized
	}
	activity, startedWorking, applied := s.piActivity.UpdateAgentTokenAt(
		projectID,
		threadID,
		activityAgent,
		input.Session,
		input.Token,
		promptStartedAt,
		input.State,
		now,
	)
	if !applied {
		// A delayed update from a settled or older prompt. Ignoring it keeps the
		// indicator from flapping between prompt states.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var promptedAt *time.Time
	if input.State == piActivityWorking {
		promptedAt = promptStartedAt
		if promptedAt == nil && startedWorking {
			// Compatibility for already-running integrations that predate explicit
			// prompt timestamps. Repeated working heartbeats do not take this path.
			promptedAt = &now
		}
	}
	if promptedAt != nil {
		promptTime := promptedAt.UTC()
		if thread.LastPromptAt == nil || promptTime.After(*thread.LastPromptAt) {
			if _, err := s.projects.RecordThreadPrompt(projectID, threadID, promptTime); err != nil {
				writeError(w, http.StatusInternalServerError, "Could not record thread prompt activity.")
				return
			}
		}
	}
	if activity == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (s *Server) acknowledgePiActivity(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	if _, _, err := s.projects.GetThread(projectID, threadID); err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Thread not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}
	s.piActivity.Acknowledge(projectID, threadID)
	w.WriteHeader(http.StatusNoContent)
}

func newPiActivityTracker() *piActivityTracker {
	return activity.NewTracker(nil)
}

const (
	piWorkingTimeout         = activity.WorkingTimeout
	piActivityOrderRetention = activity.OrderRetention
)
