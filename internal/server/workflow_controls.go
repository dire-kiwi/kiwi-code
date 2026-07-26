package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func (m *workflowManager) writeManifest(record workflowRunRecord, manifest workflowRunnerManifest) error {
	directory, err := m.runDirectory(record.ProjectID, record.ThreadID, record.ID)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, workflowManifestFileName)
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(path, append(contents, '\n'), serverAtomicFileOptions{Mode: 0o600, SyncFile: true})
}

func (s *Server) pauseWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	runID := r.PathValue("runId")
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the workflow thread.")
		return
	}
	record, err := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if run.State == workflowStatePaused {
			return nil
		}
		if !workflowIsActive(run.State) {
			return errors.New("only a queued or running workflow can be paused")
		}
		run.State = workflowStatePaused
		run.Error = ""
		run.FinishedAt = nil
		for index := range run.Agents {
			agent := &run.Agents[index]
			if agent.State == workflowAgentStateStarting || agent.State == workflowAgentStateWorking {
				agent.State = workflowAgentStatePaused
				agent.FinishedAt = nil
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	runtimeErr := s.stopWorkflowRuntime(item, thread, record)
	updated, updateErr := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if run.State == workflowStatePaused && runtimeErr == nil {
			run.ProcessID = ""
		}
		return nil
	})
	if updateErr == nil {
		record = updated
	}
	s.workflowLifecycleChanged(projectID, threadID)
	if errors.Join(runtimeErr, updateErr) != nil {
		writeError(w, http.StatusInternalServerError, "The workflow was paused, but one or more runner processes could not be stopped. Retry pause before resuming.")
		return
	}
	writeJSON(w, http.StatusOK, workflowSnapshot(record))
}

func (s *Server) resumeWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowsDisabled() {
		writeError(w, http.StatusServiceUnavailable, "Dynamic workflows are disabled in Settings or by the startup environment.")
		return
	}
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	runID := r.PathValue("runId")
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the workflow thread.")
		return
	}
	if thread.RollbackPending || thread.ArchivedAt != nil || thread.ClosedAt != nil {
		writeError(w, http.StatusConflict, "The thread must be open and active before resuming a workflow.")
		return
	}
	if reached, _, budgetErr := s.threadBudgetReached(projectID, threadID); budgetErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify the thread's usage limit.")
		return
	} else if reached {
		writeError(w, http.StatusConflict, "The thread's token or cost limit has been reached; the workflow was not resumed.")
		return
	}

	s.workflows.startMu.Lock()
	defer s.workflows.startMu.Unlock()
	runs, err := s.workflows.list(projectID, threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not inspect existing workflows.")
		return
	}
	active := 0
	var previous workflowRunRecord
	for _, run := range runs {
		if run.ID == runID {
			previous = run
		}
		if workflowIsActive(run.State) {
			active++
		}
	}
	if previous.ID == "" {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if previous.State != workflowStatePaused {
		writeError(w, http.StatusConflict, "Only a paused workflow can be resumed.")
		return
	}
	if previous.ProcessID != "" {
		writeError(w, http.StatusConflict, "The paused workflow still has pending process cleanup. Retry pause before resuming.")
		return
	}
	if active >= maxActiveWorkflowsPerThread {
		writeError(w, http.StatusConflict, "This thread already has the maximum number of active workflows.")
		return
	}

	record, err := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if run.State != workflowStatePaused {
			return errors.New("workflow is no longer paused")
		}
		run.Attempt++
		if run.Attempt <= 1 {
			run.Attempt = 2
		}
		run.State = workflowStateQueued
		run.StartedAt = nil
		run.Error = ""
		run.ProcessID = ""
		run.FinishedAt = nil
		for index := range run.Agents {
			agent := &run.Agents[index]
			if agent.State == workflowAgentStateFinished && len(agent.Value) > 0 && !agent.ValueOmitted {
				continue
			}
			agent.State = workflowAgentStatePaused
			agent.Error = ""
			agent.Output = ""
			agent.Value = nil
			agent.ValueOmitted = false
			agent.ChildRunID = 0
			agent.Response = nil
			agent.FinishedAt = nil
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	manifest := workflowManifest(record, threadEndpointURL(r, projectID, threadID))
	if err := s.workflows.writeManifest(record, manifest); err != nil {
		s.failWorkflowResume(record, err)
		writeError(w, http.StatusInternalServerError, "Could not prepare the workflow to resume.")
		return
	}
	command, err := s.workflowRunnerCommand(record, manifest.Endpoint)
	if err != nil {
		s.failWorkflowResume(record, err)
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	window, err := s.launchWorkflowWindow(item, thread, record, command)
	if err != nil {
		s.failWorkflowResume(record, err)
		writeError(w, http.StatusServiceUnavailable, "Could not resume the workflow process.")
		return
	}
	record, err = s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if !workflowIsActive(run.State) {
			return errors.New("workflow was stopped while it resumed")
		}
		run.ProcessID = window.ID
		return nil
	})
	if err != nil {
		_ = s.workflowStopper()(item, thread, window.ID)
		s.failWorkflowResume(record, err)
		writeError(w, http.StatusInternalServerError, "Could not finish resuming the workflow.")
		return
	}
	s.workflowLifecycleChanged(projectID, threadID)
	writeJSON(w, http.StatusOK, workflowSnapshot(record))
}
