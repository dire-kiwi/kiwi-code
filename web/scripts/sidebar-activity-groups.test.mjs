import assert from 'node:assert/strict'
import test from 'node:test'
import { activityViewGroups, formatRelativeShort } from '../src/sidebar-activity-groups.mjs'

const at = (day) => `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`
const keys = (entries) => entries.map((entry) => `${entry.projectId}:${entry.threadId}`)

test('threads land in the first activity section that claims them', () => {
  const projects = [{
    id: 'p1',
    threads: [
      { id: 'working', createdAt: at(1) },
      { id: 'finished', createdAt: at(2) },
      { id: 'recent-a', createdAt: at(3) },
      { id: 'recent-b', createdAt: at(4) },
    ],
  }]
  const activities = [
    { projectId: 'p1', threadId: 'working', state: 'working', updatedAt: at(5) },
    { projectId: 'p1', threadId: 'finished', state: 'finished', updatedAt: at(6) },
  ]
  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(keys(groups.working), ['p1:working'])
  assert.deepEqual(keys(groups.needsReview), ['p1:finished'])
  assert.deepEqual(keys(groups.recent), ['p1:recent-b', 'p1:recent-a'])
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

test('settled threads and unknown activity are excluded', () => {
  const projects = [{ id: 'p1', threads: [{ id: 'settled', createdAt: at(1), settledAt: at(2) }] }]
  const groups = activityViewGroups(projects, [
    { projectId: 'p1', threadId: 'settled', state: 'working', updatedAt: at(3) },
    { projectId: 'p1', threadId: 'missing', state: 'finished', updatedAt: at(3) },
  ])
  assert.deepEqual(keys(groups.settled), ['p1:settled'])
  assert.deepEqual(groups.working, [])
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(groups.recent, [])
})

test('formatRelativeShort compresses elapsed time', () => {
  const now = Date.parse('2026-01-08T00:00:00Z')
  assert.equal(formatRelativeShort(now - 30_000, now), 'now')
  assert.equal(formatRelativeShort(now - 5 * 60_000, now), '5m')
  assert.equal(formatRelativeShort(now - 3 * 60 * 60_000, now), '3h')
  assert.equal(formatRelativeShort(now - 2 * 24 * 60 * 60_000, now), '2d')
  assert.equal(formatRelativeShort(now - 7 * 24 * 60 * 60_000, now), '1w')
})

test('settled shelf is sorted by settlement and unsettle promotes old work', () => {
  const groups = activityViewGroups([{ id: 'p', threads: [
    { id: 'older', createdAt: at(1), settledAt: at(4) },
    { id: 'newer', createdAt: at(2), settledAt: at(6) },
    { id: 'resumed', createdAt: at(1), unsettledAt: at(8) },
    { id: 'active', createdAt: at(5) },
  ] }], [{projectId:'p',threadId:'newer',state:'finished',updatedAt:at(9)}])
  assert.deepEqual(keys(groups.settled), ['p:newer', 'p:older'])
  assert.deepEqual(keys(groups.recent), ['p:resumed', 'p:active'])
  assert.deepEqual(groups.needsReview, [])
})

test('scope applies even when using a shared index of all projects', async () => {
  const { createSidebarThreadIndex } = await import('../src/sidebar-thread-index.mjs')
  const projects = ['one', 'two'].map(id => ({id,threads:[{id:'thread',createdAt:at(1),settledAt:at(2)}]}))
  const index = createSidebarThreadIndex(projects, [])
  const groups = activityViewGroups([projects[1]], [], 8, index)
  assert.deepEqual(keys(groups.settled), ['two:thread'])
})
