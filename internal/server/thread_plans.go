package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ivan/dire-mux/internal/project"
)

const (
	threadPlanRecordVersion      = 1
	threadPlanDirectoryName      = "thread-plans-v1"
	maxThreadPlanContentBytes    = 256 << 10
	maxThreadPlanRequestBytes    = 2 << 20
	maxThreadPlanTitleCharacters = 120
	maxRetainedThreadPlans       = 25
)

var errThreadPlanNotFound = errors.New("thread plan not found")

type threadPlanRecord struct {
	Version        int       `json:"version"`
	ID             string    `json:"id"`
	ProjectID      string    `json:"projectId"`
	ThreadID       string    `json:"threadId"`
	SourceThreadID string    `json:"sourceThreadId"`
	Title          string    `json:"title"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"createdAt"`
}

type threadPlanSnapshot struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"projectId"`
	ThreadID       string    `json:"threadId"`
	SourceThreadID string    `json:"sourceThreadId"`
	Title          string    `json:"title"`
	CreatedAt      time.Time `json:"createdAt"`
	SizeBytes      int       `json:"sizeBytes"`
}

type threadPlanManager struct {
	root string
	mu   sync.Mutex
}

func newThreadPlanManager(dataDirectory string) (*threadPlanManager, error) {
	root := filepath.Join(dataDirectory, threadPlanDirectoryName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create thread plan directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure thread plan directory: %w", err)
	}
	return &threadPlanManager{root: root}, nil
}

func validThreadPlanPathID(value string) bool {
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

func randomThreadPlanID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validateThreadPlanRecord(record threadPlanRecord, projectID, threadID string) error {
	if record.Version != threadPlanRecordVersion ||
		!validThreadPlanPathID(record.ID) ||
		record.ProjectID != projectID || record.ThreadID != threadID ||
		!validThreadPlanPathID(record.SourceThreadID) || record.CreatedAt.IsZero() {
		return errors.New("thread plan metadata is invalid")
	}
	if record.Title == "" || utf8.RuneCountInString(record.Title) > maxThreadPlanTitleCharacters {
		return errors.New("thread plan title is invalid")
	}
	if len(record.Content) == 0 || len(record.Content) > maxThreadPlanContentBytes ||
		!utf8.ValidString(record.Content) || strings.ContainsRune(record.Content, '\x00') {
		return errors.New("thread plan content is invalid")
	}
	return nil
}

func (m *threadPlanManager) threadDirectory(projectID, threadID string) (string, error) {
	if !validThreadPlanPathID(projectID) || !validThreadPlanPathID(threadID) {
		return "", errors.New("invalid thread plan scope")
	}
	return filepath.Join(m.root, projectID, threadID), nil
}

func (m *threadPlanManager) create(projectID, threadID, sourceThreadID, title, content string) (threadPlanSnapshot, error) {
	title = strings.TrimSpace(title)
	if !validThreadPlanPathID(sourceThreadID) {
		return threadPlanSnapshot{}, errors.New("invalid thread plan source")
	}
	if title == "" || utf8.RuneCountInString(title) > maxThreadPlanTitleCharacters {
		return threadPlanSnapshot{}, errors.New("invalid thread plan title")
	}
	if strings.TrimSpace(content) == "" || len(content) > maxThreadPlanContentBytes ||
		!utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return threadPlanSnapshot{}, errors.New("invalid thread plan content")
	}
	directory, err := m.threadDirectory(projectID, threadID)
	if err != nil {
		return threadPlanSnapshot{}, err
	}
	id, err := randomThreadPlanID()
	if err != nil {
		return threadPlanSnapshot{}, fmt.Errorf("create thread plan ID: %w", err)
	}
	record := threadPlanRecord{
		Version:        threadPlanRecordVersion,
		ID:             id,
		ProjectID:      projectID,
		ThreadID:       threadID,
		SourceThreadID: sourceThreadID,
		Title:          title,
		Content:        content,
		CreatedAt:      time.Now().UTC(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return threadPlanSnapshot{}, fmt.Errorf("encode thread plan: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return threadPlanSnapshot{}, fmt.Errorf("create scoped thread plan directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return threadPlanSnapshot{}, fmt.Errorf("secure scoped thread plan directory: %w", err)
	}
	path := filepath.Join(directory, id+".json")
	if err := writeFileAtomically(path, encoded, serverAtomicFileOptions{
		Mode:          0o600,
		SyncFile:      true,
		SyncDirectory: true,
	}); err != nil {
		return threadPlanSnapshot{}, fmt.Errorf("persist thread plan: %w", err)
	}
	if err := m.pruneLocked(projectID, threadID, directory); err != nil {
		// The newly published plan remains valid even if best-effort retention
		// cleanup fails. A later publish gets another chance to prune it.
		log.Printf("prune retained thread plans: project=%q thread=%q error=%v", projectID, threadID, err)
	}
	return threadPlanSummary(record), nil
}

func threadPlanSummary(record threadPlanRecord) threadPlanSnapshot {
	return threadPlanSnapshot{
		ID:             record.ID,
		ProjectID:      record.ProjectID,
		ThreadID:       record.ThreadID,
		SourceThreadID: record.SourceThreadID,
		Title:          record.Title,
		CreatedAt:      record.CreatedAt,
		SizeBytes:      len([]byte(record.Content)),
	}
}

func (m *threadPlanManager) readRecordLocked(projectID, threadID, planID string) (threadPlanRecord, error) {
	if !validThreadPlanPathID(planID) {
		return threadPlanRecord{}, errThreadPlanNotFound
	}
	directory, err := m.threadDirectory(projectID, threadID)
	if err != nil {
		return threadPlanRecord{}, err
	}
	contents, err := os.ReadFile(filepath.Join(directory, planID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return threadPlanRecord{}, errThreadPlanNotFound
	}
	if err != nil {
		return threadPlanRecord{}, fmt.Errorf("read thread plan: %w", err)
	}
	var record threadPlanRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		return threadPlanRecord{}, fmt.Errorf("decode thread plan: %w", err)
	}
	if err := validateThreadPlanRecord(record, projectID, threadID); err != nil {
		return threadPlanRecord{}, err
	}
	return record, nil
}

func (m *threadPlanManager) get(projectID, threadID, planID string) (threadPlanRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readRecordLocked(projectID, threadID, planID)
}

func (m *threadPlanManager) recordsLocked(projectID, threadID, directory string) ([]threadPlanRecord, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []threadPlanRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list thread plans: %w", err)
	}
	records := make([]threadPlanRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("thread plan record cannot be a symbolic link")
		}
		planID := strings.TrimSuffix(entry.Name(), ".json")
		record, err := m.readRecordLocked(projectID, threadID, planID)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].CreatedAt.Equal(records[right].CreatedAt) {
			return records[left].ID > records[right].ID
		}
		return records[left].CreatedAt.After(records[right].CreatedAt)
	})
	return records, nil
}

func (m *threadPlanManager) pruneLocked(projectID, threadID, directory string) error {
	records, err := m.recordsLocked(projectID, threadID, directory)
	if err != nil {
		return err
	}
	if len(records) <= maxRetainedThreadPlans {
		return nil
	}
	var pruneErrors []error
	for _, record := range records[maxRetainedThreadPlans:] {
		if err := os.Remove(filepath.Join(directory, record.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			pruneErrors = append(pruneErrors, err)
		}
	}
	return errors.Join(pruneErrors...)
}

func (m *threadPlanManager) list(projectID, threadID string) ([]threadPlanSnapshot, error) {
	directory, err := m.threadDirectory(projectID, threadID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	records, err := m.recordsLocked(projectID, threadID, directory)
	if err != nil {
		return nil, err
	}
	plans := make([]threadPlanSnapshot, 0, len(records))
	for _, record := range records {
		plans = append(plans, threadPlanSummary(record))
	}
	return plans, nil
}

func (m *threadPlanManager) removeThread(projectID, threadID string) error {
	directory, err := m.threadDirectory(projectID, threadID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove thread plans: %w", err)
	}
	_ = os.Remove(filepath.Dir(directory))
	return nil
}

func (m *threadPlanManager) removeProject(projectID string) error {
	if !validThreadPlanPathID(projectID) {
		return errors.New("invalid thread plan project")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(m.root, projectID)); err != nil {
		return fmt.Errorf("remove project thread plans: %w", err)
	}
	return nil
}

func threadPlanOwner(item project.Project, thread project.Thread) (project.Thread, error) {
	if thread.ParentThreadID == "" {
		return thread, nil
	}
	for _, candidate := range item.Threads {
		if candidate.ID == thread.ParentThreadID {
			return candidate, nil
		}
	}
	return project.Thread{}, project.ErrThreadNotFound
}

func (s *Server) threadPlanScope(w http.ResponseWriter, r *http.Request) (project.Project, project.Thread, project.Thread, bool) {
	item, thread, err := s.projects.GetThread(r.PathValue("id"), r.PathValue("threadId"))
	if errors.Is(err, project.ErrNotFound) || errors.Is(err, project.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "Thread not found.")
		return project.Project{}, project.Thread{}, project.Thread{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread's plans.")
		return project.Project{}, project.Thread{}, project.Thread{}, false
	}
	if thread.RollbackPending {
		writeError(w, http.StatusConflict, "The thread is being rolled back.")
		return project.Project{}, project.Thread{}, project.Thread{}, false
	}
	owner, err := threadPlanOwner(item, thread)
	if err != nil || owner.RollbackPending {
		writeError(w, http.StatusConflict, "The plan's parent thread is unavailable.")
		return project.Project{}, project.Thread{}, project.Thread{}, false
	}
	return item, thread, owner, true
}

func (s *Server) listThreadPlans(w http.ResponseWriter, r *http.Request) {
	item, _, owner, ok := s.threadPlanScope(w, r)
	if !ok {
		return
	}
	if s.plans == nil {
		writeJSON(w, http.StatusOK, []threadPlanSnapshot{})
		return
	}
	plans, err := s.plans.list(item.ID, owner.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread's plans.")
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) uploadThreadPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentCapability(w, r) {
		return
	}
	item, source, owner, ok := s.threadPlanScope(w, r)
	if !ok {
		return
	}
	if source.ParentThreadID == "" || source.WorkflowRunID != "" || source.WorkflowAgentID != "" {
		writeError(w, http.StatusConflict, "Only a context: fork skill child can publish a plan to its parent thread.")
		return
	}
	if source.ClosedAt != nil || source.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "Reopen the planning child before publishing its plan.")
		return
	}
	if owner.ClosedAt != nil || owner.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "Reopen the parent thread before publishing a plan.")
		return
	}
	if s.plans == nil {
		writeError(w, http.StatusServiceUnavailable, "Thread plan storage is unavailable.")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxThreadPlanRequestBytes))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "The plan upload is too large.")
			return
		}
		writeError(w, http.StatusBadRequest, "Could not read the plan upload.")
		return
	}
	var input struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid plan upload.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid plan upload.")
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > maxThreadPlanTitleCharacters {
		writeError(w, http.StatusBadRequest, "A plan title of 120 characters or fewer is required.")
		return
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, "Plan content is required.")
		return
	}
	if len(input.Content) > maxThreadPlanContentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Plans must be 256 KB or smaller.")
		return
	}
	if !utf8.ValidString(input.Content) || strings.ContainsRune(input.Content, '\x00') {
		writeError(w, http.StatusBadRequest, "The plan contains an invalid character.")
		return
	}

	plan, err := s.plans.create(item.ID, owner.ID, source.ID, input.Title, input.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save the thread plan.")
		return
	}
	s.notifyThreadStatusChanged(item.ID, owner.ID)
	s.notifyThreadStatusChanged(item.ID, source.ID)
	writeJSON(w, http.StatusCreated, plan)
}

func threadPlanDownloadName(plan threadPlanRecord) string {
	var name strings.Builder
	separator := false
	for _, character := range strings.ToLower(plan.Title) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			if separator && name.Len() > 0 {
				name.WriteByte('-')
			}
			name.WriteRune(character)
			separator = false
			if name.Len() >= 64 {
				break
			}
			continue
		}
		separator = true
	}
	value := strings.Trim(name.String(), "-")
	if value == "" {
		suffix := plan.ID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		value = "plan-" + suffix
	}
	return value + ".md"
}

func (s *Server) downloadThreadPlan(w http.ResponseWriter, r *http.Request) {
	item, _, owner, ok := s.threadPlanScope(w, r)
	if !ok {
		return
	}
	if s.plans == nil {
		writeError(w, http.StatusServiceUnavailable, "Thread plan storage is unavailable.")
		return
	}
	plan, err := s.plans.get(item.ID, owner.ID, r.PathValue("planId"))
	if errors.Is(err, errThreadPlanNotFound) {
		writeError(w, http.StatusNotFound, "Plan not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load the thread plan.")
		return
	}
	content := []byte(plan.Content)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": threadPlanDownloadName(plan),
	}))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) removeDeletedProjectPlans(projectID string) {
	if s.plans == nil {
		return
	}
	if err := s.plans.removeProject(projectID); err != nil {
		log.Printf("remove deleted project plans: project=%q error=%v", projectID, err)
	}
}
