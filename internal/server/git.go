package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/ivan/dire-mux/internal/project"
)

const maxGitBranchNameBytes = 255

type gitBranch struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type gitBranchState struct {
	IsRepository bool        `json:"isRepository"`
	Current      string      `json:"current"`
	Detached     bool        `json:"detached"`
	Branches     []gitBranch `json:"branches"`
}

type gitBranchInput struct {
	Name string `json:"name"`
}

func (s *Server) listProjectGitBranches(w http.ResponseWriter, r *http.Request) {
	item, err := s.projects.Get(r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Project not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the project.")
		return
	}
	state, err := readGitBranchState(r.Context(), item.Path)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) listGitBranches(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.gitThread(w, r)
	if !ok {
		return
	}
	state, err := readGitBranchState(r.Context(), thread.Cwd)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) switchGitBranch(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.gitThread(w, r)
	if !ok {
		return
	}
	input, ok := decodeGitBranchInput(w, r)
	if !ok {
		return
	}

	state, err := readGitBranchState(r.Context(), thread.Cwd)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	if !state.IsRepository {
		writeError(w, http.StatusBadRequest, "This thread is not inside a Git repository.")
		return
	}

	found := false
	for _, branch := range state.Branches {
		if branch.Name == input.Name {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusBadRequest, "That local branch does not exist.")
		return
	}
	if !state.Detached && state.Current == input.Name {
		writeJSON(w, http.StatusOK, state)
		return
	}

	if _, err := runGit(r.Context(), thread.Cwd, "switch", "--", input.Name); err != nil {
		writeError(w, http.StatusConflict, gitErrorMessage(err))
		return
	}
	state, err = readGitBranchState(r.Context(), thread.Cwd)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	s.notifyThreadStatusChanged(r.PathValue("id"), r.PathValue("threadId"))
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) createGitBranch(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.gitThread(w, r)
	if !ok {
		return
	}
	input, ok := decodeGitBranchInput(w, r)
	if !ok {
		return
	}

	state, err := readGitBranchState(r.Context(), thread.Cwd)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	if !state.IsRepository {
		writeError(w, http.StatusBadRequest, "This thread is not inside a Git repository.")
		return
	}
	normalizedName, err := runGit(r.Context(), thread.Cwd, "check-ref-format", "--branch", input.Name)
	if err != nil || strings.TrimSpace(normalizedName) != input.Name {
		writeError(w, http.StatusBadRequest, "Enter a valid Git branch name.")
		return
	}
	for _, branch := range state.Branches {
		if branch.Name == input.Name {
			writeError(w, http.StatusConflict, "That branch already exists.")
			return
		}
	}

	if _, err := runGit(r.Context(), thread.Cwd, "switch", "-c", input.Name, "--"); err != nil {
		writeError(w, http.StatusConflict, gitErrorMessage(err))
		return
	}
	state, err = readGitBranchState(r.Context(), thread.Cwd)
	if err != nil {
		writeGitReadError(w, r, err)
		return
	}
	s.notifyThreadStatusChanged(r.PathValue("id"), r.PathValue("threadId"))
	writeJSON(w, http.StatusCreated, state)
}

func (s *Server) gitThread(w http.ResponseWriter, r *http.Request) (project.Thread, bool) {
	_, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return project.Thread{}, false
	}
	if thread.RollbackPending {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return project.Thread{}, false
	}
	return thread, true
}

func decodeGitBranchInput(w http.ResponseWriter, r *http.Request) (gitBranchInput, bool) {
	var input gitBranchInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid branch details.")
		return gitBranchInput{}, false
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "Branch name is required.")
		return gitBranchInput{}, false
	}
	if len(input.Name) > maxGitBranchNameBytes {
		writeError(w, http.StatusBadRequest, "Branch name must be 255 characters or fewer.")
		return gitBranchInput{}, false
	}
	return input, true
}

func readGitBranchState(ctx context.Context, cwd string) (gitBranchState, error) {
	empty := gitBranchState{Branches: []gitBranch{}}
	inside, err := runGit(ctx, cwd, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return empty, err
		}
		return empty, nil
	}
	if strings.TrimSpace(inside) != "true" {
		return empty, nil
	}

	current, err := runGit(ctx, cwd, "branch", "--show-current")
	if err != nil {
		return empty, err
	}
	current = strings.TrimSpace(current)
	detached := current == ""
	if detached {
		current, err = runGit(ctx, cwd, "rev-parse", "--short", "HEAD")
		if err != nil {
			return empty, err
		}
		current = strings.TrimSpace(current)
	}

	output, err := runGit(ctx, cwd, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return empty, err
	}
	branches := make([]gitBranch, 0)
	for _, name := range strings.Split(strings.TrimRight(output, "\r\n"), "\n") {
		name = strings.TrimSuffix(name, "\r")
		if name == "" {
			continue
		}
		branches = append(branches, gitBranch{Name: name, Current: !detached && name == current})
	}

	return gitBranchState{
		IsRepository: true,
		Current:      current,
		Detached:     detached,
		Branches:     branches,
	}, nil
}

func runGit(ctx context.Context, cwd string, arguments ...string) (string, error) {
	commandArguments := append([]string{"-C", cwd}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	output, err := command.Output()
	if err == nil {
		return string(output), nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		detail := strings.TrimSpace(string(exitError.Stderr))
		if detail != "" {
			return "", errors.New(detail)
		}
	}
	return "", err
}

func writeGitReadError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	if errors.Is(err, exec.ErrNotFound) {
		writeError(w, http.StatusServiceUnavailable, "git is required for branch controls. Install git and restart dire-mux.")
		return
	}
	writeError(w, http.StatusInternalServerError, gitErrorMessage(err))
}

func gitErrorMessage(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.TrimPrefix(message, "fatal: ")
	if message == "" {
		return "Git could not complete that operation."
	}
	if len(message) > 1_000 {
		message = message[:1_000] + "…"
	}
	return message
}
