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

	"github.com/ivan/dire-mux/internal/project"
)

const (
	maxChildThreadPromptBytes   = 256 << 10
	maxThreadMessageBytes       = 64 << 10
	maxPendingThreadMessages    = 100
	childCapabilityProbeTimeout = 30 * time.Second
	childProbeCleanupTimeout    = 15 * time.Second
)

type workflowChildIdentityContextKey struct{}

type workflowChildIdentity struct {
	RunID   string
	AgentID string
}

type childThreadRunResponse struct {
	Thread project.Thread      `json:"thread"`
	Run    piNativeRunSnapshot `json:"run"`
	Agent  string              `json:"agent"`
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

func writeChildThreadModelValidationError(w http.ResponseWriter, status int, message string, models []piModelCapability) {
	writeJSON(w, status, childThreadModelValidationError{
		Error:           message,
		Agent:           codingAgentPi,
		AvailableModels: childThreadModelOptions(models),
	})
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
		writeError(w, http.StatusForbidden, "This endpoint is only available to Dire Mux-managed agents.")
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
	s.createChildThreadAuthorized(w, r, false)
}

// createChildThreadAuthorized keeps direct child creation disabled while
// allowing a scoped, server-owned workflow process to use the same hardened
// child creation transaction. Workflow callers are authenticated before this
// method and never receive the general managed-agent capability.
func (s *Server) createChildThreadAuthorized(w http.ResponseWriter, r *http.Request, workflow bool) {
	if !workflow && !s.allowChildThreadCreation {
		writeError(w, http.StatusServiceUnavailable, "Direct sub-agent creation is temporarily disabled; use a Dire Mux workflow.")
		return
	}
	projectID := r.PathValue("id")
	parentID := r.PathValue("threadId")
	item, parent, err := s.projects.GetThread(projectID, parentID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Parent thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the parent thread.")
		return
	}
	if parent.RollbackPending {
		writeError(w, http.StatusConflict, "The parent thread is being rolled back; no child thread was created.")
		return
	}
	if parent.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "Restore the parent thread before creating a child.")
		return
	}
	if parent.ClosedAt != nil {
		writeError(w, http.StatusConflict, "Reopen the parent thread before creating a child.")
		return
	}
	if reached, _, budgetErr := s.threadBudgetReached(projectID, parentID); budgetErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify the parent thread's usage limit.")
		return
	} else if reached {
		writeError(w, http.StatusConflict, "The parent thread's token or cost limit has been reached; no child thread was created.")
		return
	}
	if stopping, stopErr := s.childParentStopping(projectID, parentID); stopErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify that the parent thread is active.")
		return
	} else if stopping {
		writeError(w, http.StatusConflict, "The parent thread is being deleted; no child thread was created.")
		return
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
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChildThreadPromptBytes+(1<<20)))
	if err != nil || !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "Invalid child thread details.")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid child thread details.")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Agent = strings.TrimSpace(input.Agent)
	if input.Title == "" || strings.TrimSpace(input.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "A child title and prompt are required.")
		return
	}
	if len(input.Prompt) > maxChildThreadPromptBytes || !utf8.ValidString(input.Prompt) || strings.ContainsRune(input.Prompt, '\x00') {
		writeError(w, http.StatusBadRequest, "The child prompt is too long or contains an invalid character.")
		return
	}
	projectMaxDepth := s.projects.GetSettings().SubAgentNestingDepth
	if item.SubAgentNestingDepthOverride != nil {
		projectMaxDepth = *item.SubAgentNestingDepthOverride
	}
	if input.NestedDepth != nil && (*input.NestedDepth < 0 || *input.NestedDepth > projectMaxDepth) {
		writeError(w, http.StatusBadRequest, "Nested depth must be between 0 and the project's maximum nesting depth.")
		return
	}
	if input.Agent == "" {
		input.Agent = codingAgentPi
	}
	if input.Agent != codingAgentPi {
		writeError(w, http.StatusBadRequest, "Only Pi child threads are supported for now.")
		return
	}
	launchOptions, err := normalizeCodingAgentLaunchOptions(codingAgentPi, input.Model, input.ThinkingLevel)
	if err != nil {
		writeChildThreadModelValidationError(w, http.StatusBadRequest, err.Error()+"; no child thread was created.", nil)
		return
	}
	resolved := s.projects.ResolveSnapshot([]project.Project{item})
	worktree := len(resolved) == 1 && resolved[0].IsGitRepo
	if input.Worktree != nil {
		worktree = *input.Worktree
	}
	baseBranch := strings.TrimSpace(input.BaseBranch)
	if worktree && baseBranch == "" && parent.Worktree {
		baseBranch = parent.Branch
	}

	capabilityCwd := item.Path
	baseRevision := ""
	if worktree {
		probeContext, cancelProbe := context.WithTimeout(r.Context(), childCapabilityProbeTimeout)
		defer cancelProbe()
		var resolveErr error
		baseRevision, resolveErr = resolveChildCapabilityRevision(probeContext, item, baseBranch)
		if resolveErr != nil {
			writeError(w, http.StatusBadRequest, resolveErr.Error()+"; no child thread was created.")
			return
		}
		probeCwd, cleanup, probeErr := createChildCapabilityProbeWorktree(probeContext, item, s.projects.DataDirectory(), baseRevision)
		if probeErr != nil {
			writeError(w, http.StatusBadRequest, "Could not prepare the child baseline for model validation; no child thread was created.")
			return
		}
		capabilityCwd = probeCwd
		defer cleanup()
	}
	discoveryContext, cancelDiscovery := context.WithTimeout(r.Context(), codingAgentModelDiscoveryTimeout)
	availableModels, discoveryErr := s.terminal.availablePiModelCapabilities(discoveryContext, capabilityCwd, true)
	cancelDiscovery()
	if discoveryErr != nil || len(availableModels) == 0 {
		writeChildThreadModelValidationError(w, http.StatusServiceUnavailable, "Could not query Pi's available models in the child baseline; no child thread was created.", nil)
		return
	}
	if validationErr := validatePiModelLaunchOptions(availableModels, launchOptions); validationErr != nil {
		writeChildThreadModelValidationError(w, http.StatusBadRequest, childThreadModelValidationMessage(validationErr), availableModels)
		return
	}

	workflowIdentity, _ := r.Context().Value(workflowChildIdentityContextKey{}).(workflowChildIdentity)
	thread, err := s.projects.AddThreadWithOptions(projectID, input.Title, project.AddThreadOptions{
		Worktree:           worktree,
		BaseBranch:         baseBranch,
		BaseRevision:       baseRevision,
		ParentThreadID:     parentID,
		AgentModel:         launchOptions.Model,
		AgentThinkingLevel: launchOptions.ThinkingLevel,
		WorkflowRunID:      workflowIdentity.RunID,
		WorkflowAgentID:    workflowIdentity.AgentID,
		NestedDepth:        input.NestedDepth,
		CreationPending:    true,
	})
	// A save can be published before its final durability step reports an
	// error. AddThreadWithOptions returns the persisted thread in that case so
	// this request can still roll the transient creation back.
	if err != nil && thread.ID != "" {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, false, "persist child thread"); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not save the child thread, and cleanup did not complete.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not save the child thread; no child thread was created.")
		return
	}
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Parent thread not found.")
		return
	}
	if errors.Is(err, project.ErrChildThreadDepthLimit) {
		writeError(w, http.StatusConflict, "The effective sub-agent nesting depth for this thread tree has been reached.")
		return
	}
	if errors.Is(err, project.ErrThreadClosed) {
		writeError(w, http.StatusConflict, "Reopen the parent thread before creating a child.")
		return
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		writeError(w, http.StatusConflict, "The parent thread is being rolled back; no child thread was created.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if stopping, stopErr := s.childParentStopping(projectID, parentID); stopErr != nil || stopping {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, false, "verify parent remained active"); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "Child creation failed and cleanup did not complete.")
			return
		}
		if stopErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not verify that the parent thread remained active.")
		} else {
			writeError(w, http.StatusConflict, "The parent thread is being deleted; no child thread was created.")
		}
		return
	}

	launchOptions.AllowPendingCreation = true
	process, err := s.terminal.startPiNativeProcess(
		item,
		thread,
		threadEndpointURL(r, projectID, thread.ID),
		launchOptions,
	)
	if err != nil {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "start Pi"); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not start Pi in the child thread, and cleanup did not complete.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not start Pi in the child thread.")
		return
	}
	run, err := process.startPrompt(input.Prompt)
	if err != nil {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "send child prompt"); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not send the child prompt to Pi, and cleanup did not complete.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not send the child prompt to Pi.")
		return
	}
	if s.childCreationBeforeCommit != nil {
		s.childCreationBeforeCommit(thread)
	}
	committed, commitErr := s.projects.CommitThreadCreation(projectID, thread.ID)
	if commitErr != nil && committed.ID == "" {
		if rollbackErr := s.rollbackCreatedChildThread(item, thread, true, "commit child thread"); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not commit the child thread, and cleanup did not complete.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not commit the child thread; no child thread was created.")
		return
	}
	if commitErr != nil {
		log.Printf("child creation commit was published with a durability error: project=%q parent=%q thread=%q error=%v", item.ID, thread.ParentThreadID, thread.ID, commitErr)
	}
	thread = committed
	writeJSON(w, http.StatusCreated, childThreadRunResponse{Thread: thread, Run: run, Agent: codingAgentPi})
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
	s.closeChildThreadAuthorized(w, r)
}

func (s *Server) closeChildThreadAuthorized(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	parentID := r.PathValue("threadId")
	childID := r.PathValue("childId")
	item, child, err := s.projects.GetThread(projectID, childID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Child thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the child thread.")
		return
	}
	if child.ParentThreadID != parentID {
		writeError(w, http.StatusNotFound, "Child thread not found.")
		return
	}
	if child.RollbackPending {
		writeError(w, http.StatusConflict, "The child thread is being rolled back.")
		return
	}
	if child.ClosedAt != nil {
		writeJSON(w, http.StatusOK, child)
		return
	}
	if hasOpenChildThreadDescendants(item.Threads, childID) {
		writeError(w, http.StatusConflict, "Close this thread's open descendants before closing it.")
		return
	}

	closedAt := time.Now().UTC()
	if run, found := s.terminal.nativePi.latestChildRun(projectID, childID); found {
		if run.State == "starting" || run.State == "working" {
			writeError(w, http.StatusConflict, "Wait for the child run to settle before closing it.")
			return
		}
		if run.FinishedAt != nil {
			closedAt = run.FinishedAt.UTC()
		}
	}
	closed, err := s.projects.CloseChildThread(projectID, parentID, childID, closedAt)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Child thread not found.")
		return
	}
	if errors.Is(err, project.ErrThreadHasOpenDescendants) {
		writeError(w, http.StatusConflict, "Close this thread's open descendants before closing it.")
		return
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		writeError(w, http.StatusConflict, "The child thread is being rolled back.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not retain the completed child thread.")
		return
	}
	if err := s.terminal.nativePi.stopThread(projectID, childID); err != nil {
		_, _ = s.projects.ReopenChildThread(projectID, parentID, childID)
		writeError(w, http.StatusInternalServerError, "Could not stop Pi in the child thread.")
		return
	}
	writeJSON(w, http.StatusOK, closed)
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
