package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ivan/dire-mux/internal/project"
)

const savedWorkflowDirectory = ".claude/workflows"

type savedWorkflowSnapshot struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Path  string `json:"path"`
}

func validSavedWorkflowName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	for index, character := range name {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func savedWorkflowInvocationName(prompt string) string {
	prompt = workflowPromptWithoutQuotedSegments(prompt)
	for index := 0; index < len(prompt); index++ {
		if prompt[index] != '/' || (index > 0 && (prompt[index-1] == '/' ||
			prompt[index-1] >= 'A' && prompt[index-1] <= 'Z' ||
			prompt[index-1] >= 'a' && prompt[index-1] <= 'z' ||
			prompt[index-1] >= '0' && prompt[index-1] <= '9' || prompt[index-1] == '_')) {
			continue
		}
		if index > 0 {
			prefixWords := strings.Fields(strings.ToLower(strings.TrimSpace(prompt[:index])))
			if len(prefixWords) == 0 {
				continue
			}
			verb := strings.Trim(prefixWords[len(prefixWords)-1], `"'“”‘’`)
			if verb != "run" && verb != "use" && verb != "invoke" && verb != "execute" {
				continue
			}
		}
		end := index + 1
		for end < len(prompt) {
			character := prompt[end]
			if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
				character >= '0' && character <= '9' || character == '-' || character == '_' {
				end++
				continue
			}
			break
		}
		name := prompt[index+1 : end]
		if validSavedWorkflowName(name) {
			return name
		}
	}
	return ""
}

func personalWorkflowDirectory() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve CLAUDE_CONFIG_DIR: %w", err)
		}
		return filepath.Join(filepath.Clean(absolute), "workflows"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "workflows"), nil
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func workflowRepositoryRoot(item project.Project, thread project.Thread) string {
	cwd := filepath.Clean(thread.Cwd)
	for current := cwd; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	projectRoot := filepath.Clean(item.Path)
	if pathContains(projectRoot, cwd) {
		return projectRoot
	}
	return cwd
}

// projectWorkflowDirectories returns project workflow directories from closest
// to farthest, matching Claude Code's monorepo precedence rules.
func projectWorkflowDirectories(item project.Project, thread project.Thread) ([]string, string) {
	cwd := filepath.Clean(thread.Cwd)
	root := workflowRepositoryRoot(item, thread)
	if !pathContains(root, cwd) {
		root = cwd
	}
	directories := make([]string, 0)
	for current := cwd; ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, savedWorkflowDirectory)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			directories = append(directories, candidate)
		}
		if current == root {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !pathContains(root, parent) {
			break
		}
	}
	return directories, root
}

func workflowsInDirectory(directory, scope string) []savedWorkflowSnapshot {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	result := make([]savedWorkflowSnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".js" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !validSavedWorkflowName(name) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxWorkflowScriptBytes {
			continue
		}
		result = append(result, savedWorkflowSnapshot{Name: name, Scope: scope, Path: path})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func listSavedWorkflowSnapshots(item project.Project, thread project.Thread) ([]savedWorkflowSnapshot, error) {
	byName := make(map[string]savedWorkflowSnapshot)
	personal, err := personalWorkflowDirectory()
	if err != nil {
		return nil, err
	}
	for _, workflow := range workflowsInDirectory(personal, "personal") {
		byName[workflow.Name] = workflow
	}
	projectDirectories, _ := projectWorkflowDirectories(item, thread)
	// Apply project directories from farthest to closest so the closest file
	// wins. Every project definition wins over a personal definition.
	for index := len(projectDirectories) - 1; index >= 0; index-- {
		for _, workflow := range workflowsInDirectory(projectDirectories[index], "project") {
			byName[workflow.Name] = workflow
		}
	}
	result := make([]savedWorkflowSnapshot, 0, len(byName))
	for _, workflow := range byName {
		result = append(result, workflow)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func resolveSavedWorkflow(item project.Project, thread project.Thread, name string) (savedWorkflowSnapshot, error) {
	if !validSavedWorkflowName(name) {
		return savedWorkflowSnapshot{}, errors.New("invalid saved workflow name")
	}
	workflows, err := listSavedWorkflowSnapshots(item, thread)
	if err != nil {
		return savedWorkflowSnapshot{}, err
	}
	for _, workflow := range workflows {
		if workflow.Name == name {
			return workflow, nil
		}
	}
	return savedWorkflowSnapshot{}, os.ErrNotExist
}

func readSavedWorkflowScript(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxWorkflowScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 || len(contents) > maxWorkflowScriptBytes || !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
		return nil, errors.New("saved workflow script is invalid or too large")
	}
	return contents, nil
}

func (s *Server) listSavedWorkflows(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	if s.workflowsDisabled() {
		writeError(w, http.StatusServiceUnavailable, "Dynamic workflows are disabled in Settings or by the startup environment.")
		return
	}
	item, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	workflows, err := listSavedWorkflowSnapshots(item, thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list saved workflows.")
		return
	}
	writeJSON(w, http.StatusOK, workflows)
}

func (s *Server) startSavedWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	if s.workflowsDisabled() {
		writeError(w, http.StatusServiceUnavailable, "Dynamic workflows are disabled in Settings or by the startup environment.")
		return
	}
	item, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	workflow, err := resolveSavedWorkflow(item, thread, r.PathValue("name"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Saved workflow not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve the saved workflow.")
		return
	}
	script, err := readSavedWorkflowScript(workflow.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Args            json.RawMessage `json:"args"`
		Model           string          `json:"model"`
		ThinkingLevel   string          `json:"thinkingLevel"`
		CloseOnComplete *bool           `json:"closeOnComplete"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkflowArgsBytes+(64<<10)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid saved workflow invocation.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid saved workflow invocation.")
		return
	}
	forward := map[string]any{
		"script":          string(script),
		"model":           input.Model,
		"thinkingLevel":   input.ThinkingLevel,
		"closeOnComplete": input.CloseOnComplete,
	}
	if input.Args != nil {
		if len(input.Args) > maxWorkflowArgsBytes || !json.Valid(input.Args) {
			writeError(w, http.StatusBadRequest, "Workflow args must be valid JSON no larger than 1 MiB.")
			return
		}
		forward["args"] = input.Args
	}
	if input.CloseOnComplete == nil {
		delete(forward, "closeOnComplete")
	}
	body, err := json.Marshal(forward)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not prepare the saved workflow invocation.")
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(body))
	capture := newCapturedResponse()
	s.startWorkflow(capture, request)
	copyCapturedResponse(w, capture)
}

func (s *Server) saveWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	threadID := r.PathValue("threadId")
	item, thread, err := s.projects.GetThread(projectID, threadID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return
	}
	record, err := s.workflows.get(projectID, threadID, r.PathValue("runId"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Workflow not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the workflow.")
		return
	}
	var input struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		Overwrite bool   `json:"overwrite"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid saved workflow details.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid saved workflow details.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Scope = strings.ToLower(strings.TrimSpace(input.Scope))
	if !validSavedWorkflowName(input.Name) || (input.Scope != "project" && input.Scope != "personal") {
		writeError(w, http.StatusBadRequest, "Use a workflow name containing letters, numbers, hyphens, or underscores and choose project or personal scope.")
		return
	}
	script, err := readSavedWorkflowScript(record.ScriptPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the workflow script.")
		return
	}
	var directory string
	if input.Scope == "personal" {
		directory, err = personalWorkflowDirectory()
	} else {
		directories, root := projectWorkflowDirectories(item, thread)
		if len(directories) > 0 {
			directory = directories[0]
		} else {
			directory = filepath.Join(root, savedWorkflowDirectory)
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve the workflow save location.")
		return
	}
	directoryMode := os.FileMode(0o700)
	if input.Scope == "project" {
		directoryMode = 0o755
	}
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create the workflow save directory.")
		return
	}
	destination := filepath.Join(directory, input.Name+".js")
	if !input.Overwrite {
		if _, err := os.Lstat(destination); err == nil {
			writeError(w, http.StatusConflict, "A saved workflow with that name already exists in this location. Confirm overwrite to replace it.")
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "Could not inspect the workflow destination.")
			return
		}
	}
	mode := os.FileMode(0o600)
	if input.Scope == "project" {
		mode = 0o644
	}
	if err := writeFileAtomically(destination, script, serverAtomicFileOptions{Mode: mode, SyncFile: true}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save the workflow.")
		return
	}
	writeJSON(w, http.StatusCreated, savedWorkflowSnapshot{Name: input.Name, Scope: input.Scope, Path: destination})
}
