import assert from 'node:assert/strict'
import test from 'node:test'
import {
  activityDisplayThread,
  activityDisplayThreadId,
  sidebarProjectActivityCounts,
  sidebarThreadActivity,
} from '../src/sidebar-thread-activity.mjs'

const threads = [{ id: 'one' }, { id: 'two', archivedAt: '2026-01-01T00:00:00Z' }]

test('activity stays on the thread that emitted it', () => {
  const activity = { projectId: 'p1', threadId: 'one', state: 'finished' }
  assert.equal(activityDisplayThreadId(threads, activity), 'one')
  assert.equal(activityDisplayThread(threads, activity)?.id, 'one')
  assert.equal(sidebarThreadActivity(threads, [activity], 'p1', 'one').activity, activity)
})

test('archived and unknown activity is excluded from sidebar counts', () => {
  const projects = [{ id: 'p1', threads }]
  const activities = [
    { projectId: 'p1', threadId: 'one', state: 'working' },
    { projectId: 'p1', threadId: 'two', state: 'finished' },
    { projectId: 'p1', threadId: 'missing', state: 'working' },
  ]
  assert.deepEqual(sidebarProjectActivityCounts(projects, activities).get('p1'), {
    working: 1,
    finished: 0,
  })
})
