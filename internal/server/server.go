package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ivan/dire-mux/internal/broadcast"
	"github.com/ivan/dire-mux/internal/browsercontrol"
	"github.com/ivan/dire-mux/internal/project"
)

//go:embed static
var staticFiles embed.FS

type Server struct {
	projects                  *project.Store
	browser                   *browsercontrol.Client
	terminal                  *terminalHandler
	piActivity                *piActivityTracker
	threadUsage               *threadUsageTracker
	contextStatuses           *contextStatusTracker
	threadMessages            *childThreadMessageStore
	threadStatusChanges       *broadcast.Broker[threadStatusKey]
	workflows                 *workflowManager
	plans                     *threadPlanManager
	agentSkills               *agentSkillInstaller
	instanceID                string
	restart                   func()
	allowChildThreadCreation  bool
	childCreationBeforeCommit func(project.Thread)
	workflowProcessLauncher   workflowProcessLauncher
	workflowProcessStopper    workflowProcessStopper
	assets                    fs.FS
	static                    http.Handler
	handler                   http.Handler
}

// General direct child creation remains disabled. Scoped server-side workflow
// runners and context: fork skills enter the hardened child transaction through
// separate authorization paths.
const childThreadCreationEnabled = false

const (
	maxPiImageBytes   int64 = 50 << 20
	piImageTempPrefix       = "dire-mux-pi-clipboard-"
)

func New(projects *project.Store) (http.Handler, error) {
	return NewWithOptions(projects, Options{})
}

func NewWithOptions(projects *project.Store, options Options) (http.Handler, error) {
	originPolicy, err := newOriginPolicy(options)
	if err != nil {
		return nil, err
	}
	tmuxSocket, err := configuredTmuxSocketName(options)
	if err != nil {
		return nil, err
	}
	var tmuxMigration *tmuxSocketMigration
	if tmuxSocket == tmuxSocketName {
		tmuxMigration, err = prepareDefaultTmuxSocketMigration(projects.DataDirectory(), tmuxSocketName, legacyTmuxSocketName)
		if err != nil {
			return nil, err
		}
	}
	assets, err := frontendAssets(staticFiles)
	if err != nil {
		return nil, err
	}
	agentSkillsDirectory, err := defaultAgentSkillsDirectory()
	if err != nil {
		return nil, err
	}
	usage, err := newThreadUsageTracker(projects.DataDirectory())
	if err != nil {
		return nil, err
	}
	workflows, err := newWorkflowManager(projects.DataDirectory())
	if err != nil {
		return nil, err
	}
	plans, err := newThreadPlanManager(projects.DataDirectory())
	if err != nil {
		return nil, err
	}
	terminal := newTerminalHandlerUnreconciledWithOptions(projects, originPolicy, tmuxSocket)
	terminal.tmuxSocketMigration = tmuxMigration
	if err := terminal.reconcileTerminalStops(); err != nil {
		log.Printf("reconcile durable terminal stops: error=%v", err)
	}
	if terminal.tmuxLogErr != nil {
		return nil, terminal.tmuxLogErr
	}
	if terminal.piExtensionErr != nil {
		return nil, terminal.piExtensionErr
	}
	if terminal.agentTokenErr != nil {
		return nil, terminal.agentTokenErr
	}
	if terminal.claudePluginErr != nil {
		return nil, terminal.claudePluginErr
	}
	terminal.nativePi.stopOnContext(options.CleanupContext)
	terminal.nativeClaude.stopOnContext(options.CleanupContext)
	server := &Server{
		projects:            projects,
		browser:             browsercontrol.New(browsercontrol.ConfigPath(projects.DataDirectory())),
		terminal:            terminal,
		piActivity:          newPiActivityTracker(),
		threadUsage:         usage,
		contextStatuses:     newContextStatusTracker(),
		threadMessages:      newChildThreadMessageStore(),
		threadStatusChanges: broadcast.NewBroker[threadStatusKey](broadcast.DefaultMaxPending),
		workflows:           workflows,
		plans:               plans,
		agentSkills:         newAgentSkillInstaller(agentSkillsDirectory),
		instanceID:          newServerInstanceID(),
		restart:             options.Restart,
		assets:              assets,
		static:              http.FileServer(http.FS(assets)),
	}
	server.allowChildThreadCreation = childThreadCreationEnabled
	terminal.threadStatusChanged = server.notifyThreadStatusChanged
	terminal.budgetReached = server.threadBudgetReached
	terminal.nativePi.usageReporter = func(key piNativeProcessKey, sessionID string, totals threadUsageTotals) {
		if err := server.threadUsage.report(key.ProjectID, key.ThreadID, sessionID, totals); err != nil {
			log.Printf("record native Pi usage: project=%q thread=%q error=%v", key.ProjectID, key.ThreadID, err)
		}
	}
	terminal.nativeClaude.usageReporter = func(key piNativeProcessKey, sessionID string, totals threadUsageTotals) {
		if err := server.threadUsage.report(key.ProjectID, key.ThreadID, sessionID, totals); err != nil {
			log.Printf("record native Claude usage: project=%q thread=%q error=%v", key.ProjectID, key.ThreadID, err)
		}
	}
	if err := server.recoverPendingThreadCreationRollbacks(); err != nil {
		return nil, fmt.Errorf("recover pending thread creation rollbacks: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("POST /api/restart", server.restartApplication)
	mux.HandleFunc("GET /api/settings", server.getSettings)
	mux.HandleFunc("PUT /api/settings", server.updateSettings)
	mux.HandleFunc("GET /api/settings/agent-skills", server.getAgentSkillStatus)
	mux.HandleFunc("POST /api/settings/agent-skills", server.installAgentSkill)
	mux.HandleFunc("GET /api/cleanup", server.getCleanupOverview)
	mux.HandleFunc("GET /api/coding-agents", server.terminal.listCodingAgents)
	mux.HandleFunc("GET /api/profiles", server.listProfiles)
	mux.HandleFunc("POST /api/profiles", server.addProfile)
	mux.HandleFunc("GET /api/tmux/sessions", server.terminal.listTmuxSessions)
	mux.HandleFunc("GET /api/tmux/terminal", server.terminal.serveTmuxBrowserTerminal)
	mux.HandleFunc("GET /api/projects", server.listProjects)
	mux.HandleFunc("GET /api/filesystem/directories", server.listDirectorySuggestions)
	mux.HandleFunc("GET /api/events", server.streamEvents)
	mux.HandleFunc("GET /api/projects/events", server.streamProjects)
	mux.HandleFunc("POST /api/projects", server.addProject)
	mux.HandleFunc("PUT /api/projects/order", server.reorderProjects)
	mux.HandleFunc("GET /api/pi/activity", server.listPiActivity)
	mux.HandleFunc("GET /api/pi/activity/events", server.streamPiActivity)
	mux.HandleFunc("GET /api/thread-usage", server.listThreadUsage)
	mux.HandleFunc("PATCH /api/projects/{id}", server.updateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", server.deleteProject)
	mux.HandleFunc("GET /api/projects/{id}/git/branches", server.listProjectGitBranches)
	mux.HandleFunc("POST /api/projects/{id}/threads", server.addThread)
	mux.HandleFunc("PUT /api/projects/{id}/threads/order", server.reorderThreads)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/nesting", server.getThreadNestingContext)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows", server.startWorkflow)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/workflows", server.listWorkflows)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/activation", server.activateWorkflows)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/workflows/saved", server.listSavedWorkflows)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/commands/run/{name}", server.startSavedWorkflow)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/workflows/{runId}", server.getWorkflow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/save", server.saveWorkflow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/pause", server.pauseWorkflow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/resume", server.resumeWorkflow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/stop", server.stopWorkflow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/events", server.workflowRunnerEvent)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/agents/{agentId}", server.createWorkflowAgent)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/workflows/{runId}/agents/{agentId}", server.getWorkflowAgentRun)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/workflows/{runId}/agents/{agentId}/close", server.closeWorkflowAgent)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/skill-forks", server.createSkillForkChild)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/skill-forks/{childId}/stop", server.stopSkillForkChild)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/children", server.createChildThread)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/children", server.listChildThreads)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/children/{childId}/close", server.closeChildThread)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/children/{childId}/runs/{runId}", server.getChildThreadRun)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/messages", server.sendThreadMessage)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/messages/receive", server.receiveThreadMessages)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/plans", server.listThreadPlans)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/plans", server.uploadThreadPlan)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/plans/{planId}", server.downloadThreadPlan)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}", server.getThread)
	mux.HandleFunc("PATCH /api/projects/{id}/threads/{threadId}", server.updateThread)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/limits", server.updateThreadLimits)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/usage", server.updateThreadUsage)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/budget", server.threadBudget)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}", server.deleteThread)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/browser", server.browserStatus)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/browser/actions", server.browserAction)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/browser/frame", server.browserFrame)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/pi/activity", server.updatePiActivity)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/claude/activity", server.updateClaudeActivity)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}/pi/activity", server.acknowledgePiActivity)
	mux.HandleFunc("PUT /api/projects/{id}/threads/{threadId}/context/status", server.updateContextStatus)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/events", server.streamThreadEvents)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/git/branches", server.listGitBranches)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/git/branches", server.createGitBranch)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/git/branches/switch", server.switchGitBranch)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/shell/windows", server.terminal.listShellWindows)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/shell/windows", server.terminal.createShellWindow)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/shell/windows/{index}/select", server.terminal.selectShellWindow)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/processes", server.terminal.listProcesses)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/processes", server.terminal.createProcess)
	mux.HandleFunc("PATCH /api/projects/{id}/threads/{threadId}/processes/{processId}", server.terminal.updateProcess)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/processes/{processId}/logs", server.terminal.processLogs)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal/lines", server.terminal.readTmuxLines)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/processes/{processId}/input", server.terminal.sendProcessInput)
	mux.HandleFunc("POST /api/projects/{id}/threads/{threadId}/processes/{processId}/interrupt", server.terminal.interruptProcess)
	mux.HandleFunc("DELETE /api/projects/{id}/threads/{threadId}/processes/{processId}", server.terminal.deleteProcess)
	mux.HandleFunc("POST /api/projects/{id}/pi/images", server.uploadPiImage)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/pi/native", server.terminal.servePiNative)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/claude/native", server.terminal.serveClaudeNative)
	mux.HandleFunc("GET /api/projects/{id}/threads/{threadId}/terminal", server.terminal.serve)
	mux.HandleFunc("/", server.serveFrontend)
	server.handler = withRequestLogging(withOriginPolicy(mux, originPolicy))
	if !options.DisableCleanup {
		server.startCleanupLoop(options.CleanupContext, options.CleanupInterval)
	}
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "ok",
		"instanceId": s.instanceID,
	})
}

func (s *Server) restartApplication(w http.ResponseWriter, _ *http.Request) {
	if s.restart == nil {
		writeError(w, http.StatusServiceUnavailable, "Application restart is unavailable.")
		return
	}

	w.Header().Set("Connection", "close")
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":     "restarting",
		"instanceId": s.instanceID,
	})
	s.restart()
}

func (s *Server) recoverPendingThreadCreationRollbacks() error {
	items, err := s.projects.ListPersisted()
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, item := range items {
		for _, thread := range item.Threads {
			if !thread.RollbackPending || thread.RollbackCleanupReady {
				continue
			}
			nativeErr := error(nil)
			if s.terminal != nil && s.terminal.nativePi != nil {
				nativeErr = s.terminal.nativePi.removeThread(item.ID, thread.ID)
			}
			if s.terminal != nil && s.terminal.nativeClaude != nil {
				nativeErr = errors.Join(nativeErr, s.terminal.nativeClaude.removeThread(item.ID, thread.ID))
			}
			usageErr := error(nil)
			if s.threadUsage != nil {
				usageErr = s.threadUsage.remove(item.ID, thread.ID)
			}
			if nativeErr != nil || usageErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("prepare project %q thread %q: %w", item.ID, thread.ID, errors.Join(nativeErr, usageErr)))
				continue
			}
			if finalizeErr := s.projects.FinalizeThreadCreationRollback(item.ID, thread.ID); finalizeErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("finalize project %q thread %q: %w", item.ID, thread.ID, finalizeErr))
			}
		}
	}
	return errors.Join(recoveryErrors...)
}

func newServerInstanceID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
}

func (s *Server) listProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.projects.ListProfiles())
}

func (s *Server) addProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile details.")
		return
	}
	profile, err := s.projects.AddProfile(input.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func clientProjects(projects []project.Project) []project.Project {
	filtered := make([]project.Project, len(projects))
	for index, item := range projects {
		filtered[index] = item
		threads := make([]project.Thread, 0, len(item.Threads))
		for _, thread := range item.Threads {
			if !thread.RollbackPending {
				threads = append(threads, thread)
			}
		}
		filtered[index].Threads = threads
	}
	return filtered
}

func (s *Server) listProjects(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, clientProjects(s.projects.List()))
}

func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid project details.")
		return
	}
	item, err := s.projects.Add(input.Name, input.Path, input.ProfileID)
	if errors.Is(err, project.ErrProfileNotFound) {
		writeError(w, http.StatusBadRequest, "Profile not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) reorderProjects(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProfileID  string   `json:"profileId"`
		ProjectIDs []string `json:"projectIds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid project order.")
		return
	}
	if err := s.projects.ReorderProjects(input.ProfileID, input.ProjectIDs); err != nil {
		switch {
		case errors.Is(err, project.ErrProfileNotFound):
			writeError(w, http.StatusBadRequest, "Profile not found.")
		case errors.Is(err, project.ErrInvalidOrder):
			writeError(w, http.StatusBadRequest, "Project order must include every project in the profile exactly once.")
		default:
			writeError(w, http.StatusInternalServerError, "Could not save the project order.")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type optionalProjectNestingDepth struct {
	present bool
	value   *int
}

func (o *optionalProjectNestingDepth) UnmarshalJSON(data []byte) error {
	o.present = true
	if string(data) == "null" {
		o.value = nil
		return nil
	}
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.value = &value
	return nil
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProfileID                    *string                     `json:"profileId"`
		SubAgentNestingDepthOverride optionalProjectNestingDepth `json:"subAgentNestingDepthOverride"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid project details.")
		return
	}

	item, err := s.projects.UpdateProject(r.PathValue("id"), project.ProjectUpdate{
		ProfileID:                          input.ProfileID,
		SubAgentNestingDepthOverride:       input.SubAgentNestingDepthOverride.value,
		UpdateSubAgentNestingDepthOverride: input.SubAgentNestingDepthOverride.present,
	})
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if errors.Is(err, project.ErrProfileNotFound) {
		writeError(w, http.StatusBadRequest, "Profile not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	item, err := s.projects.GetPersisted(projectID)
	if errors.Is(err, project.ErrNotFound) {
		ref := terminalStopMarkerRef{
			Scope:     terminalStopScopeProject,
			ProjectID: projectID,
		}
		found, recoveryErr := s.terminal.reconcileTerminalStop(ref)
		if recoveryErr != nil {
			log.Printf("retry deleted project terminal cleanup: project=%q error=%v", projectID, recoveryErr)
			writeError(w, http.StatusInternalServerError, "Could not finish the project's terminal cleanup.")
			return
		}
		if found {
			threadIDs, markerFound, markerErr := s.terminal.terminalStopBrowserThreadIDs(ref)
			if markerErr != nil || !markerFound {
				log.Printf("retry deleted project browser cleanup recipe: project=%q found=%t error=%v", projectID, markerFound, markerErr)
				writeError(w, http.StatusInternalServerError, "Could not finish the project's browser cleanup.")
				return
			}
			s.stopDeletedBrowserSessions(projectID, threadIDs)
			s.piActivity.removeProject(projectID)
			if s.threadUsage != nil {
				_ = s.threadUsage.remove(projectID, "")
			}
			s.contextStatuses.removeProject(projectID)
			s.removeDeletedProjectPlans(projectID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// A previous deletion may have removed the project before its auxiliary
		// plan cleanup completed and before a durable stop marker remained.
		s.removeDeletedProjectPlans(projectID)
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the project.")
		return
	}
	for _, thread := range item.Threads {
		if thread.RollbackPending {
			writeError(w, http.StatusConflict, "A thread creation rollback is still pending for this project.")
			return
		}
	}
	stoppedItem, stopLease, err := s.terminal.stopProjectSessions(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stop the project's terminal sessions.")
		return
	}
	item = stoppedItem
	if err := s.projects.Delete(projectID); err != nil {
		storeErr := err
		published, resolveErr := s.terminal.resolveStopProjectStoreError(item, stopLease)
		if published {
			s.stopDeletedBrowserSessions(projectID, browserProjectThreadIDs(item))
			if markerErr := s.terminal.removeCodingAgentExitMarkersForProject(item); markerErr != nil {
				log.Printf("remove published deleted project coding agent exit markers: project=%q error=%v", projectID, markerErr)
			}
			s.piActivity.removeProject(projectID)
			if s.threadUsage != nil {
				_ = s.threadUsage.remove(projectID, "")
			}
			s.contextStatuses.removeProject(projectID)
			s.removeDeletedProjectPlans(projectID)
			if resolveErr != nil {
				log.Printf("finish published deleted project terminal stop: project=%q store_error=%v finish_error=%v", projectID, storeErr, resolveErr)
				writeError(w, http.StatusInternalServerError, "The project was removed, but its terminal cleanup did not finish.")
				return
			}
			if errors.Is(storeErr, project.ErrNotFound) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			log.Printf("project deletion was published with a Store durability error: project=%q error=%v", projectID, storeErr)
		} else if resolveErr != nil {
			log.Printf("resolve failed project Store deletion: project=%q store_error=%v resolve_error=%v", projectID, storeErr, resolveErr)
		}
		writeError(w, http.StatusInternalServerError, "Could not remove the project.")
		return
	}
	s.stopDeletedBrowserSessions(projectID, browserProjectThreadIDs(item))
	finishErr := s.terminal.finishStopProject(item, stopLease)
	if err := s.terminal.removeCodingAgentExitMarkersForProject(item); err != nil {
		log.Printf("remove deleted project coding agent exit markers: project=%q error=%v", projectID, err)
	}
	s.piActivity.removeProject(projectID)
	if s.threadUsage != nil {
		if err := s.threadUsage.remove(projectID, ""); err != nil {
			log.Printf("remove deleted project usage: project=%q error=%v", projectID, err)
		}
	}
	s.contextStatuses.removeProject(projectID)
	s.removeDeletedProjectPlans(projectID)
	if s.threadMessages != nil {
		s.threadMessages.removeProject(projectID)
	}
	if finishErr != nil {
		log.Printf("finish deleted project terminal stop: project=%q error=%v", projectID, finishErr)
		writeError(w, http.StatusInternalServerError, "The project was removed, but its terminal cleanup did not finish.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addThread(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title       string `json:"title"`
		Worktree    bool   `json:"worktree"`
		BaseBranch  string `json:"baseBranch"`
		NestedDepth *int   `json:"nestedDepth"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid thread details.")
		return
	}

	projectID := r.PathValue("id")
	thread, err := s.projects.AddThreadWithOptions(projectID, input.Title, project.AddThreadOptions{
		Worktree:    input.Worktree,
		BaseBranch:  input.BaseBranch,
		NestedDepth: input.NestedDepth,
	})
	if err != nil && thread.ID != "" {
		rollbackErr := s.projects.RollbackThreadCreation(projectID, thread.ID)
		if rollbackErr != nil {
			log.Printf("rollback failed for thread creation: project=%q thread=%q error=%v", projectID, thread.ID, rollbackErr)
			writeError(w, http.StatusInternalServerError, "Could not save the thread, and cleanup did not complete.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not save the thread; no thread was created.")
		return
	}
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, thread)
}

func (s *Server) reorderThreads(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ThreadIDs []string `json:"threadIds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid thread order.")
		return
	}
	if err := s.projects.ReorderThreads(r.PathValue("id"), input.ThreadIDs); err != nil {
		switch {
		case errors.Is(err, project.ErrNotFound):
			writeError(w, http.StatusNotFound, "Project not found.")
		case errors.Is(err, project.ErrInvalidOrder):
			writeError(w, http.StatusBadRequest, "Thread order must include every thread in the project exactly once.")
		default:
			writeError(w, http.StatusInternalServerError, "Could not save the thread order.")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	_, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread.")
		return
	}
	if thread.RollbackPending {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) updateThread(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title         *string `json:"title"`
		AutoGenerated bool    `json:"autoGenerated"`
		Archived      *bool   `json:"archived"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Title == nil) == (input.Archived == nil) {
		writeError(w, http.StatusBadRequest, "Invalid thread details.")
		return
	}

	var thread project.Thread
	var err error
	if input.Title != nil {
		thread, err = s.projects.UpdateThreadTitle(
			r.PathValue("id"),
			r.PathValue("threadId"),
			*input.Title,
			input.AutoGenerated,
		)
	} else {
		thread, err = s.projects.SetThreadArchived(
			r.PathValue("id"),
			r.PathValue("threadId"),
			*input.Archived,
		)
	}
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyThreadStatusChanged(r.PathValue("id"), r.PathValue("threadId"))
	writeJSON(w, http.StatusOK, thread)
}

type deleteThreadFailure struct {
	status  int
	message string
	cause   error
}

func (failure *deleteThreadFailure) Error() string {
	return failure.message
}

func (failure *deleteThreadFailure) Unwrap() error {
	return failure.cause
}

func (s *Server) updateThreadLimits(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TokenLimit   json.RawMessage `json:"tokenLimit"`
		CostLimitUSD json.RawMessage `json:"costLimitUsd"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.TokenLimit == nil || input.CostLimitUSD == nil {
		writeError(w, http.StatusBadRequest, "Invalid thread limits.")
		return
	}
	var tokenLimit *int64
	if strings.TrimSpace(string(input.TokenLimit)) != "null" {
		var value int64
		if json.Unmarshal(input.TokenLimit, &value) != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "Token limit must be a positive whole number or null.")
			return
		}
		tokenLimit = &value
	}
	var costLimit *float64
	if strings.TrimSpace(string(input.CostLimitUSD)) != "null" {
		var value float64
		if json.Unmarshal(input.CostLimitUSD, &value) != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "Cost limit must be a positive USD amount or null.")
			return
		}
		costLimit = &value
	}
	thread, err := s.projects.UpdateThreadLimits(r.PathValue("id"), r.PathValue("threadId"), tokenLimit, costLimit)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if errors.Is(err, project.ErrThreadRollbackPending) {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.threadUsage.notify()
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request) {
	if failure := s.deleteThreadTree(r.PathValue("id"), r.PathValue("threadId"), nil); failure != nil {
		writeError(w, failure.status, failure.message)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type threadTreeStop struct {
	threadID string
	lease    *terminalStopLease
}

func projectThreadTree(item project.Project, rootThreadID string) []project.Thread {
	included := map[string]struct{}{rootThreadID: {}}
	for changed := true; changed; {
		changed = false
		for _, thread := range item.Threads {
			if _, found := included[thread.ID]; found {
				continue
			}
			if _, parentFound := included[thread.ParentThreadID]; !parentFound {
				continue
			}
			included[thread.ID] = struct{}{}
			changed = true
		}
	}
	tree := make([]project.Thread, 0, len(included))
	for _, thread := range item.Threads {
		if thread.ID == rootThreadID {
			tree = append(tree, thread)
			break
		}
	}
	for _, thread := range item.Threads {
		if thread.ID == rootThreadID {
			continue
		}
		if _, found := included[thread.ID]; found {
			tree = append(tree, thread)
		}
	}
	return tree
}

func (s *Server) reconcileDeletedThreadMarkers(projectID, requestedThreadID string) (bool, error) {
	manager := s.terminal.durableTerminalStopManager()
	if manager == nil {
		return false, errors.New("terminal stop marker manager is unavailable")
	}
	refs, listErr := manager.listMarkers()
	reconcileErrors := []error{listErr}
	requestedFound := false
	for _, ref := range refs {
		if ref.Scope != terminalStopScopeThread || ref.ProjectID != projectID {
			continue
		}
		exists, inspectErr := s.projects.PersistedResourceExists(ref.ProjectID, ref.ThreadID)
		if inspectErr != nil {
			reconcileErrors = append(reconcileErrors, inspectErr)
			continue
		}
		if exists {
			continue
		}
		found, reconcileErr := s.terminal.reconcileTerminalStop(ref)
		if ref.ThreadID == requestedThreadID && found {
			requestedFound = true
		}
		if reconcileErr != nil {
			reconcileErrors = append(reconcileErrors, reconcileErr)
			continue
		}
		if !found {
			continue
		}
		if s.terminal.nativePi != nil {
			reconcileErrors = append(reconcileErrors, s.terminal.nativePi.removeThread(ref.ProjectID, ref.ThreadID))
		}
		if s.terminal.nativeClaude != nil {
			reconcileErrors = append(reconcileErrors, s.terminal.nativeClaude.removeThread(ref.ProjectID, ref.ThreadID))
		}
		s.stopDeletedBrowserSessions(ref.ProjectID, []string{ref.ThreadID})
		s.finishDeletedThreadRuntime(ref.ProjectID, ref.ThreadID, "retried")
	}
	return requestedFound, errors.Join(reconcileErrors...)
}

func (s *Server) deleteThreadTree(projectID, threadID string, archivedBefore *time.Time) *deleteThreadFailure {
	item, _, err := s.projects.GetThreadPersisted(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		found, recoveryErr := s.reconcileDeletedThreadMarkers(projectID, threadID)
		if recoveryErr != nil {
			log.Printf("retry deleted thread terminal cleanup: project=%q thread=%q error=%v", projectID, threadID, recoveryErr)
			return &deleteThreadFailure{status: http.StatusInternalServerError, message: "Could not finish the thread tree's terminal cleanup.", cause: recoveryErr}
		}
		if found {
			return nil
		}
		if s.plans != nil {
			if planErr := s.plans.removeThread(projectID, threadID); planErr != nil {
				log.Printf("remove plans for already-deleted thread: project=%q thread=%q error=%v", projectID, threadID, planErr)
			}
		}
		return &deleteThreadFailure{status: http.StatusNotFound, message: "Thread not found.", cause: err}
	}
	if err != nil {
		return &deleteThreadFailure{status: http.StatusInternalServerError, message: "Could not load the thread.", cause: err}
	}

	tree := projectThreadTree(item, threadID)
	for _, treeThread := range tree {
		if treeThread.RollbackPending {
			return &deleteThreadFailure{
				status:  http.StatusConflict,
				message: "The thread tree contains a pending creation rollback.",
				cause:   project.ErrThreadRollbackPending,
			}
		}
	}
	expectedThreadIDs := make([]string, 0, len(tree))
	stops := make([]threadTreeStop, 0, len(tree))
	rollbackStops := func() error {
		var rollbackErrors []error
		for index := len(stops) - 1; index >= 0; index-- {
			rollbackErrors = append(rollbackErrors, s.terminal.cancelStopThread(projectID, stops[index].threadID, stops[index].lease))
		}
		return errors.Join(rollbackErrors...)
	}
	for _, treeThread := range tree {
		expectedThreadIDs = append(expectedThreadIDs, treeThread.ID)
		lease, stopErr := s.terminal.stopThreadSessions(item, treeThread.ID)
		if stopErr != nil {
			rollbackErr := rollbackStops()
			return &deleteThreadFailure{
				status:  http.StatusInternalServerError,
				message: "Could not stop the thread tree's terminal sessions.",
				cause:   errors.Join(stopErr, rollbackErr),
			}
		}
		stops = append(stops, threadTreeStop{threadID: treeThread.ID, lease: lease})
	}

	if archivedBefore == nil {
		err = s.projects.DeleteThreadTree(projectID, threadID, expectedThreadIDs)
	} else {
		err = s.projects.DeleteArchivedThreadTree(projectID, threadID, expectedThreadIDs, *archivedBefore)
	}
	if err != nil {
		publishedCount := 0
		publishedThreadIDs := make([]string, 0, len(stops))
		var resolveErrors []error
		for _, stop := range stops {
			published, resolveErr := s.terminal.resolveStopThreadStoreError(item, stop.threadID, stop.lease)
			if published {
				publishedCount++
				publishedThreadIDs = append(publishedThreadIDs, stop.threadID)
				s.finishDeletedThreadRuntime(projectID, stop.threadID, "published")
			}
			resolveErrors = append(resolveErrors, resolveErr)
		}
		s.stopDeletedBrowserSessions(projectID, publishedThreadIDs)
		resolveErr := errors.Join(resolveErrors...)
		if publishedCount == len(stops) {
			if resolveErr != nil {
				log.Printf("finish published deleted thread tree: project=%q thread=%q store_error=%v finish_error=%v", projectID, threadID, err, resolveErr)
				return &deleteThreadFailure{status: http.StatusInternalServerError, message: "The thread tree was removed, but its terminal cleanup did not finish.", cause: errors.Join(err, resolveErr)}
			}
			if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
				return nil
			}
			log.Printf("thread tree deletion was published with a Store durability error: project=%q thread=%q error=%v", projectID, threadID, err)
			return &deleteThreadFailure{status: http.StatusInternalServerError, message: "The thread tree was removed, but its metadata cleanup did not finish.", cause: err}
		}
		if publishedCount != 0 || resolveErr != nil {
			log.Printf("resolve failed thread tree Store deletion: project=%q thread=%q published=%d/%d store_error=%v resolve_error=%v", projectID, threadID, publishedCount, len(stops), err, resolveErr)
			return &deleteThreadFailure{status: http.StatusInternalServerError, message: "Could not resolve the thread tree deletion.", cause: errors.Join(err, resolveErr)}
		}
		switch {
		case errors.Is(err, project.ErrThreadTreeChanged):
			return &deleteThreadFailure{status: http.StatusConflict, message: "The thread tree changed while deletion was in progress. Try again.", cause: err}
		case errors.Is(err, project.ErrThreadNotArchived):
			return &deleteThreadFailure{status: http.StatusConflict, message: "The thread is no longer archived.", cause: err}
		case errors.Is(err, project.ErrNotFound), errors.Is(err, project.ErrThreadNotFound):
			return &deleteThreadFailure{status: http.StatusNotFound, message: "Thread not found.", cause: err}
		default:
			return &deleteThreadFailure{status: http.StatusInternalServerError, message: "Could not remove the thread tree.", cause: err}
		}
	}

	s.stopDeletedBrowserSessions(projectID, expectedThreadIDs)
	var finishErrors []error
	for _, stop := range stops {
		finishErrors = append(finishErrors, s.terminal.finishStopThread(item, stop.threadID, stop.lease))
		s.finishDeletedThreadRuntime(projectID, stop.threadID, "")
	}
	if finishErr := errors.Join(finishErrors...); finishErr != nil {
		log.Printf("finish deleted thread tree terminal stop: project=%q thread=%q error=%v", projectID, threadID, finishErr)
		return &deleteThreadFailure{status: http.StatusInternalServerError, message: "The thread tree was removed, but its terminal cleanup did not finish.", cause: finishErr}
	}
	return nil
}

func (s *Server) finishDeletedThreadRuntime(projectID, threadID, context string) {
	if err := s.terminal.removeCodingAgentExitMarkersForThread(projectID, threadID); err != nil {
		description := "deleted"
		if context != "" {
			description = context + " deleted"
		}
		log.Printf("remove %s thread coding agent exit markers: project=%q thread=%q error=%v", description, projectID, threadID, err)
	}
	s.piActivity.removeThread(projectID, threadID)
	if s.threadUsage != nil {
		if err := s.threadUsage.remove(projectID, threadID); err != nil {
			log.Printf("remove deleted thread usage: project=%q thread=%q error=%v", projectID, threadID, err)
		}
	}
	s.contextStatuses.removeThread(projectID, threadID)
	if s.threadMessages != nil {
		s.threadMessages.removeThread(projectID, threadID)
	}
	if s.workflows != nil {
		if err := s.workflows.removeThread(projectID, threadID); err != nil {
			log.Printf("remove deleted thread workflows: project=%q thread=%q error=%v", projectID, threadID, err)
		}
	}
	if s.plans != nil {
		if err := s.plans.removeThread(projectID, threadID); err != nil {
			log.Printf("remove deleted thread plans: project=%q thread=%q error=%v", projectID, threadID, err)
		}
	}
}

func (s *Server) uploadPiImage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.projects.Get(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if r.ContentLength > maxPiImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Pasted images must be 50 MB or smaller.")
		return
	}

	contents, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPiImageBytes))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "Pasted images must be 50 MB or smaller.")
			return
		}
		writeError(w, http.StatusBadRequest, "Could not read the pasted image.")
		return
	}
	if len(contents) == 0 {
		writeError(w, http.StatusBadRequest, "The pasted image is empty.")
		return
	}

	extension, ok := piImageExtension(contents)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "Pi accepts PNG, JPEG, GIF, and WebP images.")
		return
	}

	file, err := os.CreateTemp("", piImageTempPrefix+"*."+extension)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store the pasted image.")
		return
	}
	filePath := file.Name()
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(filePath)
		writeError(w, http.StatusInternalServerError, "Could not store the pasted image.")
		return
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(filePath)
		writeError(w, http.StatusInternalServerError, "Could not store the pasted image.")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"path": filePath})
}

func piImageExtension(contents []byte) (string, bool) {
	mimeType, ok := piImageMIMEType(contents)
	if !ok {
		return "", false
	}
	switch mimeType {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	default:
		return "", false
	}
}

func piImageMIMEType(contents []byte) (string, bool) {
	switch mimeType := http.DetectContentType(contents); mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mimeType, true
	default:
		return "", false
	}
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	requested := strings.TrimPrefix(r.URL.Path, "/")
	if requested != "" {
		if file, err := fs.Stat(s.assets, requested); err == nil && !file.IsDir() {
			s.static.ServeHTTP(w, r)
			return
		}
	}
	r.URL.Path = "/"
	s.static.ServeHTTP(w, r)
}

func frontendAssets(source fs.FS) (fs.FS, error) {
	if _, err := fs.Stat(source, "static/app/index.html"); err == nil {
		return fs.Sub(source, "static/app")
	}
	return fs.Sub(source, "static")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
