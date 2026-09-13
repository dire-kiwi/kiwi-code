package project

import (
	"encoding/json"
	"errors"
	"time"
)

// UnmarshalJSON migrates the old archive state without retaining its deletion policy.
func (t *Thread) UnmarshalJSON(data []byte) error {
	type plain Thread
	var value struct {
		plain
		ArchivedAt *time.Time `json:"archivedAt"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = Thread(value.plain)
	if t.SettledAt == nil && value.ArchivedAt != nil {
		t.SettledAt = value.ArchivedAt
	}
	return nil
}

// Settlement retains the thread record and workspace until explicitly deleted.
func (s *Store) SetThreadSettled(projectID, threadID string, settled bool) (Thread, error) {
	return s.setThreadSettledAt(projectID, threadID, settled, time.Now().UTC())
}

var ErrThreadRecentlyActive = errors.New("thread was recently active")

func (s *Store) SetThreadSettledIfIdle(projectID, threadID string, before time.Time) (Thread, error) {
	return s.setThreadSettledAt(projectID, threadID, true, time.Now().UTC(), before)
}

func (s *Store) setThreadSettledAt(projectID, threadID string, settled bool, now time.Time, idleBefore ...time.Time) (Thread, error) {
	now = now.UTC()
	return withProjectMutationResult(s, func() (Thread, error) {
		for pi := range s.projects {
			if s.projects[pi].ID != projectID {
				continue
			}
			for ti := range s.projects[pi].Threads {
				thread := &s.projects[pi].Threads[ti]
				if thread.ID != threadID {
					continue
				}
				if thread.RollbackPending {
					return Thread{}, ErrThreadRollbackPending
				}
				if settled == (thread.SettledAt != nil) {
					return cloneThread(*thread), nil
				}
				if settled && len(idleBefore) > 0 {
					latest := thread.CreatedAt
					for _, at := range []*time.Time{thread.LastPromptAt, thread.LastActivityAt, thread.UnsettledAt} {
						if at != nil && at.After(latest) {
							latest = *at
						}
					}
					if latest.After(idleBefore[0]) {
						return Thread{}, ErrThreadRecentlyActive
					}
				}
				previous := cloneThread(*thread)
				if settled {
					thread.SettledAt = &now
					thread.UnsettledAt = nil
				} else {
					thread.SettledAt = nil
					thread.UnsettledAt = &now
				}
				return saveProjectMutationResult(s, cloneThread(*thread), func() { *thread = previous })
			}
			return Thread{}, ErrThreadNotFound
		}
		return Thread{}, ErrNotFound
	})
}

// RecordThreadActivity persists human visits and agent transitions. Minute
// coalescing bounds writes from the workspace heartbeat.
func (s *Store) RecordThreadActivity(projectID, threadID string, at time.Time) error {
	return s.withProjectMutation(func() error {
		for pi := range s.projects {
			if s.projects[pi].ID != projectID {
				continue
			}
			for ti := range s.projects[pi].Threads {
				thread := &s.projects[pi].Threads[ti]
				if thread.ID != threadID {
					continue
				}
				if thread.SettledAt != nil || thread.RollbackPending {
					return nil
				}
				if thread.LastActivityAt != nil && !at.After(thread.LastActivityAt.Add(time.Minute)) {
					return nil
				}
				previous := thread.LastActivityAt
				value := at.UTC()
				thread.LastActivityAt = &value
				_, err := saveProjectMutationResult(s, struct{}{}, func() { thread.LastActivityAt = previous })
				return err
			}
			return ErrThreadNotFound
		}
		return ErrNotFound
	})
}
