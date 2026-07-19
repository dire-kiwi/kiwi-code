package project

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ArchivedThreadRef struct {
	ProjectID      string
	ThreadID       string
	ArchivedBefore time.Time
}

type WorktreeCleanupResult struct {
	Deleted             []string
	RetainedWithChanges []string
}

type CleanupOverview struct {
	GeneratedAt                   time.Time                 `json:"generatedAt"`
	ArchivedThreadRetentionDays   int                       `json:"archivedThreadRetentionDays"`
	OrphanedWorktreeRetentionDays int                       `json:"orphanedWorktreeRetentionDays"`
	Threads                       []ThreadCleanupOverview   `json:"threads"`
	Worktrees                     []WorktreeCleanupOverview `json:"worktrees"`
}

type ThreadCleanupOverview struct {
	ProjectID           string     `json:"projectId"`
	ProjectName         string     `json:"projectName"`
	ThreadID            string     `json:"threadId"`
	ThreadTitle         string     `json:"threadTitle"`
	ArchivedAt          time.Time  `json:"archivedAt"`
	ScheduledDeletionAt *time.Time `json:"scheduledDeletionAt"`
}

type WorktreeCleanupOverview struct {
	ProjectID             string     `json:"projectId"`
	ProjectName           string     `json:"projectName,omitempty"`
	ThreadID              string     `json:"threadId"`
	ThreadTitle           string     `json:"threadTitle,omitempty"`
	WorktreePath          string     `json:"worktreePath"`
	Branch                string     `json:"branch,omitempty"`
	DetachedAt            time.Time  `json:"detachedAt"`
	ScheduledDeletionAt   *time.Time `json:"scheduledDeletionAt"`
	HasUncommittedChanges bool       `json:"hasUncommittedChanges"`
	InspectionError       string     `json:"inspectionError,omitempty"`
}

type orphanedWorktree struct {
	ProjectID    string    `json:"projectId"`
	ProjectName  string    `json:"projectName,omitempty"`
	ThreadID     string    `json:"threadId"`
	ThreadTitle  string    `json:"threadTitle,omitempty"`
	ProjectPath  string    `json:"projectPath"`
	WorktreePath string    `json:"worktreePath"`
	Branch       string    `json:"branch,omitempty"`
	DetachedAt   time.Time `json:"detachedAt"`
	DeleteBranch bool      `json:"deleteBranch,omitempty"`
}

func (s *Store) ArchivedThreadsDue(now time.Time) ([]ArchivedThreadRef, error) {
	s.mu.RLock()
	retentionDays := s.archivedThreadRetentionDays
	s.mu.RUnlock()
	if retentionDays == 0 {
		return nil, nil
	}

	projects, err := s.ListPersisted()
	if err != nil {
		return nil, err
	}
	cutoff := now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var due []ArchivedThreadRef
	for _, item := range projects {
		for _, thread := range item.Threads {
			if thread.ArchivedAt == nil || thread.ArchivedAt.After(cutoff) {
				continue
			}
			due = append(due, ArchivedThreadRef{
				ProjectID:      item.ID,
				ThreadID:       thread.ID,
				ArchivedBefore: cutoff,
			})
		}
	}
	return due, nil
}

func (s *Store) CleanupOverview(now time.Time) (CleanupOverview, error) {
	now = now.UTC()

	s.mu.RLock()
	archivedRetentionDays := s.archivedThreadRetentionDays
	worktreeRetentionDays := s.orphanedWorktreeRetentionDays
	projects, projectsErr := readProjectsFile(s.filePath)
	var records []orphanedWorktree
	var recordsErr error
	if projectsErr == nil {
		records, recordsErr = readOrphanedWorktreesFile(s.orphanedWorktreesFilePath)
	}
	s.mu.RUnlock()
	if projectsErr != nil {
		return CleanupOverview{}, fmt.Errorf("read cleanup projects: %w", projectsErr)
	}
	if recordsErr != nil {
		return CleanupOverview{}, fmt.Errorf("read cleanup worktrees: %w", recordsErr)
	}

	overview := CleanupOverview{
		GeneratedAt:                   now,
		ArchivedThreadRetentionDays:   archivedRetentionDays,
		OrphanedWorktreeRetentionDays: worktreeRetentionDays,
		Threads:                       []ThreadCleanupOverview{},
		Worktrees:                     []WorktreeCleanupOverview{},
	}
	projectNames := make(map[string]string, len(projects))
	activePaths := make(map[string]struct{})
	activeThreads := make(map[string]struct{})
	for _, item := range projects {
		projectNames[item.ID] = item.Name
		for _, thread := range item.Threads {
			if thread.ArchivedAt != nil {
				archivedAt := thread.ArchivedAt.UTC()
				overview.Threads = append(overview.Threads, ThreadCleanupOverview{
					ProjectID:           item.ID,
					ProjectName:         item.Name,
					ThreadID:            thread.ID,
					ThreadTitle:         thread.Title,
					ArchivedAt:          archivedAt,
					ScheduledDeletionAt: cleanupScheduledDeletionAt(archivedAt, archivedRetentionDays),
				})
			}
			if !thread.Worktree {
				continue
			}
			activeThreads[worktreeThreadKey(item.ID, thread.ID)] = struct{}{}
			if thread.WorktreePath != "" {
				activePaths[worktreeIdentityPath(thread.WorktreePath)] = struct{}{}
			}
		}
	}

	for _, record := range records {
		if _, active := activeThreads[worktreeThreadKey(record.ProjectID, record.ThreadID)]; active {
			continue
		}
		if _, active := activePaths[worktreeIdentityPath(record.WorktreePath)]; active {
			continue
		}

		projectName := record.ProjectName
		if projectName == "" {
			projectName = projectNames[record.ProjectID]
		}
		entry := WorktreeCleanupOverview{
			ProjectID:           record.ProjectID,
			ProjectName:         projectName,
			ThreadID:            record.ThreadID,
			ThreadTitle:         record.ThreadTitle,
			WorktreePath:        filepath.Clean(record.WorktreePath),
			Branch:              record.Branch,
			DetachedAt:          record.DetachedAt.UTC(),
			ScheduledDeletionAt: cleanupScheduledDeletionAt(record.DetachedAt, worktreeRetentionDays),
		}
		info, statErr := os.Stat(entry.WorktreePath)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		switch {
		case statErr != nil:
			entry.InspectionError = fmt.Sprintf("Could not inspect the worktree path: %v", statErr)
		case !info.IsDir():
			entry.InspectionError = "The worktree path is not a directory."
		default:
			status, statusErr := gitOutput(entry.WorktreePath, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
			if statusErr != nil {
				entry.InspectionError = fmt.Sprintf("Could not inspect uncommitted changes: %v", statusErr)
			} else {
				entry.HasUncommittedChanges = strings.TrimSpace(string(status)) != ""
			}
		}
		overview.Worktrees = append(overview.Worktrees, entry)
	}

	sort.Slice(overview.Threads, func(left, right int) bool {
		leftTime := overview.Threads[left].ScheduledDeletionAt
		rightTime := overview.Threads[right].ScheduledDeletionAt
		if leftTime == nil || rightTime == nil {
			if leftTime == nil && rightTime != nil {
				return false
			}
			if leftTime != nil && rightTime == nil {
				return true
			}
		} else if !leftTime.Equal(*rightTime) {
			return leftTime.Before(*rightTime)
		}
		if overview.Threads[left].ProjectName != overview.Threads[right].ProjectName {
			return overview.Threads[left].ProjectName < overview.Threads[right].ProjectName
		}
		return overview.Threads[left].ThreadTitle < overview.Threads[right].ThreadTitle
	})
	sort.Slice(overview.Worktrees, func(left, right int) bool {
		leftTime := overview.Worktrees[left].ScheduledDeletionAt
		rightTime := overview.Worktrees[right].ScheduledDeletionAt
		if leftTime == nil || rightTime == nil {
			if leftTime == nil && rightTime != nil {
				return false
			}
			if leftTime != nil && rightTime == nil {
				return true
			}
		} else if !leftTime.Equal(*rightTime) {
			return leftTime.Before(*rightTime)
		}
		return overview.Worktrees[left].WorktreePath < overview.Worktrees[right].WorktreePath
	})
	return overview, nil
}

func cleanupScheduledDeletionAt(start time.Time, retentionDays int) *time.Time {
	if retentionDays == 0 {
		return nil
	}
	deletionAt := start.UTC().Add(time.Duration(retentionDays) * 24 * time.Hour)
	return &deletionAt
}

func (s *Store) rememberOrphanedWorktreeLocked(item Project, thread Thread, detachedAt time.Time) {
	if !thread.Worktree || strings.TrimSpace(thread.WorktreePath) == "" {
		return
	}
	worktreePath := filepath.Clean(thread.WorktreePath)
	worktreeIdentity := worktreeIdentityPath(worktreePath)
	record := orphanedWorktree{
		ProjectID:    item.ID,
		ProjectName:  item.Name,
		ThreadID:     thread.ID,
		ThreadTitle:  thread.Title,
		ProjectPath:  filepath.Clean(item.Path),
		WorktreePath: worktreePath,
		Branch:       thread.Branch,
		DetachedAt:   detachedAt.UTC(),
	}
	for index := range s.orphanedWorktrees {
		if worktreeIdentityPath(s.orphanedWorktrees[index].WorktreePath) == worktreeIdentity {
			s.orphanedWorktrees[index] = record
			return
		}
	}
	s.orphanedWorktrees = append(s.orphanedWorktrees, record)
}

func (s *Store) recordWorktreeCreationIntentLocked(item Project, thread Thread) error {
	previous := append([]orphanedWorktree(nil), s.orphanedWorktrees...)
	s.rememberOrphanedWorktreeLocked(item, thread, time.Now().UTC())
	identity := worktreeIdentityPath(thread.WorktreePath)
	for index := range s.orphanedWorktrees {
		if worktreeIdentityPath(s.orphanedWorktrees[index].WorktreePath) == identity {
			s.orphanedWorktrees[index].DeleteBranch = true
			break
		}
	}
	if err := s.saveOrphanedWorktreesLocked(); err != nil {
		s.orphanedWorktrees = previous
		return err
	}
	return nil
}

func (s *Store) clearWorktreeCreationIntentLocked(thread Thread) error {
	identity := worktreeIdentityPath(thread.WorktreePath)
	previous := append([]orphanedWorktree(nil), s.orphanedWorktrees...)
	kept := make([]orphanedWorktree, 0, len(previous))
	removed := false
	for _, record := range previous {
		if record.DeleteBranch && record.ThreadID == thread.ID && worktreeIdentityPath(record.WorktreePath) == identity {
			removed = true
			continue
		}
		kept = append(kept, record)
	}
	if !removed {
		return nil
	}
	s.orphanedWorktrees = kept
	if err := s.saveOrphanedWorktreesLocked(); err != nil {
		s.orphanedWorktrees = previous
		return err
	}
	return nil
}

func (s *Store) recoverWorktreeCreationIntentsLocked() error {
	activePaths := make(map[string]struct{})
	activeThreads := make(map[string]struct{})
	for _, item := range s.projects {
		for _, thread := range item.Threads {
			if !thread.Worktree {
				continue
			}
			activeThreads[worktreeThreadKey(item.ID, thread.ID)] = struct{}{}
			activePaths[worktreeIdentityPath(thread.WorktreePath)] = struct{}{}
		}
	}

	previous := append([]orphanedWorktree(nil), s.orphanedWorktrees...)
	kept := make([]orphanedWorktree, 0, len(previous))
	changed := false
	for _, record := range previous {
		if !record.DeleteBranch {
			kept = append(kept, record)
			continue
		}
		_, activeThread := activeThreads[worktreeThreadKey(record.ProjectID, record.ThreadID)]
		_, activePath := activePaths[worktreeIdentityPath(record.WorktreePath)]
		if activeThread || activePath {
			changed = true
			continue
		}
		thread := Thread{
			ID:           record.ThreadID,
			Title:        record.ThreadTitle,
			Worktree:     true,
			Branch:       record.Branch,
			WorktreePath: record.WorktreePath,
		}
		if err := removeWorktreeForRollback(record.ProjectPath, thread); err != nil {
			return fmt.Errorf("recover unfinished worktree creation %q: %w", record.WorktreePath, err)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	s.orphanedWorktrees = kept
	if err := s.saveOrphanedWorktreesLocked(); err != nil {
		s.orphanedWorktrees = previous
		return err
	}
	return nil
}

func (s *Store) loadOrphanedWorktreesLocked() error {
	orphanedWorktrees, err := readOrphanedWorktreesFile(s.orphanedWorktreesFilePath)
	if err != nil {
		return err
	}
	s.orphanedWorktrees = orphanedWorktrees
	return nil
}

func readOrphanedWorktreesFile(path string) ([]orphanedWorktree, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []orphanedWorktree{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read unattached worktrees: %w", err)
	}
	var records []orphanedWorktree
	if err := json.Unmarshal(contents, &records); err != nil {
		return nil, fmt.Errorf("decode unattached worktrees: %w", err)
	}
	if records == nil {
		return nil, errors.New("decode unattached worktrees: expected a JSON array")
	}
	seen := make(map[string]struct{}, len(records))
	for index := range records {
		record := &records[index]
		record.ProjectName = strings.TrimSpace(record.ProjectName)
		record.ThreadTitle = strings.TrimSpace(record.ThreadTitle)
		record.ProjectPath = filepath.Clean(strings.TrimSpace(record.ProjectPath))
		record.WorktreePath = filepath.Clean(strings.TrimSpace(record.WorktreePath))
		if record.ProjectID == "" || record.ThreadID == "" || record.ProjectPath == "." || record.WorktreePath == "." {
			return nil, errors.New("decode unattached worktrees: project, thread, and paths are required")
		}
		if !filepath.IsAbs(record.ProjectPath) || !filepath.IsAbs(record.WorktreePath) {
			return nil, errors.New("decode unattached worktrees: paths must be absolute")
		}
		if record.DetachedAt.IsZero() {
			return nil, errors.New("decode unattached worktrees: detached time is required")
		}
		record.DetachedAt = record.DetachedAt.UTC()
		identity := worktreeIdentityPath(record.WorktreePath)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("decode unattached worktrees: duplicate path %q", record.WorktreePath)
		}
		seen[identity] = struct{}{}
	}
	return records, nil
}

func (s *Store) saveOrphanedWorktreesLocked() error {
	directory := filepath.Dir(s.orphanedWorktreesFilePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create unattached worktree directory: %w", err)
	}
	contents, err := json.MarshalIndent(s.orphanedWorktrees, "", "  ")
	if err != nil {
		return fmt.Errorf("encode unattached worktrees: %w", err)
	}
	if _, err := writeAtomicFile(s.orphanedWorktreesFilePath, contents, atomicFileOptions{
		Mode:          0o600,
		SyncFile:      true,
		SyncDirectory: true,
	}); err != nil {
		return fmt.Errorf("save unattached worktrees: %w", err)
	}
	return nil
}

func (s *Store) CleanupOrphanedWorktrees(now time.Time) (result WorktreeCleanupResult, err error) {
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation, err := s.lockAndReloadProjectMutationsLocked()
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	defer func() {
		err = errors.Join(err, mutation.Release())
	}()

	activePaths := make(map[string]struct{})
	activeThreads := make(map[string]struct{})
	for _, item := range s.projects {
		for _, thread := range item.Threads {
			if !thread.Worktree {
				continue
			}
			activeThreads[worktreeThreadKey(item.ID, thread.ID)] = struct{}{}
			if thread.WorktreePath != "" {
				activePaths[worktreeIdentityPath(thread.WorktreePath)] = struct{}{}
			}
		}
	}

	changed := false
	tracked := make(map[string]struct{}, len(s.orphanedWorktrees))
	orphaned := make([]orphanedWorktree, 0, len(s.orphanedWorktrees))
	for _, record := range s.orphanedWorktrees {
		path := filepath.Clean(record.WorktreePath)
		identity := worktreeIdentityPath(path)
		_, activePath := activePaths[identity]
		_, activeThread := activeThreads[worktreeThreadKey(record.ProjectID, record.ThreadID)]
		if activePath || activeThread {
			changed = true
			continue
		}
		record.WorktreePath = path
		tracked[identity] = struct{}{}
		orphaned = append(orphaned, record)
	}

	discovered, discoveryErr := discoverManagedOrphanedWorktrees(s.projects, activePaths, activeThreads, tracked, now)
	if len(discovered) > 0 {
		orphaned = append(orphaned, discovered...)
		changed = true
	}

	retentionDays := s.orphanedWorktreeRetentionDays
	kept := make([]orphanedWorktree, 0, len(orphaned))
	cleanupErrors := []error{discoveryErr}
	for _, record := range orphaned {
		info, statErr := os.Stat(record.WorktreePath)
		if errors.Is(statErr, os.ErrNotExist) {
			if pruneErr := pruneOrphanedWorktree(record); pruneErr != nil {
				kept = append(kept, record)
				cleanupErrors = append(cleanupErrors, pruneErr)
				continue
			}
			changed = true
			continue
		}
		if statErr != nil {
			kept = append(kept, record)
			cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect unattached worktree %q: %w", record.WorktreePath, statErr))
			continue
		}
		if !info.IsDir() {
			kept = append(kept, record)
			cleanupErrors = append(cleanupErrors, fmt.Errorf("unattached worktree %q is not a directory", record.WorktreePath))
			continue
		}
		if retentionDays == 0 || now.Before(record.DetachedAt.Add(time.Duration(retentionDays)*24*time.Hour)) {
			kept = append(kept, record)
			continue
		}

		status, statusErr := gitOutput(record.WorktreePath, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
		if statusErr != nil {
			kept = append(kept, record)
			cleanupErrors = append(cleanupErrors, fmt.Errorf("check unattached worktree %q: %w", record.WorktreePath, statusErr))
			continue
		}
		if strings.TrimSpace(string(status)) != "" {
			kept = append(kept, record)
			result.RetainedWithChanges = append(result.RetainedWithChanges, record.WorktreePath)
			continue
		}
		if removeErr := removeOrphanedWorktree(record); removeErr != nil {
			kept = append(kept, record)
			cleanupErrors = append(cleanupErrors, removeErr)
			continue
		}
		changed = true
		result.Deleted = append(result.Deleted, record.WorktreePath)
	}

	s.orphanedWorktrees = kept
	if changed {
		if saveErr := s.saveOrphanedWorktreesLocked(); saveErr != nil {
			cleanupErrors = append(cleanupErrors, saveErr)
		}
	}
	return result, errors.Join(cleanupErrors...)
}

func discoverManagedOrphanedWorktrees(
	projects []Project,
	activePaths map[string]struct{},
	activeThreads map[string]struct{},
	trackedPaths map[string]struct{},
	now time.Time,
) ([]orphanedWorktree, error) {
	var discovered []orphanedWorktree
	var discoveryErrors []error
	for _, item := range projects {
		if !isGitRepository(item.Path) {
			continue
		}
		output, err := gitOutput(item.Path, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			discoveryErrors = append(discoveryErrors, fmt.Errorf("list worktrees for project %q: %w", item.ID, err))
			continue
		}
		for _, candidate := range parseGitWorktreeList(output) {
			path := filepath.Clean(candidate.path)
			identity := worktreeIdentityPath(path)
			if _, active := activePaths[identity]; active {
				continue
			}
			if _, tracked := trackedPaths[identity]; tracked {
				continue
			}
			threadID := filepath.Base(path)
			if filepath.Base(filepath.Dir(path)) != item.ID || !looksLikeThreadID(threadID) || !strings.HasPrefix(candidate.branch, "dire-mux/") {
				continue
			}
			if _, active := activeThreads[worktreeThreadKey(item.ID, threadID)]; active {
				continue
			}
			discovered = append(discovered, orphanedWorktree{
				ProjectID:    item.ID,
				ProjectName:  item.Name,
				ThreadID:     threadID,
				ProjectPath:  filepath.Clean(item.Path),
				WorktreePath: path,
				Branch:       candidate.branch,
				DetachedAt:   now,
			})
			trackedPaths[identity] = struct{}{}
		}
	}
	return discovered, errors.Join(discoveryErrors...)
}

type gitWorktreeEntry struct {
	path   string
	branch string
}

func parseGitWorktreeList(output []byte) []gitWorktreeEntry {
	var entries []gitWorktreeEntry
	var current *gitWorktreeEntry
	for _, field := range strings.Split(string(output), "\x00") {
		switch {
		case strings.HasPrefix(field, "worktree "):
			entries = append(entries, gitWorktreeEntry{path: strings.TrimPrefix(field, "worktree ")})
			current = &entries[len(entries)-1]
		case current != nil && strings.HasPrefix(field, "branch refs/heads/"):
			current.branch = strings.TrimPrefix(field, "branch refs/heads/")
		}
	}
	return entries
}

func worktreeThreadKey(projectID, threadID string) string {
	return projectID + "\x00" + threadID
}

func worktreeIdentityPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(filepath.Clean(resolvedParent), filepath.Base(path))
	}
	return path
}

func looksLikeThreadID(value string) bool {
	if len(value) != 16 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func removeOrphanedWorktree(record orphanedWorktree) error {
	_, projectErr := gitOutput(record.ProjectPath, "worktree", "remove", record.WorktreePath)
	if projectErr != nil {
		if _, statErr := os.Stat(record.WorktreePath); !errors.Is(statErr, os.ErrNotExist) {
			if _, worktreeErr := gitOutput(record.WorktreePath, "worktree", "remove", record.WorktreePath); worktreeErr != nil {
				return fmt.Errorf("delete unattached worktree %q: %w", record.WorktreePath, errors.Join(projectErr, worktreeErr))
			}
		}
	}
	if err := pruneOrphanedWorktree(record); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(record.WorktreePath))
	return nil
}

func pruneOrphanedWorktree(record orphanedWorktree) error {
	if !isGitRepository(record.ProjectPath) {
		if record.DeleteBranch {
			return fmt.Errorf("delete transient worktree branch %q: project repository is unavailable", record.Branch)
		}
		return nil
	}
	if _, err := gitOutput(record.ProjectPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune unattached Git worktree %q: %w", record.WorktreePath, err)
	}
	if !record.DeleteBranch || strings.TrimSpace(record.Branch) == "" {
		return nil
	}
	exists, err := localGitBranchExists(record.ProjectPath, record.Branch)
	if err != nil {
		return fmt.Errorf("check transient worktree branch %q: %w", record.Branch, err)
	}
	if !exists {
		return nil
	}
	if _, err := gitOutput(record.ProjectPath, "branch", "-D", record.Branch); err != nil {
		return fmt.Errorf("delete transient worktree branch %q: %w", record.Branch, err)
	}
	return nil
}
