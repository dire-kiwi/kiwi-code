package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ivan/dire-mux/internal/project"
)

const (
	workflowRecordVersion           = 1
	workflowManifestVersion         = 1
	workflowDirectoryName           = "workflows-v1"
	workflowRunnerMaterialized      = "dire-mux-workflow-runner.mjs"
	workflowRecordFileName          = "run.json"
	workflowManifestFileName        = "runner.json"
	workflowScriptFileName          = "workflow.js"
	maxWorkflowScriptBytes          = 2 << 20
	maxWorkflowArgsBytes            = 1 << 20
	maxWorkflowRequestBytes         = 16 << 20
	maxWorkflowEventBytes           = 768 << 10
	maxWorkflowLogEntries           = 200
	maxWorkflowLogBytes             = 4 << 10
	maxWorkflowErrorBytes           = 16 << 10
	maxWorkflowEventHistory         = 10_000
	maxWorkflowAgents               = 1_000
	maxWorkflowPhases               = 128
	maxWorkflowAgentOutputBytes     = 8 << 10
	maxWorkflowAgentErrorBytes      = 4 << 10
	maxWorkflowAgentValueBytes      = 128 << 10
	maxWorkflowRetainedAgentBytes   = 16 << 20
	maxWorkflowRetainedRequestBytes = 32 << 20
	maxWorkflowRunnerCommandBytes   = 900
	workflowProcessStartupGrace     = 15 * time.Second
	maxActiveWorkflowsPerThread     = 4
	maxRetainedWorkflowsPerThread   = 25
	workflowTokenHeader             = "X-Dire-Mux-Workflow-Token"
	workflowStateQueued             = "queued"
	workflowStateRunning            = "running"
	workflowStatePaused             = "paused"
	workflowStateFinished           = "finished"
	workflowStateFailed             = "failed"
	workflowStateStopped            = "stopped"
	workflowAgentStateStarting      = "starting"
	workflowAgentStateWorking       = "working"
	workflowAgentStatePaused        = "paused"
	workflowAgentStateFinished      = "finished"
	workflowAgentStateFailed        = "failed"
)

//go:embed workflow-runner.mjs
var workflowRunnerSource []byte

var (
	errWorkflowRequestStorageLimit = errors.New("workflow retained request limit reached")
	errWorkflowRetentionLimit      = errors.New("workflow active and paused retention limit reached")
)

type workflowPhase struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Model  string `json:"model,omitempty"`
}

type workflowMetadata struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	WhenToUse   string          `json:"whenToUse,omitempty"`
	Phases      []workflowPhase `json:"phases,omitempty"`
}

type workflowLogEntry struct {
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type workflowAgentRecord struct {
	ID           string                  `json:"id"`
	Label        string                  `json:"label"`
	Phase        string                  `json:"phase,omitempty"`
	State        string                  `json:"state"`
	ThreadID     string                  `json:"threadId,omitempty"`
	ChildRunID   uint64                  `json:"childRunId,omitempty"`
	StartedAt    time.Time               `json:"startedAt"`
	FinishedAt   *time.Time              `json:"finishedAt,omitempty"`
	Error        string                  `json:"error,omitempty"`
	Output       string                  `json:"output,omitempty"`
	Value        json.RawMessage         `json:"value,omitempty"`
	ValueOmitted bool                    `json:"valueOmitted,omitempty"`
	Request      json.RawMessage         `json:"request,omitempty"`
	Response     *childThreadRunResponse `json:"response,omitempty"`
}

type workflowRunRecord struct {
	Version         int                   `json:"version"`
	ID              string                `json:"id"`
	ProjectID       string                `json:"projectId"`
	ThreadID        string                `json:"threadId"`
	Token           string                `json:"token"`
	State           string                `json:"state"`
	Attempt         int                   `json:"attempt"`
	Name            string                `json:"name"`
	Description     string                `json:"description,omitempty"`
	WhenToUse       string                `json:"whenToUse,omitempty"`
	Phases          []workflowPhase       `json:"phases,omitempty"`
	CurrentPhase    string                `json:"currentPhase,omitempty"`
	ScriptPath      string                `json:"scriptPath"`
	ProcessID       string                `json:"processId,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	StartedAt       *time.Time            `json:"startedAt,omitempty"`
	FinishedAt      *time.Time            `json:"finishedAt,omitempty"`
	UpdatedAt       time.Time             `json:"updatedAt"`
	Error           string                `json:"error,omitempty"`
	Result          json.RawMessage       `json:"result,omitempty"`
	Logs            []workflowLogEntry    `json:"logs,omitempty"`
	Agents          []workflowAgentRecord `json:"agents,omitempty"`
	HasArgs         bool                  `json:"hasArgs,omitempty"`
	Args            json.RawMessage       `json:"args,omitempty"`
	DefaultModel    string                `json:"defaultModel,omitempty"`
	DefaultEffort   string                `json:"defaultThinkingLevel,omitempty"`
	CloseChildren   bool                  `json:"closeOnComplete"`
	ProcessedEvents []string              `json:"processedEvents,omitempty"`
}

type workflowAgentSnapshot struct {
	ID           string          `json:"id"`
	Label        string          `json:"label"`
	Phase        string          `json:"phase,omitempty"`
	State        string          `json:"state"`
	ThreadID     string          `json:"threadId,omitempty"`
	ChildRunID   uint64          `json:"childRunId,omitempty"`
	StartedAt    time.Time       `json:"startedAt"`
	FinishedAt   *time.Time      `json:"finishedAt,omitempty"`
	Error        string          `json:"error,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	ValueOmitted bool            `json:"valueOmitted,omitempty"`
}

type workflowRunSnapshot struct {
	ID           string                  `json:"id"`
	ProjectID    string                  `json:"projectId"`
	ThreadID     string                  `json:"threadId"`
	State        string                  `json:"state"`
	Attempt      int                     `json:"attempt"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description,omitempty"`
	WhenToUse    string                  `json:"whenToUse,omitempty"`
	Phases       []workflowPhase         `json:"phases,omitempty"`
	CurrentPhase string                  `json:"currentPhase,omitempty"`
	ScriptPath   string                  `json:"scriptPath"`
	ProcessID    string                  `json:"processId,omitempty"`
	CreatedAt    time.Time               `json:"createdAt"`
	StartedAt    *time.Time              `json:"startedAt,omitempty"`
	FinishedAt   *time.Time              `json:"finishedAt,omitempty"`
	UpdatedAt    time.Time               `json:"updatedAt"`
	Error        string                  `json:"error,omitempty"`
	Result       json.RawMessage         `json:"result,omitempty"`
	Logs         []workflowLogEntry      `json:"logs,omitempty"`
	Agents       []workflowAgentSnapshot `json:"agents"`
}

type workflowRunnerManifest struct {
	Version              int             `json:"version"`
	RunID                string          `json:"runId"`
	Attempt              int             `json:"attempt"`
	Endpoint             string          `json:"endpoint"`
	Token                string          `json:"token"`
	ScriptPath           string          `json:"scriptPath"`
	HasArgs              bool            `json:"hasArgs"`
	Args                 json.RawMessage `json:"args,omitempty"`
	DefaultModel         string          `json:"defaultModel,omitempty"`
	DefaultThinkingLevel string          `json:"defaultThinkingLevel,omitempty"`
	CloseOnComplete      bool            `json:"closeOnComplete"`
	MaxConcurrency       int             `json:"maxConcurrency"`
}

type workflowAgentLock struct {
	mu    sync.Mutex
	users int
}

type workflowManager struct {
	root           string
	nodePath       string
	permissionFlag string
	allowNet       bool
	forceDisabled  bool
	startMu        sync.Mutex
	mu             sync.Mutex
	activationMu   sync.Mutex
	activations    map[string]workflowActivation
	agentLocks     map[string]*workflowAgentLock
}

func newWorkflowManager(dataDirectory string) (*workflowManager, error) {
	root := filepath.Join(dataDirectory, workflowDirectoryName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create workflow directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure workflow directory: %w", err)
	}
	nodePath, _ := exec.LookPath("node")
	permissionFlag := ""
	allowNet := false
	if nodePath != "" {
		probeContext, cancelProbe := context.WithTimeout(context.Background(), 5*time.Second)
		output, helpErr := exec.CommandContext(probeContext, nodePath, "--help").CombinedOutput()
		cancelProbe()
		if helpErr == nil {
			help := string(output)
			switch {
			case strings.Contains(help, "--permission"):
				permissionFlag = "--permission"
			case strings.Contains(help, "--experimental-permission"):
				permissionFlag = "--experimental-permission"
			}
			allowNet = strings.Contains(help, "--allow-net")
		}
	}
	return &workflowManager{
		root:           root,
		nodePath:       nodePath,
		permissionFlag: permissionFlag,
		allowNet:       allowNet,
		forceDisabled:  workflowDisabledByEnvironment(),
		activations:    make(map[string]workflowActivation),
		agentLocks:     make(map[string]*workflowAgentLock),
	}, nil
}

func validWorkflowPathID(value string) bool {
	if value == "" || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func newWorkflowIdentifier(prefix string, bytesCount int) (string, error) {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func (m *workflowManager) runDirectory(projectID, threadID, runID string) (string, error) {
	for name, value := range map[string]string{"project": projectID, "thread": threadID, "workflow": runID} {
		if !validWorkflowPathID(value) {
			return "", fmt.Errorf("invalid %s identifier", name)
		}
	}
	return filepath.Join(m.root, projectID, threadID, runID), nil
}

func (m *workflowManager) recordPath(projectID, threadID, runID string) (string, error) {
	directory, err := m.runDirectory(projectID, threadID, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, workflowRecordFileName), nil
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneWorkflowRecord(record workflowRunRecord) workflowRunRecord {
	record.Phases = append([]workflowPhase(nil), record.Phases...)
	record.Result = cloneRawMessage(record.Result)
	record.Args = cloneRawMessage(record.Args)
	record.Logs = append([]workflowLogEntry(nil), record.Logs...)
	record.Agents = append([]workflowAgentRecord(nil), record.Agents...)
	record.ProcessedEvents = append([]string(nil), record.ProcessedEvents...)
	for index := range record.Agents {
		record.Agents[index].Value = cloneRawMessage(record.Agents[index].Value)
		record.Agents[index].Request = cloneRawMessage(record.Agents[index].Request)
		if record.Agents[index].Response != nil {
			response := *record.Agents[index].Response
			record.Agents[index].Response = &response
		}
	}
	return record
}

func workflowSnapshot(record workflowRunRecord) workflowRunSnapshot {
	agents := make([]workflowAgentSnapshot, len(record.Agents))
	for index, agent := range record.Agents {
		agents[index] = workflowAgentSnapshot{
			ID:           agent.ID,
			Label:        agent.Label,
			Phase:        agent.Phase,
			State:        agent.State,
			ThreadID:     agent.ThreadID,
			ChildRunID:   agent.ChildRunID,
			StartedAt:    agent.StartedAt,
			FinishedAt:   agent.FinishedAt,
			Error:        agent.Error,
			Value:        cloneRawMessage(agent.Value),
			ValueOmitted: agent.ValueOmitted,
		}
	}
	return workflowRunSnapshot{
		ID:           record.ID,
		ProjectID:    record.ProjectID,
		ThreadID:     record.ThreadID,
		State:        record.State,
		Attempt:      record.Attempt,
		Name:         record.Name,
		Description:  record.Description,
		WhenToUse:    record.WhenToUse,
		Phases:       append([]workflowPhase(nil), record.Phases...),
		CurrentPhase: record.CurrentPhase,
		ScriptPath:   record.ScriptPath,
		ProcessID:    record.ProcessID,
		CreatedAt:    record.CreatedAt,
		StartedAt:    record.StartedAt,
		FinishedAt:   record.FinishedAt,
		UpdatedAt:    record.UpdatedAt,
		Error:        record.Error,
		Result:       cloneRawMessage(record.Result),
		Logs:         append([]workflowLogEntry(nil), record.Logs...),
		Agents:       agents,
	}
}

func workflowSummarySnapshot(record workflowRunRecord) workflowRunSnapshot {
	snapshot := workflowSnapshot(record)
	snapshot.Result = nil
	if len(snapshot.Logs) > 20 {
		snapshot.Logs = append([]workflowLogEntry(nil), snapshot.Logs[len(snapshot.Logs)-20:]...)
	}
	for index := range snapshot.Agents {
		if len(snapshot.Agents[index].Value) > 0 {
			snapshot.Agents[index].Value = nil
			snapshot.Agents[index].ValueOmitted = true
		}
	}
	return snapshot
}

func (m *workflowManager) writeRecordLocked(record workflowRunRecord, durable bool) error {
	path, err := m.recordPath(record.ProjectID, record.ThreadID, record.ID)
	if err != nil {
		return err
	}
	contents, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(path, append(contents, '\n'), serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: durable,
	})
}

func (m *workflowManager) readRecordLocked(projectID, threadID, runID string) (workflowRunRecord, error) {
	path, err := m.recordPath(projectID, threadID, runID)
	if err != nil {
		return workflowRunRecord{}, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return workflowRunRecord{}, err
	}
	var record workflowRunRecord
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return workflowRunRecord{}, fmt.Errorf("decode workflow record: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return workflowRunRecord{}, errors.New("decode workflow record: trailing JSON data")
	}
	if record.Version != workflowRecordVersion || record.ID != runID || record.ProjectID != projectID || record.ThreadID != threadID {
		return workflowRunRecord{}, errors.New("workflow record identity is invalid")
	}
	if record.Attempt <= 0 {
		record.Attempt = 1
	}
	return record, nil
}

func (m *workflowManager) pruneThreadLocked(projectID, threadID string, pendingRuns int) error {
	threadDirectory := filepath.Join(m.root, projectID, threadID)
	entries, err := os.ReadDir(threadDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	terminal := make([]workflowRunRecord, 0, len(entries))
	active := pendingRuns
	for _, entry := range entries {
		if !entry.IsDir() || !validWorkflowPathID(entry.Name()) {
			continue
		}
		record, readErr := m.readRecordLocked(projectID, threadID, entry.Name())
		if readErr != nil {
			continue
		}
		if workflowIsActive(record.State) || record.State == workflowStatePaused {
			active++
		} else {
			terminal = append(terminal, record)
		}
	}
	keepTerminal := maxRetainedWorkflowsPerThread - active
	if keepTerminal < 0 {
		return errWorkflowRetentionLimit
	}
	sort.Slice(terminal, func(left, right int) bool {
		return terminal[left].CreatedAt.After(terminal[right].CreatedAt)
	})
	if keepTerminal >= len(terminal) {
		return nil
	}
	for _, record := range terminal[keepTerminal:] {
		directory, pathErr := m.runDirectory(projectID, threadID, record.ID)
		if pathErr != nil {
			continue
		}
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
	}
	return nil
}

func (m *workflowManager) create(record workflowRunRecord, script []byte, manifest workflowRunnerManifest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.pruneThreadLocked(record.ProjectID, record.ThreadID, 1); err != nil {
		return fmt.Errorf("prune retained workflows: %w", err)
	}
	directory, err := m.runDirectory(record.ProjectID, record.ThreadID, record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	scriptPath := filepath.Join(directory, workflowScriptFileName)
	if scriptPath != record.ScriptPath || scriptPath != manifest.ScriptPath {
		return errors.New("workflow script identity is invalid")
	}
	if err := writeFileAtomically(filepath.Join(directory, workflowRunnerMaterialized), workflowRunnerSource, serverAtomicFileOptions{Mode: 0o500, SyncFile: true}); err != nil {
		return err
	}
	if err := writeFileAtomically(scriptPath, script, serverAtomicFileOptions{Mode: 0o600, SyncFile: true}); err != nil {
		return err
	}
	manifestContents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(directory, workflowManifestFileName), append(manifestContents, '\n'), serverAtomicFileOptions{Mode: 0o600, SyncFile: true}); err != nil {
		return err
	}
	if err := m.writeRecordLocked(record, true); err != nil {
		return err
	}
	complete = true
	return nil
}

func (m *workflowManager) get(projectID, threadID, runID string) (workflowRunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.readRecordLocked(projectID, threadID, runID)
	return cloneWorkflowRecord(record), err
}

func (m *workflowManager) mutate(projectID, threadID, runID string, update func(*workflowRunRecord) error) (workflowRunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.readRecordLocked(projectID, threadID, runID)
	if err != nil {
		return workflowRunRecord{}, err
	}
	if err := update(&record); err != nil {
		return workflowRunRecord{}, err
	}
	record.UpdatedAt = time.Now().UTC()
	if err := m.writeRecordLocked(record, !workflowIsActive(record.State)); err != nil {
		return workflowRunRecord{}, err
	}
	return cloneWorkflowRecord(record), nil
}

func (m *workflowManager) list(projectID, threadID string) ([]workflowRunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validWorkflowPathID(projectID) || !validWorkflowPathID(threadID) {
		return nil, errors.New("invalid workflow owner")
	}
	directory := filepath.Join(m.root, projectID, threadID)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []workflowRunRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]workflowRunRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validWorkflowPathID(entry.Name()) {
			continue
		}
		record, readErr := m.readRecordLocked(projectID, threadID, entry.Name())
		if readErr == nil {
			records = append(records, cloneWorkflowRecord(record))
		}
	}
	sort.Slice(records, func(left, right int) bool {
		return records[left].CreatedAt.After(records[right].CreatedAt)
	})
	return records, nil
}

func (m *workflowManager) removeThread(projectID, threadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validWorkflowPathID(projectID) || !validWorkflowPathID(threadID) {
		return errors.New("invalid workflow owner")
	}
	m.clearActivation(projectID, threadID)
	return os.RemoveAll(filepath.Join(m.root, projectID, threadID))
}

func (m *workflowManager) lockAgent(projectID, threadID, runID, agentID string) func() {
	key := strings.Join([]string{projectID, threadID, runID, agentID}, "\x00")
	m.mu.Lock()
	lock := m.agentLocks[key]
	if lock == nil {
		lock = &workflowAgentLock{}
		m.agentLocks[key] = lock
	}
	lock.users++
	m.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		m.mu.Lock()
		lock.users--
		if lock.users == 0 && m.agentLocks[key] == lock {
			delete(m.agentLocks, key)
		}
		m.mu.Unlock()
	}
}

func workflowIsActive(state string) bool {
	return state == workflowStateQueued || state == workflowStateRunning
}

func compactSettledWorkflow(record *workflowRunRecord) {
	for index := range record.Agents {
		agent := &record.Agents[index]
		agent.Request = nil
		agent.Response = nil
		agent.Output = ""
		if len(agent.Value) > 0 {
			agent.Value = nil
			agent.ValueOmitted = true
		}
	}
}

func workflowAgentIndex(record *workflowRunRecord, agentID string) int {
	for index := range record.Agents {
		if record.Agents[index].ID == agentID {
			return index
		}
	}
	return -1
}

func truncateWorkflowText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (m *workflowManager) authorize(projectID, threadID, runID, token string, active bool) (workflowRunRecord, error) {
	record, err := m.get(projectID, threadID, runID)
	if err != nil {
		return workflowRunRecord{}, err
	}
	if token == "" || len(token) != len(record.Token) || subtle.ConstantTimeCompare([]byte(token), []byte(record.Token)) != 1 {
		return workflowRunRecord{}, errors.New("workflow capability is invalid")
	}
	if active && !workflowIsActive(record.State) {
		return workflowRunRecord{}, errors.New("workflow is no longer running")
	}
	return record, nil
}

func (m *workflowManager) runnerCommand(manifestPath, scriptPath, endpoint, envPath string) (string, error) {
	if m.nodePath == "" {
		return "", errors.New("Node.js is not installed or not on PATH")
	}
	if m.permissionFlag == "" {
		return "", errors.New("Node.js does not support the workflow permission model")
	}
	directory := filepath.Dir(manifestPath)
	if !filepath.IsAbs(directory) || filepath.Dir(scriptPath) != directory ||
		filepath.Base(manifestPath) != workflowManifestFileName || filepath.Base(scriptPath) != workflowScriptFileName {
		return "", errors.New("workflow runner files must use their canonical absolute paths")
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
		return "", errors.New("workflow runner endpoint is invalid")
	}
	if envPath == "" {
		envPath = "env"
	}
	home, _ := os.UserHomeDir()
	arguments := []string{"-i"}
	if home != "" {
		arguments = append(arguments, "HOME="+home)
	}
	arguments = append(arguments,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		m.nodePath,
		m.permissionFlag,
	)
	if m.allowNet {
		arguments = append(arguments, "--allow-net="+parsedEndpoint.Host)
	}
	arguments = append(arguments,
		"--allow-fs-read="+directory,
		"./"+workflowRunnerMaterialized,
		"./"+workflowManifestFileName,
	)
	command := "cd " + shellQuote(directory) + " && exec " + shellCommand(envPath, arguments)
	if len(command) > maxWorkflowRunnerCommandBytes {
		return "", errors.New("workflow data directory path is too long to launch safely")
	}
	return command, nil
}

type workflowProcessLauncher func(project.Project, project.Thread, string, string) (processWindow, error)
type workflowProcessStopper func(project.Project, project.Thread, string) error

func (h *terminalHandler) stopWorkflowProcess(item project.Project, thread project.Thread, processID string) error {
	_, target, found, err := h.processForRequest(item, thread, processID)
	if err != nil || !found {
		return err
	}
	_, err = h.tmuxProcessCommand(target, "kill-window", "-t", target.ID)
	if errors.Is(err, errTmuxProcessIncarnationChanged) {
		return nil
	}
	return err
}

func workflowChildThreadIDs(item project.Project, runID string) []string {
	seen := make(map[string]struct{})
	children := make([]string, 0)
	for _, thread := range item.Threads {
		if thread.WorkflowRunID != runID || thread.ID == "" {
			continue
		}
		if _, duplicate := seen[thread.ID]; duplicate {
			continue
		}
		seen[thread.ID] = struct{}{}
		children = append(children, thread.ID)
	}
	return children
}

func (s *Server) reconcileWorkflowProcesses(item project.Project, thread project.Thread, records []workflowRunRecord) []workflowRunRecord {
	// Injected launchers are test doubles without a tmux window to reconcile.
	if s.workflowProcessLauncher != nil || s.terminal == nil || s.terminal.tmuxPath == "" {
		return records
	}
	for index := range records {
		record := records[index]
		if !workflowIsActive(record.State) {
			continue
		}
		if record.ProcessID == "" {
			adopted := false
			windows, listErr := s.terminal.processWindows(item, thread)
			if listErr == nil {
				suffix := strings.TrimPrefix(record.ID, "wf-")
				expectedName := ""
				if len(suffix) >= 6 {
					expectedName = "workflow-" + suffix[:6]
				}
				for _, window := range windows {
					if window.Name != expectedName {
						continue
					}
					command := strings.TrimSpace(window.CurrentCommand)
					nodeCommand := filepath.Base(s.workflows.nodePath)
					processHealthy := time.Since(record.CreatedAt) < workflowProcessStartupGrace || command == nodeCommand || command == "env"
					updated, updateErr := s.workflows.mutate(item.ID, thread.ID, record.ID, func(run *workflowRunRecord) error {
						if workflowIsActive(run.State) && run.ProcessID == "" {
							run.ProcessID = window.ID
						}
						return nil
					})
					if updateErr == nil {
						record = updated
						records[index] = updated
						adopted = processHealthy
						s.notifyThreadStatusChanged(item.ID, thread.ID)
					}
					break
				}
			}
			if adopted || (record.ProcessID == "" && time.Since(record.CreatedAt) < workflowProcessStartupGrace) {
				continue
			}
		} else {
			window, _, found, err := s.terminal.processForRequest(item, thread, record.ProcessID)
			if err != nil {
				continue
			}
			if found {
				command := strings.TrimSpace(window.CurrentCommand)
				nodeCommand := filepath.Base(s.workflows.nodePath)
				if time.Since(record.CreatedAt) < workflowProcessStartupGrace || command == nodeCommand || command == "env" {
					continue
				}
			}
		}
		updated, updateErr := s.workflows.mutate(item.ID, thread.ID, record.ID, func(run *workflowRunRecord) error {
			if !workflowIsActive(run.State) {
				return nil
			}
			now := time.Now().UTC()
			run.State = workflowStateFailed
			run.Error = "Workflow process exited before reporting a final result."
			run.FinishedAt = &now
			compactSettledWorkflow(run)
			return nil
		})
		if updateErr == nil {
			records[index] = updated
			if record.ProcessID != "" {
				_ = s.terminal.stopWorkflowProcess(item, thread, record.ProcessID)
			}
			for _, childID := range workflowChildThreadIDs(item, updated.ID) {
				_ = s.terminal.nativePi.stopThread(item.ID, childID)
			}
			s.notifyThreadStatusChanged(item.ID, thread.ID)
		}
	}
	return records
}

func (s *Server) startWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	if s.workflowsDisabled() {
		writeError(w, http.StatusServiceUnavailable, "Dynamic workflows are disabled in Settings or by the startup environment.")
		return
	}
	if s.workflows == nil || s.workflows.nodePath == "" || s.workflows.permissionFlag == "" {
		writeError(w, http.StatusServiceUnavailable, "Workflow execution requires a Node.js runtime with permission-model support.")
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
	if thread.RollbackPending || thread.ArchivedAt != nil || thread.ClosedAt != nil {
		writeError(w, http.StatusConflict, "The thread must be open and active before starting a workflow.")
		return
	}
	nesting, err := s.projects.SubAgentNestingContext(projectID, threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread's workflow nesting limit.")
		return
	}
	if nesting.CurrentDepth >= nesting.MaxDepth {
		writeError(w, http.StatusConflict, "The effective sub-agent nesting depth for this thread tree has been reached.")
		return
	}
	if reached, _, budgetErr := s.threadBudgetReached(projectID, threadID); budgetErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify the thread's usage limit.")
		return
	} else if reached {
		writeError(w, http.StatusConflict, "The thread's token or cost limit has been reached; no workflow was started.")
		return
	}
	if _, activated := s.workflowActivationForStart(item, thread, false); !activated {
		writeError(w, http.StatusConflict, "The current human prompt did not activate workflows. Use “ultracode”, explicitly ask to use/run a workflow, or invoke a saved workflow command.")
		return
	}

	var input struct {
		Script          string          `json:"script"`
		Args            json.RawMessage `json:"args"`
		Model           string          `json:"model"`
		ThinkingLevel   string          `json:"thinkingLevel"`
		CloseOnComplete *bool           `json:"closeOnComplete"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes))
	if err != nil || !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "Invalid workflow details.")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid workflow details.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid workflow details.")
		return
	}
	if strings.TrimSpace(input.Script) == "" || len(input.Script) > maxWorkflowScriptBytes || strings.ContainsRune(input.Script, '\x00') {
		writeError(w, http.StatusBadRequest, "A valid workflow script is required.")
		return
	}
	if input.Args != nil && (len(input.Args) > maxWorkflowArgsBytes || !json.Valid(input.Args)) {
		writeError(w, http.StatusBadRequest, "Workflow args must be valid JSON no larger than 1 MiB.")
		return
	}
	launchOptions, err := normalizeCodingAgentLaunchOptions(codingAgentPi, input.Model, input.ThinkingLevel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Admission and durable record creation are one critical section so
	// concurrent starts cannot all observe the same free slot.
	s.workflows.startMu.Lock()
	defer s.workflows.startMu.Unlock()
	runs, err := s.workflows.list(projectID, threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not inspect existing workflows.")
		return
	}
	runs = s.reconcileWorkflowProcesses(item, thread, runs)
	active := 0
	unsettled := 0
	for _, run := range runs {
		if workflowIsActive(run.State) {
			active++
			unsettled++
		} else if run.State == workflowStatePaused {
			unsettled++
		}
	}
	if active >= maxActiveWorkflowsPerThread {
		writeError(w, http.StatusConflict, "This thread already has the maximum number of active workflows.")
		return
	}
	if unsettled >= maxRetainedWorkflowsPerThread {
		writeError(w, http.StatusConflict, "This thread already has the maximum number of active or paused workflows. Stop one before starting another.")
		return
	}
	if _, activated := s.workflowActivationForStart(item, thread, true); !activated {
		writeError(w, http.StatusConflict, "Workflow activation expired before the run could start. Ask to use/run a workflow again.")
		return
	}

	runID, err := newWorkflowIdentifier("wf-", 8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a workflow identifier.")
		return
	}
	token, err := newWorkflowIdentifier("", 32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a workflow capability.")
		return
	}
	directory, err := s.workflows.runDirectory(projectID, threadID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not prepare the workflow directory.")
		return
	}
	scriptPath := filepath.Join(directory, workflowScriptFileName)
	closeChildren := true
	if input.CloseOnComplete != nil {
		closeChildren = *input.CloseOnComplete
	}
	now := time.Now().UTC()
	record := workflowRunRecord{
		Version:       workflowRecordVersion,
		ID:            runID,
		ProjectID:     projectID,
		ThreadID:      threadID,
		Token:         token,
		State:         workflowStateQueued,
		Attempt:       1,
		Name:          "Workflow " + strings.TrimPrefix(runID, "wf-")[:6],
		ScriptPath:    scriptPath,
		CreatedAt:     now,
		UpdatedAt:     now,
		HasArgs:       input.Args != nil,
		Args:          cloneRawMessage(input.Args),
		DefaultModel:  launchOptions.Model,
		DefaultEffort: launchOptions.ThinkingLevel,
		CloseChildren: closeChildren,
	}
	manifest := workflowRunnerManifest{
		Version:              workflowManifestVersion,
		RunID:                runID,
		Attempt:              1,
		Endpoint:             threadEndpointURL(r, projectID, threadID),
		Token:                token,
		ScriptPath:           scriptPath,
		HasArgs:              input.Args != nil,
		Args:                 cloneRawMessage(input.Args),
		DefaultModel:         launchOptions.Model,
		DefaultThinkingLevel: launchOptions.ThinkingLevel,
		CloseOnComplete:      closeChildren,
		MaxConcurrency:       16,
	}
	if err := s.workflows.create(record, []byte(input.Script), manifest); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not persist the workflow.")
		return
	}
	manifestPath := filepath.Join(directory, workflowManifestFileName)
	command, err := s.workflows.runnerCommand(manifestPath, scriptPath, manifest.Endpoint, s.terminal.envPath)
	if err != nil {
		_, _ = s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
			now := time.Now().UTC()
			run.State = workflowStateFailed
			run.Error = truncateWorkflowText(err.Error(), maxWorkflowErrorBytes)
			run.FinishedAt = &now
			return nil
		})
		s.notifyThreadStatusChanged(projectID, threadID)
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	launcher := s.workflowProcessLauncher
	if launcher == nil {
		launcher = s.terminal.newProcessWindow
	}
	window, err := launcher(item, thread, "workflow-"+strings.TrimPrefix(runID, "wf-")[:6], command)
	if err != nil {
		_, _ = s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
			now := time.Now().UTC()
			run.State = workflowStateFailed
			run.Error = truncateWorkflowText(err.Error(), maxWorkflowErrorBytes)
			run.FinishedAt = &now
			return nil
		})
		s.notifyThreadStatusChanged(projectID, threadID)
		writeError(w, http.StatusServiceUnavailable, "Could not start the workflow process.")
		return
	}
	record, err = s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if !workflowIsActive(run.State) {
			return errors.New("workflow was paused or stopped while it started")
		}
		run.ProcessID = window.ID
		return nil
	})
	if err != nil {
		stopper := s.workflowProcessStopper
		if stopper == nil {
			stopper = s.terminal.stopWorkflowProcess
		}
		_ = stopper(item, thread, window.ID)
		writeError(w, http.StatusInternalServerError, "Could not finish starting the workflow.")
		return
	}
	s.notifyThreadStatusChanged(projectID, threadID)
	writeJSON(w, http.StatusCreated, workflowSnapshot(record))
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	item, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	records, err := s.workflows.list(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list workflows.")
		return
	}
	records = s.reconcileWorkflowProcesses(item, thread, records)
	snapshots := make([]workflowRunSnapshot, len(records))
	for index, record := range records {
		snapshots[index] = workflowSummarySnapshot(record)
	}
	writeJSON(w, http.StatusOK, snapshots)
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	item, thread, threadErr := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if threadErr != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	record, err := s.workflows.get(r.PathValue("id"), r.PathValue("threadId"), r.PathValue("runId"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the workflow.")
		return
	}
	records := s.reconcileWorkflowProcesses(item, thread, []workflowRunRecord{record})
	writeJSON(w, http.StatusOK, workflowSnapshot(records[0]))
}

func (s *Server) stopWorkflow(w http.ResponseWriter, r *http.Request) {
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
	needsCleanup := false
	record, err := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if run.State == workflowStateStopped {
			needsCleanup = true
			return nil
		}
		if !workflowIsActive(run.State) && run.State != workflowStatePaused {
			return nil
		}
		needsCleanup = true
		now := time.Now().UTC()
		run.State = workflowStateStopped
		run.Error = "Workflow stopped."
		run.FinishedAt = &now
		compactSettledWorkflow(run)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stop the workflow.")
		return
	}
	if !needsCleanup {
		writeJSON(w, http.StatusOK, workflowSnapshot(record))
		return
	}
	var processStopErr error
	if record.ProcessID != "" {
		stopper := s.workflowProcessStopper
		if stopper == nil {
			stopper = s.terminal.stopWorkflowProcess
		}
		processStopErr = stopper(item, thread, record.ProcessID)
	}
	var childStopErr error
	for _, childID := range workflowChildThreadIDs(item, record.ID) {
		childStopErr = errors.Join(childStopErr, s.terminal.nativePi.stopThread(projectID, childID))
	}
	updated, updateErr := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if run.State == workflowStateStopped && processStopErr == nil && childStopErr == nil {
			run.ProcessID = ""
		}
		return nil
	})
	if updateErr == nil {
		record = updated
	}
	s.notifyThreadStatusChanged(projectID, threadID)
	if processStopErr != nil || childStopErr != nil || updateErr != nil {
		writeError(w, http.StatusInternalServerError, "The workflow was marked stopped, but one or more runner processes could not be stopped. Retry stop to finish cleanup.")
		return
	}
	writeJSON(w, http.StatusOK, workflowSnapshot(record))
}

func (s *Server) authorizeWorkflowRunner(w http.ResponseWriter, r *http.Request, active bool) (workflowRunRecord, bool) {
	if s.workflows == nil {
		writeError(w, http.StatusServiceUnavailable, "Workflow execution is unavailable.")
		return workflowRunRecord{}, false
	}
	record, err := s.workflows.authorize(
		r.PathValue("id"),
		r.PathValue("threadId"),
		r.PathValue("runId"),
		r.Header.Get(workflowTokenHeader),
		active,
	)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return workflowRunRecord{}, false
	}
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return workflowRunRecord{}, false
	}
	return record, true
}

type workflowRunnerEvent struct {
	EventID    string            `json:"eventId"`
	Type       string            `json:"type"`
	Meta       *workflowMetadata `json:"meta,omitempty"`
	Message    string            `json:"message,omitempty"`
	Phase      string            `json:"phase,omitempty"`
	AgentID    string            `json:"agentId,omitempty"`
	Label      string            `json:"label,omitempty"`
	ThreadID   string            `json:"threadId,omitempty"`
	ChildRunID uint64            `json:"childRunId,omitempty"`
	Error      string            `json:"error,omitempty"`
	Output     string            `json:"output,omitempty"`
	Value      json.RawMessage   `json:"value,omitempty"`
	Result     json.RawMessage   `json:"result,omitempty"`
}

func (s *Server) workflowRunnerEvent(w http.ResponseWriter, r *http.Request) {
	// Settled runs still accept an exact duplicate of their final event so a
	// lost HTTP response cannot turn a successfully persisted workflow into a
	// runner-side failure.
	if _, ok := s.authorizeWorkflowRunner(w, r, false); !ok {
		return
	}
	var event workflowRunnerEvent
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkflowEventBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid workflow event.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid workflow event.")
		return
	}
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	runID := r.PathValue("runId")
	record, err := s.workflows.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
		if !validWorkflowPathID(event.EventID) {
			return errors.New("invalid workflow event identifier")
		}
		for _, processed := range run.ProcessedEvents {
			if processed == event.EventID {
				return nil
			}
		}
		if !workflowIsActive(run.State) {
			return errors.New("workflow is no longer running")
		}
		if run.State == workflowStateQueued && event.Type != "started" && event.Type != "failed" {
			return errors.New("workflow has not reported its metadata")
		}
		now := time.Now().UTC()
		agentIndex := -1
		if event.AgentID != "" {
			if !validWorkflowPathID(event.AgentID) {
				return errors.New("invalid workflow agent identifier")
			}
			agentIndex = workflowAgentIndex(run, event.AgentID)
		}
		switch event.Type {
		case "started":
			if run.State != workflowStateQueued {
				return errors.New("workflow metadata was already reported")
			}
			if event.Meta == nil || strings.TrimSpace(event.Meta.Name) == "" || strings.TrimSpace(event.Meta.Description) == "" {
				return errors.New("workflow metadata is required")
			}
			if len(event.Meta.Phases) > maxWorkflowPhases {
				return errors.New("workflow metadata has too many phases")
			}
			phases := make([]workflowPhase, len(event.Meta.Phases))
			for index, candidate := range event.Meta.Phases {
				phases[index] = workflowPhase{
					Title:  truncateWorkflowText(strings.TrimSpace(candidate.Title), 120),
					Detail: truncateWorkflowText(strings.TrimSpace(candidate.Detail), 500),
					Model:  truncateWorkflowText(strings.TrimSpace(candidate.Model), 120),
				}
				if phases[index].Title == "" {
					return errors.New("workflow phase titles are required")
				}
			}
			run.State = workflowStateRunning
			run.Name = truncateWorkflowText(strings.TrimSpace(event.Meta.Name), 120)
			run.Description = truncateWorkflowText(strings.TrimSpace(event.Meta.Description), 2_000)
			run.WhenToUse = truncateWorkflowText(strings.TrimSpace(event.Meta.WhenToUse), 2_000)
			run.Phases = phases
			if run.StartedAt == nil {
				run.StartedAt = &now
			}
		case "log":
			message := truncateWorkflowText(strings.TrimSpace(event.Message), maxWorkflowLogBytes)
			if message != "" {
				run.Logs = append(run.Logs, workflowLogEntry{Message: message, CreatedAt: now})
				if len(run.Logs) > maxWorkflowLogEntries {
					run.Logs = append([]workflowLogEntry(nil), run.Logs[len(run.Logs)-maxWorkflowLogEntries:]...)
				}
			}
		case "phase":
			run.CurrentPhase = truncateWorkflowText(strings.TrimSpace(event.Phase), 120)
		case "agent_started":
			if event.AgentID == "" {
				return errors.New("workflow agent identifier is required")
			}
			if agentIndex >= 0 {
				agent := &run.Agents[agentIndex]
				if agent.State == workflowAgentStateFinished && len(agent.Value) > 0 && !agent.ValueOmitted {
					// A resumed runner receives this completed value from the
					// response below instead of creating another child.
					break
				}
				if agent.State != workflowAgentStatePaused && agent.State != workflowAgentStateFailed {
					return errors.New("workflow agent already exists")
				}
				agent.Label = truncateWorkflowText(strings.TrimSpace(event.Label), 120)
				agent.Phase = truncateWorkflowText(strings.TrimSpace(event.Phase), 120)
				agent.State = workflowAgentStateStarting
				agent.StartedAt = now
				agent.FinishedAt = nil
				agent.Error = ""
				agent.Output = ""
				agent.Value = nil
				agent.ValueOmitted = false
				agent.ChildRunID = 0
				agent.Response = nil
				break
			}
			if len(run.Agents) >= maxWorkflowAgents {
				return errors.New("workflow agent limit reached")
			}
			run.Agents = append(run.Agents, workflowAgentRecord{
				ID:        event.AgentID,
				Label:     truncateWorkflowText(strings.TrimSpace(event.Label), 120),
				Phase:     truncateWorkflowText(strings.TrimSpace(event.Phase), 120),
				State:     workflowAgentStateStarting,
				StartedAt: now,
			})
		case "agent_working":
			if agentIndex < 0 {
				return errors.New("workflow agent was not started")
			}
			agent := &run.Agents[agentIndex]
			if agent.State != workflowAgentStateStarting {
				return errors.New("workflow agent is not awaiting its child binding")
			}
			if agent.ThreadID == "" || agent.ChildRunID == 0 || event.ThreadID != agent.ThreadID || event.ChildRunID != agent.ChildRunID {
				return errors.New("workflow agent binding does not match its created child")
			}
			agent.State = workflowAgentStateWorking
		case "agent_finished", "agent_failed":
			if agentIndex < 0 {
				return errors.New("workflow agent was not started")
			}
			agent := &run.Agents[agentIndex]
			if event.Type == "agent_finished" && agent.State != workflowAgentStateWorking {
				return errors.New("workflow agent cannot finish before its child is working")
			}
			if event.Type == "agent_failed" && agent.State != workflowAgentStateStarting && agent.State != workflowAgentStateWorking {
				return errors.New("workflow agent is already settled")
			}
			if event.Type == "agent_finished" {
				agent.State = workflowAgentStateFinished
			} else {
				agent.State = workflowAgentStateFailed
			}
			agent.Error = truncateWorkflowText(event.Error, maxWorkflowAgentErrorBytes)
			agent.Output = truncateWorkflowText(event.Output, maxWorkflowAgentOutputBytes)
			agent.Value = nil
			agent.ValueOmitted = false
			if event.Value != nil {
				if !json.Valid(event.Value) {
					return errors.New("workflow agent value is invalid JSON")
				}
				retainedBytes := len(agent.Error) + len(agent.Output)
				for index := range run.Agents {
					if index != agentIndex {
						retainedBytes += len(run.Agents[index].Error) + len(run.Agents[index].Output) + len(run.Agents[index].Value)
					}
				}
				if len(event.Value) <= maxWorkflowAgentValueBytes && retainedBytes+len(event.Value) <= maxWorkflowRetainedAgentBytes {
					agent.Value = cloneRawMessage(event.Value)
				} else {
					agent.ValueOmitted = true
				}
			}
			agent.FinishedAt = &now
		case "finished":
			if event.Result == nil || !json.Valid(event.Result) {
				return errors.New("workflow result is invalid JSON")
			}
			for _, agent := range run.Agents {
				if agent.State == workflowAgentStateStarting || agent.State == workflowAgentStateWorking {
					return errors.New("workflow cannot finish while an agent is still active")
				}
			}
			run.State = workflowStateFinished
			run.Result = cloneRawMessage(event.Result)
			run.Error = ""
			run.FinishedAt = &now
			compactSettledWorkflow(run)
		case "failed":
			run.State = workflowStateFailed
			run.Error = truncateWorkflowText(strings.TrimSpace(event.Error), maxWorkflowErrorBytes)
			if run.Error == "" {
				run.Error = "Workflow process failed."
			}
			run.FinishedAt = &now
			compactSettledWorkflow(run)
		default:
			return errors.New("unknown workflow event")
		}
		run.ProcessedEvents = append(run.ProcessedEvents, event.EventID)
		if len(run.ProcessedEvents) > maxWorkflowEventHistory {
			run.ProcessedEvents = append([]string(nil), run.ProcessedEvents[len(run.ProcessedEvents)-maxWorkflowEventHistory:]...)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyThreadStatusChanged(projectID, threadID)
	response := map[string]any{
		"ok":        true,
		"state":     record.State,
		"updatedAt": record.UpdatedAt,
	}
	if event.Type == "agent_started" {
		if index := workflowAgentIndex(&record, event.AgentID); index >= 0 {
			agent := record.Agents[index]
			if agent.State == workflowAgentStateFinished && len(agent.Value) > 0 && !agent.ValueOmitted {
				response["cached"] = true
				response["value"] = cloneRawMessage(agent.Value)
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapturedResponse() *capturedResponse {
	return &capturedResponse{header: make(http.Header)}
}

func (w *capturedResponse) Header() http.Header { return w.header }
func (w *capturedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *capturedResponse) Write(contents []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(contents)
}

func (s *Server) bindWorkflowAgentResponse(record workflowRunRecord, agentID string, created childThreadRunResponse) error {
	_, err := s.workflows.mutate(record.ProjectID, record.ThreadID, record.ID, func(run *workflowRunRecord) error {
		if !workflowIsActive(run.State) {
			return errors.New("workflow is no longer running")
		}
		index := workflowAgentIndex(run, agentID)
		if index < 0 {
			return errors.New("workflow agent is missing")
		}
		run.Agents[index].ThreadID = created.Thread.ID
		run.Agents[index].ChildRunID = created.Run.ID
		run.Agents[index].Response = &created
		return nil
	})
	return err
}

func copyCapturedResponse(destination http.ResponseWriter, response *capturedResponse) {
	for name, values := range response.header {
		for _, value := range values {
			destination.Header().Add(name, value)
		}
	}
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = destination.Write(response.body.Bytes())
}

func (s *Server) createWorkflowAgent(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeWorkflowRunner(w, r, true)
	if !ok {
		return
	}
	agentID := r.PathValue("agentId")
	if !validWorkflowPathID(agentID) {
		writeError(w, http.StatusBadRequest, "Invalid workflow agent identifier.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChildThreadPromptBytes+(1<<20)))
	if err != nil || !json.Valid(body) {
		writeError(w, http.StatusBadRequest, "Invalid workflow agent details.")
		return
	}
	unlock := s.workflows.lockAgent(record.ProjectID, record.ThreadID, record.ID, agentID)
	defer unlock()
	record, err = s.workflows.get(record.ProjectID, record.ThreadID, record.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if !workflowIsActive(record.State) {
		writeError(w, http.StatusConflict, "Workflow is no longer running.")
		return
	}
	agentIndex := workflowAgentIndex(&record, agentID)
	if agentIndex < 0 {
		writeError(w, http.StatusConflict, "The workflow agent was not registered by the runner.")
		return
	}
	if response := record.Agents[agentIndex].Response; response != nil {
		writeJSON(w, http.StatusCreated, response)
		return
	}
	if _, err := s.workflows.mutate(record.ProjectID, record.ThreadID, record.ID, func(run *workflowRunRecord) error {
		index := workflowAgentIndex(run, agentID)
		if index < 0 {
			return errors.New("workflow agent is missing")
		}
		retainedBytes := len(body)
		for candidate := range run.Agents {
			if candidate != index {
				retainedBytes += len(run.Agents[candidate].Request)
			}
		}
		if retainedBytes > maxWorkflowRetainedRequestBytes {
			return errWorkflowRequestStorageLimit
		}
		run.Agents[index].Request = cloneRawMessage(body)
		return nil
	}); err != nil {
		if errors.Is(err, errWorkflowRequestStorageLimit) {
			writeError(w, http.StatusConflict, "The workflow's retained agent prompt limit has been reached.")
		} else {
			writeError(w, http.StatusInternalServerError, "Could not persist the workflow agent request.")
		}
		return
	}

	// The project record carries the workflow identity as a durable idempotency
	// key. If the server stopped after committing the child but before binding
	// the workflow response, adopt that exact thread instead of creating a
	// duplicate child and worktree.
	item, parent, lookupErr := s.projects.GetThread(record.ProjectID, record.ThreadID)
	if lookupErr != nil {
		writeError(w, http.StatusInternalServerError, "Could not inspect existing workflow children.")
		return
	}
	if parent.RollbackPending || parent.ClosedAt != nil || parent.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "The workflow parent thread is no longer active.")
		return
	}
	for _, child := range item.Threads {
		if child.WorkflowRunID != record.ID || child.WorkflowAgentID != agentID {
			continue
		}
		if child.ParentThreadID != record.ThreadID || child.RollbackPending || child.ClosedAt != nil || child.ArchivedAt != nil {
			writeError(w, http.StatusConflict, "The retained workflow child is not active and cannot be adopted.")
			return
		}
		retainedAgent := record.Agents[agentIndex]
		resumingRetainedAgent := retainedAgent.ThreadID == child.ID && retainedAgent.ChildRunID == 0
		run, found := s.terminal.nativePi.latestChildRun(record.ProjectID, child.ID)
		if resumingRetainedAgent || !found {
			if reached, _, budgetErr := s.threadBudgetReached(record.ProjectID, record.ThreadID); budgetErr != nil {
				writeError(w, http.StatusInternalServerError, "Could not verify the retained workflow child's usage limit.")
				return
			} else if reached {
				writeError(w, http.StatusConflict, "The workflow thread's token or cost limit has been reached.")
				return
			}
			var original struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(body, &original); err != nil || strings.TrimSpace(original.Prompt) == "" {
				writeError(w, http.StatusInternalServerError, "The retained workflow child's prompt is unavailable.")
				return
			}
			launchOptions, err := normalizeCodingAgentLaunchOptions(codingAgentPi, child.AgentModel, child.AgentThinkingLevel)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "The retained workflow child's model is invalid.")
				return
			}
			process, err := s.terminal.startPiNativeProcess(item, child, threadEndpointURL(r, record.ProjectID, child.ID), launchOptions)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Could not recover the retained workflow child.")
				return
			}
			recoveryPrompt := "[Dire Mux workflow recovery]\nContinue this retained workflow task from your saved conversation, preserve completed work, and return the requested final value.\n\nOriginal task:\n" + original.Prompt
			if resumingRetainedAgent {
				recoveryPrompt = "[Dire Mux workflow resumed]\nThis workflow was paused by the user. Continue the retained task from your saved conversation, preserve completed work, and return the requested final value.\n\nOriginal task:\n" + original.Prompt
			}
			run, err = process.startPrompt(recoveryPrompt)
			if err != nil {
				_ = s.terminal.nativePi.stopThread(record.ProjectID, child.ID)
				writeError(w, http.StatusServiceUnavailable, "Could not resume the retained workflow child.")
				return
			}
		}
		created := childThreadRunResponse{Thread: child, Run: run, Agent: codingAgentPi}
		if err := s.bindWorkflowAgentResponse(record, agentID, created); err != nil {
			_ = s.terminal.nativePi.stopThread(record.ProjectID, child.ID)
			writeError(w, http.StatusInternalServerError, "Could not bind the retained workflow child.")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}

	latest, latestErr := s.workflows.get(record.ProjectID, record.ThreadID, record.ID)
	if latestErr != nil || !workflowIsActive(latest.State) {
		writeError(w, http.StatusConflict, "Workflow is no longer running.")
		return
	}

	requestContext := context.WithValue(
		context.WithoutCancel(r.Context()),
		workflowChildIdentityContextKey{},
		workflowChildIdentity{RunID: record.ID, AgentID: agentID},
	)
	request := r.Clone(requestContext)
	request.Body = io.NopCloser(bytes.NewReader(body))
	capture := newCapturedResponse()
	s.createChildThreadAuthorized(capture, request, true)
	if capture.status != http.StatusCreated {
		copyCapturedResponse(w, capture)
		return
	}
	var created childThreadRunResponse
	if err := json.Unmarshal(capture.body.Bytes(), &created); err != nil || created.Thread.ID == "" || created.Run.ID == 0 {
		writeError(w, http.StatusInternalServerError, "Workflow agent creation returned an invalid response.")
		return
	}
	err = s.bindWorkflowAgentResponse(record, agentID, created)
	if err != nil {
		_ = s.terminal.nativePi.stopThread(record.ProjectID, created.Thread.ID)
		writeError(w, http.StatusInternalServerError, "Could not bind the workflow agent to its thread.")
		return
	}
	copyCapturedResponse(w, capture)
}

func (s *Server) recoverWorkflowAgentRun(record workflowRunRecord, agentID string, r *http.Request) (piNativeRunSnapshot, error) {
	unlock := s.workflows.lockAgent(record.ProjectID, record.ThreadID, record.ID, agentID)
	defer unlock()

	record, err := s.workflows.get(record.ProjectID, record.ThreadID, record.ID)
	if err != nil {
		return piNativeRunSnapshot{}, err
	}
	if !workflowIsActive(record.State) {
		return piNativeRunSnapshot{}, errors.New("workflow is no longer running")
	}
	index := workflowAgentIndex(&record, agentID)
	if index < 0 || record.Agents[index].ThreadID == "" {
		return piNativeRunSnapshot{}, errors.New("workflow agent is not bound to a thread")
	}
	agent := record.Agents[index]
	if run, found := s.terminal.nativePi.childRun(record.ProjectID, agent.ThreadID, agent.ChildRunID); found {
		return run, nil
	}
	if reached, _, budgetErr := s.threadBudgetReached(record.ProjectID, record.ThreadID); budgetErr != nil {
		return piNativeRunSnapshot{}, fmt.Errorf("verify workflow budget: %w", budgetErr)
	} else if reached {
		return piNativeRunSnapshot{}, errors.New("the workflow thread's token or cost limit has been reached")
	}
	item, child, err := s.projects.GetThread(record.ProjectID, agent.ThreadID)
	if err != nil {
		return piNativeRunSnapshot{}, err
	}
	if child.ClosedAt != nil || child.ArchivedAt != nil || child.RollbackPending {
		return piNativeRunSnapshot{}, errors.New("workflow agent thread is no longer active")
	}
	var original struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(agent.Request, &original); err != nil || strings.TrimSpace(original.Prompt) == "" {
		return piNativeRunSnapshot{}, errors.New("workflow agent recovery prompt is unavailable")
	}
	launchOptions, err := normalizeCodingAgentLaunchOptions(codingAgentPi, child.AgentModel, child.AgentThinkingLevel)
	if err != nil {
		return piNativeRunSnapshot{}, err
	}
	process, err := s.terminal.startPiNativeProcess(
		item,
		child,
		threadEndpointURL(r, record.ProjectID, child.ID),
		launchOptions,
	)
	if err != nil {
		return piNativeRunSnapshot{}, err
	}
	recoveryPrompt := "[Dire Mux workflow recovery]\nThe Dire Mux backend restarted while this workflow agent was running. Continue from your saved conversation, preserve completed work, and finish the original task. Return the requested final value again so the workflow can continue.\n\nOriginal task:\n" + original.Prompt
	run, err := process.startPrompt(recoveryPrompt)
	if err != nil {
		_ = s.terminal.nativePi.stopThread(record.ProjectID, child.ID)
		return piNativeRunSnapshot{}, err
	}
	_, err = s.workflows.mutate(record.ProjectID, record.ThreadID, record.ID, func(current *workflowRunRecord) error {
		currentIndex := workflowAgentIndex(current, agentID)
		if currentIndex < 0 || !workflowIsActive(current.State) {
			return errors.New("workflow agent can no longer be recovered")
		}
		currentAgent := &current.Agents[currentIndex]
		currentAgent.State = workflowAgentStateWorking
		currentAgent.ChildRunID = run.ID
		if currentAgent.Response != nil {
			currentAgent.Response.Run = run
		}
		return nil
	})
	if err != nil {
		_ = s.terminal.nativePi.stopThread(record.ProjectID, child.ID)
		return piNativeRunSnapshot{}, err
	}
	return run, nil
}

func (s *Server) getWorkflowAgentRun(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeWorkflowRunner(w, r, true)
	if !ok {
		return
	}
	agentID := r.PathValue("agentId")
	agentIndex := workflowAgentIndex(&record, agentID)
	if agentIndex < 0 || record.Agents[agentIndex].ThreadID == "" || record.Agents[agentIndex].ChildRunID == 0 {
		writeError(w, http.StatusNotFound, "Workflow agent not found.")
		return
	}
	agent := record.Agents[agentIndex]
	if agent.State == workflowAgentStateFinished || agent.State == workflowAgentStateFailed {
		state := "finished"
		if agent.State == workflowAgentStateFailed {
			state = "failed"
		}
		writeJSON(w, http.StatusOK, piNativeRunSnapshot{
			ID:         agent.ChildRunID,
			State:      state,
			Output:     agent.Output,
			Error:      agent.Error,
			StartedAt:  agent.StartedAt,
			FinishedAt: agent.FinishedAt,
		})
		return
	}
	run, found := s.terminal.nativePi.childRun(record.ProjectID, agent.ThreadID, agent.ChildRunID)
	if !found {
		var recoveryErr error
		run, recoveryErr = s.recoverWorkflowAgentRun(record, agentID, r)
		if recoveryErr != nil {
			writeError(w, http.StatusServiceUnavailable, "Could not recover the workflow agent after the backend restart: "+recoveryErr.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) closeWorkflowAgent(w http.ResponseWriter, r *http.Request) {
	record, ok := s.authorizeWorkflowRunner(w, r, true)
	if !ok {
		return
	}
	agentIndex := workflowAgentIndex(&record, r.PathValue("agentId"))
	if agentIndex < 0 || record.Agents[agentIndex].ThreadID == "" {
		writeError(w, http.StatusNotFound, "Workflow agent not found.")
		return
	}
	r.SetPathValue("childId", record.Agents[agentIndex].ThreadID)
	s.closeChildThreadAuthorized(w, r)
}
