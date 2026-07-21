package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

func enableChildThreadCreationForTest(t *testing.T, handler http.Handler) *Server {
	t.Helper()
	server, ok := handler.(*Server)
	if !ok {
		t.Fatalf("isolated handler type = %T, want *Server", handler)
	}
	server.allowChildThreadCreation = true
	return server
}

func TestChildThreadCreationIsTemporarilyDisabled(t *testing.T) {
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
	server := handler.(*Server)
	request := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+item.Threads[0].ID+"/children",
		bytes.NewBufferString(`{"title":"Blocked child","prompt":"Do not run.","agent":"pi","worktree":false}`),
	)
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled child creation status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "temporarily disabled") {
		t.Fatalf("disabled child creation body = %s", response.Body.String())
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 1 {
		t.Fatalf("disabled child creation changed threads: %#v", persisted.Threads)
	}
}

func TestForkedSkillChildStartsWithoutWorkflowActivation(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	disableWorkflows := true
	if _, err := store.UpdateSettingsValues(project.SettingsUpdate{DisableWorkflows: &disableWorkflows}); err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := handler.(*Server)
	fakePi := filepath.Join(t.TempDir(), "fake-pi")
	fakeScript := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"model-a","name":"Model A","reasoning":true}]}}'
      ;;
    *'Keep running'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_start"}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Forked skill finished"}],"timestamp":2}}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"get_state"'*)
      printf '%s\n' '{"type":"response","command":"get_state","success":true,"data":{"isStreaming":false}}'
      ;;
    *'"type":"get_messages"'*)
      printf '%s\n' '{"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	server.terminal.nativePi.piPath = fakePi

	parent := item.Threads[0]
	path := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/skill-forks"
	body := `{"title":"deep-research","prompt":"Run the loaded skill.","agent":"pi","model":"custom/model-a","worktree":false}`
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized skill fork status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}
	isolatedRequest := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(
		`{"title":"isolated","prompt":"Do not run.","agent":"pi","model":"custom/model-a","worktree":true}`,
	))
	isolatedRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	isolatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(isolatedResponse, isolatedRequest)
	if isolatedResponse.Code != http.StatusBadRequest || !strings.Contains(isolatedResponse.Body.String(), "share the parent workspace") {
		t.Fatalf("isolated skill fork status = %d, body = %s", isolatedResponse.Code, isolatedResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("skill fork status = %d, body = %s", response.Code, response.Body.String())
	}
	var created childThreadRunResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Thread.ParentThreadID != parent.ID || created.Thread.Worktree ||
		created.Thread.WorkflowRunID != "" || created.Thread.WorkflowAgentID != "" || created.Run.ID == 0 {
		t.Fatalf("created skill fork = %#v", created)
	}
	runs, err := server.workflows.list(item.ID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("skill fork created workflow records: %#v", runs)
	}

	runPath := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/children/" + created.Thread.ID + "/runs/" + strconv.FormatUint(created.Run.ID, 10)
	var run piNativeRunSnapshot
	deadline := time.Now().Add(5 * time.Second)
	for {
		runRequest := httptest.NewRequest(http.MethodGet, runPath, nil)
		runRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
		runResponse := httptest.NewRecorder()
		handler.ServeHTTP(runResponse, runRequest)
		if runResponse.Code != http.StatusOK {
			t.Fatalf("skill fork run status = %d, body = %s", runResponse.Code, runResponse.Body.String())
		}
		if err := json.NewDecoder(runResponse.Body).Decode(&run); err != nil {
			t.Fatal(err)
		}
		if run.State == "finished" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("skill fork run did not finish: %#v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Output != "Forked skill finished" {
		t.Fatalf("skill fork output = %q", run.Output)
	}

	closeRequest := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+parent.ID+"/children/"+created.Thread.ID+"/close",
		bytes.NewBufferString(`{}`),
	)
	closeRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	closeResponse := httptest.NewRecorder()
	handler.ServeHTTP(closeResponse, closeRequest)
	if closeResponse.Code != http.StatusOK {
		t.Fatalf("close skill fork status = %d, body = %s", closeResponse.Code, closeResponse.Body.String())
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(
		`{"title":"cancel-me","prompt":"Keep running","agent":"pi","model":"custom/model-a","worktree":false}`,
	))
	cancelRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusCreated {
		t.Fatalf("cancellable skill fork status = %d, body = %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var cancellable childThreadRunResponse
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancellable); err != nil {
		t.Fatal(err)
	}
	stopRequest := httptest.NewRequest(http.MethodPost,
		path+"/"+cancellable.Thread.ID+"/stop",
		bytes.NewBufferString(`{}`),
	)
	stopRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	stopResponse := httptest.NewRecorder()
	handler.ServeHTTP(stopResponse, stopRequest)
	if stopResponse.Code != http.StatusOK {
		t.Fatalf("stop skill fork status = %d, body = %s", stopResponse.Code, stopResponse.Body.String())
	}
	_, stopped, err := store.GetThread(item.ID, cancellable.Thread.ID)
	if err != nil || stopped.ClosedAt == nil {
		t.Fatalf("stopped skill fork = %#v, error = %v", stopped, err)
	}
}

func TestEnabledChildThreadStartFailureRollsBackTransientWorktree(t *testing.T) {
	repository := t.TempDir()
	serverGit(t, repository, "init", "-q")
	serverGit(t, repository, "config", "user.email", "test@example.com")
	serverGit(t, repository, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repository, "add", "README.md")
	serverGit(t, repository, "commit", "-q", "-m", "initial")

	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repository)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := enableChildThreadCreationForTest(t, handler)
	fakePi := filepath.Join(t.TempDir(), "fake-pi")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      chmod 600 "$0"
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"model-a","name":"Model A","reasoning":true}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server.terminal.nativePi.piPath = fakePi
	parent := item.Threads[0]
	request := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+parent.ID+"/children",
		bytes.NewBufferString(`{"title":"Transient child","prompt":"Start should fail.","agent":"pi","model":"custom/model-a","worktree":true}`),
	)
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("start failure status = %d, body = %s", response.Code, response.Body.String())
	}

	updated, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Threads) != 1 || updated.Threads[0].ID != parent.ID {
		t.Fatalf("threads after failed creation = %#v", updated.Threads)
	}
	worktrees := serverGit(t, repository, "worktree", "list", "--porcelain")
	if strings.Count(worktrees, "worktree ") != 1 {
		t.Fatalf("failed creation left a registered worktree:\n%s", worktrees)
	}
	if branches := strings.TrimSpace(serverGit(t, repository, "branch", "--list", "kiwi-code/*")); branches != "" {
		t.Fatalf("failed creation left a worktree branch: %s", branches)
	}
	orphanedPath := filepath.Join(filepath.Dir(dataFile), "orphaned-worktrees.json")
	if contents, err := os.ReadFile(orphanedPath); err == nil {
		if strings.TrimSpace(string(contents)) != "[]" {
			t.Fatalf("orphaned worktree manifest = %s", contents)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestChildRollbackDefersArtifactsAfterNativeRemoveFailureAndRecoversOnRestart(t *testing.T) {
	repository := t.TempDir()
	serverGit(t, repository, "init", "-q")
	serverGit(t, repository, "config", "user.email", "test@example.com")
	serverGit(t, repository, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repository, "add", "README.md")
	serverGit(t, repository, "commit", "-q", "-m", "initial")

	dataFile := filepath.Join(t.TempDir(), "data", "projects.json")
	store, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repository)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.AddThreadWithOptions(item.ID, "Provisional child", project.AddThreadOptions{
		Worktree:        true,
		ParentThreadID:  item.Threads[0].ID,
		CreationPending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := newThreadUsageTracker(store.DataDirectory())
	if err != nil {
		t.Fatal(err)
	}
	native := newPiNativeManager(store.DataDirectory(), nil, nil, "token")
	native.removeThreadHook = func(string, string) error { return errors.New("injected native remove failure") }
	server := &Server{projects: store, terminal: &terminalHandler{nativePi: native}, threadUsage: usage}
	if err := server.rollbackCreatedChildThread(item, child, true, "injected failure"); err == nil || !strings.Contains(err.Error(), "injected native remove failure") {
		t.Fatalf("rollback error = %v", err)
	}
	_, pending, err := store.GetThread(item.ID, child.ID)
	if err != nil || !pending.RollbackPending || pending.RollbackCleanupReady {
		t.Fatalf("rollback marker after failed native removal = %#v, error = %v", pending, err)
	}
	if _, err := os.Stat(child.WorktreePath); err != nil {
		t.Fatalf("worktree was removed before native shutdown: %v", err)
	}
	if branch := strings.TrimSpace(serverGit(t, repository, "branch", "--list", child.Branch)); branch == "" {
		t.Fatal("branch was removed before native shutdown")
	}

	restartedStore, err := project.NewStore(dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, restartedPending, err := restartedStore.GetThread(item.ID, child.ID); err != nil || !restartedPending.RollbackPending || restartedPending.RollbackCleanupReady {
		t.Fatalf("restart did not retain the teardown-gated marker: %#v, error = %v", restartedPending, err)
	}
	restartedUsage, err := newThreadUsageTracker(restartedStore.DataDirectory())
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Server{
		projects:    restartedStore,
		terminal:    &terminalHandler{nativePi: newPiNativeManager(restartedStore.DataDirectory(), nil, nil, "token")},
		threadUsage: restartedUsage,
	}
	if err := restarted.recoverPendingThreadCreationRollbacks(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restartedStore.GetThread(item.ID, child.ID); !errors.Is(err, project.ErrThreadNotFound) {
		t.Fatalf("recovered thread error = %v, want ErrThreadNotFound", err)
	}
	if _, err := os.Stat(child.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left worktree path: %v", err)
	}
	if branch := strings.TrimSpace(serverGit(t, repository, "branch", "--list", child.Branch)); branch != "" {
		t.Fatalf("recovery left branch: %q", branch)
	}
}

func TestEnabledChildCreationIsQuarantinedUntilPromptHandoffCommits(t *testing.T) {
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
	server := enableChildThreadCreationForTest(t, handler)
	fakePi := filepath.Join(t.TempDir(), "fake-pi")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"custom","id":"model-a","name":"Model A","reasoning":true}]}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server.terminal.nativePi.piPath = fakePi
	entered := make(chan project.Thread, 1)
	release := make(chan struct{})
	server.childCreationBeforeCommit = func(thread project.Thread) {
		entered <- thread
		<-release
	}
	parent := item.Threads[0]
	request := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+parent.ID+"/children",
		bytes.NewBufferString(`{"title":"Provisional child","prompt":"Start.","agent":"pi","model":"custom/model-a","worktree":false}`),
	)
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()

	var provisional project.Thread
	select {
	case provisional = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for provisional child barrier")
	}
	_, persisted, err := store.GetThread(item.ID, provisional.ID)
	if err != nil || !persisted.RollbackPending || persisted.RollbackCleanupReady {
		t.Fatalf("provisional child = %#v, error = %v", persisted, err)
	}
	visible := clientProjects(store.List())
	if len(visible) != 1 || len(visible[0].Threads) != 1 || visible[0].Threads[0].ID != parent.ID {
		t.Fatalf("provisional child leaked into client project snapshot: %#v", visible)
	}
	threadRequest := httptest.NewRequest(http.MethodGet, "/thread", nil)
	threadRequest.SetPathValue("id", item.ID)
	threadRequest.SetPathValue("threadId", provisional.ID)
	threadResponse := httptest.NewRecorder()
	server.getThread(threadResponse, threadRequest)
	if threadResponse.Code != http.StatusNotFound {
		t.Fatalf("provisional thread read status = %d, body = %s", threadResponse.Code, threadResponse.Body.String())
	}
	if _, err := store.AddThreadWithOptions(item.ID, "Blocked descendant", project.AddThreadOptions{ParentThreadID: provisional.ID}); !errors.Is(err, project.ErrThreadRollbackPending) {
		t.Fatalf("provisional child accepted a descendant: %v", err)
	}
	if _, err := store.UpdateThreadTitle(item.ID, provisional.ID, "Blocked rename", false); !errors.Is(err, project.ErrThreadRollbackPending) {
		t.Fatalf("provisional child accepted a mutation: %v", err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out committing provisional child")
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	_, committed, err := store.GetThread(item.ID, provisional.ID)
	if err != nil || committed.RollbackPending || committed.RollbackCleanupReady {
		t.Fatalf("committed child = %#v, error = %v", committed, err)
	}
	if err := server.terminal.nativePi.removeThread(item.ID, provisional.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEnabledNonWorktreeChildCapabilityDiscoveryUsesProjectPath(t *testing.T) {
	repository := t.TempDir()
	serverGit(t, repository, "init", "-q")
	serverGit(t, repository, "config", "user.email", "test@example.com")
	serverGit(t, repository, "config", "user.name", "Test User")
	if err := os.MkdirAll(filepath.Join(repository, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(repository, ".pi", "extensions", "provider.ts")
	if err := os.WriteFile(providerPath, []byte("root-provider\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repository, "add", ".pi/extensions/provider.ts")
	serverGit(t, repository, "commit", "-q", "-m", "root provider")
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repository)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.AddThreadWithOptions(item.ID, "Worktree parent", project.AddThreadOptions{Worktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent.Cwd, ".pi", "extensions", "provider.ts"), []byte("parent-provider\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := enableChildThreadCreationForTest(t, handler)
	fakePi := filepath.Join(t.TempDir(), "fake-pi")
	script := `#!/bin/sh
model=''
if grep -q root-provider .pi/extensions/provider.ts 2>/dev/null; then model='root-model'; fi
if grep -q parent-provider .pi/extensions/provider.ts 2>/dev/null; then model='parent-model'; fi
if [ -z "$model" ]; then exit 8; fi
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' "{\"type\":\"response\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"provider\":\"cwd-provider\",\"id\":\"$model\",\"name\":\"$model\",\"reasoning\":true,\"thinkingLevelMap\":{\"max\":\"max\"}}]}}"
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server.terminal.nativePi.piPath = fakePi
	path := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/children"
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{
		"title":"Project-folder child",
		"prompt":"Run from the project folder",
		"agent":"pi",
		"model":"cwd-provider/root-model",
		"thinkingLevel":"max",
		"worktree":false
	}`))
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("non-worktree child status = %d, body = %s", response.Code, response.Body.String())
	}
	var created childThreadRunResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Thread.Cwd != item.Path {
		t.Fatalf("non-worktree child cwd = %q, want project path %q", created.Thread.Cwd, item.Path)
	}
}

func TestEnabledChildThreadAgentAPIStartsTracksCommunicatesAndCloses(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := enableChildThreadCreationForTest(t, handler)

	fakePi := filepath.Join(t.TempDir(), "fake-pi")
	fakeScript := `#!/bin/sh
if [ "$1" = "--list-models" ]; then
  printf '%s\n' 'provider model context max-out thinking images' 'openai-codex gpt-5.6-luna 372K 128K yes yes' 'custom no-thinking 128K 16K no no'
  exit 0
fi
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_available_models"'*)
      printf '%s\n' '{"type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"openai-codex","id":"gpt-5.6-luna","name":"gpt-5.6-luna","reasoning":true,"thinkingLevelMap":{"max":"max"}},{"provider":"custom","id":"no-thinking","name":"no-thinking","reasoning":false}]}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Implemented from the child"}],"timestamp":2}}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"get_state"'*)
      printf '%s\n' '{"type":"response","command":"get_state","success":true,"data":{"isStreaming":false}}'
      ;;
    *'"type":"get_messages"'*)
      printf '%s\n' '{"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
      ;;
  esac
done
`
	if err := os.WriteFile(fakePi, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	server.terminal.nativePi.piPath = fakePi

	childrenPath := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/children"
	nestingPath := "/api/projects/" + item.ID + "/threads/" + parent.ID + "/nesting"
	unauthorizedNesting := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedNesting, httptest.NewRequest(http.MethodGet, nestingPath, nil))
	if unauthorizedNesting.Code != http.StatusForbidden {
		t.Fatalf("unauthorized nesting status = %d, body = %s", unauthorizedNesting.Code, unauthorizedNesting.Body.String())
	}
	nestingRequest := httptest.NewRequest(http.MethodGet, nestingPath, nil)
	nestingRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	nestingResponse := httptest.NewRecorder()
	handler.ServeHTTP(nestingResponse, nestingRequest)
	var nesting struct {
		CurrentDepth   int `json:"currentDepth"`
		MaxDepth       int `json:"maxDepth"`
		RemainingDepth int `json:"remainingDepth"`
	}
	if nestingResponse.Code != http.StatusOK {
		t.Fatalf("nesting status = %d, body = %s", nestingResponse.Code, nestingResponse.Body.String())
	}
	if err := json.NewDecoder(nestingResponse.Body).Decode(&nesting); err != nil {
		t.Fatal(err)
	}
	if nesting.CurrentDepth != 0 || nesting.MaxDepth != 1 || nesting.RemainingDepth != 1 {
		t.Fatalf("root nesting context = %#v", nesting)
	}

	body := []byte(`{"title":"Delegated implementation","prompt":"Implement the feature independently.","agent":"pi","model":"openai-codex/gpt-5.6-luna","thinkingLevel":"max","worktree":false}`)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewReader(body)))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized child status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	nonPiRequest := httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewBufferString(
		`{"title":"Unsupported harness","prompt":"Do not run.","agent":"claude","worktree":false}`,
	))
	nonPiRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	nonPiResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonPiResponse, nonPiRequest)
	if nonPiResponse.Code != http.StatusBadRequest {
		t.Fatalf("non-Pi child status = %d, body = %s", nonPiResponse.Code, nonPiResponse.Body.String())
	}

	invalidModelRequest := httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewBufferString(
		`{"title":"Ambiguous model","prompt":"Do not run.","agent":"pi","model":"gpt-5.6-luna","thinkingLevel":"max","worktree":false}`,
	))
	invalidModelRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	invalidModelResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidModelResponse, invalidModelRequest)
	if invalidModelResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid model status = %d, body = %s", invalidModelResponse.Code, invalidModelResponse.Body.String())
	}
	var invalidModelDetails childThreadModelValidationError
	if err := json.NewDecoder(invalidModelResponse.Body).Decode(&invalidModelDetails); err != nil {
		t.Fatal(err)
	}
	if len(invalidModelDetails.AvailableModels) != 2 || invalidModelDetails.AvailableModels[0].ID != "openai-codex/gpt-5.6-luna" {
		t.Fatalf("invalid model details = %#v", invalidModelDetails)
	}

	invalidReasoningRequest := httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewBufferString(
		`{"title":"Unsupported reasoning","prompt":"Do not run.","agent":"pi","model":"custom/no-thinking","thinkingLevel":"high","worktree":false}`,
	))
	invalidReasoningRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	invalidReasoningResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidReasoningResponse, invalidReasoningRequest)
	if invalidReasoningResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid reasoning status = %d, body = %s", invalidReasoningResponse.Code, invalidReasoningResponse.Body.String())
	}

	parentStop, err := server.terminal.stopThreadSessions(item, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	blockedCreateRequest := httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewReader(body))
	blockedCreateRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	blockedCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(blockedCreateResponse, blockedCreateRequest)
	if blockedCreateResponse.Code != http.StatusConflict {
		t.Fatalf("child creation while parent stops status = %d, body = %s", blockedCreateResponse.Code, blockedCreateResponse.Body.String())
	}
	if err := server.terminal.cancelStopThread(item.ID, parent.ID, parentStop); err != nil {
		t.Fatal(err)
	}

	createRequest := httptest.NewRequest(http.MethodPost, childrenPath, bytes.NewReader(body))
	createRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create child status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created childThreadRunResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Agent != codingAgentPi ||
		created.Thread.ParentThreadID != parent.ID ||
		created.Thread.AgentModel != "openai-codex/gpt-5.6-luna" ||
		created.Thread.AgentThinkingLevel != "max" ||
		created.Thread.NestedDepth != nil ||
		created.Thread.Worktree ||
		created.Run.ID == 0 {
		t.Fatalf("created child = %#v", created)
	}
	assertChildNestingPrompt(t, server.terminal.nativePi, item.ID, created.Thread.ID, 1, 1, "")

	runPath := childrenPath + "/" + created.Thread.ID + "/runs/" + strconv.FormatUint(created.Run.ID, 10)
	var run piNativeRunSnapshot
	deadline := time.Now().Add(5 * time.Second)
	for {
		request := httptest.NewRequest(http.MethodGet, runPath, nil)
		request.Header.Set(agentTokenHeader, server.terminal.agentToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("child run status = %d, body = %s", response.Code, response.Body.String())
		}
		if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
			t.Fatal(err)
		}
		if run.State == "finished" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child run did not finish: %#v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Output != "Implemented from the child" || run.FinishedAt == nil {
		t.Fatalf("finished child run = %#v", run)
	}

	listRequest := httptest.NewRequest(http.MethodGet, childrenPath, nil)
	listRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	var children []listedChildThread
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list children status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&children); err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].Thread.ID != created.Thread.ID || children[0].Run == nil || children[0].Run.State != "finished" {
		t.Fatalf("listed children = %#v", children)
	}

	nestedCreateRequest := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/children",
		bytes.NewBufferString(`{"title":"Too deep","prompt":"Do not run.","agent":"pi","worktree":false}`),
	)
	nestedCreateRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	nestedCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(nestedCreateResponse, nestedCreateRequest)
	if nestedCreateResponse.Code != http.StatusConflict {
		t.Fatalf("nested child status = %d, body = %s", nestedCreateResponse.Code, nestedCreateResponse.Body.String())
	}

	depth := 2
	if _, err := store.UpdateSettingsValues(project.SettingsUpdate{SubAgentNestingDepth: &depth}); err != nil {
		t.Fatal(err)
	}
	nestedCreateRequest = httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/children",
		bytes.NewBufferString(`{"title":"Allowed grandchild","prompt":"Check nesting.","agent":"pi","worktree":false}`),
	)
	nestedCreateRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	nestedCreateResponse = httptest.NewRecorder()
	handler.ServeHTTP(nestedCreateResponse, nestedCreateRequest)
	if nestedCreateResponse.Code != http.StatusCreated {
		t.Fatalf("configured nested child status = %d, body = %s", nestedCreateResponse.Code, nestedCreateResponse.Body.String())
	}
	var nested childThreadRunResponse
	if err := json.NewDecoder(nestedCreateResponse.Body).Decode(&nested); err != nil {
		t.Fatal(err)
	}
	if nested.Thread.ParentThreadID != created.Thread.ID {
		t.Fatalf("nested child relationship = %#v", nested.Thread)
	}
	assertChildNestingPrompt(t, server.terminal.nativePi, item.ID, nested.Thread.ID, 2, depth, "")
	nestedRunPath := "/api/projects/" + item.ID + "/threads/" + created.Thread.ID + "/children/" + nested.Thread.ID + "/runs/" + strconv.FormatUint(nested.Run.ID, 10)
	var nestedRun piNativeRunSnapshot
	deadline = time.Now().Add(5 * time.Second)
	for {
		request := httptest.NewRequest(http.MethodGet, nestedRunPath, nil)
		request.Header.Set(agentTokenHeader, server.terminal.agentToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("nested child run status = %d, body = %s", response.Code, response.Body.String())
		}
		if err := json.NewDecoder(response.Body).Decode(&nestedRun); err != nil {
			t.Fatal(err)
		}
		if nestedRun.State == "finished" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nested child run did not finish: %#v", nestedRun)
		}
		time.Sleep(10 * time.Millisecond)
	}
	prematureParentCloseRequest := httptest.NewRequest(http.MethodPost,
		childrenPath+"/"+created.Thread.ID+"/close",
		bytes.NewBufferString(`{}`),
	)
	prematureParentCloseRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	prematureParentCloseResponse := httptest.NewRecorder()
	handler.ServeHTTP(prematureParentCloseResponse, prematureParentCloseRequest)
	if prematureParentCloseResponse.Code != http.StatusConflict {
		t.Fatalf("parent close with open descendant status = %d, body = %s", prematureParentCloseResponse.Code, prematureParentCloseResponse.Body.String())
	}

	nestedCloseRequest := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/children/"+nested.Thread.ID+"/close",
		bytes.NewBufferString(`{}`),
	)
	nestedCloseRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	nestedCloseResponse := httptest.NewRecorder()
	handler.ServeHTTP(nestedCloseResponse, nestedCloseRequest)
	if nestedCloseResponse.Code != http.StatusOK {
		t.Fatalf("nested child close status = %d, body = %s", nestedCloseResponse.Code, nestedCloseResponse.Body.String())
	}

	sendThreadTestMessage(t, handler, server.terminal.agentToken,
		"/api/projects/"+item.ID+"/threads/"+parent.ID+"/messages",
		`{"threadId":"`+created.Thread.ID+`","message":"Please also check the tests."}`,
		http.StatusCreated,
	)
	childInbox := receiveThreadTestMessages(t, handler, server.terminal.agentToken,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/messages/receive")
	if len(childInbox) != 1 || childInbox[0].FromThreadID != parent.ID || childInbox[0].Message != "Please also check the tests." {
		t.Fatalf("child inbox = %#v", childInbox)
	}
	emptyInboxRequest := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/messages/receive",
		bytes.NewBufferString(`{}`),
	)
	emptyInboxRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	emptyInboxResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyInboxResponse, emptyInboxRequest)
	if got := string(bytes.TrimSpace(emptyInboxResponse.Body.Bytes())); got != "[]" {
		t.Fatalf("empty child inbox JSON = %q, want []", got)
	}

	sendThreadTestMessage(t, handler, server.terminal.agentToken,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/messages",
		`{"message":"Tests pass."}`,
		http.StatusCreated,
	)
	parentInbox := receiveThreadTestMessages(t, handler, server.terminal.agentToken,
		"/api/projects/"+item.ID+"/threads/"+parent.ID+"/messages/receive")
	if len(parentInbox) != 1 || parentInbox[0].FromThreadID != created.Thread.ID || parentInbox[0].Message != "Tests pass." {
		t.Fatalf("parent inbox = %#v", parentInbox)
	}

	closePath := childrenPath + "/" + created.Thread.ID + "/close"
	unauthorizedClose := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedClose, httptest.NewRequest(http.MethodPost, closePath, bytes.NewBufferString(`{}`)))
	if unauthorizedClose.Code != http.StatusForbidden {
		t.Fatalf("unauthorized close status = %d, body = %s", unauthorizedClose.Code, unauthorizedClose.Body.String())
	}
	closeRequest := httptest.NewRequest(http.MethodPost, closePath, bytes.NewBufferString(`{}`))
	closeRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	closeResponse := httptest.NewRecorder()
	handler.ServeHTTP(closeResponse, closeRequest)
	if closeResponse.Code != http.StatusOK {
		t.Fatalf("close child status = %d, body = %s", closeResponse.Code, closeResponse.Body.String())
	}
	var closed project.Thread
	if err := json.NewDecoder(closeResponse.Body).Decode(&closed); err != nil {
		t.Fatal(err)
	}
	if closed.ID != created.Thread.ID || closed.ClosedAt == nil || !closed.ClosedAt.Equal(*run.FinishedAt) {
		t.Fatalf("closed child = %#v, run = %#v", closed, run)
	}
	closedParentCreateRequest := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads/"+created.Thread.ID+"/children",
		bytes.NewBufferString(`{"title":"Hidden child","prompt":"Do not run.","agent":"pi","worktree":false}`),
	)
	closedParentCreateRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	closedParentCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(closedParentCreateResponse, closedParentCreateRequest)
	if closedParentCreateResponse.Code != http.StatusConflict {
		t.Fatalf("closed parent child status = %d, body = %s", closedParentCreateResponse.Code, closedParentCreateResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(server.terminal.nativePi.dataDirectory, piNativeSessionDirectoryName, item.ID, created.Thread.ID)); err != nil {
		t.Fatalf("completed child conversation was not retained: %v", err)
	}

	listClosedRequest := httptest.NewRequest(http.MethodGet, childrenPath, nil)
	listClosedRequest.Header.Set(agentTokenHeader, server.terminal.agentToken)
	listClosedResponse := httptest.NewRecorder()
	handler.ServeHTTP(listClosedResponse, listClosedRequest)
	if listClosedResponse.Code != http.StatusOK || string(bytes.TrimSpace(listClosedResponse.Body.Bytes())) != "[]" {
		t.Fatalf("open children after close status = %d, body = %s", listClosedResponse.Code, listClosedResponse.Body.String())
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 3 || persisted.Threads[1].ID != created.Thread.ID || persisted.Threads[1].ClosedAt == nil ||
		persisted.Threads[2].ID != nested.Thread.ID || persisted.Threads[2].ClosedAt == nil {
		t.Fatalf("retained completed child tree = %#v", persisted.Threads)
	}

	deleteParent := httptest.NewRecorder()
	handler.ServeHTTP(deleteParent, httptest.NewRequest(http.MethodDelete,
		"/api/projects/"+item.ID+"/threads/"+parent.ID, nil))
	if deleteParent.Code != http.StatusNoContent {
		t.Fatalf("delete parent status = %d, body = %s", deleteParent.Code, deleteParent.Body.String())
	}
	persisted, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 0 {
		t.Fatalf("threads after deleting parent tree = %#v", persisted.Threads)
	}
	if _, err := os.Stat(filepath.Join(server.terminal.nativePi.dataDirectory, piNativeSessionDirectoryName, item.ID, created.Thread.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted parent retained child conversation: %v", err)
	}
}

func TestDeleteThreadTreeRetryReconcilesChildMarkers(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := item.Threads[0]
	child, err := store.AddThreadWithOptions(item.ID, "Retained child", project.AddThreadOptions{ParentThreadID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	server := handler.(*Server)
	item, err = store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	parentStop, err := server.terminal.stopThreadSessions(item, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	childStop, err := server.terminal.stopThreadSessions(item, child.ID)
	if err != nil {
		_ = server.terminal.cancelStopThread(item.ID, parent.ID, parentStop)
		t.Fatal(err)
	}
	if err := store.DeleteThreadTree(item.ID, parent.ID, []string{parent.ID, child.ID}); err != nil {
		t.Fatal(err)
	}
	if err := server.terminal.finishStopThread(item, parent.ID, parentStop); err != nil {
		t.Fatal(err)
	}
	if err := childStop.Retain(); err != nil {
		t.Fatal(err)
	}
	childSessionDirectory := filepath.Join(server.terminal.nativePi.dataDirectory, piNativeSessionDirectoryName, item.ID, child.ID)
	if err := os.MkdirAll(childSessionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childSessionDirectory, "conversation.jsonl"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete,
		"/api/projects/"+item.ID+"/threads/"+parent.ID, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("retry parent deletion status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(childSessionDirectory); !os.IsNotExist(err) {
		t.Fatalf("retry retained child native session: %v", err)
	}
	marker, found, err := server.terminal.terminalStops.readThread(item.ID, child.ID)
	if err != nil || !found || !marker.Committed {
		t.Fatalf("reconciled child marker = %#v, found=%t, error=%v", marker, found, err)
	}
}

func TestEnabledChildThreadAPIValidatesNestedDepthBeforeStartingPi(t *testing.T) {
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
	server := enableChildThreadCreationForTest(t, handler)
	path := "/api/projects/" + item.ID + "/threads/" + item.Threads[0].ID + "/children"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(
		`{"title":"Invalid depth","prompt":"No run","nestedDepth":2,"worktree":false}`,
	))
	request.Header.Set(agentTokenHeader, server.terminal.agentToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid nested depth status = %d, body = %s", response.Code, response.Body.String())
	}
	persisted, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads) != 1 {
		t.Fatalf("invalid requests created threads: %#v", persisted.Threads)
	}
}

func TestNormalThreadAPICannotCreateChildRelationship(t *testing.T) {
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
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
		"/api/projects/"+item.ID+"/threads",
		bytes.NewBufferString(`{"title":"Not allowed","parentThreadId":"`+item.Threads[0].ID+`"}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("normal child creation status = %d, body = %s", response.Code, response.Body.String())
	}
}

func sendThreadTestMessage(t *testing.T, handler http.Handler, token, path, body string, wantStatus int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set(agentTokenHeader, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("send message status = %d, want %d, body = %s", response.Code, wantStatus, response.Body.String())
	}
}

func assertChildNestingPrompt(
	t *testing.T,
	manager *piNativeManager,
	projectID string,
	threadID string,
	currentDepth int,
	maxDepth int,
	customPrompt string,
) {
	t.Helper()
	manager.mu.Lock()
	process := manager.processes[piNativeProcessKey{ProjectID: projectID, ThreadID: threadID}]
	var arguments []string
	if process != nil && process.command != nil {
		arguments = append(arguments, process.command.Args...)
	}
	manager.mu.Unlock()
	if len(arguments) == 0 {
		t.Fatalf("child Pi process %q has no command arguments", threadID)
	}
	prompt := ""
	for index, argument := range arguments[:len(arguments)-1] {
		if argument == "--append-system-prompt" {
			prompt = arguments[index+1]
			break
		}
	}
	for _, expected := range []string{
		customPrompt,
		"sub-agent at nesting depth " + strconv.Itoa(currentDepth),
		"effective maximum sub-agent nesting depth for this thread tree is " + strconv.Itoa(maxDepth),
	} {
		if expected == "" {
			continue
		}
		if !strings.Contains(prompt, expected) {
			t.Fatalf("child Pi system prompt %q does not contain %q; args=%#v", prompt, expected, arguments)
		}
	}
}

func receiveThreadTestMessages(t *testing.T, handler http.Handler, token, path string) []childThreadMessage {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
	request.Header.Set(agentTokenHeader, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("receive messages status = %d, body = %s", response.Code, response.Body.String())
	}
	var messages []childThreadMessage
	if err := json.NewDecoder(response.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	return messages
}
