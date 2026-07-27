package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

const (
	maxChildThreadPromptBytes   = 256 << 10
	maxThreadMessageBytes       = 64 << 10
	maxPendingThreadMessages    = 100
	childCapabilityProbeTimeout = 30 * time.Second
	childProbeCleanupTimeout    = 15 * time.Second
)

func validChildSkillForkRequestID(value string) bool {
	if value == "" || len(value) > 200 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

type childThreadCreationSource uint8

const (
	childThreadCreationDirect childThreadCreationSource = iota
	childThreadCreationWorkflow
	childThreadCreationSkillFork
)

type workflowChildIdentity struct {
	RunID   string
	AgentID string
}

type childThreadEndpoint func(projectID, threadID string) string

type childThreadRunResponse struct {
	Thread   project.Thread      `json:"thread"`
	Run      piNativeRunSnapshot `json:"run"`
	Agent    string              `json:"agent"`
	Existing bool                `json:"-"`
}

type listedChildThread struct {
	Thread project.Thread       `json:"thread"`
	Run    *piNativeRunSnapshot `json:"run,omitempty"`
	Agent  string               `json:"agent"`
}

type childThreadModelOption struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	ReasoningLevels []string `json:"reasoningLevels"`
}

type childThreadModelValidationError struct {
	Error           string                   `json:"error"`
	Agent           string                   `json:"agent"`
	AvailableModels []childThreadModelOption `json:"availableModels"`
}

type childThreadMessage struct {
	ID              uint64    `json:"id"`
	FromThreadID    string    `json:"fromThreadId"`
	FromThreadTitle string    `json:"fromThreadTitle"`
	Message         string    `json:"message"`
	CreatedAt       time.Time `json:"createdAt"`
}

type childThreadMessageStore struct {
	mu      sync.Mutex
	nextID  uint64
	pending map[terminalThreadKey][]childThreadMessage
}

func newChildThreadMessageStore() *childThreadMessageStore {
	return &childThreadMessageStore{pending: make(map[terminalThreadKey][]childThreadMessage)}
}

func (s *childThreadMessageStore) enqueue(key terminalThreadKey, message childThreadMessage) (childThreadMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending[key]) >= maxPendingThreadMessages {
		return childThreadMessage{}, errors.New("the related thread has too many undelivered messages")
	}
	s.nextID++
	message.ID = s.nextID
	message.CreatedAt = time.Now().UTC()
	s.pending[key] = append(s.pending[key], message)
	return message, nil
}

func (s *childThreadMessageStore) drain(key terminalThreadKey) []childThreadMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := append([]childThreadMessage{}, s.pending[key]...)
	delete(s.pending, key)
	return messages
}

func (s *childThreadMessageStore) removeThread(projectID, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, terminalThreadKey{ProjectID: projectID, ThreadID: threadID})
	for key, messages := range s.pending {
		filtered := messages[:0]
		for _, message := range messages {
			if message.FromThreadID != threadID || key.ProjectID != projectID {
				filtered = append(filtered, message)
			}
		}
		if len(filtered) == 0 {
			delete(s.pending, key)
		} else {
			s.pending[key] = filtered
		}
	}
}

func (s *childThreadMessageStore) removeProject(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.pending {
		if key.ProjectID == projectID {
			delete(s.pending, key)
		}
	}
}

func childThreadModelOptions(models []piModelCapability) []childThreadModelOption {
	options := make([]childThreadModelOption, 0, len(models))
	for _, model := range models {
		options = append(options, childThreadModelOption{
			ID:              model.ID,
			Label:           model.Label,
			ReasoningLevels: explicitPiReasoningLevels(model),
		})
	}
	return options
}

func childThreadModelValidationFailure(status int, message string, models []piModelCapability) *apiOperationFailure {
	return &apiOperationFailure{
		status: status,
		body: childThreadModelValidationError{
			Error:           message,
			Agent:           codingAgentPi,
			AvailableModels: childThreadModelOptions(models),
		},
	}
}

func childThreadModelValidationMessage(validationErr error) string {
	switch {
	case errors.Is(validationErr, errPiModelUnavailable):
		return "The requested Pi model is not available. Use an exact provider/model ID from the returned list; no child thread was created."
	case errors.Is(validationErr, errPiModelRequiredForThinking):
		return "Choose an explicit Pi model when setting a reasoning level; no child thread was created."
	case errors.Is(validationErr, errPiThinkingLevelUnsupported):
		return "The requested Pi model does not support that reasoning level; no child thread was created."
	default:
		return validationErr.Error() + "; no child thread was created."
	}
}

func (s *Server) requireAgentCapability(w http.ResponseWriter, r *http.Request) bool {
	expected := ""
	if s.terminal != nil {
		expected = s.terminal.agentToken
	}
	provided := r.Header.Get(agentTokenHeader)
	if expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		writeError(w, http.StatusForbidden, "This endpoint is only available to Kiwi Code-managed agents.")
		return false
	}
	return true
}

func (s *Server) getThreadNestingContext(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	context, err := s.projects.SubAgentNestingContext(r.PathValue("id"), r.PathValue("threadId"))
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread's nesting context.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		CurrentDepth   int `json:"currentDepth"`
		MaxDepth       int `json:"maxDepth"`
		RemainingDepth int `json:"remainingDepth"`
	}{
		CurrentDepth:   context.CurrentDepth,
		MaxDepth:       context.MaxDepth,
		RemainingDepth: max(0, context.MaxDepth-context.CurrentDepth),
	})
}

func (s *Server) childParentStopping(projectID, parentThreadID string) (bool, error) {
	if s.terminal == nil {
		return false, errors.New("terminal handler is unavailable")
	}
	manager := s.terminal.durableTerminalStopManager()
	if manager == nil {
		return false, errors.New("terminal stop manager is unavailable")
	}
	return manager.threadStopped(projectID, parentThreadID)
}

func validChildBaseRevision(revision string) bool {
	if len(revision) != 40 && len(revision) != 64 {
		return false
	}
	for _, character := range revision {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func resolveChildCapabilityRevision(ctx context.Context, item project.Project, baseBranch string) (string, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("Git is not installed or not on PATH")
	}
	ref := "HEAD"
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch != "" {
		ref = "refs/heads/" + baseBranch
		if err := exec.CommandContext(ctx, gitPath, "check-ref-format", ref).Run(); err != nil {
			return "", errors.New("base branch has an invalid Git ref name")
		}
	}
	resolve := exec.CommandContext(ctx, gitPath, "-C", item.Path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	output, err := resolve.Output()
	if err != nil {
		return "", errors.New("base branch does not resolve to a commit")
	}
	revision := strings.TrimSpace(string(output))
	if !validChildBaseRevision(revision) {
		return "", errors.New("base branch did not resolve to a full Git object ID")
	}
	return revision, nil
}

func createChildCapabilityProbeWorktree(ctx context.Context, item project.Project, dataDirectory, revision string) (string, func(), error) {
	if !validChildBaseRevision(revision) {
		return "", nil, errors.New("invalid child base revision")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", nil, errors.New("Git is not installed or not on PATH")
	}
	verify := exec.CommandContext(ctx, gitPath, "-C", item.Path, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	resolved, err := verify.Output()
	if err != nil || strings.TrimSpace(string(resolved)) != revision {
		return "", nil, errors.New("child base revision is not an available commit")
	}
	prefixCommand := exec.CommandContext(ctx, gitPath, "-C", item.Path, "rev-parse", "--show-prefix")
	prefixOutput, err := prefixCommand.Output()
	if err != nil {
		return "", nil, errors.New("could not resolve the project path inside its Git repository")
	}
	prefix := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(prefixOutput))))
	if prefix == "" {
		prefix = "."
	}
	if filepath.IsAbs(prefix) || prefix == ".." || strings.HasPrefix(prefix, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("could not resolve the project path inside its Git repository")
	}
	probeParent := filepath.Join(dataDirectory, "child-capability-probes")
	if err := os.MkdirAll(probeParent, 0o700); err != nil {
		return "", nil, fmt.Errorf("create child capability probe directory: %w", err)
	}
	probeRoot, err := os.MkdirTemp(probeParent, "probe-")
	if err != nil {
		return "", nil, fmt.Errorf("create child capability probe: %w", err)
	}
	worktreePath := filepath.Join(probeRoot, "worktree")
	add := exec.CommandContext(ctx, gitPath, "-C", item.Path, "worktree", "add", "--detach", worktreePath, revision)
	if output, addErr := add.CombinedOutput(); addErr != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), childProbeCleanupTimeout)
		_ = exec.CommandContext(cleanupContext, gitPath, "-C", item.Path, "worktree", "remove", "--force", worktreePath).Run()
		cancel()
		_ = os.RemoveAll(probeRoot)
		return "", nil, fmt.Errorf("create child capability probe worktree: %w: %s", addErr, strings.TrimSpace(string(output)))
	}
	cleanup := func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), childProbeCleanupTimeout)
		defer cancel()
		_ = exec.CommandContext(cleanupContext, gitPath, "-C", item.Path, "worktree", "remove", "--force", worktreePath).Run()
		_ = os.RemoveAll(probeRoot)
	}
	probeCwd := filepath.Join(worktreePath, prefix)
	if info, err := os.Stat(probeCwd); err != nil || !info.IsDir() {
		cleanup()
		return "", nil, errors.New("project path is missing from the child baseline")
	}
	return probeCwd, cleanup, nil
}

func (s *Server) rollbackCreatedChildThread(item project.Project, thread project.Thread, stopNative bool, stage string) error {
	marked, markErr := s.projects.BeginThreadCreationRollback(item.ID, thread.ID)
	if !marked {
		if markErr != nil {
			log.Printf("could not quarantine failed child creation: project=%q parent=%q thread=%q stage=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, stage, markErr)
		}
		return markErr
	}
	if stopNative && s.terminal != nil && s.terminal.nativePi != nil {
		if nativeErr := s.terminal.nativePi.removeThread(item.ID, thread.ID); nativeErr != nil {
			rollbackErr := errors.Join(markErr, nativeErr)
			log.Printf("rollback deferred after Pi teardown failed: project=%q parent=%q thread=%q stage=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, stage, rollbackErr)
			return rollbackErr
		}
	}
	if s.threadUsage != nil {
		if usageErr := s.threadUsage.remove(item.ID, thread.ID); usageErr != nil {
			rollbackErr := errors.Join(markErr, usageErr)
			log.Printf("rollback deferred after usage teardown failed: project=%q parent=%q thread=%q stage=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, stage, rollbackErr)
			return rollbackErr
		}
	}
	finalizeErr := s.projects.FinalizeThreadCreationRollback(item.ID, thread.ID)
	if finalizeErr != nil {
		rollbackErr := errors.Join(markErr, finalizeErr)
		log.Printf("rollback failed for child thread creation: project=%q parent=%q thread=%q stage=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, stage, rollbackErr)
		return rollbackErr
	}
	if markErr != nil {
		log.Printf("child creation rollback completed after a marker durability error: project=%q parent=%q thread=%q stage=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, stage, markErr)
	}
	return markErr
}

func (s *Server) createChildThread(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	s.createChildThreadAuthorized(w, r, childThreadCreationDirect)
}

func (s *Server) createSkillForkChild(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	s.createChildThreadAuthorized(w, r, childThreadCreationSkillFork)
}

func (s *Server) existingSkillForkResponse(
	projectID string,
	thread project.Thread,
) (childThreadRunResponse, *apiOperationFailure) {
	if thread.RollbackPending {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The matching skill fork is still being created; retry this request.")
	}
	if s.terminal == nil || s.terminal.nativePi == nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusServiceUnavailable, "Pi child management is unavailable.")
	}
	run, found := s.terminal.nativePi.latestChildRun(projectID, thread.ID)
	if !found {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The matching skill fork already exists, but its run state is unavailable after restart. Review the retained child thread.")
	}
	return childThreadRunResponse{Thread: thread, Run: run, Agent: codingAgentPi, Existing: true}, nil
}

func (s *Server) stopSkillForkChild(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	parentID := r.PathValue("threadId")
	childID := r.PathValue("childId")
	_, child, err := s.projects.GetThread(projectID, childID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Skill fork child not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the skill fork child.")
		return
	}
	if child.ParentThreadID != parentID || child.WorkflowRunID != "" || child.WorkflowAgentID != "" {
		writeError(w, http.StatusNotFound, "Skill fork child not found.")
		return
	}
	if child.RollbackPending {
		writeError(w, http.StatusConflict, "The skill fork child is being rolled back.")
		return
	}
	if child.ClosedAt != nil {
		writeJSON(w, http.StatusOK, child)
		return
	}
	if s.terminal == nil || s.terminal.nativePi == nil {
		writeError(w, http.StatusServiceUnavailable, "Pi child management is unavailable.")
		return
	}
	if err := s.terminal.nativePi.stopThread(projectID, childID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stop the skill fork child.")
		return
	}
	closed, failure := s.closeChildThreadOperation(projectID, parentID, childID)
	if failure != nil {
		failure.write(w)
		return
	}
	writeJSON(w, http.StatusOK, closed)
}

// createChildThreadAuthorized keeps general direct child creation disabled
// while letting the scoped workflow runner and the context: fork skill tool
// share the same hardened child creation transaction. The workflow caller has
// its own per-run capability; skill forks use the managed Pi capability and do
// not enter the workflow control plane.
func (s *Server) createChildThreadAuthorized(w http.ResponseWriter, r *http.Request, source childThreadCreationSource) {
	endpoint := func(projectID, threadID string) string {
		return threadEndpointURL(r, projectID, threadID)
	}
	created, failure := s.createChildThreadOperation(
		r.Context(),
		http.MaxBytesReader(w, r.Body, maxChildThreadPromptBytes+(1<<20)),
		r.PathValue("id"),
		r.PathValue("threadId"),
		source,
		workflowChildIdentity{},
		endpoint,
	)
	if failure != nil {
		failure.write(w)
		return
	}
	status := http.StatusCreated
	if created.Existing {
		status = http.StatusOK
	}
	writeJSON(w, status, created)
}

func (s *Server) createChildThreadOperation(
	ctx context.Context,
	bodyReader io.Reader,
	projectID, parentID string,
	source childThreadCreationSource,
	workflowIdentity workflowChildIdentity,
	endpoint childThreadEndpoint,
) (childThreadRunResponse, *apiOperationFailure) {
	if source == childThreadCreationDirect && !s.allowChildThreadCreation {
		return childThreadRunResponse{}, apiOperationError(http.StatusServiceUnavailable, "Direct sub-agent creation is temporarily disabled; use a context: fork skill or a Kiwi Code workflow.")
	}
	if source != childThreadCreationDirect && source != childThreadCreationWorkflow && source != childThreadCreationSkillFork {
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Invalid child creation source.")
	}
	item, parent, err := s.projects.GetThread(projectID, parentID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		return childThreadRunResponse{}, apiOperationError(http.StatusNotFound, "Parent thread not found.")
	}
	if err != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not load the parent thread.")
	}
	if parent.RollbackPending {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The parent thread is being rolled back; no child thread was created.")
	}
	if parent.ArchivedAt != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "Restore the parent thread before creating a child.")
	}
	if parent.ClosedAt != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "Reopen the parent thread before creating a child.")
	}
	if reached, _, budgetErr := s.threadBudgetReached(projectID, parentID); budgetErr != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not verify the parent thread's usage limit.")
	} else if reached {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The parent thread's token or cost limit has been reached; no child thread was created.")
	}
	if stopping, stopErr := s.childParentStopping(projectID, parentID); stopErr != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not verify that the parent thread is active.")
	} else if stopping {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The parent thread is being deleted; no child thread was created.")
	}

	var input struct {
		Title         string `json:"title"`
		Prompt        string `json:"prompt"`
		Agent         string `json:"agent"`
		Model         string `json:"model"`
		ThinkingLevel string `json:"thinkingLevel"`
		Worktree      *bool  `json:"worktree"`
		BaseBranch    string `json:"baseBranch"`
		NestedDepth   *int   `json:"nestedDepth"`
		RequestID     string `json:"requestId"`
	}
	body, err := io.ReadAll(bodyReader)
	if err != nil || !utf8.Valid(body) {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "Invalid child thread details.")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "Invalid child thread details.")
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Agent = strings.TrimSpace(input.Agent)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.Title == "" || strings.TrimSpace(input.Prompt) == "" {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "A child title and prompt are required.")
	}
	if len(input.Prompt) > maxChildThreadPromptBytes || !utf8.ValidString(input.Prompt) || strings.ContainsRune(input.Prompt, '\x00') {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "The child prompt is too long or contains an invalid character.")
	}
	projectMaxDepth := s.projects.GetSettings().SubAgentNestingDepth
	if item.SubAgentNestingDepthOverride != nil {
		projectMaxDepth = *item.SubAgentNestingDepthOverride
	}
	if input.NestedDepth != nil && (*input.NestedDepth < 0 || *input.NestedDepth > projectMaxDepth) {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "Nested depth must be between 0 and the project's maximum nesting depth.")
	}
	if input.Agent == "" {
		input.Agent = codingAgentPi
	}
	if input.Agent != codingAgentPi {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "Only Pi child threads are supported for now.")
	}
	if source == childThreadCreationSkillFork {
		// Older already-running Pi extension processes do not send requestId.
		// Keep that path compatible; newly materialized extensions always send a
		// durable ID and receive idempotent creation/recovery semantics.
		if input.RequestID != "" && !validChildSkillForkRequestID(input.RequestID) {
			return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "The skill fork request ID is invalid.")
		}
		if input.RequestID != "" {
			for _, existing := range item.Threads {
				if existing.ParentThreadID == parentID && existing.SkillForkRequestID == input.RequestID {
					return s.existingSkillForkResponse(projectID, existing)
				}
			}
		}
	} else if input.RequestID != "" {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "A request ID is only supported for context: fork skills.")
	}
	launchOptions, err := normalizeCodingAgentLaunchOptions(codingAgentPi, input.Model, input.ThinkingLevel)
	if err != nil {
		return childThreadRunResponse{}, childThreadModelValidationFailure(http.StatusBadRequest, err.Error()+"; no child thread was created.", nil)
	}
	worktree := false
	if source == childThreadCreationSkillFork {
		if input.Worktree != nil && *input.Worktree {
			return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "A context: fork skill must share the parent workspace.")
		}
	} else {
		resolved := s.projects.ResolveSnapshot([]project.Project{item})
		worktree = len(resolved) == 1 && resolved[0].IsGitRepo
		if input.Worktree != nil {
			worktree = *input.Worktree
		}
	}
	baseBranch := strings.TrimSpace(input.BaseBranch)
	if worktree && baseBranch == "" && parent.Worktree {
		baseBranch = parent.Branch
	}

	capabilityCwd := item.Path
	baseRevision := ""
	if worktree {
		probeContext, cancelProbe := context.WithTimeout(ctx, childCapabilityProbeTimeout)
		defer cancelProbe()
		var resolveErr error
		baseRevision, resolveErr = resolveChildCapabilityRevision(probeContext, item, baseBranch)
		if resolveErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, resolveErr.Error()+"; no child thread was created.")
		}
		probeCwd, cleanup, probeErr := createChildCapabilityProbeWorktree(probeContext, item, s.projects.DataDirectory(), baseRevision)
		if probeErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, "Could not prepare the child baseline for model validation; no child thread was created.")
		}
		capabilityCwd = probeCwd
		defer cleanup()
	}
	discoveryContext, cancelDiscovery := context.WithTimeout(ctx, codingAgentModelDiscoveryTimeout)
	availableModels, discoveryErr := s.terminal.availablePiModelCapabilities(discoveryContext, capabilityCwd, true)
	cancelDiscovery()
	if discoveryErr != nil || len(availableModels) == 0 {
		return childThreadRunResponse{}, childThreadModelValidationFailure(http.StatusServiceUnavailable, "Could not query Pi's available models in the child baseline; no child thread was created.", nil)
	}
	if validationErr := validatePiModelLaunchOptions(availableModels, launchOptions); validationErr != nil {
		return childThreadRunResponse{}, childThreadModelValidationFailure(http.StatusBadRequest, childThreadModelValidationMessage(validationErr), availableModels)
	}
	// Do not publish a child if the caller disappeared during capability
	// discovery. After persistence starts, the operation is intentionally
	// durable and can be recovered through its request ID.
	if ctx.Err() != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusRequestTimeout, "The child request was cancelled before creation; no child thread was created.")
	}

	thread, err := s.projects.AddThreadWithOptions(projectID, input.Title, project.AddThreadOptions{
		Worktree:           worktree,
		BaseBranch:         baseBranch,
		BaseRevision:       baseRevision,
		ParentThreadID:     parentID,
		AgentModel:         launchOptions.Model,
		AgentThinkingLevel: launchOptions.ThinkingLevel,
		WorkflowRunID:      workflowIdentity.RunID,
		WorkflowAgentID:    workflowIdentity.AgentID,
		SkillForkRequestID: input.RequestID,
		NestedDepth:        input.NestedDepth,
		CreationPending:    true,
	})
	if errors.Is(err, project.ErrChildCreationRequestExists) {
		return s.existingSkillForkResponse(projectID, thread)
	}
	// A save can be published before its final durability step reports an
	// error. AddThreadWithOptions returns the persisted thread in that case so
	// this request can still roll the transient creation back.
	if err != nil && thread.ID != "" {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, false, "persist child thread"); rollbackErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not save the child thread, and cleanup did not complete.")
		}
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not save the child thread; no child thread was created.")
	}
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		return childThreadRunResponse{}, apiOperationError(http.StatusNotFound, "Parent thread not found.")
	}
	if errors.Is(err, project.ErrChildThreadDepthLimit) {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The effective sub-agent nesting depth for this thread tree has been reached.")
	}
	if errors.Is(err, project.ErrThreadClosed) {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "Reopen the parent thread before creating a child.")
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The parent thread is being rolled back; no child thread was created.")
	}
	if err != nil {
		return childThreadRunResponse{}, apiOperationError(http.StatusBadRequest, err.Error())
	}
	if stopping, stopErr := s.childParentStopping(projectID, parentID); stopErr != nil || stopping {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, false, "verify parent remained active"); rollbackErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Child creation failed and cleanup did not complete.")
		}
		if stopErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not verify that the parent thread remained active.")
		}
		return childThreadRunResponse{}, apiOperationError(http.StatusConflict, "The parent thread is being deleted; no child thread was created.")
	}

	launchOptions.AllowPendingCreation = true
	if source == childThreadCreationSkillFork {
		// A context: fork skill is an implementation detail of the invoking
		// thread. Browser work performed by that child must remain visible in
		// the invoking thread's Browser workspace rather than creating a
		// short-lived session owned by the retained child.
		launchOptions.BrowserThreadEndpoint = endpoint(projectID, parentID)
	}
	process, err := s.terminal.startPiNativeProcess(
		item,
		thread,
		endpoint(projectID, thread.ID),
		launchOptions,
	)
	if err != nil {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "start Pi"); rollbackErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not start Pi in the child thread, and cleanup did not complete.")
		}
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not start Pi in the child thread.")
	}
	run, err := process.startPrompt(input.Prompt)
	if err != nil {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "send child prompt"); rollbackErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not send the child prompt to Pi, and cleanup did not complete.")
		}
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not send the child prompt to Pi.")
	}
	if s.childCreationBeforeCommit != nil {
		s.childCreationBeforeCommit(thread)
	}
	committed, commitErr := s.projects.CommitThreadCreation(projectID, thread.ID)
	if commitErr != nil && committed.ID == "" {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "commit child thread"); rollbackErr != nil {
			return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not commit the child thread, and cleanup did not complete.")
		}
		return childThreadRunResponse{}, apiOperationError(http.StatusInternalServerError, "Could not commit the child thread; no child thread was created.")
	}
	if commitErr != nil {
		log.Printf("child creation commit was published with a durability error: project=%q parent=%q thread=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, commitErr)
	}
	thread = committed
	return childThreadRunResponse{Thread: thread, Run: run, Agent: codingAgentPi}, nil
}

func (s *Server) listChildThreads(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	parentID := r.PathValue("threadId")
	item, _, err := s.projects.GetThread(projectID, parentID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Parent thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load child threads.")
		return
	}
	children := make([]listedChildThread, 0)
	for _, thread := range item.Threads {
		if thread.ParentThreadID != parentID || thread.ClosedAt != nil || thread.RollbackPending {
			continue
		}
		child := listedChildThread{Thread: thread, Agent: codingAgentPi}
		if run, found := s.terminal.nativePi.latestChildRun(projectID, thread.ID); found {
			child.Run = &run
		}
		children = append(children, child)
	}
	writeJSON(w, http.StatusOK, children)
}

func hasOpenChildThreadDescendants(threads []project.Thread, threadID string) bool {
	descendants := map[string]struct{}{threadID: {}}
	changed := true
	for changed {
		changed = false
		for _, thread := range threads {
			if _, known := descendants[thread.ID]; known {
				continue
			}
			if _, parentKnown := descendants[thread.ParentThreadID]; !parentKnown {
				continue
			}
			descendants[thread.ID] = struct{}{}
			changed = true
		}
	}
	delete(descendants, threadID)
	for _, thread := range threads {
		if _, descendant := descendants[thread.ID]; descendant && thread.ClosedAt == nil {
			return true
		}
	}
	return false
}

func (s *Server) closeChildThread(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	closed, failure := s.closeChildThreadOperation(
		r.PathValue("id"),
		r.PathValue("threadId"),
		r.PathValue("childId"),
	)
	if failure != nil {
		failure.write(w)
		return
	}
	writeJSON(w, http.StatusOK, closed)
}

func (s *Server) closeChildThreadOperation(projectID, parentID, childID string) (project.Thread, *apiOperationFailure) {
	item, child, err := s.projects.GetThread(projectID, childID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		return project.Thread{}, apiOperationError(http.StatusNotFound, "Child thread not found.")
	}
	if err != nil {
		return project.Thread{}, apiOperationError(http.StatusInternalServerError, "Could not load the child thread.")
	}
	if child.ParentThreadID != parentID {
		return project.Thread{}, apiOperationError(http.StatusNotFound, "Child thread not found.")
	}
	if child.RollbackPending {
		return project.Thread{}, apiOperationError(http.StatusConflict, "The child thread is being rolled back.")
	}
	if child.ClosedAt != nil {
		return child, nil
	}
	if hasOpenChildThreadDescendants(item.Threads, childID) {
		return project.Thread{}, apiOperationError(http.StatusConflict, "Close this thread's open descendants before closing it.")
	}

	closedAt := time.Now().UTC()
	if run, found := s.terminal.nativePi.latestChildRun(projectID, childID); found {
		if run.State == "starting" || run.State == "working" {
			return project.Thread{}, apiOperationError(http.StatusConflict, "Wait for the child run to settle before closing it.")
		}
		if run.FinishedAt != nil {
			closedAt = run.FinishedAt.UTC()
		}
	}
	closed, err := s.projects.CloseChildThread(projectID, parentID, childID, closedAt)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		return project.Thread{}, apiOperationError(http.StatusNotFound, "Child thread not found.")
	}
	if errors.Is(err, project.ErrThreadHasOpenDescendants) {
		return project.Thread{}, apiOperationError(http.StatusConflict, "Close this thread's open descendants before closing it.")
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		return project.Thread{}, apiOperationError(http.StatusConflict, "The child thread is being rolled back.")
	}
	if err != nil {
		return project.Thread{}, apiOperationError(http.StatusInternalServerError, "Could not retain the completed child thread.")
	}
	if err := s.terminal.nativePi.stopThread(projectID, childID); err != nil {
		_, _ = s.projects.ReopenChildThread(projectID, parentID, childID)
		return project.Thread{}, apiOperationError(http.StatusInternalServerError, "Could not stop Pi in the child thread.")
	}
	return closed, nil
}

func (s *Server) getChildThreadRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	parentID := r.PathValue("threadId")
	childID := r.PathValue("childId")
	_, child, err := s.projects.GetThread(projectID, childID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) || child.ParentThreadID != parentID {
		writeError(w, http.StatusNotFound, "Child thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the child thread.")
		return
	}
	runID, err := strconv.ParseUint(r.PathValue("runId"), 10, 64)
	if err != nil || runID == 0 {
		writeError(w, http.StatusBadRequest, "Invalid child run ID.")
		return
	}
	run, found := s.terminal.nativePi.childRun(projectID, childID, runID)
	if !found {
		writeError(w, http.StatusNotFound, "Child run not found.")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) sendThreadMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	senderID := r.PathValue("threadId")
	_, sender, err := s.projects.GetThread(projectID, senderID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Sending thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the sending thread.")
		return
	}
	if sender.RollbackPending {
		writeError(w, http.StatusConflict, "The sending thread is being rolled back.")
		return
	}
	var input struct {
		ThreadID string `json:"threadId"`
		Message  string `json:"message"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxThreadMessageBytes+(1<<10)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid thread message.")
		return
	}
	input.ThreadID = strings.TrimSpace(input.ThreadID)
	input.Message = strings.TrimSpace(input.Message)
	if input.ThreadID == "" {
		input.ThreadID = sender.ParentThreadID
	}
	if input.ThreadID == "" || input.Message == "" || len(input.Message) > maxThreadMessageBytes || !utf8.ValidString(input.Message) || strings.ContainsRune(input.Message, '\x00') {
		writeError(w, http.StatusBadRequest, "A valid related thread and message are required.")
		return
	}
	_, receiver, err := s.projects.GetThread(projectID, input.ThreadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Receiving thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the receiving thread.")
		return
	}
	if receiver.RollbackPending {
		writeError(w, http.StatusConflict, "The receiving thread is being rolled back.")
		return
	}
	if sender.ParentThreadID != receiver.ID && receiver.ParentThreadID != sender.ID {
		writeError(w, http.StatusForbidden, "Messages may only be sent between direct parent and child threads.")
		return
	}
	message, err := s.threadMessages.enqueue(
		terminalThreadKey{ProjectID: projectID, ThreadID: receiver.ID},
		childThreadMessage{FromThreadID: sender.ID, FromThreadTitle: sender.Title, Message: input.Message},
	)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, message)
}

func (s *Server) receiveThreadMessages(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	_, receiver, err := s.projects.GetThread(projectID, threadID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
			writeError(w, http.StatusNotFound, "Receiving thread not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "Could not load the receiving thread.")
		}
		return
	}
	if receiver.RollbackPending {
		writeError(w, http.StatusConflict, "The receiving thread is being rolled back.")
		return
	}
	writeJSON(w, http.StatusOK, s.threadMessages.drain(terminalThreadKey{ProjectID: projectID, ThreadID: threadID}))
}
