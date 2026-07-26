package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func workflowRunnerTestCommand(t *testing.T, nodePath, manifestPath, scriptPath string) *exec.Cmd {
	t.Helper()
	runnerPath, err := filepath.Abs("workflow-runner.mjs")
	if err != nil {
		t.Fatal(err)
	}
	helpOutput, helpErr := exec.Command(nodePath, "--help").CombinedOutput()
	if helpErr != nil {
		t.Skipf("inspect Node.js permission flags: %v", helpErr)
	}
	help := string(helpOutput)
	permissionFlag := ""
	if strings.Contains(help, "--permission") {
		permissionFlag = "--permission"
	} else if strings.Contains(help, "--experimental-permission") {
		permissionFlag = "--experimental-permission"
	} else {
		t.Skip("Node.js permission model is unavailable")
	}
	arguments := []string{
		permissionFlag,
		"--allow-fs-read=" + runnerPath,
		"--allow-fs-read=" + manifestPath,
		"--allow-fs-read=" + scriptPath,
	}
	if strings.Contains(help, "--allow-net") {
		arguments = append(arguments, "--allow-net=127.0.0.1")
	}
	arguments = append(arguments, runnerPath, manifestPath)
	return exec.Command(nodePath, arguments...)
}

func TestWorkflowRunnerPipelinesKiwiCodeAgentsAndReturnsStructuredResult(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	var mu sync.Mutex
	events := make([]workflowRunnerEvent, 0)
	created := make(map[string]bool)
	closed := make(map[string]bool)
	activeCreates := 0
	maxActiveCreates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(workflowTokenHeader); got != "scoped-token" {
			t.Errorf("workflow token = %q", got)
			writeError(w, http.StatusForbidden, "bad token")
			return
		}
		prefix := "/api/projects/project/threads/thread/workflows/wf-test"
		if r.URL.Path == prefix+"/events" && r.Method == http.MethodPost {
			var event workflowRunnerEvent
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				t.Errorf("decode event: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		remaining := strings.TrimPrefix(r.URL.Path, prefix+"/agents/")
		parts := strings.Split(remaining, "/")
		if len(parts) == 0 || !strings.HasPrefix(parts[0], "agent-") {
			writeError(w, http.StatusNotFound, "unknown path")
			return
		}
		agentID := parts[0]
		if len(parts) == 1 && r.Method == http.MethodPost {
			var input struct {
				Title  string `json:"title"`
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode agent request: %v", err)
			}
			if !strings.Contains(input.Prompt, "machine-consumed value") {
				t.Errorf("structured prompt did not include return contract: %q", input.Prompt)
			}
			mu.Lock()
			activeCreates++
			if activeCreates > maxActiveCreates {
				maxActiveCreates = activeCreates
			}
			mu.Unlock()
			time.Sleep(40 * time.Millisecond)
			mu.Lock()
			activeCreates--
			created[agentID] = true
			mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{
				"thread": map[string]any{"id": "thread-" + agentID, "title": input.Title, "cwd": "/tmp", "createdAt": time.Now().UTC()},
				"run":    map[string]any{"id": 1, "state": "working", "startedAt": time.Now().UTC()},
				"agent":  "pi",
			})
			return
		}
		if len(parts) == 1 && r.Method == http.MethodGet {
			mu.Lock()
			wasCreated := created[agentID]
			mu.Unlock()
			if !wasCreated {
				writeError(w, http.StatusNotFound, "not created")
				return
			}
			value := 2
			if agentID == "agent-0002" {
				value = 4
			}
			finished := time.Now().UTC()
			writeJSON(w, http.StatusOK, piNativeRunSnapshot{
				ID: 1, State: "finished", Output: `{"value":` + string(rune('0'+value)) + `}`,
				StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
			})
			return
		}
		if len(parts) == 2 && parts[1] == "close" && r.Method == http.MethodPost {
			mu.Lock()
			closed[agentID] = true
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"id": "thread-" + agentID})
			return
		}
		writeError(w, http.StatusNotFound, "unknown workflow request")
	}))
	defer server.Close()

	directory := t.TempDir()
	scriptPath := filepath.Join(directory, workflowScriptFileName)
	script := `export const meta = {
  name: 'sum-items',
  description: 'Map items through visible child threads',
  phases: [{ title: 'Map' }],
}
const schema = {
  type: 'object',
  required: ['value'],
  properties: { value: { type: 'integer' } },
  additionalProperties: false,
}
phase('Map')
agent('Return a background value', { label: 'background', phase: 'Map', schema })
const rows = await pipeline(args.items, (item, _original, index) =>
  agent('Return the requested value ' + item, { label: 'item-' + index, phase: 'Map', schema }))
log('mapped ' + rows.length + ' items')
return { sum: rows.filter(Boolean).reduce((total, row) => total + row.value, 0) }
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, workflowManifestFileName)
	manifest := workflowRunnerManifest{
		Version: workflowManifestVersion, RunID: "wf-test",
		Endpoint: server.URL + "/api/projects/project/threads/thread", Token: "scoped-token",
		ScriptPath: scriptPath, HasArgs: true, Args: json.RawMessage(`{"items":[2,4]}`),
		CloseOnComplete: true, MaxConcurrency: 16,
	}
	manifestContents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestContents, 0o600); err != nil {
		t.Fatal(err)
	}

	command := workflowRunnerTestCommand(t, nodePath, manifestPath, scriptPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("workflow runner failed: %v\n%s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActiveCreates < 2 {
		t.Fatalf("workflow agents did not start concurrently; max=%d", maxActiveCreates)
	}
	if len(created) != 3 || len(closed) != 3 {
		t.Fatalf("created=%v closed=%v", created, closed)
	}
	var finished *workflowRunnerEvent
	backgroundFinishedIndex, workflowFinishedIndex := -1, -1
	for index := range events {
		if events[index].Type == "agent_finished" && events[index].AgentID == "agent-0001" {
			backgroundFinishedIndex = index
		}
		if events[index].Type == "finished" {
			finished = &events[index]
			workflowFinishedIndex = index
		}
	}
	if backgroundFinishedIndex < 0 || workflowFinishedIndex <= backgroundFinishedIndex {
		t.Fatalf("workflow finished before its unawaited agent: %#v", events)
	}
	if finished == nil || string(finished.Result) != `{"sum":6}` {
		t.Fatalf("finished event = %#v; events=%#v", finished, events)
	}
	if !strings.Contains(string(output), "mapped 2 items") || !strings.Contains(string(output), "Workflow sum-items finished") {
		t.Fatalf("runner output = %s", output)
	}
}

func TestWorkflowRunnerSandboxDoesNotExposeHostProcess(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	failed := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event workflowRunnerEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if event.Type == "failed" {
			failed <- event.Error
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer server.Close()

	directory := t.TempDir()
	scriptPath := filepath.Join(directory, workflowScriptFileName)
	script := `export const meta = { name: 'escape-test', description: 'Must stay sandboxed' }
return agent.constructor.constructor('return process')().env
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, workflowManifestFileName)
	manifest := workflowRunnerManifest{
		Version: workflowManifestVersion, RunID: "wf-test", Endpoint: server.URL,
		Token: "scoped-token", ScriptPath: scriptPath, CloseOnComplete: true, MaxConcurrency: 1,
	}
	contents, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	command := workflowRunnerTestCommand(t, nodePath, manifestPath, scriptPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("sandbox escape unexpectedly succeeded: %s", output)
	}
	select {
	case message := <-failed:
		if !strings.Contains(message, "Code generation from strings disallowed") {
			t.Fatalf("sandbox failure = %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("runner did not report sandbox failure: %s", output)
	}
	if strings.Contains(string(output), "scoped-token") {
		t.Fatalf("runner leaked its capability: %s", output)
	}
}

func TestWorkflowAPIStartsPersistsReportsAndStopsServerProcess(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)
	var launchedName, launchedCommand, stoppedID string
	application.workflowProcessLauncher = func(_ project.Project, _ project.Thread, name, command string) (processWindow, error) {
		launchedName, launchedCommand = name, command
		return processWindow{ID: "process-workflow", Name: name}, nil
	}
	application.workflowProcessStopper = func(_ project.Project, _ project.Thread, processID string) error {
		stoppedID = processID
		return nil
	}
	thread := item.Threads[0]
	path := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/workflows"
	dismissedRequest := httptest.NewRequest(http.MethodPost, path+"/activation", bytes.NewBufferString(`{"prompt":"ultracode this API test","source":"rpc","mode":"prompt","keywordDismissed":true}`))
	dismissedRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	dismissedResponse := httptest.NewRecorder()
	handler.ServeHTTP(dismissedResponse, dismissedRequest)
	if dismissedResponse.Code != http.StatusOK || strings.Contains(dismissedResponse.Body.String(), `"activated":true`) {
		t.Fatalf("dismissed activation status = %d, body = %s", dismissedResponse.Code, dismissedResponse.Body.String())
	}
	claudeRequest := httptest.NewRequest(http.MethodPost, path+"/activation", bytes.NewBufferString(`{"prompt":"use a workflow for this API test","source":"claude-hook","mode":"prompt"}`))
	claudeRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	claudeResponse := httptest.NewRecorder()
	handler.ServeHTTP(claudeResponse, claudeRequest)
	if claudeResponse.Code != http.StatusOK || strings.Contains(claudeResponse.Body.String(), `"activated":true`) {
		t.Fatalf("Claude activation status = %d, body = %s", claudeResponse.Code, claudeResponse.Body.String())
	}
	activationRequest := httptest.NewRequest(http.MethodPost, path+"/activation", bytes.NewBufferString(`{"prompt":"use a workflow for this API test","source":"rpc","mode":"prompt"}`))
	activationRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	activationResponse := httptest.NewRecorder()
	handler.ServeHTTP(activationResponse, activationRequest)
	if activationResponse.Code != http.StatusOK || !strings.Contains(activationResponse.Body.String(), `"activated":true`) {
		t.Fatalf("activation status = %d, body = %s", activationResponse.Code, activationResponse.Body.String())
	}
	script := `export const meta = { name: 'api-test', description: 'API lifecycle' }
return { ok: true }
`
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{"script":`+strconvQuote(script)+`,"args":{"scope":"all"}}`)))
	request.Header.Set(agentTokenHeader, application.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("start status = %d, body = %s", response.Code, response.Body.String())
	}
	var started workflowRunSnapshot
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started.State != workflowStateQueued || started.ProcessID != "process-workflow" || !strings.HasPrefix(started.ID, "wf-") {
		t.Fatalf("started workflow = %#v", started)
	}
	if launchedName == "" || !strings.Contains(launchedCommand, workflowRunnerMaterialized) || !strings.Contains(launchedCommand, workflowManifestFileName) {
		t.Fatalf("launch = name %q command %q", launchedName, launchedCommand)
	}
	if contents, err := os.ReadFile(started.ScriptPath); err != nil || string(contents) != script {
		t.Fatalf("persisted script = %q, error = %v", contents, err)
	}
	localRunnerPath := filepath.Join(filepath.Dir(started.ScriptPath), workflowRunnerMaterialized)
	if contents, err := os.ReadFile(localRunnerPath); err != nil || !bytes.Equal(contents, workflowRunnerSource) {
		t.Fatalf("persisted local runner differs from embedded source: %v", err)
	}

	record, err := application.workflows.get(item.ID, thread.ID, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+thread.ID+"/children",
		bytes.NewBufferString(`{"title":"forbidden","prompt":"do not create","agent":"pi","worktree":false}`),
	)
	unrelated.Header.Set(agentTokenHeader, record.Token)
	unrelatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unrelatedResponse, unrelated)
	if unrelatedResponse.Code != http.StatusForbidden {
		t.Fatalf("workflow token reached unrelated managed-agent API: status=%d body=%s", unrelatedResponse.Code, unrelatedResponse.Body.String())
	}
	skillFork := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+thread.ID+"/skill-forks",
		bytes.NewBufferString(`{"title":"forbidden skill","prompt":"do not create","agent":"pi","worktree":false}`),
	)
	skillFork.Header.Set(agentTokenHeader, record.Token)
	skillForkResponse := httptest.NewRecorder()
	handler.ServeHTTP(skillForkResponse, skillFork)
	if skillForkResponse.Code != http.StatusForbidden {
		t.Fatalf("workflow token reached skill-fork API: status=%d body=%s", skillForkResponse.Code, skillForkResponse.Body.String())
	}
	eventPath := path + "/" + started.ID + "/events"
	wrong := httptest.NewRequest(http.MethodPost, eventPath, bytes.NewBufferString(`{"eventId":"event-wrong","type":"started","meta":{"name":"api-test","description":"API lifecycle"}}`))
	wrong.Header.Set(workflowTokenHeader, "wrong")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusForbidden {
		t.Fatalf("wrong scoped token status = %d", wrongResponse.Code)
	}
	startedEvent := httptest.NewRequest(http.MethodPost, eventPath, bytes.NewBufferString(`{"eventId":"event-started","type":"started","meta":{"name":"api-test","description":"API lifecycle","phases":[{"title":"Scan"}]}}`))
	startedEvent.Header.Set(workflowTokenHeader, record.Token)
	startedEventResponse := httptest.NewRecorder()
	handler.ServeHTTP(startedEventResponse, startedEvent)
	if startedEventResponse.Code != http.StatusOK {
		t.Fatalf("started event status = %d, body = %s", startedEventResponse.Code, startedEventResponse.Body.String())
	}
	for attempt := 0; attempt < 2; attempt++ {
		logEvent := httptest.NewRequest(http.MethodPost, eventPath, bytes.NewBufferString(`{"eventId":"event-log","type":"log","message":"once"}`))
		logEvent.Header.Set(workflowTokenHeader, record.Token)
		logResponse := httptest.NewRecorder()
		handler.ServeHTTP(logResponse, logEvent)
		if logResponse.Code != http.StatusOK {
			t.Fatalf("duplicate log attempt %d status = %d, body = %s", attempt, logResponse.Code, logResponse.Body.String())
		}
	}
	persisted, err := application.workflows.get(item.ID, thread.ID, started.ID)
	if err != nil || len(persisted.Logs) != 1 {
		t.Fatalf("idempotent workflow logs = %#v, error = %v", persisted.Logs, err)
	}

	get := httptest.NewRequest(http.MethodGet, path+"/"+started.ID, nil)
	get.Header.Set(agentTokenHeader, application.terminal.agentToken)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"state":"running"`) {
		t.Fatalf("get workflow status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	if strings.Contains(getResponse.Body.String(), record.Token) {
		t.Fatal("public workflow response exposed its scoped token")
	}

	stopRequest := httptest.NewRequest(http.MethodPost, path+"/"+started.ID+"/stop", bytes.NewBufferString(`{}`))
	stopRequest.Header.Set(agentTokenHeader, application.terminal.agentToken)
	stopResponse := httptest.NewRecorder()
	handler.ServeHTTP(stopResponse, stopRequest)
	if stopResponse.Code != http.StatusOK || stoppedID != "process-workflow" || !strings.Contains(stopResponse.Body.String(), `"state":"stopped"`) {
		t.Fatalf("stop status = %d, stopped=%q body=%s", stopResponse.Code, stoppedID, stopResponse.Body.String())
	}
	stopAgain := httptest.NewRequest(http.MethodPost, path+"/"+started.ID+"/stop", bytes.NewBufferString(`{}`))
	stopAgainResponse := httptest.NewRecorder()
	handler.ServeHTTP(stopAgainResponse, stopAgain)
	if stopAgainResponse.Code != http.StatusOK {
		t.Fatalf("idempotent stop status = %d body=%s", stopAgainResponse.Code, stopAgainResponse.Body.String())
	}
}

func strconvQuote(value string) string {
	contents, _ := json.Marshal(value)
	return string(contents)
}

type workflowChildStopWriter struct {
	once sync.Once
	done chan struct{}
}

func (w *workflowChildStopWriter) Write(contents []byte) (int, error) {
	return len(contents), nil
}

func (w *workflowChildStopWriter) Close() error {
	w.once.Do(func() {
		close(w.done)
	})
	return nil
}

func TestWorkflowProcessWatchesStopOnServerClose(t *testing.T) {
	key := threadStatusKey{projectID: "project", threadID: "thread"}
	stopped := make(chan struct{})
	var stopOnce sync.Once
	application := &Server{
		terminal:  &terminalHandler{tmuxPath: "/unused/tmux"},
		workflows: &workflowManager{},
		workflowWatches: map[threadStatusKey]*workflowProcessWatch{
			key: {
				stop: func() {
					stopOnce.Do(func() {
						close(stopped)
					})
				},
			},
		},
	}

	if err := application.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("server close did not release the workflow process watch")
	}

	application.syncWorkflowProcessWatch(key.projectID, key.threadID, []workflowRunRecord{{
		State:     workflowStateRunning,
		CreatedAt: time.Now().UTC(),
	}}, false)
	application.queueWorkflowProcessReconcile(key.projectID, key.threadID)
	application.workflowWatchMu.Lock()
	watches := len(application.workflowWatches)
	watchesStopped := application.workflowWatchesStopped
	application.workflowWatchMu.Unlock()
	application.workflowKickMu.Lock()
	kicks := len(application.workflowKicks)
	kicksStopped := application.workflowKicksStopped
	application.workflowKickMu.Unlock()
	if watches != 0 || kicks != 0 || !watchesStopped || !kicksStopped {
		t.Fatalf("workflow lifecycle recreated after close: watches=%d kicks=%d watchStopped=%t kickStopped=%t",
			watches, kicks, watchesStopped, kicksStopped)
	}
}

func TestWorkflowProcessInspectionErrorKeepsRunActiveAndSchedulesRetry(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	workflows, err := newWorkflowManager(store.DataDirectory())
	if err != nil {
		t.Fatal(err)
	}
	runID := "wf-inspection-retry"
	runDirectory, err := workflows.runDirectory(item.ID, thread.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(runDirectory, workflowScriptFileName)
	startedAt := time.Now().UTC().Add(-time.Minute)
	record := workflowRunRecord{
		Version:    workflowRecordVersion,
		ID:         runID,
		ProjectID:  item.ID,
		ThreadID:   thread.ID,
		Token:      "inspection-retry-token",
		State:      workflowStateRunning,
		Attempt:    1,
		Name:       "Inspection retry",
		ScriptPath: scriptPath,
		ProcessID:  "process-inspection-retry",
		CreatedAt:  startedAt,
		StartedAt:  &startedAt,
		UpdatedAt:  startedAt,
	}
	if err := workflows.create(record, []byte("return null\n"), workflowRunnerManifest{
		Version:    workflowManifestVersion,
		RunID:      runID,
		Attempt:    1,
		Endpoint:   "http://127.0.0.1.invalid/api/thread",
		Token:      record.Token,
		ScriptPath: scriptPath,
	}); err != nil {
		t.Fatal(err)
	}

	terminal := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, "kcv-inspection-retry")
	terminal.tmuxPath = filepath.Join(t.TempDir(), "missing-tmux")
	application := &Server{
		projects:     store,
		terminal:     terminal,
		workflows:    workflows,
		stateChanges: newStateChangeBroker(),
	}
	defer application.stopWorkflowProcessWatches()

	records, transient := application.reconcileWorkflowProcessesDetailed(item, thread, []workflowRunRecord{record})
	if !transient || len(records) != 1 || records[0].State != workflowStateRunning {
		t.Fatalf("transient workflow inspection = retry %t, records %#v", transient, records)
	}
	persisted, err := workflows.get(item.ID, thread.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != workflowStateRunning || persisted.FinishedAt != nil {
		t.Fatalf("transient inspection settled workflow: %#v", persisted)
	}

	application.syncWorkflowProcessWatch(item.ID, thread.ID, records, transient)
	application.workflowWatchMu.Lock()
	watch := application.workflowWatches[threadStatusKey{projectID: item.ID, threadID: thread.ID}]
	var retryDelay time.Duration
	retryScheduled := false
	if watch != nil {
		retryDelay = watch.retryDelay
		retryScheduled = watch.retry != nil
	}
	application.workflowWatchMu.Unlock()
	if watch == nil || !retryScheduled || retryDelay != workflowProcessRetryMin {
		t.Fatalf("transient inspection retry watch = %#v, scheduled=%t delay=%s",
			watch, retryScheduled, retryDelay)
	}
}

func TestWorkflowReconcileCannotFailANewerAttempt(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)
	t.Cleanup(func() {
		_ = application.Close(context.Background())
	})
	thread := item.Threads[0]
	runID := "wf-newer-attempt"
	runDirectory, err := application.workflows.runDirectory(item.ID, thread.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	createdAt := now.Add(-time.Hour)
	current := workflowRunRecord{
		Version:    workflowRecordVersion,
		ID:         runID,
		ProjectID:  item.ID,
		ThreadID:   thread.ID,
		Token:      "newer-attempt-token",
		State:      workflowStateQueued,
		Attempt:    2,
		Name:       "Newer attempt",
		ScriptPath: filepath.Join(runDirectory, workflowScriptFileName),
		CreatedAt:  createdAt,
		UpdatedAt:  now,
	}
	if err := application.workflows.create(current, []byte("return null\n"), workflowRunnerManifest{
		Version:    workflowManifestVersion,
		RunID:      runID,
		Attempt:    current.Attempt,
		Endpoint:   "http://127.0.0.1.invalid/api/thread",
		Token:      current.Token,
		ScriptPath: current.ScriptPath,
	}); err != nil {
		t.Fatal(err)
	}

	oldStartedAt := createdAt
	stale := current
	stale.State = workflowStateRunning
	stale.Attempt = 1
	stale.ProcessID = "old-process"
	stale.StartedAt = &oldStartedAt
	stale.UpdatedAt = createdAt
	records, transient := application.reconcileWorkflowProcessesDetailed(item, thread, []workflowRunRecord{stale})
	if transient {
		t.Fatal("stale workflow attempt reconciliation reported a transient failure")
	}
	if len(records) != 1 || records[0].Attempt != current.Attempt || records[0].State != workflowStateQueued {
		t.Fatalf("reconciled workflow records = %#v", records)
	}
	persisted, err := application.workflows.get(item.ID, thread.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Attempt != current.Attempt || persisted.State != workflowStateQueued ||
		persisted.ProcessID != "" || persisted.FinishedAt != nil {
		t.Fatalf("stale reconciliation changed the newer attempt: %#v", persisted)
	}
	if !workflowProcessIsStarting(persisted, now.Add(workflowProcessStartupGrace/2)) {
		t.Fatalf("newer queued attempt did not receive a fresh startup grace period: %#v", persisted)
	}
}

func TestWorkflowProcessWatchRecoversAfterRestartAndSettlesRunnerDisappearance(t *testing.T) {
	_, socketName := isolatedTmuxServer(t)
	dataDirectory := t.TempDir()
	projectsPath := filepath.Join(dataDirectory, "projects.json")
	store, err := project.NewStore(projectsPath)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	beforeRestartContext, stopBeforeRestart := context.WithCancel(context.Background())
	handler, err := NewWithOptions(store, Options{
		TmuxSocketName: socketName,
		CleanupContext: beforeRestartContext,
		DisableCleanup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeRestart := handler.(*Server)
	if beforeRestart.workflows.nodePath == "" {
		stopBeforeRestart()
		t.Skip("node is not installed")
	}
	thread := item.Threads[0]

	runnerCommand := shellCommand(beforeRestart.workflows.nodePath, []string{
		"-e", "setInterval(() => {}, 1000)",
	})
	window, err := beforeRestart.terminal.newProcessWindow(item, thread, "workflow-runner", runnerCommand)
	if err != nil {
		stopBeforeRestart()
		t.Fatal(err)
	}
	runID := "wf-runner-gone"
	createdAt := time.Now().UTC().Add(-workflowProcessStartupGrace - time.Second)
	runDirectory, err := beforeRestart.workflows.runDirectory(item.ID, thread.ID, runID)
	if err != nil {
		stopBeforeRestart()
		t.Fatal(err)
	}
	scriptPath := filepath.Join(runDirectory, workflowScriptFileName)
	record := workflowRunRecord{
		Version:       workflowRecordVersion,
		ID:            runID,
		ProjectID:     item.ID,
		ThreadID:      thread.ID,
		Token:         "runner-disappearance-token",
		State:         workflowStateRunning,
		Attempt:       1,
		Name:          "Runner disappearance",
		ScriptPath:    scriptPath,
		ProcessID:     window.ID,
		CreatedAt:     createdAt,
		StartedAt:     &createdAt,
		UpdatedAt:     createdAt,
		CloseChildren: true,
	}
	manifest := workflowRunnerManifest{
		Version:    workflowManifestVersion,
		RunID:      runID,
		Attempt:    1,
		Endpoint:   "http://127.0.0.1.invalid/api/thread",
		Token:      record.Token,
		ScriptPath: scriptPath,
	}
	if err := beforeRestart.workflows.create(record, []byte("return null\n"), manifest); err != nil {
		stopBeforeRestart()
		t.Fatal(err)
	}

	child, err := store.AddThreadWithOptions(item.ID, "Workflow child", project.AddThreadOptions{
		ParentThreadID:  thread.ID,
		WorkflowRunID:   runID,
		WorkflowAgentID: "agent-runner-gone",
	})
	if err != nil {
		stopBeforeRestart()
		t.Fatal(err)
	}
	stopBeforeRestart()

	restartedStore, err := project.NewStore(projectsPath)
	if err != nil {
		t.Fatal(err)
	}
	restartedContext, stopRestarted := context.WithCancel(context.Background())
	t.Cleanup(stopRestarted)
	handler, err = NewWithOptions(restartedStore, Options{
		TmuxSocketName: socketName,
		CleanupContext: restartedContext,
		DisableCleanup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)

	childDone := make(chan struct{})
	childProcess := &piNativeProcess{
		key:     piNativeProcessKey{ProjectID: item.ID, ThreadID: child.ID},
		command: &exec.Cmd{},
		stdin:   &workflowChildStopWriter{done: childDone},
		done:    childDone,
	}
	application.terminal.nativePi.mu.Lock()
	application.terminal.nativePi.processes[childProcess.key] = childProcess
	application.terminal.nativePi.mu.Unlock()

	application.workflowWatchMu.Lock()
	_, lifecycleOwned := application.workflowWatches[threadStatusKey{projectID: item.ID, threadID: thread.ID}]
	application.workflowWatchMu.Unlock()
	if !lifecycleOwned {
		t.Fatal("restarted server did not recover the workflow-owned process watch")
	}
	beforeDisappearance, err := application.workflows.get(item.ID, thread.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeDisappearance.State != workflowStateRunning {
		t.Fatalf("workflow settled before its runner disappeared: %#v", beforeDisappearance)
	}

	output, err := application.terminal.tmuxCommand("send-keys", "-t", window.TmuxID, "C-c").CombinedOutput()
	if err != nil {
		t.Fatalf("interrupt workflow runner: %v: %s", err, output)
	}

	deadline := time.Now().Add(3 * time.Second)
	var settled workflowRunRecord
	for time.Now().Before(deadline) {
		settled, err = application.workflows.get(item.ID, thread.ID, runID)
		if err == nil && settled.State == workflowStateFailed && channelClosed(childDone) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != workflowStateFailed ||
		settled.Error != "Workflow process exited before reporting a final result." ||
		settled.FinishedAt == nil {
		t.Fatalf("workflow after runner disappearance = %#v", settled)
	}
	if !channelClosed(childDone) {
		t.Fatal("workflow child Pi process was not stopped")
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		application.workflowWatchMu.Lock()
		ownedWatches := len(application.workflowWatches)
		application.workflowWatchMu.Unlock()
		application.terminal.tmuxWatchMu.Lock()
		tmuxWatches := len(application.terminal.tmuxWatches)
		application.terminal.tmuxWatchMu.Unlock()
		if ownedWatches == 0 && tmuxWatches == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("settled workflow retained its lifecycle-owned tmux watch")
}

func TestSavedWorkflowAPISavesListsAndRunsCommandsWithActivation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)
	launches := 0
	application.workflowProcessLauncher = func(_ project.Project, _ project.Thread, name, _ string) (processWindow, error) {
		launches++
		return processWindow{ID: fmt.Sprintf("saved-process-%d", launches), Name: name}, nil
	}
	thread := item.Threads[0]
	basePath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/workflows"
	activate := httptest.NewRequest(http.MethodPost, basePath+"/activation", bytes.NewBufferString(`{"prompt":"use a workflow to create a saved command","source":"rpc","mode":"prompt"}`))
	activate.Header.Set(agentTokenHeader, application.terminal.agentToken)
	activateResponse := httptest.NewRecorder()
	handler.ServeHTTP(activateResponse, activate)
	if activateResponse.Code != http.StatusOK || !strings.Contains(activateResponse.Body.String(), `"activated":true`) {
		t.Fatalf("activation status=%d body=%s", activateResponse.Code, activateResponse.Body.String())
	}
	script := "export const meta = { name: 'saved-api', description: 'saved API test' }\nreturn args\n"
	start := httptest.NewRequest(http.MethodPost, basePath, bytes.NewReader([]byte(`{"script":`+strconvQuote(script)+`}`)))
	start.Header.Set(agentTokenHeader, application.terminal.agentToken)
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var run workflowRunSnapshot
	if err := json.NewDecoder(startResponse.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}

	save := httptest.NewRequest(http.MethodPost, basePath+"/"+run.ID+"/save", bytes.NewBufferString(`{"name":"saved-api","scope":"project"}`))
	saveResponse := httptest.NewRecorder()
	handler.ServeHTTP(saveResponse, save)
	if saveResponse.Code != http.StatusCreated {
		t.Fatalf("save status=%d body=%s", saveResponse.Code, saveResponse.Body.String())
	}
	destination := filepath.Join(projectRoot, savedWorkflowDirectory, "saved-api.js")
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != script {
		t.Fatalf("saved script=%q error=%v", contents, err)
	}
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("saved script mode=%v error=%v", info, err)
	}
	overwrite := httptest.NewRequest(http.MethodPost, basePath+"/"+run.ID+"/save", bytes.NewBufferString(`{"name":"saved-api","scope":"project"}`))
	overwriteResponse := httptest.NewRecorder()
	handler.ServeHTTP(overwriteResponse, overwrite)
	if overwriteResponse.Code != http.StatusConflict {
		t.Fatalf("overwrite status=%d body=%s", overwriteResponse.Code, overwriteResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, basePath+"/saved", nil)
	list.Header.Set(agentTokenHeader, application.terminal.agentToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"saved-api"`) {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	clear := httptest.NewRequest(http.MethodPost, basePath+"/activation", bytes.NewBufferString(`{"prompt":"continue normally","source":"rpc","mode":"prompt"}`))
	clear.Header.Set(agentTokenHeader, application.terminal.agentToken)
	clearResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearResponse, clear)
	withoutActivation := httptest.NewRequest(http.MethodPost, basePath+"/commands/run/saved-api", bytes.NewBufferString(`{}`))
	withoutActivation.Header.Set(agentTokenHeader, application.terminal.agentToken)
	withoutActivationResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutActivationResponse, withoutActivation)
	if withoutActivationResponse.Code != http.StatusConflict {
		t.Fatalf("saved run without activation status=%d body=%s", withoutActivationResponse.Code, withoutActivationResponse.Body.String())
	}
	savedActivation := httptest.NewRequest(http.MethodPost, basePath+"/activation", bytes.NewBufferString(`{"prompt":"/saved-api","source":"rpc","mode":"prompt"}`))
	savedActivation.Header.Set(agentTokenHeader, application.terminal.agentToken)
	savedActivationResponse := httptest.NewRecorder()
	handler.ServeHTTP(savedActivationResponse, savedActivation)
	if savedActivationResponse.Code != http.StatusOK || !strings.Contains(savedActivationResponse.Body.String(), `"mode":"saved"`) {
		t.Fatalf("saved activation status=%d body=%s", savedActivationResponse.Code, savedActivationResponse.Body.String())
	}
	runSaved := httptest.NewRequest(http.MethodPost, basePath+"/commands/run/saved-api", bytes.NewBufferString(`{"args":{"issue":42}}`))
	runSaved.Header.Set(agentTokenHeader, application.terminal.agentToken)
	runSavedResponse := httptest.NewRecorder()
	handler.ServeHTTP(runSavedResponse, runSaved)
	if runSavedResponse.Code != http.StatusCreated || launches != 2 {
		t.Fatalf("saved run status=%d launches=%d body=%s", runSavedResponse.Code, launches, runSavedResponse.Body.String())
	}
}

func TestWorkflowRunnerCommandUsesARestrictedEnvironment(t *testing.T) {
	manager, err := newWorkflowManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if manager.nodePath == "" || manager.permissionFlag == "" {
		t.Skip("Node.js workflow permissions are unavailable")
	}
	command, err := manager.runnerCommand("/tmp/"+workflowManifestFileName, "/tmp/"+workflowScriptFileName, "http://127.0.0.1:4321/api/thread", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	requiredValues := []string{
		"cd '/tmp' && exec ",
		"/usr/bin/env",
		"'-i'",
		"'PATH=/usr/bin:/bin:/usr/sbin:/sbin'",
		manager.nodePath,
		"'" + manager.permissionFlag + "'",
		"'--allow-fs-read=/tmp'",
		"'./" + workflowRunnerMaterialized + "'",
		"'./" + workflowManifestFileName + "'",
	}
	if manager.allowNet {
		requiredValues = append(requiredValues, "'--allow-net=127.0.0.1:4321'")
	}
	for _, required := range requiredValues {
		if !strings.Contains(command, required) {
			t.Fatalf("runner command %q does not contain %q", command, required)
		}
	}
	if strings.Count(command, "/tmp") != 2 || len(command) > maxWorkflowRunnerCommandBytes {
		t.Fatalf("runner command does not keep its workflow directory bounded: %q", command)
	}
	longDirectory := "/tmp/" + strings.Repeat("x", maxWorkflowRunnerCommandBytes)
	if _, err := manager.runnerCommand(filepath.Join(longDirectory, workflowManifestFileName), filepath.Join(longDirectory, workflowScriptFileName), "http://127.0.0.1:4321", "/usr/bin/env"); err == nil {
		t.Fatal("runner command accepted an unsafe command length")
	}
}

func TestWorkflowManagerRetainsOnlyRecentSettledRuns(t *testing.T) {
	manager, err := newWorkflowManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const projectID = "project"
	const threadID = "thread"
	for index := 0; index < maxRetainedWorkflowsPerThread+5; index++ {
		runID := fmt.Sprintf("wf-%03d", index)
		directory, err := manager.runDirectory(projectID, threadID, runID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Unix(int64(index+1), 0).UTC()
		record := workflowRunRecord{
			Version: workflowRecordVersion, ID: runID, ProjectID: projectID, ThreadID: threadID,
			Token: "token", State: workflowStateQueued, Name: runID,
			ScriptPath: filepath.Join(directory, workflowScriptFileName), CreatedAt: now, UpdatedAt: now,
		}
		manifest := workflowRunnerManifest{
			Version: workflowManifestVersion, RunID: runID, Endpoint: "http://127.0.0.1",
			Token: "token", ScriptPath: record.ScriptPath, MaxConcurrency: 1,
		}
		if err := manager.create(record, []byte("export const meta = { name: 'x', description: 'x' }\nreturn null\n"), manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.mutate(projectID, threadID, runID, func(run *workflowRunRecord) error {
			run.State = workflowStateFinished
			run.FinishedAt = &now
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := manager.list(projectID, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != maxRetainedWorkflowsPerThread {
		t.Fatalf("retained workflows = %d, want %d", len(records), maxRetainedWorkflowsPerThread)
	}
	if records[0].ID != fmt.Sprintf("wf-%03d", maxRetainedWorkflowsPerThread+4) || records[len(records)-1].ID != "wf-005" {
		t.Fatalf("retained workflow range = %q ... %q", records[0].ID, records[len(records)-1].ID)
	}
	if _, err := os.Stat(filepath.Join(manager.root, projectID, threadID, "wf-000")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old workflow directory still exists: %v", err)
	}
}

func TestWorkflowDisableEnvironmentMatchesClaudeCode(t *testing.T) {
	for _, value := range []string{"1", "true", "YES", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("KIWI_CODE_DISABLE_WORKFLOWS", "")
			t.Setenv("CLAUDE_CODE_DISABLE_WORKFLOWS", value)
			if !workflowDisabledByEnvironment() {
				t.Fatalf("CLAUDE_CODE_DISABLE_WORKFLOWS=%q was not honored", value)
			}
		})
	}
}

func TestWorkflowActivationRequiresCurrentHumanOptIn(t *testing.T) {
	tests := []struct {
		name           string
		prompt         string
		keywordEnabled bool
		want           bool
	}{
		{name: "keyword", prompt: "ultracode: audit every route", keywordEnabled: true, want: true},
		{name: "keyword disabled", prompt: "ultracode: audit every route", keywordEnabled: false, want: false},
		{name: "direct use", prompt: "Please use a workflow to audit every route", keywordEnabled: true, want: true},
		{name: "direct run", prompt: "run this as a workflow", keywordEnabled: true, want: true},
		{name: "verb after workflow", prompt: "workflow this migration", keywordEnabled: true, want: true},
		{name: "discussion is not activation", prompt: "workflows should follow the same activation rules", keywordEnabled: true, want: false},
		{name: "implementation mention", prompt: "fix the workflow implementation", keywordEnabled: true, want: false},
		{name: "quoted request", prompt: "Document the phrase `use a workflow` here", keywordEnabled: true, want: false},
		{name: "empty", prompt: "", keywordEnabled: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := workflowPromptExplicitlyRequestsRun(test.prompt, test.keywordEnabled); got != test.want {
				t.Fatalf("workflowPromptExplicitlyRequestsRun(%q, %t) = %t, want %t", test.prompt, test.keywordEnabled, got, test.want)
			}
		})
	}
	if got := savedWorkflowInvocationName("Run /triage-issues on 1024 and 1025"); got != "triage-issues" {
		t.Fatalf("saved invocation = %q", got)
	}
	if got := savedWorkflowInvocationName("read https://example.test/workflow"); got != "" {
		t.Fatalf("URL unexpectedly activated saved workflow %q", got)
	}
	if got := savedWorkflowInvocationName("edit /triage-issues before the release"); got != "" {
		t.Fatalf("file-like path unexpectedly activated saved workflow %q", got)
	}
	if got := savedWorkflowInvocationName("Explain `run /triage-issues` in the docs"); got != "" {
		t.Fatalf("quoted command unexpectedly activated saved workflow %q", got)
	}
}

func TestSavedWorkflowResolutionUsesClaudeCodePrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	personalConfig := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", personalConfig)
	personal := filepath.Join(personalConfig, "workflows")
	rootWorkflows := filepath.Join(root, savedWorkflowDirectory)
	nestedWorkflows := filepath.Join(nested, savedWorkflowDirectory)
	for _, directory := range []string{personal, rootWorkflows, nestedWorkflows} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := func(name string) []byte {
		return []byte("export const meta = { name: '" + name + "', description: 'test' }\nreturn '" + name + "'\n")
	}
	for _, candidate := range []struct {
		path string
		name string
	}{
		{filepath.Join(personal, "shared.js"), "personal"},
		{filepath.Join(personal, "personal-only.js"), "personal-only"},
		{filepath.Join(rootWorkflows, "shared.js"), "root"},
		{filepath.Join(rootWorkflows, "root-only.js"), "root-only"},
		{filepath.Join(nestedWorkflows, "shared.js"), "nested"},
	} {
		if err := os.WriteFile(candidate.path, script(candidate.name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	item := project.Project{ID: "project", Path: root}
	thread := project.Thread{ID: "thread", Cwd: nested}
	resolved, err := resolveSavedWorkflow(item, thread, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Scope != "project" || resolved.Path != filepath.Join(nestedWorkflows, "shared.js") {
		t.Fatalf("resolved shared workflow = %#v", resolved)
	}
	workflows, err := listSavedWorkflowSnapshots(item, thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 3 {
		t.Fatalf("resolved workflows = %#v", workflows)
	}
	directories, repositoryRoot := projectWorkflowDirectories(item, thread)
	if repositoryRoot != root || len(directories) != 2 || directories[0] != nestedWorkflows || directories[1] != rootWorkflows {
		t.Fatalf("project workflow directories = %#v, root=%q", directories, repositoryRoot)
	}
}

func TestWorkflowPauseResumePreservesCompletedAgentValues(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	application := handler.(*Server)
	launches := 0
	stops := 0
	application.workflowProcessLauncher = func(_ project.Project, _ project.Thread, _ string, _ string) (processWindow, error) {
		launches++
		return processWindow{ID: fmt.Sprintf("process-%d", launches)}, nil
	}
	application.workflowProcessStopper = func(_ project.Project, _ project.Thread, _ string) error {
		stops++
		return nil
	}
	thread := item.Threads[0]
	basePath := "/api/projects/" + item.ID + "/threads/" + thread.ID + "/workflows"
	application.workflows.setActivation(item.ID, thread.ID, "prompt", "small")
	script := "export const meta = { name: 'resume-test', description: 'resume test' }\nreturn null\n"
	start := httptest.NewRequest(http.MethodPost, basePath, bytes.NewReader([]byte(`{"script":`+strconvQuote(script)+`}`)))
	start.Header.Set(agentTokenHeader, application.terminal.agentToken)
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusCreated {
		t.Fatalf("start status = %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var snapshot workflowRunSnapshot
	if err := json.NewDecoder(startResponse.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := application.workflows.mutate(item.ID, thread.ID, snapshot.ID, func(run *workflowRunRecord) error {
		now := time.Now().UTC()
		run.State = workflowStateRunning
		run.StartedAt = &now
		run.Agents = []workflowAgentRecord{
			{ID: "agent-0001", Label: "cached", State: workflowAgentStateFinished, StartedAt: now.Add(-time.Minute), FinishedAt: &now, Value: json.RawMessage(`{"ok":true}`)},
			{ID: "agent-0002", Label: "unfinished", State: workflowAgentStateWorking, StartedAt: now, ThreadID: "retained-child", ChildRunID: 7, Response: &childThreadRunResponse{Thread: project.Thread{ID: "retained-child"}, Run: piNativeRunSnapshot{ID: 7}}},
			{ID: "agent-0003", Label: "omitted", State: workflowAgentStateFinished, StartedAt: now.Add(-time.Minute), FinishedAt: &now, ValueOmitted: true},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	pause := httptest.NewRequest(http.MethodPost, basePath+"/"+record.ID+"/pause", bytes.NewBufferString(`{}`))
	pauseResponse := httptest.NewRecorder()
	handler.ServeHTTP(pauseResponse, pause)
	if pauseResponse.Code != http.StatusOK {
		t.Fatalf("pause status = %d body=%s", pauseResponse.Code, pauseResponse.Body.String())
	}
	paused, err := application.workflows.get(item.ID, thread.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != workflowStatePaused || paused.Agents[0].State != workflowAgentStateFinished || !strings.Contains(string(paused.Agents[0].Value), `"ok"`) || paused.Agents[1].State != workflowAgentStatePaused || stops != 1 {
		t.Fatalf("paused workflow = %#v, stops=%d", paused, stops)
	}
	pauseAgain := httptest.NewRequest(http.MethodPost, basePath+"/"+record.ID+"/pause", bytes.NewBufferString(`{}`))
	pauseAgainResponse := httptest.NewRecorder()
	handler.ServeHTTP(pauseAgainResponse, pauseAgain)
	if pauseAgainResponse.Code != http.StatusOK || stops != 1 {
		t.Fatalf("idempotent pause status=%d stops=%d body=%s", pauseAgainResponse.Code, stops, pauseAgainResponse.Body.String())
	}

	resumeLaunchEntered := make(chan struct{})
	resumeLaunchRelease := make(chan struct{})
	blockingLauncher := func(_ project.Project, _ project.Thread, _ string, _ string) (processWindow, error) {
		launches++
		close(resumeLaunchEntered)
		<-resumeLaunchRelease
		return processWindow{ID: fmt.Sprintf("process-%d", launches)}, nil
	}
	application.workflowProcessLauncher = blockingLauncher
	resume := httptest.NewRequest(http.MethodPost, basePath+"/"+record.ID+"/resume", bytes.NewBufferString(`{}`))
	resumeResponse := httptest.NewRecorder()
	resumeDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(resumeResponse, resume)
		close(resumeDone)
	}()
	select {
	case <-resumeLaunchEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("resume did not reach the workflow launcher")
	}

	queuedResume, err := application.workflows.get(item.ID, thread.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queuedResume.State != workflowStateQueued || queuedResume.Attempt != 2 ||
		queuedResume.ProcessID != "" || queuedResume.StartedAt != nil ||
		!workflowProcessIsStarting(queuedResume, time.Now()) {
		t.Fatalf("workflow awaiting resumed launch = %#v", queuedResume)
	}
	// The injected launcher has already been captured by resumeWorkflow and is
	// blocked above. Temporarily enable real reconciliation to prove that this
	// queued attempt receives startup grace instead of being failed while its
	// replacement process has not been persisted yet.
	application.workflowProcessLauncher = nil
	reconciled, transient := application.reconcileWorkflowProcessesDetailed(item, thread, []workflowRunRecord{queuedResume})
	if transient || len(reconciled) != 1 || reconciled[0].State != workflowStateQueued {
		t.Fatalf("workflow during resumed launch = transient %t, records %#v", transient, reconciled)
	}
	application.workflowProcessLauncher = blockingLauncher
	close(resumeLaunchRelease)
	select {
	case <-resumeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("resume did not finish after releasing the workflow launcher")
	}
	if resumeResponse.Code != http.StatusOK {
		t.Fatalf("resume status = %d body=%s", resumeResponse.Code, resumeResponse.Body.String())
	}
	resumed, err := application.workflows.get(item.ID, thread.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != workflowStateQueued || resumed.Attempt != 2 || launches != 2 ||
		resumed.StartedAt != nil || !workflowProcessIsStarting(resumed, time.Now()) ||
		resumed.Agents[0].State != workflowAgentStateFinished ||
		resumed.Agents[1].State != workflowAgentStatePaused || resumed.Agents[1].ChildRunID != 0 || resumed.Agents[1].Response != nil ||
		resumed.Agents[2].State != workflowAgentStatePaused || resumed.Agents[2].ValueOmitted {
		t.Fatalf("resumed workflow = %#v, launches=%d", resumed, launches)
	}
	manifestContents, err := os.ReadFile(filepath.Join(filepath.Dir(resumed.ScriptPath), workflowManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest workflowRunnerManifest
	if err := json.Unmarshal(manifestContents, &manifest); err != nil || manifest.Attempt != 2 {
		t.Fatalf("resumed manifest = %#v, error=%v", manifest, err)
	}

	eventPath := basePath + "/" + record.ID + "/events"
	startedEvent := httptest.NewRequest(http.MethodPost, eventPath, bytes.NewBufferString(`{"eventId":"attempt-2-started","type":"started","meta":{"name":"resume-test","description":"resume test"}}`))
	startedEvent.Header.Set(workflowTokenHeader, resumed.Token)
	startedResponse := httptest.NewRecorder()
	handler.ServeHTTP(startedResponse, startedEvent)
	if startedResponse.Code != http.StatusOK {
		t.Fatalf("resumed started event = %d body=%s", startedResponse.Code, startedResponse.Body.String())
	}
	cachedEvent := httptest.NewRequest(http.MethodPost, eventPath, bytes.NewBufferString(`{"eventId":"attempt-2-cached","type":"agent_started","agentId":"agent-0001","label":"cached"}`))
	cachedEvent.Header.Set(workflowTokenHeader, resumed.Token)
	cachedResponse := httptest.NewRecorder()
	handler.ServeHTTP(cachedResponse, cachedEvent)
	if cachedResponse.Code != http.StatusOK || !strings.Contains(cachedResponse.Body.String(), `"cached":true`) || !strings.Contains(cachedResponse.Body.String(), `"ok":true`) {
		t.Fatalf("cached agent event = %d body=%s", cachedResponse.Code, cachedResponse.Body.String())
	}
}
