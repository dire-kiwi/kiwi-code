import assert from 'node:assert/strict'
import test from 'node:test'
import { activityViewGroups, formatRelativeShort } from '../src/sidebar-activity-groups.mjs'

const at = (day) => `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`
const keys = (entries) => entries.map((entry) => `${entry.projectId}:${entry.threadId}`)

test('threads land in the first activity section that claims them', () => {
  const projects = [{
    id: 'p1',
    threads: [
      { id: 'working', createdAt: at(1), bookmarked: true },
      { id: 'finished', createdAt: at(2), bookmarked: true },
      { id: 'pinned', createdAt: at(3), bookmarked: true },
      { id: 'recent', createdAt: at(4) },
    ],
  }]
  const activities = [
    { projectId: 'p1', threadId: 'working', state: 'working', updatedAt: at(5) },
    { projectId: 'p1', threadId: 'finished', state: 'finished', updatedAt: at(6) },
  ]
  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(keys(groups.working), ['p1:working'])
  assert.deepEqual(keys(groups.needsReview), ['p1:finished'])
  assert.deepEqual(keys(groups.pinned), ['p1:pinned'])
  assert.deepEqual(keys(groups.recent), ['p1:recent'])
})

test('sections sort newest first and recent reports overflow', () => {
  const projects = [{
    id: 'p1',
    threads: [1, 2, 3].map((day) => ({ id: String(day), createdAt: at(day) })),
  }]
  const groups = activityViewGroups(projects, [], 2)
  assert.deepEqual(keys(groups.recent), ['p1:3', 'p1:2'])
  assert.equal(groups.hiddenRecentCount, 1)
})

test('archived threads and unknown activity are excluded', () => {
  const projects = [{ id: 'p1', threads: [{ id: 'archived', createdAt: at(1), archivedAt: at(2) }] }]
  const groups = activityViewGroups(projects, [
    { projectId: 'p1', threadId: 'archived', state: 'working', updatedAt: at(3) },
    { projectId: 'p1', threadId: 'missing', state: 'finished', updatedAt: at(3) },
  ])
  assert.deepEqual(groups, { working: [], needsReview: [], pinned: [], recent: [], hiddenRecentCount: 0 })
})

test('formatRelativeShort compresses elapsed time', () => {
  const now = Date.parse('2026-01-08T00:00:00Z')
  assert.equal(formatRelativeShort(now - 30_000, now), 'now')
  assert.equal(formatRelativeShort(now - 5 * 60_000, now), '5m')
  assert.equal(formatRelativeShort(now - 3 * 60 * 60_000, now), '3h')
  assert.equal(formatRelativeShort(now - 2 * 24 * 60 * 60_000, now), '2d')
  assert.equal(formatRelativeShort(now - 7 * 24 * 60 * 60_000, now), '1w')
})
