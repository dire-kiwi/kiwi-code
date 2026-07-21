package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	workflowActivationLifetime = 30 * time.Minute
	workflowActivationUses     = 8
)

var (
	workflowUltracodePattern      = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_])ultracode(?:$|[^[:alnum:]_])`)
	workflowDirectRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:use|run|start|launch|write|create|build|execute|orchestrate)\s+(?:(?:this|the|a|an|one|another)\s+)?(?:dynamic\s+)?workflows?\b`),
		regexp.MustCompile(`(?i)\b(?:as|with|via|through)\s+(?:a\s+)?(?:dynamic\s+)?workflow\b`),
		regexp.MustCompile(`(?i)\bworkflow\s+(?:this|that|it|the\s+(?:task|audit|migration|research|review|work))\b`),
	}
)

type workflowActivation struct {
	ExpiresAt     time.Time
	UsesRemaining int
	Mode          string
	SizeGuideline string
}

type workflowActivationSnapshot struct {
	Activated     bool      `json:"activated"`
	Mode          string    `json:"mode,omitempty"`
	SizeGuideline string    `json:"sizeGuideline,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`
	Reason        string    `json:"reason,omitempty"`
}

func workflowDisabledByEnvironment() bool {
	for _, name := range []string{"KIWI_CODE_DISABLE_WORKFLOWS", "CLAUDE_CODE_DISABLE_WORKFLOWS"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
		if value == "1" || value == "true" || value == "yes" || value == "on" {
			return true
		}
	}
	return false
}

func workflowPromptExplicitlyRequestsRun(prompt string, keywordEnabled bool) bool {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return false
	}
	if keywordEnabled && workflowUltracodePattern.MatchString(prompt) {
		return true
	}
	prompt = workflowPromptWithoutQuotedSegments(prompt)
	for _, pattern := range workflowDirectRequestPatterns {
		if pattern.MatchString(prompt) {
			return true
		}
	}
	return false
}

func workflowPromptWithoutQuotedSegments(prompt string) string {
	runes := []rune(prompt)
	result := make([]rune, 0, len(runes))
	var closing rune
	escaped := false
	for _, candidate := range runes {
		if closing != 0 {
			if escaped {
				escaped = false
				continue
			}
			if candidate == '\\' && (closing == '`' || closing == '"') {
				escaped = true
				continue
			}
			if candidate == closing {
				closing = 0
				result = append(result, ' ')
			}
			continue
		}
		switch candidate {
		case '`', '"':
			closing = candidate
			result = append(result, ' ')
		case '“':
			closing = '”'
			result = append(result, ' ')
		case '‘':
			closing = '’'
			result = append(result, ' ')
		default:
			result = append(result, candidate)
		}
	}
	return string(result)
}

func workflowActivationKey(projectID, threadID string) string {
	return projectID + "\x00" + threadID
}

func (m *workflowManager) setActivation(projectID, threadID, mode, sizeGuideline string) workflowActivationSnapshot {
	now := time.Now().UTC()
	activation := workflowActivation{
		ExpiresAt:     now.Add(workflowActivationLifetime),
		UsesRemaining: workflowActivationUses,
		Mode:          mode,
		SizeGuideline: sizeGuideline,
	}
	m.activationMu.Lock()
	m.activations[workflowActivationKey(projectID, threadID)] = activation
	m.activationMu.Unlock()
	return workflowActivationSnapshot{
		Activated:     true,
		Mode:          mode,
		SizeGuideline: sizeGuideline,
		ExpiresAt:     activation.ExpiresAt,
	}
}

func (m *workflowManager) clearActivation(projectID, threadID string) {
	m.activationMu.Lock()
	delete(m.activations, workflowActivationKey(projectID, threadID))
	m.activationMu.Unlock()
}

func (m *workflowManager) activation(projectID, threadID string, consume bool) (workflowActivation, bool) {
	key := workflowActivationKey(projectID, threadID)
	now := time.Now().UTC()
	m.activationMu.Lock()
	defer m.activationMu.Unlock()
	activation, ok := m.activations[key]
	if !ok || activation.UsesRemaining <= 0 || !activation.ExpiresAt.After(now) {
		delete(m.activations, key)
		return workflowActivation{}, false
	}
	if consume {
		activation.UsesRemaining--
		if activation.UsesRemaining == 0 {
			delete(m.activations, key)
		} else {
			m.activations[key] = activation
		}
	}
	return activation, true
}

func workflowRootThread(item project.Project, thread project.Thread) project.Thread {
	byID := make(map[string]project.Thread, len(item.Threads))
	for _, candidate := range item.Threads {
		byID[candidate.ID] = candidate
	}
	seen := make(map[string]struct{})
	for thread.ParentThreadID != "" {
		if _, duplicate := seen[thread.ID]; duplicate {
			break
		}
		seen[thread.ID] = struct{}{}
		parent, ok := byID[thread.ParentThreadID]
		if !ok {
			break
		}
		thread = parent
	}
	return thread
}

func (s *Server) workflowsDisabled() bool {
	return s.workflows == nil || s.workflows.forceDisabled || s.projects.GetSettings().DisableWorkflows
}

func (s *Server) workflowActivationForStart(item project.Project, thread project.Thread, consume bool) (workflowActivation, bool) {
	if s.workflowsDisabled() {
		return workflowActivation{}, false
	}
	root := workflowRootThread(item, thread)
	return s.workflows.activation(item.ID, root.ID, consume)
}

func (s *Server) activateWorkflows(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the workflow thread.")
		return
	}
	root := workflowRootThread(item, thread)
	if thread.ID != root.ID {
		activation, active := s.workflows.activation(projectID, root.ID, false)
		if !active || s.workflowsDisabled() {
			writeJSON(w, http.StatusOK, workflowActivationSnapshot{
				Reason: "This subagent has no active workflow grant from the root human prompt.",
			})
			return
		}
		writeJSON(w, http.StatusOK, workflowActivationSnapshot{
			Activated:     true,
			Mode:          "inherited",
			SizeGuideline: activation.SizeGuideline,
			ExpiresAt:     activation.ExpiresAt,
		})
		return
	}

	var input struct {
		Prompt           string `json:"prompt"`
		Source           string `json:"source"`
		Mode             string `json:"mode"`
		KeywordDismissed bool   `json:"keywordDismissed"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid workflow activation.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid workflow activation.")
		return
	}

	settings := s.projects.GetSettings()
	if s.workflowsDisabled() {
		s.workflows.clearActivation(projectID, root.ID)
		writeJSON(w, http.StatusOK, workflowActivationSnapshot{Reason: "Dynamic workflows are disabled in Settings or by the startup environment."})
		return
	}
	input.Source = strings.TrimSpace(input.Source)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	humanSource := input.Source == "interactive" || input.Source == "rpc"
	activated := false
	mode := "prompt"
	if humanSource && input.Mode == "ultracode" {
		activated = true
		mode = "ultracode"
	} else if humanSource && workflowPromptExplicitlyRequestsRun(input.Prompt, settings.WorkflowKeywordTrigger && !input.KeywordDismissed) {
		activated = true
	}
	if !activated && humanSource {
		if name := savedWorkflowInvocationName(input.Prompt); name != "" {
			if _, resolveErr := resolveSavedWorkflow(item, thread, name); resolveErr == nil {
				activated = true
				mode = "saved"
			}
		}
	}
	if !activated {
		s.workflows.clearActivation(projectID, root.ID)
		reason := "The current prompt did not explicitly opt in to a workflow. Use “ultracode”, ask to use/run a workflow, or invoke a saved workflow command."
		if !humanSource {
			reason = "Only a human interactive or Kiwi Code RPC prompt can activate workflows."
		}
		writeJSON(w, http.StatusOK, workflowActivationSnapshot{Reason: reason})
		return
	}
	writeJSON(w, http.StatusOK, s.workflows.setActivation(projectID, root.ID, mode, settings.WorkflowSizeGuideline))
}
