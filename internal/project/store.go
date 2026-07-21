package project

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ivan/dire-mux/internal/broadcast"
)

var ErrNotFound = errors.New("project not found")
var ErrThreadNotFound = errors.New("thread not found")
var ErrThreadNotArchived = errors.New("thread is not archived for deletion")
var ErrThreadHasChildren = errors.New("thread has child threads")
var ErrThreadHasOpenDescendants = errors.New("thread has open child threads")
var ErrThreadClosed = errors.New("thread is closed")
var ErrThreadTreeChanged = errors.New("thread tree changed")
var ErrChildThreadDepthLimit = errors.New("sub-agent nesting depth limit reached")
var ErrThreadRollbackPending = errors.New("thread creation rollback is pending")
var ErrProfileNotFound = errors.New("profile not found")
var ErrInvalidOrder = errors.New("invalid order")

const (
	defaultThreadTitle                   = "New thread"
	defaultArchivedThreadRetentionDays   = 30
	defaultOrphanedWorktreeRetentionDays = 30
	maxCleanupRetentionDays              = 3650
	DefaultSubAgentNestingDepth          = 1
	MaxSubAgentNestingDepth              = 4
	DefaultWorkflowSizeGuideline         = "unrestricted"
	PersonalProfileID                    = "personal"
	WorkProfileID                        = "work"
)

type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Thread struct {
	ID                   string     `json:"id"`
	Title                string     `json:"title"`
	Cwd                  string     `json:"cwd"`
	CreatedAt            time.Time  `json:"createdAt"`
	LastPromptAt         *time.Time `json:"lastPromptAt,omitempty"`
	ParentThreadID       string     `json:"parentThreadId,omitempty"`
	AgentModel           string     `json:"agentModel,omitempty"`
	AgentThinkingLevel   string     `json:"agentThinkingLevel,omitempty"`
	WorkflowRunID        string     `json:"workflowRunId,omitempty"`
	WorkflowAgentID      string     `json:"workflowAgentId,omitempty"`
	NestedDepth          *int       `json:"nestedDepth,omitempty"`
	Worktree             bool       `json:"worktree,omitempty"`
	Branch               string     `json:"branch,omitempty"`
	WorktreePath         string     `json:"worktreePath,omitempty"`
	RollbackPending      bool       `json:"rollbackPending,omitempty"`
	RollbackCleanupReady bool       `json:"rollbackCleanupReady,omitempty"`
	AutoNamed            bool       `json:"autoNamed,omitempty"`
	ClosedAt             *time.Time `json:"closedAt,omitempty"`
	ArchivedAt           *time.Time `json:"archivedAt,omitempty"`
	Bookmarked           bool       `json:"bookmarked,omitempty"`
	TokenLimit           *int64     `json:"tokenLimit,omitempty"`
	CostLimitUSD         *float64   `json:"costLimitUsd,omitempty"`
}

type Project struct {
	ID                           string    `json:"id"`
	Name                         string    `json:"name"`
	Path                         string    `json:"path"`
	ProfileID                    string    `json:"profileId"`
	Host                         string    `json:"host"`
	IsGitRepo                    bool      `json:"isGitRepo"`
	CreatedAt                    time.Time `json:"createdAt"`
	Threads                      []Thread  `json:"threads"`
	SubAgentNestingDepthOverride *int      `json:"subAgentNestingDepthOverride,omitempty"`
}

type SubAgentNestingContext struct {
	CurrentDepth int
	MaxDepth     int
}

type ThemeColors struct {
	Canvas              string `json:"canvas"`
	Sidebar             string `json:"sidebar"`
	Background          string `json:"background"`
	Panel               string `json:"panel"`
	Raised              string `json:"raised"`
	Selected            string `json:"selected"`
	Border              string `json:"border"`
	Foreground          string `json:"foreground"`
	Muted               string `json:"muted"`
	Dim                 string `json:"dim"`
	Cursor              string `json:"cursor"`
	SelectionBackground string `json:"selectionBackground"`
	SelectionForeground string `json:"selectionForeground"`
	Black               string `json:"black"`
	Red                 string `json:"red"`
	Green               string `json:"green"`
	Yellow              string `json:"yellow"`
	Blue                string `json:"blue"`
	Magenta             string `json:"magenta"`
	Cyan                string `json:"cyan"`
	White               string `json:"white"`
	BrightBlack         string `json:"brightBlack"`
	BrightRed           string `json:"brightRed"`
	BrightGreen         string `json:"brightGreen"`
	BrightYellow        string `json:"brightYellow"`
	BrightBlue          string `json:"brightBlue"`
	BrightMagenta       string `json:"brightMagenta"`
	BrightCyan          string `json:"brightCyan"`
	BrightWhite         string `json:"brightWhite"`
}

type Theme struct {
	FontFamily string      `json:"fontFamily"`
	FontSize   int         `json:"fontSize"`
	Colors     ThemeColors `json:"colors"`
}

//go:embed default-theme.json
var defaultThemeJSON []byte

var defaultTheme = loadDefaultTheme()

func loadDefaultTheme() Theme {
	var theme Theme
	if err := json.Unmarshal(defaultThemeJSON, &theme); err != nil {
		panic(fmt.Errorf("decode embedded default theme: %w", err))
	}
	normalized, err := normalizeTheme(theme)
	if err != nil {
		panic(fmt.Errorf("validate embedded default theme: %w", err))
	}
	return normalized
}

func DefaultTheme() Theme {
	return defaultTheme
}

type Settings struct {
	WorktreeBasePath              string `json:"worktreeBasePath"`
	DefaultWorktreeBasePath       string `json:"defaultWorktreeBasePath"`
	UsingDefault                  bool   `json:"usingDefault"`
	ArchivedThreadRetentionDays   int    `json:"archivedThreadRetentionDays"`
	OrphanedWorktreeRetentionDays int    `json:"orphanedWorktreeRetentionDays"`
	SubAgentNestingDepth          int    `json:"subAgentNestingDepth"`
	MaxSubAgentNestingDepth       int    `json:"maxSubAgentNestingDepth"`
	DisableWorkflows              bool   `json:"disableWorkflows"`
	WorkflowKeywordTrigger        bool   `json:"workflowKeywordTriggerEnabled"`
	WorkflowSizeGuideline         string `json:"workflowSizeGuideline"`
	Theme                         Theme  `json:"theme"`
	DefaultTheme                  Theme  `json:"defaultTheme"`
	UsingDefaultTheme             bool   `json:"usingDefaultTheme"`
}

type SettingsUpdate struct {
	WorktreeBasePath              *string
	ArchivedThreadRetentionDays   *int
	OrphanedWorktreeRetentionDays *int
	SubAgentNestingDepth          *int
	DisableWorkflows              *bool
	WorkflowKeywordTrigger        *bool
	WorkflowSizeGuideline         *string
	Theme                         *Theme
}

type persistedSettings struct {
	WorktreeBasePath              string  `json:"worktreeBasePath,omitempty"`
	ArchivedThreadRetentionDays   *int    `json:"archivedThreadRetentionDays,omitempty"`
	OrphanedWorktreeRetentionDays *int    `json:"orphanedWorktreeRetentionDays,omitempty"`
	SubAgentNestingDepth          *int    `json:"subAgentNestingDepth,omitempty"`
	DisableWorkflows              *bool   `json:"disableWorkflows,omitempty"`
	WorkflowKeywordTrigger        *bool   `json:"workflowKeywordTriggerEnabled,omitempty"`
	WorkflowSizeGuideline         *string `json:"workflowSizeGuideline,omitempty"`
	Theme                         *Theme  `json:"theme,omitempty"`
}

type ProjectUpdate struct {
	ProfileID                          *string
	SubAgentNestingDepthOverride       *int
	UpdateSubAgentNestingDepthOverride bool
}

type AddThreadOptions struct {
	Worktree           bool
	BaseBranch         string
	BaseRevision       string
	ParentThreadID     string
	AgentModel         string
	AgentThinkingLevel string
	WorkflowRunID      string
	WorkflowAgentID    string
	NestedDepth        *int
	CreationPending    bool
}

func validWorkflowIdentity(value string) bool {
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

type Store struct {
	mu                            sync.RWMutex
	filePath                      string
	profilesFilePath              string
	settingsFilePath              string
	orphanedWorktreesFilePath     string
	defaultWorktreeBasePath       string
	worktreeBasePath              string
	archivedThreadRetentionDays   int
	orphanedWorktreeRetentionDays int
	subAgentNestingDepth          int
	disableWorkflows              bool
	workflowKeywordTrigger        bool
	workflowSizeGuideline         string
	theme                         Theme
	usingDefaultTheme             bool
	profiles                      []Profile
	projects                      []Project
	orphanedWorktrees             []orphanedWorktree
	changes                       *broadcast.Broker[[]Project]
	profileChanges                *broadcast.Broker[[]Profile]
	rollbackRemoveWorktree        func(string, Thread) error
	rollbackSave                  func(string) error
	addThreadSave                 func() error
	worktreeSetup                 func(Thread) error
}

var projectMutationLocalLocks = struct {
	sync.Mutex
	locks map[string]*sync.Mutex
}{locks: make(map[string]*sync.Mutex)}

type projectMutationLease struct {
	file  *os.File
	local *sync.Mutex
}

type projectSavePublishedError struct {
	err error
}

func (e *projectSavePublishedError) Error() string {
	return e.err.Error()
}

func (e *projectSavePublishedError) Unwrap() error {
	return e.err
}

func projectSaveWasPublished(err error) bool {
	var published *projectSavePublishedError
	return errors.As(err, &published)
}

func NewStore(filePath string) (*Store, error) {
	absoluteFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve projects file: %w", err)
	}
	absoluteFilePath = filepath.Clean(absoluteFilePath)
	dataDirectory := filepath.Dir(absoluteFilePath)
	store := &Store{
		filePath:                      absoluteFilePath,
		profilesFilePath:              filepath.Join(dataDirectory, "profiles.json"),
		settingsFilePath:              filepath.Join(dataDirectory, "settings.json"),
		orphanedWorktreesFilePath:     filepath.Join(dataDirectory, "orphaned-worktrees.json"),
		defaultWorktreeBasePath:       filepath.Join(dataDirectory, "worktrees"),
		archivedThreadRetentionDays:   defaultArchivedThreadRetentionDays,
		orphanedWorktreeRetentionDays: defaultOrphanedWorktreeRetentionDays,
		subAgentNestingDepth:          DefaultSubAgentNestingDepth,
		workflowKeywordTrigger:        true,
		workflowSizeGuideline:         DefaultWorkflowSizeGuideline,
		theme:                         DefaultTheme(),
		usingDefaultTheme:             true,
		profiles:                      defaultProfiles(),
		projects:                      []Project{},
		orphanedWorktrees:             []orphanedWorktree{},
		changes:                       broadcast.NewBroker[[]Project](broadcast.DefaultMaxPending),
		profileChanges:                broadcast.NewBroker[[]Profile](broadcast.DefaultMaxPending),
	}
	if err := store.loadProfiles(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	mutation, err := store.lockProjectMutationsLocked()
	if err == nil {
		err = store.load()
		if err == nil {
			err = store.loadOrphanedWorktreesLocked()
		}
		if err == nil {
			err = store.recoverThreadCreationRollbacksLocked()
		}
		if err == nil {
			err = store.recoverWorktreeCreationIntentsLocked()
		}
		err = errors.Join(err, mutation.Release())
	}
	store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := store.loadSettings(); err != nil {
		return nil, err
	}
	return store, nil
}

func defaultProfiles() []Profile {
	return []Profile{
		{ID: PersonalProfileID, Name: "Personal"},
		{ID: WorkProfileID, Name: "Work"},
	}
}

func (s *Store) List() []Project {
	s.mu.RLock()
	projects := s.snapshotLocked()
	s.mu.RUnlock()
	return resolveSnapshot(projects)
}

// ListPersisted returns a fresh projects.json snapshot instead of this Store
// instance's potentially stale in-memory state. Cross-process ownership checks
// use it when no project ID is known in advance.
func (s *Store) ListPersisted() ([]Project, error) {
	projects, err := s.readPersistedProjects()
	if err != nil {
		return nil, err
	}
	return resolveSnapshot(projects), nil
}

func (s *Store) ListProfiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Profile{}, s.profiles...)
}

// ResolveSnapshot copies a committed event snapshot and refreshes status that
// can change outside Dire Mux, such as whether a folder is now a Git repository.
func (s *Store) ResolveSnapshot(projects []Project) []Project {
	return resolveSnapshot(cloneProjects(projects))
}

func resolveSnapshot(projects []Project) []Project {
	for index := range projects {
		projects[index].IsGitRepo = isGitRepository(projects[index].Path)
	}
	return projects
}

// SubscribeChanges reports an immutable snapshot for every committed project
// and thread mutation, in commit order.
func (s *Store) SubscribeChanges() (<-chan []Project, func()) {
	subscription := s.changes.Subscribe()
	return subscription.Events(), subscription.Close
}

func (s *Store) SubscribeProfileChanges() (<-chan []Profile, func()) {
	subscription := s.profileChanges.Subscribe()
	return subscription.Events(), subscription.Close
}

func (s *Store) notifyChangesLocked() {
	s.changes.Publish(s.snapshotLocked())
}

func (s *Store) notifyProfileChangesLocked() {
	s.profileChanges.Publish(append([]Profile{}, s.profiles...))
}

func (s *Store) snapshotLocked() []Project {
	return cloneProjects(s.projects)
}

func cloneThread(source Thread) Thread {
	thread := source
	if source.LastPromptAt != nil {
		lastPromptAt := *source.LastPromptAt
		thread.LastPromptAt = &lastPromptAt
	}
	if source.NestedDepth != nil {
		depth := *source.NestedDepth
		thread.NestedDepth = &depth
	}
	if source.ClosedAt != nil {
		closedAt := *source.ClosedAt
		thread.ClosedAt = &closedAt
	}
	if source.ArchivedAt != nil {
		archivedAt := *source.ArchivedAt
		thread.ArchivedAt = &archivedAt
	}
	if source.TokenLimit != nil {
		tokenLimit := *source.TokenLimit
		thread.TokenLimit = &tokenLimit
	}
	if source.CostLimitUSD != nil {
		costLimit := *source.CostLimitUSD
		thread.CostLimitUSD = &costLimit
	}
	return thread
}

func cloneProject(source Project) Project {
	item := source
	item.Threads = make([]Thread, len(source.Threads))
	for index, thread := range source.Threads {
		item.Threads[index] = cloneThread(thread)
	}
	if source.SubAgentNestingDepthOverride != nil {
		depth := *source.SubAgentNestingDepthOverride
		item.SubAgentNestingDepthOverride = &depth
	}
	return item
}

func cloneProjects(source []Project) []Project {
	projects := make([]Project, len(source))
	for index, item := range source {
		projects[index] = cloneProject(item)
	}
	return projects
}

func (s *Store) Get(id string) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.projects {
		if item.ID == id {
			return cloneProject(item), nil
		}
	}
	return Project{}, ErrNotFound
}

func (s *Store) GetThread(projectID, threadID string) (Project, Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.projects {
		if item.ID != projectID {
			continue
		}
		for _, thread := range item.Threads {
			if thread.ID == threadID {
				return cloneProject(item), cloneThread(thread), nil
			}
		}
		return Project{}, Thread{}, ErrThreadNotFound
	}
	return Project{}, Thread{}, ErrNotFound
}

// GetPersisted returns a project from a fresh read of projects.json instead of
// this Store instance's in-memory snapshot. It is intended for cross-process
// commit decisions where another backend may have changed the Store since this
// instance was loaded.
func (s *Store) GetPersisted(projectID string) (Project, error) {
	if projectID == "" {
		return Project{}, errors.New("persisted project ID is required")
	}
	projects, err := s.readPersistedProjects()
	if err != nil {
		return Project{}, err
	}
	for _, item := range projects {
		if item.ID == projectID {
			return item, nil
		}
	}
	return Project{}, ErrNotFound
}

// GetThreadPersisted returns a thread from a fresh projects.json snapshot.
// Deletion retries use this instead of a backend's potentially stale cache.
func (s *Store) GetThreadPersisted(projectID, threadID string) (Project, Thread, error) {
	if threadID == "" {
		return Project{}, Thread{}, errors.New("persisted thread ID is required")
	}
	item, err := s.GetPersisted(projectID)
	if err != nil {
		return Project{}, Thread{}, err
	}
	for _, thread := range item.Threads {
		if thread.ID == threadID {
			return item, thread, nil
		}
	}
	return Project{}, Thread{}, ErrThreadNotFound
}

// PersistedResourceExists checks projects.json directly. An empty thread ID
// checks project scope; a non-empty thread ID checks that thread within the
// project. Read or decode failures are returned so callers can fail closed.
func (s *Store) PersistedResourceExists(projectID, threadID string) (bool, error) {
	item, err := s.GetPersisted(projectID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if threadID == "" {
		return true, nil
	}
	for _, thread := range item.Threads {
		if thread.ID == threadID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) readPersistedProjects() ([]Project, error) {
	// Coordinate with writes through this Store. Other Store instances publish
	// with atomic rename, so ReadFile observes either complete snapshot.
	s.mu.RLock()
	defer s.mu.RUnlock()
	projects, err := readProjectsFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("read persisted projects: %w", err)
	}
	return projects, nil
}

func readProjectsFile(path string) ([]Project, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Project{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	var projects []Project
	if err := json.Unmarshal(contents, &projects); err != nil {
		return nil, fmt.Errorf("decode projects: %w", err)
	}
	if projects == nil {
		return nil, errors.New("decode projects: expected a JSON array")
	}
	seenProjects := make(map[string]struct{}, len(projects))
	for _, item := range projects {
		if item.ID == "" {
			return nil, errors.New("decode projects: project ID is required")
		}
		if _, duplicate := seenProjects[item.ID]; duplicate {
			return nil, fmt.Errorf("decode projects: duplicate project ID %q", item.ID)
		}
		seenProjects[item.ID] = struct{}{}
		if item.SubAgentNestingDepthOverride != nil {
			if err := validateSubAgentNestingDepth(*item.SubAgentNestingDepthOverride); err != nil {
				return nil, fmt.Errorf("decode projects: sub-agent nesting depth for project %q %w", item.ID, err)
			}
		}
		seenThreads := make(map[string]struct{}, len(item.Threads))
		threadsByID := make(map[string]Thread, len(item.Threads))
		for _, thread := range item.Threads {
			if thread.ID == "" {
				return nil, fmt.Errorf("decode projects: thread ID is required in project %q", item.ID)
			}
			if thread.NestedDepth != nil {
				if err := validateSubAgentNestingDepth(*thread.NestedDepth); err != nil {
					return nil, fmt.Errorf("decode projects: nested depth for thread %q %w", thread.ID, err)
				}
			}
			if (thread.WorkflowRunID == "") != (thread.WorkflowAgentID == "") ||
				(thread.WorkflowRunID != "" && (!validWorkflowIdentity(thread.WorkflowRunID) || !validWorkflowIdentity(thread.WorkflowAgentID))) {
				return nil, fmt.Errorf("decode projects: thread %q has an invalid workflow identity", thread.ID)
			}
			if thread.RollbackCleanupReady && !thread.RollbackPending {
				return nil, fmt.Errorf("decode projects: thread %q has rollback cleanup ready without a pending rollback", thread.ID)
			}
			if thread.TokenLimit != nil && *thread.TokenLimit <= 0 {
				return nil, fmt.Errorf("decode projects: thread %q has an invalid token limit", thread.ID)
			}
			if thread.CostLimitUSD != nil && (math.IsNaN(*thread.CostLimitUSD) || math.IsInf(*thread.CostLimitUSD, 0) || *thread.CostLimitUSD <= 0 || *thread.CostLimitUSD > 1_000_000) {
				return nil, fmt.Errorf("decode projects: thread %q has an invalid cost limit", thread.ID)
			}
			if _, duplicate := seenThreads[thread.ID]; duplicate {
				return nil, fmt.Errorf("decode projects: duplicate thread ID %q in project %q", thread.ID, item.ID)
			}
			seenThreads[thread.ID] = struct{}{}
			threadsByID[thread.ID] = thread
		}
		for _, thread := range item.Threads {
			if thread.ParentThreadID == "" {
				if thread.WorkflowRunID != "" {
					return nil, fmt.Errorf("decode projects: workflow thread %q has no parent", thread.ID)
				}
				continue
			}
			if thread.ParentThreadID == thread.ID {
				return nil, fmt.Errorf("decode projects: thread %q cannot be its own parent in project %q", thread.ID, item.ID)
			}
			if _, found := threadsByID[thread.ParentThreadID]; !found {
				return nil, fmt.Errorf("decode projects: parent thread %q for thread %q does not exist in project %q", thread.ParentThreadID, thread.ID, item.ID)
			}
			visited := map[string]struct{}{thread.ID: {}}
			parentID := thread.ParentThreadID
			for parentID != "" {
				if _, cycle := visited[parentID]; cycle {
					return nil, fmt.Errorf("decode projects: child thread cycle includes %q in project %q", thread.ID, item.ID)
				}
				visited[parentID] = struct{}{}
				parentID = threadsByID[parentID].ParentThreadID
			}
		}
	}
	return cloneProjects(projects), nil
}

// RecordThreadPrompt persists the latest time a user prompted a thread. Older
// or duplicate reports are ignored so delayed activity heartbeats cannot move
// recency backwards or repeatedly publish the same project snapshot.
func (s *Store) RecordThreadPrompt(projectID, threadID string, promptedAt time.Time) (result Thread, err error) {
	promptedAt = promptedAt.UTC()
	if promptedAt.IsZero() {
		promptedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex := range s.projects[projectIndex].Threads {
			thread := &s.projects[projectIndex].Threads[threadIndex]
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if thread.LastPromptAt != nil && !promptedAt.After(*thread.LastPromptAt) {
				return cloneThread(*thread), nil
			}
			previous := thread.LastPromptAt
			thread.LastPromptAt = &promptedAt
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return cloneThread(*thread), err
				}
				thread.LastPromptAt = previous
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return cloneThread(*thread), nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) UpdateThreadTitle(projectID, threadID, title string, autoGenerated bool) (result Thread, err error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Thread{}, errors.New("thread title is required")
	}
	if utf8.RuneCountInString(title) > 120 {
		return Thread{}, errors.New("thread title must be 120 characters or fewer")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex := range s.projects[projectIndex].Threads {
			thread := &s.projects[projectIndex].Threads[threadIndex]
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if autoGenerated && thread.AutoNamed {
				return cloneThread(*thread), nil
			}
			previous := *thread
			branchRenamed := false
			if autoGenerated && thread.Worktree {
				nextBranch := namedWorktreeBranch(title, thread.ID)
				if nextBranch != thread.Branch {
					if err := renameWorktreeBranch(*thread, nextBranch); err != nil {
						return Thread{}, err
					}
					thread.Branch = nextBranch
					branchRenamed = true
				}
			}
			thread.Title = title
			thread.AutoNamed = autoGenerated
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return cloneThread(*thread), err
				}
				if branchRenamed {
					renamed := *thread
					*thread = previous
					if rollbackErr := renameWorktreeBranch(renamed, previous.Branch); rollbackErr != nil {
						return Thread{}, fmt.Errorf("%w; could not restore Git worktree branch: %v", err, rollbackErr)
					}
				} else {
					*thread = previous
				}
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return cloneThread(*thread), nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) CloseChildThread(projectID, parentThreadID, threadID string, closedAt time.Time) (result Thread, err error) {
	closedAt = closedAt.UTC()
	if closedAt.IsZero() {
		closedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex := range s.projects[projectIndex].Threads {
			thread := &s.projects[projectIndex].Threads[threadIndex]
			if thread.ID != threadID || thread.ParentThreadID != parentThreadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if thread.ClosedAt != nil {
				return cloneThread(*thread), nil
			}
			if hasOpenThreadDescendants(s.projects[projectIndex].Threads, threadID) {
				return Thread{}, ErrThreadHasOpenDescendants
			}

			previous := *thread
			thread.ClosedAt = &closedAt
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return cloneThread(*thread), err
				}
				*thread = previous
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return cloneThread(*thread), nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func hasOpenThreadDescendants(threads []Thread, threadID string) bool {
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

func (s *Store) ReopenChildThread(projectID, parentThreadID, threadID string) (result Thread, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		threads := s.projects[projectIndex].Threads
		threadIndexes := make(map[string]int, len(threads))
		for index, thread := range threads {
			threadIndexes[thread.ID] = index
		}
		threadIndex, found := threadIndexes[threadID]
		if !found || threads[threadIndex].ParentThreadID != parentThreadID {
			return Thread{}, ErrThreadNotFound
		}
		if threads[threadIndex].RollbackPending {
			return Thread{}, ErrThreadRollbackPending
		}

		previous := append([]Thread(nil), threads...)
		changed := false
		currentID := threadID
		visited := make(map[string]struct{}, len(threads))
		for currentID != "" {
			if _, duplicate := visited[currentID]; duplicate {
				return Thread{}, ErrThreadTreeChanged
			}
			visited[currentID] = struct{}{}
			index, found := threadIndexes[currentID]
			if !found {
				return Thread{}, ErrThreadTreeChanged
			}
			if threads[index].RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if threads[index].ClosedAt != nil {
				threads[index].ClosedAt = nil
				changed = true
			}
			currentID = threads[index].ParentThreadID
		}
		if !changed {
			return cloneThread(threads[threadIndex]), nil
		}

		s.projects[projectIndex].Threads = threads
		if err := s.saveLocked(); err != nil {
			if projectSaveWasPublished(err) {
				s.notifyChangesLocked()
				return cloneThread(threads[threadIndex]), err
			}
			s.projects[projectIndex].Threads = previous
			return Thread{}, err
		}
		s.notifyChangesLocked()
		return cloneThread(threads[threadIndex]), nil
	}
	return Thread{}, ErrNotFound
}

func (s *Store) SetThreadBookmarked(projectID, threadID string, bookmarked bool) (result Thread, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex := range s.projects[projectIndex].Threads {
			thread := &s.projects[projectIndex].Threads[threadIndex]
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if thread.Bookmarked == bookmarked {
				return cloneThread(*thread), nil
			}

			previous := thread.Bookmarked
			thread.Bookmarked = bookmarked
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return cloneThread(*thread), err
				}
				thread.Bookmarked = previous
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return cloneThread(*thread), nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) SetThreadArchived(projectID, threadID string, archived bool) (Thread, error) {
	return s.setThreadArchivedAt(projectID, threadID, archived, time.Now().UTC())
}

func (s *Store) setThreadArchivedAt(projectID, threadID string, archived bool, now time.Time) (result Thread, err error) {
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		threads := s.projects[projectIndex].Threads
		for threadIndex := range threads {
			thread := threads[threadIndex]
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if archived == (thread.ArchivedAt != nil) {
				return cloneThread(thread), nil
			}

			previous := append([]Thread(nil), threads...)
			threads = append(threads[:threadIndex], threads[threadIndex+1:]...)
			if archived {
				thread.ArchivedAt = &now
				threads = append(threads, thread)
			} else {
				thread.ArchivedAt = nil
				insertAt := len(threads)
				for index, candidate := range threads {
					if candidate.ArchivedAt != nil {
						insertAt = index
						break
					}
				}
				threads = append(threads, Thread{})
				copy(threads[insertAt+1:], threads[insertAt:])
				threads[insertAt] = thread
			}
			s.projects[projectIndex].Threads = threads
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return cloneThread(thread), err
				}
				s.projects[projectIndex].Threads = previous
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return cloneThread(thread), nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) DataDirectory() string {
	return filepath.Dir(s.defaultWorktreeBasePath)
}

func (s *Store) GetSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settingsLocked()
}

func (s *Store) UpdateSettings(worktreeBasePath string) (Settings, error) {
	return s.UpdateSettingsFields(SettingsUpdate{WorktreeBasePath: &worktreeBasePath})
}

func (s *Store) UpdateTheme(theme Theme) (Settings, error) {
	return s.UpdateSettingsFields(SettingsUpdate{Theme: &theme})
}

func (s *Store) UpdateSettingsValues(update SettingsUpdate) (Settings, error) {
	return s.UpdateSettingsFields(update)
}

func (s *Store) UpdateSettingsFields(update SettingsUpdate) (Settings, error) {
	if update.WorktreeBasePath == nil && update.ArchivedThreadRetentionDays == nil &&
		update.OrphanedWorktreeRetentionDays == nil && update.SubAgentNestingDepth == nil &&
		update.DisableWorkflows == nil && update.WorkflowKeywordTrigger == nil &&
		update.WorkflowSizeGuideline == nil && update.Theme == nil {
		return Settings{}, errors.New("at least one setting is required")
	}

	var normalizedPath *string
	if update.WorktreeBasePath != nil {
		value, err := normalizeWorktreeBasePath(*update.WorktreeBasePath)
		if err != nil {
			return Settings{}, err
		}
		if value != "" {
			if err := os.MkdirAll(value, 0o700); err != nil {
				return Settings{}, fmt.Errorf("create worktree base directory: %w", err)
			}
			info, err := os.Stat(value)
			if err != nil {
				return Settings{}, fmt.Errorf("open worktree base directory: %w", err)
			}
			if !info.IsDir() {
				return Settings{}, errors.New("worktree base path must be a directory")
			}
		}
		normalizedPath = &value
	}
	if update.ArchivedThreadRetentionDays != nil {
		if err := validateCleanupRetentionDays(*update.ArchivedThreadRetentionDays); err != nil {
			return Settings{}, fmt.Errorf("archived thread retention: %w", err)
		}
	}
	if update.OrphanedWorktreeRetentionDays != nil {
		if err := validateCleanupRetentionDays(*update.OrphanedWorktreeRetentionDays); err != nil {
			return Settings{}, fmt.Errorf("unattached worktree retention: %w", err)
		}
	}
	if update.SubAgentNestingDepth != nil {
		if err := validateSubAgentNestingDepth(*update.SubAgentNestingDepth); err != nil {
			return Settings{}, fmt.Errorf("sub-agent nesting depth %w", err)
		}
	}
	var normalizedWorkflowSize *string
	if update.WorkflowSizeGuideline != nil {
		value := strings.ToLower(strings.TrimSpace(*update.WorkflowSizeGuideline))
		if !validWorkflowSizeGuideline(value) {
			return Settings{}, errors.New("workflow size guideline must be unrestricted, small, medium, or large")
		}
		normalizedWorkflowSize = &value
	}

	var normalizedTheme *Theme
	if update.Theme != nil {
		value, err := normalizeTheme(*update.Theme)
		if err != nil {
			return Settings{}, err
		}
		normalizedTheme = &value
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previousPath := s.worktreeBasePath
	previousArchivedDays := s.archivedThreadRetentionDays
	previousOrphanedDays := s.orphanedWorktreeRetentionDays
	previousSubAgentNestingDepth := s.subAgentNestingDepth
	previousDisableWorkflows := s.disableWorkflows
	previousWorkflowKeywordTrigger := s.workflowKeywordTrigger
	previousWorkflowSizeGuideline := s.workflowSizeGuideline
	previousTheme := s.theme
	previousUsingDefaultTheme := s.usingDefaultTheme
	if normalizedPath != nil {
		if *normalizedPath == s.defaultWorktreeBasePath {
			s.worktreeBasePath = ""
		} else {
			s.worktreeBasePath = *normalizedPath
		}
	}
	if update.ArchivedThreadRetentionDays != nil {
		s.archivedThreadRetentionDays = *update.ArchivedThreadRetentionDays
	}
	if update.OrphanedWorktreeRetentionDays != nil {
		s.orphanedWorktreeRetentionDays = *update.OrphanedWorktreeRetentionDays
	}
	if update.SubAgentNestingDepth != nil {
		s.subAgentNestingDepth = *update.SubAgentNestingDepth
	}
	if update.DisableWorkflows != nil {
		s.disableWorkflows = *update.DisableWorkflows
	}
	if update.WorkflowKeywordTrigger != nil {
		s.workflowKeywordTrigger = *update.WorkflowKeywordTrigger
	}
	if normalizedWorkflowSize != nil {
		s.workflowSizeGuideline = *normalizedWorkflowSize
	}
	if normalizedTheme != nil {
		s.theme = *normalizedTheme
		s.usingDefaultTheme = s.theme == DefaultTheme()
	}
	if err := s.saveSettingsLocked(); err != nil {
		s.worktreeBasePath = previousPath
		s.archivedThreadRetentionDays = previousArchivedDays
		s.orphanedWorktreeRetentionDays = previousOrphanedDays
		s.subAgentNestingDepth = previousSubAgentNestingDepth
		s.disableWorkflows = previousDisableWorkflows
		s.workflowKeywordTrigger = previousWorkflowKeywordTrigger
		s.workflowSizeGuideline = previousWorkflowSizeGuideline
		s.theme = previousTheme
		s.usingDefaultTheme = previousUsingDefaultTheme
		return Settings{}, err
	}
	return s.settingsLocked(), nil
}

func validateCleanupRetentionDays(days int) error {
	if days < 0 || days > maxCleanupRetentionDays {
		return fmt.Errorf("must be between 0 and %d days", maxCleanupRetentionDays)
	}
	return nil
}

func validateSubAgentNestingDepth(depth int) error {
	if depth < 0 || depth > MaxSubAgentNestingDepth {
		return fmt.Errorf("must be between 0 and %d", MaxSubAgentNestingDepth)
	}
	return nil
}

func validWorkflowSizeGuideline(value string) bool {
	switch value {
	case "unrestricted", "small", "medium", "large":
		return true
	default:
		return false
	}
}

func (s *Store) settingsLocked() Settings {
	return Settings{
		WorktreeBasePath:              s.effectiveWorktreeBasePathLocked(),
		DefaultWorktreeBasePath:       s.defaultWorktreeBasePath,
		UsingDefault:                  s.worktreeBasePath == "",
		ArchivedThreadRetentionDays:   s.archivedThreadRetentionDays,
		OrphanedWorktreeRetentionDays: s.orphanedWorktreeRetentionDays,
		SubAgentNestingDepth:          s.subAgentNestingDepth,
		MaxSubAgentNestingDepth:       MaxSubAgentNestingDepth,
		DisableWorkflows:              s.disableWorkflows,
		WorkflowKeywordTrigger:        s.workflowKeywordTrigger,
		WorkflowSizeGuideline:         s.workflowSizeGuideline,
		Theme:                         s.theme,
		DefaultTheme:                  DefaultTheme(),
		UsingDefaultTheme:             s.usingDefaultTheme,
	}
}

func (s *Store) effectiveWorktreeBasePathLocked() string {
	if s.worktreeBasePath != "" {
		return s.worktreeBasePath
	}
	return s.defaultWorktreeBasePath
}

func normalizeWorktreeBasePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if value == "~" || strings.HasPrefix(value, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~"+string(filepath.Separator)))
		}
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("worktree base path must be absolute")
	}
	return filepath.Clean(value), nil
}

func normalizeTheme(theme Theme) (Theme, error) {
	theme.FontFamily = strings.TrimSpace(theme.FontFamily)
	if theme.FontFamily == "" {
		return Theme{}, errors.New("theme font family is required")
	}
	if utf8.RuneCountInString(theme.FontFamily) > 512 {
		return Theme{}, errors.New("theme font family must be 512 characters or fewer")
	}
	if theme.FontSize < 6 || theme.FontSize > 72 {
		return Theme{}, errors.New("theme font size must be between 6 and 72")
	}

	colors := []struct {
		name  string
		value *string
	}{
		{"canvas", &theme.Colors.Canvas},
		{"sidebar", &theme.Colors.Sidebar},
		{"background", &theme.Colors.Background},
		{"panel", &theme.Colors.Panel},
		{"raised", &theme.Colors.Raised},
		{"selected", &theme.Colors.Selected},
		{"border", &theme.Colors.Border},
		{"foreground", &theme.Colors.Foreground},
		{"muted", &theme.Colors.Muted},
		{"dim", &theme.Colors.Dim},
		{"cursor", &theme.Colors.Cursor},
		{"selection background", &theme.Colors.SelectionBackground},
		{"selection foreground", &theme.Colors.SelectionForeground},
		{"black", &theme.Colors.Black},
		{"red", &theme.Colors.Red},
		{"green", &theme.Colors.Green},
		{"yellow", &theme.Colors.Yellow},
		{"blue", &theme.Colors.Blue},
		{"magenta", &theme.Colors.Magenta},
		{"cyan", &theme.Colors.Cyan},
		{"white", &theme.Colors.White},
		{"bright black", &theme.Colors.BrightBlack},
		{"bright red", &theme.Colors.BrightRed},
		{"bright green", &theme.Colors.BrightGreen},
		{"bright yellow", &theme.Colors.BrightYellow},
		{"bright blue", &theme.Colors.BrightBlue},
		{"bright magenta", &theme.Colors.BrightMagenta},
		{"bright cyan", &theme.Colors.BrightCyan},
		{"bright white", &theme.Colors.BrightWhite},
	}
	for _, color := range colors {
		value := strings.ToLower(strings.TrimSpace(*color.value))
		if len(value) != 7 || value[0] != '#' {
			return Theme{}, fmt.Errorf("theme %s color must use #rrggbb format", color.name)
		}
		if _, err := hex.DecodeString(value[1:]); err != nil {
			return Theme{}, fmt.Errorf("theme %s color must use #rrggbb format", color.name)
		}
		*color.value = value
	}
	return theme, nil
}

func (s *Store) Add(name, path string, profileIDs ...string) (result Project, err error) {
	profileID := PersonalProfileID
	if len(profileIDs) > 0 && strings.TrimSpace(profileIDs[0]) != "" {
		profileID = strings.TrimSpace(profileIDs[0])
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return Project{}, errors.New("project path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return Project{}, fmt.Errorf("resolve project path: %w", err)
	}
	absPath = filepath.Clean(absPath)
	info, err := os.Stat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(absPath, 0o755); err != nil {
			return Project{}, fmt.Errorf("create project path: %w", err)
		}
		info, err = os.Stat(absPath)
	}
	if err != nil {
		return Project{}, fmt.Errorf("open project path: %w", err)
	}
	if !info.IsDir() {
		return Project{}, errors.New("project path must be a directory")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(absPath)
	}
	if len(name) > 80 {
		return Project{}, errors.New("project name must be 80 characters or fewer")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Project{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	if !s.profileExistsLocked(profileID) {
		return Project{}, ErrProfileNotFound
	}
	for _, item := range s.projects {
		if item.Path == absPath {
			return Project{}, errors.New("that project is already in the list")
		}
	}

	id, err := randomID()
	if err != nil {
		return Project{}, fmt.Errorf("create project id: %w", err)
	}
	now := time.Now().UTC()
	threadID, err := randomID()
	if err != nil {
		return Project{}, fmt.Errorf("create initial thread id: %w", err)
	}
	item := Project{
		ID: id, Name: name, Path: absPath, ProfileID: profileID, Host: localHostname(), IsGitRepo: isGitRepository(absPath), CreatedAt: now,
		Threads: []Thread{{ID: threadID, Title: defaultThreadTitle, Cwd: absPath, CreatedAt: now}},
	}
	previous := s.projects
	updated := make([]Project, len(previous)+1)
	copy(updated, previous)
	updated[len(previous)] = item

	// Projects from different profiles keep their existing slots, while the
	// new project moves ahead of every project in its own profile.
	newProjectIndex := len(updated) - 1
	for index := newProjectIndex - 1; index >= 0; index-- {
		if updated[index].ProfileID != profileID {
			continue
		}
		updated[index], updated[newProjectIndex] = updated[newProjectIndex], updated[index]
		newProjectIndex = index
	}
	s.projects = updated
	if err := s.saveLocked(); err != nil {
		if projectSaveWasPublished(err) {
			s.notifyChangesLocked()
			return item, err
		}
		s.projects = previous
		return Project{}, err
	}
	s.notifyChangesLocked()
	return item, nil
}

func (s *Store) AddProfile(name string) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Profile{}, errors.New("profile name is required")
	}
	if utf8.RuneCountInString(name) > 80 {
		return Profile{}, errors.New("profile name must be 80 characters or fewer")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, profile := range s.profiles {
		if strings.EqualFold(profile.Name, name) {
			return Profile{}, errors.New("a profile with that name already exists")
		}
	}
	id, err := randomID()
	if err != nil {
		return Profile{}, fmt.Errorf("create profile id: %w", err)
	}
	profile := Profile{ID: id, Name: name}
	s.profiles = append(s.profiles, profile)
	if err := s.saveProfilesLocked(); err != nil {
		s.profiles = s.profiles[:len(s.profiles)-1]
		return Profile{}, err
	}
	s.notifyProfileChangesLocked()
	return profile, nil
}

func (s *Store) UpdateProjectProfile(projectID, profileID string) (Project, error) {
	return s.UpdateProject(projectID, ProjectUpdate{ProfileID: &profileID})
}

func (s *Store) UpdateProject(projectID string, update ProjectUpdate) (result Project, err error) {
	if update.ProfileID == nil && !update.UpdateSubAgentNestingDepthOverride {
		return Project{}, errors.New("at least one project setting is required")
	}
	var profileID string
	if update.ProfileID != nil {
		profileID = strings.TrimSpace(*update.ProfileID)
		if profileID == "" {
			return Project{}, errors.New("profile is required")
		}
	}
	if update.UpdateSubAgentNestingDepthOverride && update.SubAgentNestingDepthOverride != nil {
		if err := validateSubAgentNestingDepth(*update.SubAgentNestingDepthOverride); err != nil {
			return Project{}, fmt.Errorf("sub-agent nesting depth %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Project{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	projectIndex := -1
	for index := range s.projects {
		if s.projects[index].ID == projectID {
			projectIndex = index
			break
		}
	}
	if projectIndex < 0 {
		return Project{}, ErrNotFound
	}
	if update.ProfileID != nil && !s.profileExistsLocked(profileID) {
		return Project{}, ErrProfileNotFound
	}

	previous := s.projects[projectIndex]
	changed := false
	if update.ProfileID != nil && s.projects[projectIndex].ProfileID != profileID {
		s.projects[projectIndex].ProfileID = profileID
		changed = true
	}
	if update.UpdateSubAgentNestingDepthOverride && !equalOptionalInt(
		s.projects[projectIndex].SubAgentNestingDepthOverride,
		update.SubAgentNestingDepthOverride,
	) {
		s.projects[projectIndex].SubAgentNestingDepthOverride = cloneOptionalInt(update.SubAgentNestingDepthOverride)
		changed = true
	}
	if !changed {
		return s.projects[projectIndex], nil
	}

	if err := s.saveLocked(); err != nil {
		if projectSaveWasPublished(err) {
			s.notifyChangesLocked()
			return s.projects[projectIndex], err
		}
		s.projects[projectIndex] = previous
		return Project{}, err
	}
	s.notifyChangesLocked()
	return s.projects[projectIndex], nil
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func equalOptionalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func (s *Store) profileExistsLocked(profileID string) bool {
	for _, profile := range s.profiles {
		if profile.ID == profileID {
			return true
		}
	}
	return false
}

// ReorderProjects replaces the complete order of projects in one profile while
// preserving the slots occupied by projects assigned to other profiles.
func (s *Store) ReorderProjects(profileID string, projectIDs []string) (err error) {
	profileID = strings.TrimSpace(profileID)

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()

	if !s.profileExistsLocked(profileID) {
		return ErrProfileNotFound
	}

	available := make(map[string]Project)
	for _, item := range s.projects {
		if item.ProfileID == profileID {
			available[item.ID] = item
		}
	}
	if len(projectIDs) != len(available) {
		return fmt.Errorf("%w: project order must include every project in the profile exactly once", ErrInvalidOrder)
	}

	ordered := make([]Project, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		item, exists := available[projectID]
		if !exists {
			return fmt.Errorf("%w: project order contains an unknown or duplicate project", ErrInvalidOrder)
		}
		ordered = append(ordered, item)
		delete(available, projectID)
	}

	previous := append([]Project(nil), s.projects...)
	nextIndex := 0
	for index := range s.projects {
		if s.projects[index].ProfileID == profileID {
			s.projects[index] = ordered[nextIndex]
			nextIndex++
		}
	}
	if err := s.saveLocked(); err != nil {
		if projectSaveWasPublished(err) {
			s.notifyChangesLocked()
			return err
		}
		s.projects = previous
		return err
	}
	s.notifyChangesLocked()
	return nil
}

// ReorderThreads replaces the complete thread order for one project.
func (s *Store) ReorderThreads(projectID string, threadIDs []string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()

	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}

		available := make(map[string]Thread, len(s.projects[projectIndex].Threads))
		for _, thread := range s.projects[projectIndex].Threads {
			if thread.RollbackPending {
				return ErrThreadRollbackPending
			}
			available[thread.ID] = thread
		}
		if len(threadIDs) != len(available) {
			return fmt.Errorf("%w: thread order must include every thread in the project exactly once", ErrInvalidOrder)
		}

		ordered := make([]Thread, 0, len(threadIDs))
		for _, threadID := range threadIDs {
			thread, exists := available[threadID]
			if !exists {
				return fmt.Errorf("%w: thread order contains an unknown or duplicate thread", ErrInvalidOrder)
			}
			ordered = append(ordered, thread)
			delete(available, threadID)
		}

		active := make([]Thread, 0, len(ordered))
		archived := make([]Thread, 0, len(ordered))
		for _, thread := range ordered {
			if thread.ArchivedAt == nil {
				active = append(active, thread)
			} else {
				archived = append(archived, thread)
			}
		}
		ordered = append(active, archived...)

		previous := s.projects[projectIndex].Threads
		s.projects[projectIndex].Threads = ordered
		if err := s.saveLocked(); err != nil {
			if projectSaveWasPublished(err) {
				s.notifyChangesLocked()
				return err
			}
			s.projects[projectIndex].Threads = previous
			return err
		}
		s.notifyChangesLocked()
		return nil
	}
	return ErrNotFound
}

func (s *Store) UpdateThreadLimits(projectID, threadID string, tokenLimit *int64, costLimitUSD *float64) (result Thread, err error) {
	if tokenLimit != nil && *tokenLimit <= 0 {
		return Thread{}, errors.New("token limit must be greater than zero")
	}
	if costLimitUSD != nil && (math.IsNaN(*costLimitUSD) || math.IsInf(*costLimitUSD, 0) || *costLimitUSD <= 0 || *costLimitUSD > 1_000_000) {
		return Thread{}, errors.New("cost limit must be greater than zero and no more than $1,000,000")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() { err = errors.Join(err, mutation.Release()) }()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex := range s.projects[projectIndex].Threads {
			thread := &s.projects[projectIndex].Threads[threadIndex]
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			previousTokenLimit, previousCostLimit := thread.TokenLimit, thread.CostLimitUSD
			thread.TokenLimit, thread.CostLimitUSD = tokenLimit, costLimitUSD
			result = cloneThread(*thread)
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return result, err
				}
				thread.TokenLimit, thread.CostLimitUSD = previousTokenLimit, previousCostLimit
				return Thread{}, err
			}
			s.notifyChangesLocked()
			return result, nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) AddThread(projectID, title string, worktree ...bool) (Thread, error) {
	return s.AddThreadWithOptions(projectID, title, AddThreadOptions{
		Worktree: len(worktree) > 0 && worktree[0],
	})
}

func (s *Store) AddThreadWithOptions(projectID, title string, options AddThreadOptions) (result Thread, err error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultThreadTitle
	}
	if utf8.RuneCountInString(title) > 120 {
		return Thread{}, errors.New("thread title must be 120 characters or fewer")
	}
	options.BaseBranch = strings.TrimSpace(options.BaseBranch)
	options.BaseRevision = strings.ToLower(strings.TrimSpace(options.BaseRevision))
	options.ParentThreadID = strings.TrimSpace(options.ParentThreadID)
	options.AgentModel = strings.TrimSpace(options.AgentModel)
	options.AgentThinkingLevel = strings.TrimSpace(options.AgentThinkingLevel)
	options.WorkflowRunID = strings.TrimSpace(options.WorkflowRunID)
	options.WorkflowAgentID = strings.TrimSpace(options.WorkflowAgentID)
	if (options.WorkflowRunID == "") != (options.WorkflowAgentID == "") ||
		(options.WorkflowRunID != "" && (!validWorkflowIdentity(options.WorkflowRunID) || !validWorkflowIdentity(options.WorkflowAgentID))) {
		return Thread{}, errors.New("workflow run and agent IDs must be valid and supplied together")
	}
	if options.WorkflowRunID != "" && options.ParentThreadID == "" {
		return Thread{}, errors.New("workflow identity requires a parent thread")
	}
	if !options.Worktree && (options.BaseBranch != "" || options.BaseRevision != "") {
		return Thread{}, errors.New("a base branch or revision can only be used with a Git worktree")
	}
	if options.BaseRevision != "" && !validFullGitObjectID(options.BaseRevision) {
		return Thread{}, errors.New("base revision must be a full Git object ID")
	}
	if options.NestedDepth != nil {
		if err := validateSubAgentNestingDepth(*options.NestedDepth); err != nil {
			return Thread{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for index := range s.projects {
		if s.projects[index].ID != projectID {
			continue
		}
		limit := s.subAgentNestingDepth
		if override := s.projects[index].SubAgentNestingDepthOverride; override != nil {
			limit = *override
		}
		if options.NestedDepth != nil && *options.NestedDepth > limit {
			return Thread{}, errors.New("nested depth cannot exceed the project's sub-agent nesting depth")
		}
		if options.ParentThreadID != "" {
			var parent *Thread
			for threadIndex := range s.projects[index].Threads {
				if s.projects[index].Threads[threadIndex].ID == options.ParentThreadID {
					parent = &s.projects[index].Threads[threadIndex]
					break
				}
			}
			if parent == nil {
				return Thread{}, ErrThreadNotFound
			}
			if parent.RollbackPending {
				return Thread{}, ErrThreadRollbackPending
			}
			if parent.ClosedAt != nil {
				return Thread{}, ErrThreadClosed
			}
			parentDepth, effectiveLimit, depthErr := effectiveThreadNestingLimit(s.projects[index].Threads, options.ParentThreadID, limit)
			if depthErr != nil {
				return Thread{}, depthErr
			}
			if parentDepth >= effectiveLimit {
				return Thread{}, ErrChildThreadDepthLimit
			}
			childDepth := parentDepth + 1
			if options.NestedDepth != nil && childDepth+*options.NestedDepth > effectiveLimit {
				return Thread{}, errors.New("nested depth exceeds the remaining sub-agent nesting depth for this thread tree")
			}
		}
		id, err := randomID()
		if err != nil {
			return Thread{}, fmt.Errorf("create thread id: %w", err)
		}
		var nestedDepth *int
		if options.NestedDepth != nil {
			depth := *options.NestedDepth
			nestedDepth = &depth
		}
		thread := Thread{
			ID:                 id,
			Title:              title,
			Cwd:                s.projects[index].Path,
			CreatedAt:          time.Now().UTC(),
			ParentThreadID:     options.ParentThreadID,
			AgentModel:         options.AgentModel,
			AgentThinkingLevel: options.AgentThinkingLevel,
			WorkflowRunID:      options.WorkflowRunID,
			WorkflowAgentID:    options.WorkflowAgentID,
			NestedDepth:        nestedDepth,
			RollbackPending:    options.CreationPending,
		}
		if options.Worktree {
			thread, err = s.createWorktreeThread(s.projects[index], thread, options.BaseBranch, options.BaseRevision)
			if err != nil {
				return Thread{}, err
			}
		}
		previousThreads := s.projects[index].Threads
		insertAt := 0
		if options.ParentThreadID != "" {
			insertAt = len(previousThreads)
			for threadIndex, candidate := range previousThreads {
				if candidate.ArchivedAt != nil {
					insertAt = threadIndex
					break
				}
			}
		}
		updatedThreads := make([]Thread, 0, len(previousThreads)+1)
		updatedThreads = append(updatedThreads, previousThreads[:insertAt]...)
		updatedThreads = append(updatedThreads, thread)
		updatedThreads = append(updatedThreads, previousThreads[insertAt:]...)
		s.projects[index].Threads = updatedThreads
		if saveErr := s.saveAddedThreadLocked(); saveErr != nil {
			if projectSaveWasPublished(saveErr) {
				if !thread.RollbackPending {
					s.notifyChangesLocked()
				}
				return cloneThread(thread), saveErr
			}
			s.projects[index].Threads = previousThreads
			if thread.Worktree {
				cleanupErr := s.removeRollbackWorktree(s.projects[index].Path, thread)
				if cleanupErr == nil {
					cleanupErr = s.clearWorktreeCreationIntentLocked(thread)
				}
				return Thread{}, errors.Join(saveErr, cleanupErr)
			}
			return Thread{}, saveErr
		}
		if thread.Worktree {
			_ = s.clearWorktreeCreationIntentLocked(thread)
		}
		if !thread.RollbackPending {
			s.notifyChangesLocked()
		}
		return cloneThread(thread), nil
	}
	return Thread{}, ErrNotFound
}

func (s *Store) saveAddedThreadLocked() error {
	if s.addThreadSave != nil {
		return s.addThreadSave()
	}
	return s.saveLocked()
}

func (s *Store) saveRollbackStateLocked(stage string) error {
	if s.rollbackSave != nil {
		if err := s.rollbackSave(stage); err != nil {
			return err
		}
	}
	return s.saveLocked()
}

func (s *Store) removeRollbackWorktree(projectPath string, thread Thread) error {
	if s.rollbackRemoveWorktree != nil {
		return s.rollbackRemoveWorktree(projectPath, thread)
	}
	return removeWorktreeForRollback(projectPath, thread)
}

// BeginThreadCreationRollback durably quarantines a transient thread before
// any process or worktree teardown. The returned bool reports whether the
// marker was published even when its final durability step returned an error.
func (s *Store) BeginThreadCreationRollback(projectID, threadID string) (marked bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		item := &s.projects[projectIndex]
		if item.ID != projectID {
			continue
		}
		for threadIndex, thread := range item.Threads {
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return true, nil
			}
			for _, candidate := range item.Threads {
				if candidate.ParentThreadID == threadID {
					return false, ErrThreadHasChildren
				}
			}
			item.Threads[threadIndex].RollbackPending = true
			item.Threads[threadIndex].RollbackCleanupReady = false
			if saveErr := s.saveRollbackStateLocked("mark"); saveErr != nil {
				if !projectSaveWasPublished(saveErr) {
					item.Threads[threadIndex] = thread
					return false, saveErr
				}
				s.notifyChangesLocked()
				return true, saveErr
			}
			s.notifyChangesLocked()
			return true, nil
		}
		return false, ErrThreadNotFound
	}
	return false, ErrNotFound
}

// FinalizeThreadCreationRollback is called only after Pi and usage teardown
// has succeeded. It first publishes that teardown stage, then removes Git
// artifacts and the transient thread. Startup recovery only finalizes markers
// that reached this stage.
func (s *Store) FinalizeThreadCreationRollback(projectID, threadID string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		item := &s.projects[projectIndex]
		if item.ID != projectID {
			continue
		}
		for threadIndex, thread := range item.Threads {
			if thread.ID != threadID {
				continue
			}
			if !thread.RollbackPending {
				return ErrThreadRollbackPending
			}
			for _, candidate := range item.Threads {
				if candidate.ParentThreadID == threadID {
					return ErrThreadHasChildren
				}
			}
			if !thread.RollbackCleanupReady {
				item.Threads[threadIndex].RollbackCleanupReady = true
				if saveErr := s.saveRollbackStateLocked("ready"); saveErr != nil {
					if !projectSaveWasPublished(saveErr) {
						item.Threads[threadIndex] = thread
					}
					s.notifyChangesLocked()
					return saveErr
				}
				thread = item.Threads[threadIndex]
				s.notifyChangesLocked()
			}
			if thread.Worktree {
				if cleanupErr := s.removeRollbackWorktree(item.Path, thread); cleanupErr != nil {
					return cleanupErr
				}
			}
			previous := append([]Thread(nil), item.Threads...)
			updated := make([]Thread, 0, len(previous)-1)
			updated = append(updated, previous[:threadIndex]...)
			updated = append(updated, previous[threadIndex+1:]...)
			item.Threads = updated
			if saveErr := s.saveRollbackStateLocked("finalize"); saveErr != nil {
				if !projectSaveWasPublished(saveErr) {
					item.Threads = previous
				}
				s.notifyChangesLocked()
				return saveErr
			}
			s.notifyChangesLocked()
			return nil
		}
		return ErrThreadNotFound
	}
	return ErrNotFound
}

// RollbackThreadCreation is the no-process convenience path. Server-managed
// child creation uses Begin and Finalize separately so worktree cleanup cannot
// race a failed Pi shutdown.
func (s *Store) RollbackThreadCreation(projectID, threadID string) error {
	marked, markErr := s.BeginThreadCreationRollback(projectID, threadID)
	if !marked {
		return markErr
	}
	return errors.Join(markErr, s.FinalizeThreadCreationRollback(projectID, threadID))
}

func (s *Store) CommitThreadCreation(projectID, threadID string) (result Thread, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return Thread{}, err
	}
	defer func() { err = errors.Join(err, mutation.Release()) }()
	for projectIndex := range s.projects {
		item := &s.projects[projectIndex]
		if item.ID != projectID {
			continue
		}
		for threadIndex, thread := range item.Threads {
			if thread.ID != threadID {
				continue
			}
			if !thread.RollbackPending {
				return cloneThread(thread), nil
			}
			if thread.RollbackCleanupReady {
				return Thread{}, ErrThreadRollbackPending
			}
			item.Threads[threadIndex].RollbackPending = false
			item.Threads[threadIndex].RollbackCleanupReady = false
			result = cloneThread(item.Threads[threadIndex])
			if saveErr := s.saveLocked(); saveErr != nil {
				if projectSaveWasPublished(saveErr) {
					s.notifyChangesLocked()
					return result, saveErr
				}
				item.Threads[threadIndex] = thread
				return Thread{}, saveErr
			}
			s.notifyChangesLocked()
			return result, nil
		}
		return Thread{}, ErrThreadNotFound
	}
	return Thread{}, ErrNotFound
}

func (s *Store) recoverThreadCreationRollbacksLocked() error {
	previous := cloneProjects(s.projects)
	updated := cloneProjects(s.projects)
	changed := false
	for projectIndex := range updated {
		item := &updated[projectIndex]
		kept := make([]Thread, 0, len(item.Threads))
		for _, thread := range item.Threads {
			if !thread.RollbackPending || !thread.RollbackCleanupReady {
				kept = append(kept, thread)
				continue
			}
			for _, candidate := range item.Threads {
				if candidate.ParentThreadID == thread.ID {
					return ErrThreadHasChildren
				}
			}
			if thread.Worktree {
				if err := s.removeRollbackWorktree(item.Path, thread); err != nil {
					return err
				}
			}
			changed = true
		}
		item.Threads = kept
	}
	if !changed {
		return nil
	}
	s.projects = updated
	if err := s.saveRollbackStateLocked("recover"); err != nil {
		if !projectSaveWasPublished(err) {
			s.projects = previous
		}
		return err
	}
	return nil
}

func (s *Store) SubAgentNestingContext(projectID, threadID string) (SubAgentNestingContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.projects {
		if item.ID != projectID {
			continue
		}
		limit := s.subAgentNestingDepth
		if item.SubAgentNestingDepthOverride != nil {
			limit = *item.SubAgentNestingDepthOverride
		}
		depth, effectiveLimit, err := effectiveThreadNestingLimit(item.Threads, threadID, limit)
		if err != nil {
			return SubAgentNestingContext{}, err
		}
		return SubAgentNestingContext{CurrentDepth: depth, MaxDepth: effectiveLimit}, nil
	}
	return SubAgentNestingContext{}, ErrNotFound
}

func threadNestingDepth(threads []Thread, threadID string) (int, error) {
	depth, _, err := effectiveThreadNestingLimit(threads, threadID, MaxSubAgentNestingDepth)
	return depth, err
}

func effectiveThreadNestingLimit(threads []Thread, threadID string, projectLimit int) (int, int, error) {
	byID := make(map[string]Thread, len(threads))
	for _, thread := range threads {
		byID[thread.ID] = thread
	}
	thread, found := byID[threadID]
	if !found {
		return 0, 0, ErrThreadNotFound
	}
	chain := make([]Thread, 0, projectLimit+1)
	visited := make(map[string]struct{}, len(threads))
	for {
		if _, duplicate := visited[thread.ID]; duplicate {
			return 0, 0, ErrThreadTreeChanged
		}
		visited[thread.ID] = struct{}{}
		chain = append(chain, thread)
		if thread.ParentThreadID == "" {
			break
		}
		parent, found := byID[thread.ParentThreadID]
		if !found {
			return 0, 0, ErrThreadTreeChanged
		}
		thread = parent
	}

	depth := len(chain) - 1
	effectiveLimit := projectLimit
	for distanceFromThread, ancestor := range chain {
		if ancestor.NestedDepth == nil {
			continue
		}
		ancestorDepth := depth - distanceFromThread
		if relativeLimit := ancestorDepth + *ancestor.NestedDepth; relativeLimit < effectiveLimit {
			effectiveLimit = relativeLimit
		}
	}
	return depth, effectiveLimit, nil
}

func (s *Store) createWorktreeThread(item Project, thread Thread, baseBranch, baseRevision string) (Thread, error) {
	prefixOutput, err := gitOutput(item.Path, "rev-parse", "--show-prefix")
	if err != nil || !isGitRepository(item.Path) {
		return Thread{}, errors.New("Git worktrees are only available for Git repositories")
	}
	prefix := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(prefixOutput))))
	if prefix == "" {
		prefix = "."
	}
	if filepath.IsAbs(prefix) || prefix == ".." || strings.HasPrefix(prefix, ".."+string(filepath.Separator)) {
		return Thread{}, errors.New("could not resolve the project path inside its Git repository")
	}

	startPoint := "HEAD"
	if baseRevision != "" {
		resolved, err := gitOutput(item.Path, "rev-parse", "--verify", baseRevision+"^{commit}")
		if err != nil {
			return Thread{}, errors.New("the selected base revision does not exist or is not a commit")
		}
		resolvedRevision := strings.ToLower(strings.TrimSpace(string(resolved)))
		if resolvedRevision != baseRevision {
			return Thread{}, errors.New("the selected base revision must identify a commit directly")
		}
		startPoint = resolvedRevision
	} else if baseBranch != "" {
		exists, err := localGitBranchExists(item.Path, baseBranch)
		if err != nil {
			return Thread{}, fmt.Errorf("check worktree base branch: %w", err)
		}
		if !exists {
			return Thread{}, errors.New("the selected base branch does not exist")
		}
		startPoint = "refs/heads/" + baseBranch
	}

	worktreePath := filepath.Join(s.effectiveWorktreeBasePathLocked(), item.ID, thread.ID)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o700); err != nil {
		return Thread{}, fmt.Errorf("create worktree directory: %w", err)
	}
	thread.Cwd = filepath.Join(worktreePath, prefix)
	thread.Worktree = true
	thread.Branch = initialWorktreeBranch(thread)
	thread.WorktreePath = worktreePath
	if err := s.recordWorktreeCreationIntentLocked(item, thread); err != nil {
		return Thread{}, fmt.Errorf("record Git worktree creation: %w", err)
	}
	cleanupFailedCreation := func(creationErr error) (Thread, error) {
		cleanupErr := s.removeRollbackWorktree(item.Path, thread)
		if cleanupErr == nil {
			cleanupErr = s.clearWorktreeCreationIntentLocked(thread)
		}
		return Thread{}, errors.Join(creationErr, cleanupErr)
	}

	if _, err := gitOutput(item.Path, "worktree", "add", "-b", thread.Branch, worktreePath, startPoint); err != nil {
		return cleanupFailedCreation(fmt.Errorf("create Git worktree: %w", err))
	}
	if s.worktreeSetup != nil {
		if err := s.worktreeSetup(thread); err != nil {
			return cleanupFailedCreation(err)
		}
	}
	if err := os.MkdirAll(thread.Cwd, 0o700); err != nil {
		return cleanupFailedCreation(fmt.Errorf("create worktree working directory: %w", err))
	}
	return thread, nil
}

func initialWorktreeBranch(thread Thread) string {
	if thread.Title == defaultThreadTitle {
		return "dire-mux/thread-" + shortThreadID(thread.ID)
	}
	return namedWorktreeBranch(thread.Title, thread.ID)
}

func namedWorktreeBranch(title, threadID string) string {
	return "dire-mux/" + branchSlug(title) + "-" + shortThreadID(threadID)
}

func shortThreadID(threadID string) string {
	if len(threadID) > 8 {
		return threadID[:8]
	}
	return threadID
}

func renameWorktreeBranch(thread Thread, nextBranch string) error {
	if thread.Branch == "" || nextBranch == "" {
		return errors.New("worktree branch name is missing")
	}
	if thread.Branch == nextBranch {
		return nil
	}

	currentExists, err := localGitBranchExists(thread.Cwd, thread.Branch)
	if err != nil {
		return fmt.Errorf("check current Git worktree branch: %w", err)
	}
	nextExists, err := localGitBranchExists(thread.Cwd, nextBranch)
	if err != nil {
		return fmt.Errorf("check new Git worktree branch: %w", err)
	}
	if !currentExists {
		if nextExists {
			// The Git rename completed but its metadata update did not. Treat the
			// requested name as authoritative so a later update can reconcile it.
			return nil
		}
		return fmt.Errorf("rename Git worktree branch: branch %q does not exist", thread.Branch)
	}
	if nextExists {
		return fmt.Errorf("rename Git worktree branch: branch %q already exists", nextBranch)
	}
	if _, err := gitOutput(thread.Cwd, "branch", "-m", thread.Branch, nextBranch); err != nil {
		return fmt.Errorf("rename Git worktree branch: %w", err)
	}
	return nil
}

func localGitBranchExists(cwd, branch string) (bool, error) {
	_, err := gitOutput(cwd, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func removeWorktreeForRollback(projectPath string, thread Thread) error {
	worktreePath := strings.TrimSpace(thread.WorktreePath)
	if worktreePath != "" {
		_, statErr := os.Stat(worktreePath)
		switch {
		case statErr == nil:
			if _, err := gitOutput(projectPath, "worktree", "remove", "--force", worktreePath); err != nil {
				if _, remainingErr := os.Stat(worktreePath); !errors.Is(remainingErr, os.ErrNotExist) {
					return fmt.Errorf("remove Git worktree: %w", err)
				}
			}
		case errors.Is(statErr, os.ErrNotExist):
			// A prior rollback attempt may already have removed the path.
		default:
			return fmt.Errorf("inspect Git worktree path: %w", statErr)
		}
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("remove Git worktree path: %w", err)
		}
		if _, err := gitOutput(projectPath, "worktree", "prune"); err != nil {
			return fmt.Errorf("prune Git worktrees: %w", err)
		}
	}
	branch := strings.TrimSpace(thread.Branch)
	if branch == "" {
		return nil
	}
	exists, err := localGitBranchExists(projectPath, branch)
	if err != nil {
		return fmt.Errorf("check Git worktree branch: %w", err)
	}
	if !exists {
		return nil
	}
	if _, err := gitOutput(projectPath, "branch", "-D", branch); err != nil {
		return fmt.Errorf("delete Git worktree branch: %w", err)
	}
	return nil
}

func removeWorktree(projectPath string, thread Thread) error {
	var cleanupErrors []error
	if strings.TrimSpace(thread.WorktreePath) != "" {
		if _, err := gitOutput(projectPath, "worktree", "remove", "--force", thread.WorktreePath); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove Git worktree: %w", err))
		}
		if err := os.RemoveAll(thread.WorktreePath); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove Git worktree path: %w", err))
		}
		if _, err := gitOutput(projectPath, "worktree", "prune"); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("prune Git worktrees: %w", err))
		}
	}
	if strings.TrimSpace(thread.Branch) != "" {
		exists, err := localGitBranchExists(projectPath, thread.Branch)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("check Git worktree branch: %w", err))
		} else if exists {
			if _, err := gitOutput(projectPath, "branch", "-D", thread.Branch); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Git worktree branch: %w", err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func isGitRepository(path string) bool {
	output, err := gitOutput(path, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func validFullGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func gitOutput(path string, arguments ...string) ([]byte, error) {
	commandArguments := append([]string{"-C", path}, arguments...)
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		detail := strings.TrimSpace(string(exitError.Stderr))
		if detail != "" {
			return nil, errors.New(detail)
		}
	}
	return nil, err
}

func branchSlug(value string) string {
	var result strings.Builder
	separator := false
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(character)
			separator = false
			if result.Len() >= 40 {
				break
			}
			continue
		}
		separator = true
	}
	slug := strings.Trim(result.String(), "-")
	if slug == "" {
		return "thread"
	}
	return slug
}

func localHostname() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "local"
	}
	return strings.TrimSuffix(strings.TrimSpace(host), ".local")
}

func (s *Store) DeleteThread(projectID, threadID string) error {
	return s.deleteThread(projectID, threadID, nil)
}

func (s *Store) DeleteArchivedThread(projectID, threadID string, archivedBefore time.Time) error {
	archivedBefore = archivedBefore.UTC()
	return s.deleteThread(projectID, threadID, func(thread Thread) bool {
		return thread.ArchivedAt != nil && !thread.ArchivedAt.After(archivedBefore)
	})
}

func (s *Store) DeleteThreadTree(projectID, threadID string, expectedThreadIDs []string) error {
	return s.deleteThreadTree(projectID, threadID, expectedThreadIDs, nil)
}

func (s *Store) DeleteArchivedThreadTree(projectID, threadID string, expectedThreadIDs []string, archivedBefore time.Time) error {
	archivedBefore = archivedBefore.UTC()
	return s.deleteThreadTree(projectID, threadID, expectedThreadIDs, func(thread Thread) bool {
		return thread.ArchivedAt != nil && !thread.ArchivedAt.After(archivedBefore)
	})
}

func (s *Store) deleteThreadTree(projectID, threadID string, expectedThreadIDs []string, canDelete func(Thread) bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		item := &s.projects[projectIndex]
		if item.ID != projectID {
			continue
		}
		var root Thread
		rootFound := false
		for _, thread := range item.Threads {
			if thread.ID == threadID {
				root = thread
				rootFound = true
				break
			}
		}
		if !rootFound {
			return ErrThreadNotFound
		}
		if canDelete != nil && !canDelete(root) {
			return ErrThreadNotArchived
		}

		treeIDs := map[string]struct{}{threadID: {}}
		for changed := true; changed; {
			changed = false
			for _, candidate := range item.Threads {
				if _, included := treeIDs[candidate.ID]; included {
					continue
				}
				if _, parentIncluded := treeIDs[candidate.ParentThreadID]; !parentIncluded {
					continue
				}
				treeIDs[candidate.ID] = struct{}{}
				changed = true
			}
		}
		expected := make(map[string]struct{}, len(expectedThreadIDs))
		for _, expectedID := range expectedThreadIDs {
			if expectedID == "" {
				return ErrThreadTreeChanged
			}
			expected[expectedID] = struct{}{}
		}
		if len(expected) != len(expectedThreadIDs) || len(expected) != len(treeIDs) {
			return ErrThreadTreeChanged
		}
		for treeID := range treeIDs {
			if _, found := expected[treeID]; !found {
				return ErrThreadTreeChanged
			}
		}
		for _, thread := range item.Threads {
			if _, deleting := treeIDs[thread.ID]; deleting && thread.RollbackPending {
				return ErrThreadRollbackPending
			}
		}

		now := time.Now().UTC()
		orphanedChanged := false
		for _, thread := range item.Threads {
			if _, deleting := treeIDs[thread.ID]; !deleting || !thread.Worktree {
				continue
			}
			s.rememberOrphanedWorktreeLocked(*item, thread, now)
			orphanedChanged = true
		}
		if orphanedChanged {
			if err := s.saveOrphanedWorktreesLocked(); err != nil {
				return fmt.Errorf("record unattached worktrees: %w", err)
			}
		}

		previous := append([]Thread(nil), item.Threads...)
		updated := make([]Thread, 0, len(previous)-len(treeIDs))
		for _, thread := range previous {
			if _, deleting := treeIDs[thread.ID]; !deleting {
				updated = append(updated, thread)
			}
		}
		item.Threads = updated
		if err := s.saveLocked(); err != nil {
			if projectSaveWasPublished(err) {
				s.notifyChangesLocked()
				return err
			}
			item.Threads = previous
			return err
		}
		s.notifyChangesLocked()
		return nil
	}
	return ErrNotFound
}

func (s *Store) deleteThread(projectID, threadID string, canDelete func(Thread) bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()
	for projectIndex := range s.projects {
		if s.projects[projectIndex].ID != projectID {
			continue
		}
		for threadIndex, thread := range s.projects[projectIndex].Threads {
			if thread.ID != threadID {
				continue
			}
			if thread.RollbackPending {
				return ErrThreadRollbackPending
			}
			for _, candidate := range s.projects[projectIndex].Threads {
				if candidate.ParentThreadID == threadID {
					return ErrThreadHasChildren
				}
			}
			if canDelete != nil && !canDelete(thread) {
				return ErrThreadNotArchived
			}
			if thread.Worktree {
				s.rememberOrphanedWorktreeLocked(s.projects[projectIndex], thread, time.Now().UTC())
				if err := s.saveOrphanedWorktreesLocked(); err != nil {
					return fmt.Errorf("record unattached worktree: %w", err)
				}
			}
			previous := append([]Thread(nil), s.projects[projectIndex].Threads...)
			updated := make([]Thread, 0, len(previous)-1)
			updated = append(updated, previous[:threadIndex]...)
			updated = append(updated, previous[threadIndex+1:]...)
			s.projects[projectIndex].Threads = updated
			if err := s.saveLocked(); err != nil {
				if projectSaveWasPublished(err) {
					s.notifyChangesLocked()
					return err
				}
				s.projects[projectIndex].Threads = previous
				return err
			}
			s.notifyChangesLocked()
			return nil
		}
		return ErrThreadNotFound
	}
	return ErrNotFound
}

func (s *Store) Delete(id string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()

	for index, item := range s.projects {
		if item.ID != id {
			continue
		}
		for _, thread := range item.Threads {
			if thread.RollbackPending {
				return ErrThreadRollbackPending
			}
		}
		now := time.Now().UTC()
		orphanedChanged := false
		for _, thread := range item.Threads {
			if !thread.Worktree {
				continue
			}
			s.rememberOrphanedWorktreeLocked(item, thread, now)
			orphanedChanged = true
		}
		if orphanedChanged {
			if err := s.saveOrphanedWorktreesLocked(); err != nil {
				return fmt.Errorf("record unattached worktrees: %w", err)
			}
		}
		previous := append([]Project(nil), s.projects...)
		s.projects = append(s.projects[:index], s.projects[index+1:]...)
		if err := s.saveLocked(); err != nil {
			if projectSaveWasPublished(err) {
				s.notifyChangesLocked()
				return err
			}
			s.projects = previous
			return err
		}
		s.notifyChangesLocked()
		return nil
	}
	return ErrNotFound
}

func (s *Store) load() error {
	projects, err := readProjectsFile(s.filePath)
	if err != nil {
		return err
	}
	changed := false
	for index := range projects {
		if !s.profileExistsLocked(projects[index].ProfileID) {
			projects[index].ProfileID = PersonalProfileID
			changed = true
		}
		if projects[index].Host == "" {
			projects[index].Host = localHostname()
			changed = true
		}
		if projects[index].Threads == nil {
			projects[index].Threads = []Thread{}
			changed = true
		}
		if len(projects[index].Threads) == 0 {
			id, err := randomID()
			if err != nil {
				return fmt.Errorf("migrate initial thread id: %w", err)
			}
			projects[index].Threads = append(projects[index].Threads, Thread{
				ID: id, Title: defaultThreadTitle, Cwd: projects[index].Path, CreatedAt: projects[index].CreatedAt,
			})
			changed = true
		}
	}
	s.projects = projects
	if changed {
		return s.saveLocked()
	}
	return nil
}

func (s *Store) loadProfiles() error {
	contents, err := os.ReadFile(s.profilesFilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read profiles: %w", err)
	}
	var stored []Profile
	if err := json.Unmarshal(contents, &stored); err != nil {
		return fmt.Errorf("decode profiles: %w", err)
	}

	profiles := defaultProfiles()
	seenIDs := map[string]bool{PersonalProfileID: true, WorkProfileID: true}
	seenNames := map[string]bool{"personal": true, "work": true}
	for _, profile := range stored {
		profile.ID = strings.TrimSpace(profile.ID)
		profile.Name = strings.TrimSpace(profile.Name)
		if profile.ID == PersonalProfileID || profile.ID == WorkProfileID {
			continue
		}
		if profile.ID == "" || profile.Name == "" {
			return errors.New("decode profiles: profile id and name are required")
		}
		if utf8.RuneCountInString(profile.Name) > 80 {
			return errors.New("decode profiles: profile name must be 80 characters or fewer")
		}
		foldedName := strings.ToLower(profile.Name)
		if seenIDs[profile.ID] || seenNames[foldedName] {
			return errors.New("decode profiles: profile ids and names must be unique")
		}
		seenIDs[profile.ID] = true
		seenNames[foldedName] = true
		profiles = append(profiles, profile)
	}
	s.profiles = profiles
	return nil
}

func (s *Store) loadSettings() error {
	contents, err := os.ReadFile(s.settingsFilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	var settings persistedSettings
	if err := json.Unmarshal(contents, &settings); err != nil {
		return fmt.Errorf("decode settings: %w", err)
	}
	normalizedPath, err := normalizeWorktreeBasePath(settings.WorktreeBasePath)
	if err != nil {
		return fmt.Errorf("decode worktree base path: %w", err)
	}
	if normalizedPath != s.defaultWorktreeBasePath {
		s.worktreeBasePath = normalizedPath
	}
	if settings.ArchivedThreadRetentionDays != nil {
		if err := validateCleanupRetentionDays(*settings.ArchivedThreadRetentionDays); err != nil {
			return fmt.Errorf("decode archived thread retention: %w", err)
		}
		s.archivedThreadRetentionDays = *settings.ArchivedThreadRetentionDays
	}
	if settings.OrphanedWorktreeRetentionDays != nil {
		if err := validateCleanupRetentionDays(*settings.OrphanedWorktreeRetentionDays); err != nil {
			return fmt.Errorf("decode unattached worktree retention: %w", err)
		}
		s.orphanedWorktreeRetentionDays = *settings.OrphanedWorktreeRetentionDays
	}
	if settings.SubAgentNestingDepth != nil {
		if err := validateSubAgentNestingDepth(*settings.SubAgentNestingDepth); err != nil {
			return fmt.Errorf("decode sub-agent nesting depth: %w", err)
		}
		s.subAgentNestingDepth = *settings.SubAgentNestingDepth
	}
	if settings.DisableWorkflows != nil {
		s.disableWorkflows = *settings.DisableWorkflows
	}
	if settings.WorkflowKeywordTrigger != nil {
		s.workflowKeywordTrigger = *settings.WorkflowKeywordTrigger
	}
	if settings.WorkflowSizeGuideline != nil {
		value := strings.ToLower(strings.TrimSpace(*settings.WorkflowSizeGuideline))
		if !validWorkflowSizeGuideline(value) {
			return errors.New("decode workflow size guideline: must be unrestricted, small, medium, or large")
		}
		s.workflowSizeGuideline = value
	}
	if settings.Theme != nil {
		normalizedTheme, err := normalizeTheme(*settings.Theme)
		if err != nil {
			return fmt.Errorf("decode theme: %w", err)
		}
		if normalizedTheme != DefaultTheme() {
			s.theme = normalizedTheme
			s.usingDefaultTheme = false
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	directory := filepath.Dir(s.filePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	contents, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return fmt.Errorf("encode projects: %w", err)
	}
	published, err := writeAtomicFile(s.filePath, contents, atomicFileOptions{
		Mode:          0o600,
		SyncFile:      true,
		SyncDirectory: true,
	})
	if err != nil {
		wrapped := fmt.Errorf("save projects: %w", err)
		if published {
			return &projectSavePublishedError{err: wrapped}
		}
		return wrapped
	}
	return nil
}

func (s *Store) lockAndReloadProjectMutationsLocked() (*projectMutationLease, error) {
	mutation, err := s.lockProjectMutationsLocked()
	if err != nil {
		return nil, err
	}
	projects, err := readProjectsFile(s.filePath)
	if err != nil {
		return nil, errors.Join(err, mutation.Release())
	}
	orphanedWorktrees, err := readOrphanedWorktreesFile(s.orphanedWorktreesFilePath)
	if err != nil {
		return nil, errors.Join(err, mutation.Release())
	}
	s.projects = projects
	s.orphanedWorktrees = orphanedWorktrees
	return mutation, nil
}

func (s *Store) lockProjectMutationsLocked() (*projectMutationLease, error) {
	lockPath := s.filePath + ".lock"
	local := localProjectMutationLock(lockPath)
	local.Lock()

	directory := filepath.Dir(lockPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		local.Unlock()
		return nil, fmt.Errorf("create project mutation lock directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		local.Unlock()
		return nil, fmt.Errorf("set project mutation lock directory permissions: %w", err)
	}
	if pathInfo, err := os.Lstat(lockPath); err == nil {
		if !pathInfo.Mode().IsRegular() {
			local.Unlock()
			return nil, errors.New("project mutation lock must be a stable regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		local.Unlock()
		return nil, fmt.Errorf("inspect project mutation lock path: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		local.Unlock()
		return nil, fmt.Errorf("open project mutation lock: %w", err)
	}
	closeWithLocal := func(lockErr error) (*projectMutationLease, error) {
		closeErr := file.Close()
		local.Unlock()
		return nil, errors.Join(lockErr, closeErr)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return closeWithLocal(fmt.Errorf("inspect project mutation lock: %w", err))
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return closeWithLocal(fmt.Errorf("inspect project mutation lock path: %w", err))
	}
	if !fileInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(fileInfo, pathInfo) {
		return closeWithLocal(errors.New("project mutation lock must be a stable regular file"))
	}
	if err := file.Chmod(0o600); err != nil {
		return closeWithLocal(fmt.Errorf("set project mutation lock permissions: %w", err))
	}
	if err := flockProjectMutationFile(file, syscall.LOCK_EX); err != nil {
		return closeWithLocal(fmt.Errorf("lock project mutations: %w", err))
	}
	return &projectMutationLease{file: file, local: local}, nil
}

func localProjectMutationLock(path string) *sync.Mutex {
	projectMutationLocalLocks.Lock()
	defer projectMutationLocalLocks.Unlock()
	lock := projectMutationLocalLocks.locks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		projectMutationLocalLocks.locks[path] = lock
	}
	return lock
}

func flockProjectMutationFile(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func (l *projectMutationLease) Release() error {
	unlockErr := flockProjectMutationFile(l.file, syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.local.Unlock()
	l.file = nil
	l.local = nil
	return errors.Join(unlockErr, closeErr)
}

func (s *Store) saveProfilesLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.profilesFilePath), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	contents, err := json.MarshalIndent(s.profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	if _, err := writeAtomicFile(s.profilesFilePath, contents, atomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("save profiles: %w", err)
	}
	return nil
}

func (s *Store) saveSettingsLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.settingsFilePath), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	archivedDays := s.archivedThreadRetentionDays
	orphanedDays := s.orphanedWorktreeRetentionDays
	subAgentNestingDepth := s.subAgentNestingDepth
	disableWorkflows := s.disableWorkflows
	workflowKeywordTrigger := s.workflowKeywordTrigger
	workflowSizeGuideline := s.workflowSizeGuideline
	settings := persistedSettings{
		WorktreeBasePath:              s.worktreeBasePath,
		ArchivedThreadRetentionDays:   &archivedDays,
		OrphanedWorktreeRetentionDays: &orphanedDays,
		SubAgentNestingDepth:          &subAgentNestingDepth,
		DisableWorkflows:              &disableWorkflows,
		WorkflowKeywordTrigger:        &workflowKeywordTrigger,
		WorkflowSizeGuideline:         &workflowSizeGuideline,
	}
	if !s.usingDefaultTheme {
		theme := s.theme
		settings.Theme = &theme
	}
	contents, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if _, err := writeAtomicFile(s.settingsFilePath, contents, atomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

func randomID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
