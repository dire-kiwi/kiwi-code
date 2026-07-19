package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivan/dire-mux/internal/project"
)

func TestRollbackPendingThreadBlocksTmuxAndGitHelpersBeforeCommandsRun(t *testing.T) {
	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.AddThreadWithOptions(item.ID, "Pending", project.AddThreadOptions{CreationPending: true})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "command-ran")
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf ran > \"$COMMAND_MARKER\"\nexit 1\n"
	for _, name := range []string{"tmux", "git"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("COMMAND_MARKER", marker)
	t.Setenv("PATH", bin)

	terminal := &terminalHandler{projects: store, tmuxPath: filepath.Join(bin, "tmux")}
	tmuxRequest := httptest.NewRequest(http.MethodPost, "/shell-windows", nil)
	tmuxRequest.SetPathValue("id", item.ID)
	tmuxRequest.SetPathValue("threadId", thread.ID)
	tmuxResponse := httptest.NewRecorder()
	terminal.createShellWindow(tmuxResponse, tmuxRequest)
	if tmuxResponse.Code != http.StatusConflict {
		t.Fatalf("tmux helper status = %d, body = %s", tmuxResponse.Code, tmuxResponse.Body.String())
	}

	server := &Server{projects: store}
	gitRequest := httptest.NewRequest(http.MethodPost, "/branches", bytes.NewBufferString(`{"name":"blocked"}`))
	gitRequest.SetPathValue("id", item.ID)
	gitRequest.SetPathValue("threadId", thread.ID)
	gitResponse := httptest.NewRecorder()
	server.createGitBranch(gitResponse, gitRequest)
	if gitResponse.Code != http.StatusConflict {
		t.Fatalf("Git helper status = %d, body = %s", gitResponse.Code, gitResponse.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("tombstone helper executed an external command: %v", err)
	}
}

func TestGitBranchAPIListsCreatesAndSwitchesBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "init")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "add", "README.md")
	serverGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Initial commit")
	initialBranch := strings.TrimSpace(serverGit(t, repositoryPath, "branch", "--show-current"))

	store, err := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}
	projectBranchesPath := "/api/projects/" + item.ID + "/git/branches"
	response := gitAPIRequest(t, handler, http.MethodGet, projectBranchesPath, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list project branches status = %d, body = %s", response.Code, response.Body.String())
	}
	state := decodeGitBranchState(t, response)
	if !state.IsRepository || state.Current != initialBranch || state.Detached {
		t.Fatalf("unexpected project branch state: %#v", state)
	}

	branchesPath := "/api/projects/" + item.ID + "/threads/" + item.Threads[0].ID + "/git/branches"
	response = gitAPIRequest(t, handler, http.MethodGet, branchesPath, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list branches status = %d, body = %s", response.Code, response.Body.String())
	}
	state = decodeGitBranchState(t, response)
	if !state.IsRepository || state.Current != initialBranch || state.Detached {
		t.Fatalf("unexpected initial branch state: %#v", state)
	}

	response = gitAPIRequest(t, handler, http.MethodPost, branchesPath, `{"name":"feature/branch-picker"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create branch status = %d, body = %s", response.Code, response.Body.String())
	}
	state = decodeGitBranchState(t, response)
	if state.Current != "feature/branch-picker" {
		t.Fatalf("current branch after create = %q", state.Current)
	}

	response = gitAPIRequest(t, handler, http.MethodPost, branchesPath+"/switch", `{"name":"`+initialBranch+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("switch branch status = %d, body = %s", response.Code, response.Body.String())
	}
	state = decodeGitBranchState(t, response)
	if state.Current != initialBranch {
		t.Fatalf("current branch after switch = %q", state.Current)
	}
	foundCreated := false
	for _, branch := range state.Branches {
		if branch.Name == "feature/branch-picker" {
			foundCreated = true
		}
	}
	if !foundCreated {
		t.Fatalf("created branch missing from state: %#v", state.Branches)
	}

	response = gitAPIRequest(t, handler, http.MethodPost, branchesPath, `{"name":"-invalid"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid branch status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestThreadAPIUsesSelectedWorktreeBaseBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repositoryPath := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "init")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "add", "README.md")
	serverGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Initial commit")
	initialBranch := strings.TrimSpace(serverGit(t, repositoryPath, "branch", "--show-current"))
	serverGit(t, repositoryPath, "switch", "-c", "release/base")
	if err := os.WriteFile(filepath.Join(repositoryPath, "RELEASE.md"), []byte("release base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverGit(t, repositoryPath, "add", "RELEASE.md")
	serverGit(t, repositoryPath, "-c", "user.name=Dire Mux", "-c", "user.email=dire-mux@example.invalid", "commit", "-m", "Add release base")
	baseRevision := strings.TrimSpace(serverGit(t, repositoryPath, "rev-parse", "HEAD"))
	serverGit(t, repositoryPath, "switch", initialBranch)

	store, err := project.NewStore(filepath.Join(t.TempDir(), "data", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Demo", repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newIsolatedServerHandler(t, store)
	if err != nil {
		t.Fatal(err)
	}

	threadsPath := "/api/projects/" + item.ID + "/threads"
	response := gitAPIRequest(t, handler, http.MethodPost, threadsPath, `{"worktree":true,"baseBranch":"release/base"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create worktree status = %d, body = %s", response.Code, response.Body.String())
	}
	var thread project.Thread
	if err := json.NewDecoder(response.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if revision := strings.TrimSpace(serverGit(t, thread.Cwd, "rev-parse", "HEAD")); revision != baseRevision {
		t.Fatalf("worktree revision = %q, want selected base revision %q", revision, baseRevision)
	}

	response = gitAPIRequest(t, handler, http.MethodPost, threadsPath, `{"worktree":true,"baseBranch":"missing/base"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing base branch status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestGitBranchAPIReportsNonRepositories(t *testing.T) {
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

	path := "/api/projects/" + item.ID + "/threads/" + item.Threads[0].ID + "/git/branches"
	response := gitAPIRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("list branches status = %d, body = %s", response.Code, response.Body.String())
	}
	state := decodeGitBranchState(t, response)
	if state.IsRepository || state.Current != "" || len(state.Branches) != 0 {
		t.Fatalf("unexpected non-repository state: %#v", state)
	}
}

func gitAPIRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeGitBranchState(t *testing.T, response *httptest.ResponseRecorder) gitBranchState {
	t.Helper()
	var state gitBranchState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func serverGit(t *testing.T, path string, arguments ...string) string {
	t.Helper()
	commandArguments := append([]string{"-C", path}, arguments...)
	output, err := exec.Command("git", commandArguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
