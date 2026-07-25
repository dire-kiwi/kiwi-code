import assert from 'node:assert/strict'
import test from 'node:test'
import {
  activityViewGroups,
  formatRelativeShort,
  recentThreadLimit,
} from '../src/sidebar-activity-groups.mjs'

const minute = 60_000
const base = Date.parse('2026-07-25T12:00:00Z')

function iso(minutesAgo) {
  return new Date(base - minutesAgo * minute).toISOString()
}

function thread(id, overrides = {}) {
  return { id, createdAt: iso(600), ...overrides }
}

function project(id, threads) {
  return { id, threads }
}

function entryIds(entries) {
  return entries.map((entry) => `${entry.projectId}:${entry.threadId}`)
}

test('threads land in the first section that claims them', () => {
  const projects = [
    project('p1', [
      thread('root', { lastPromptAt: iso(10) }),
      thread('agent', { parentThreadId: 'root', lastPromptAt: iso(5) }),
      thread('done-agent', { parentThreadId: 'root', lastPromptAt: iso(20) }),
      thread('pinned', { bookmarked: true, lastPromptAt: iso(30) }),
      thread('plain', { lastPromptAt: iso(40) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'agent', state: 'working', updatedAt: iso(1) },
    { projectId: 'p1', threadId: 'done-agent', state: 'finished', updatedAt: iso(2) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), ['p1:agent'])
  assert.deepEqual(entryIds(groups.needsReview), ['p1:done-agent'])
  assert.deepEqual(entryIds(groups.pinned), ['p1:pinned'])
  assert.deepEqual(entryIds(groups.recent), ['p1:root', 'p1:plain'])
  assert.equal(groups.hiddenRecentCount, 0)
})

test('a working thread never duplicates into needs review or pinned', () => {
  const projects = [
    project('p1', [thread('busy', { bookmarked: true, lastPromptAt: iso(3) })]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'busy', state: 'working', updatedAt: iso(1) },
    { projectId: 'p1', threadId: 'busy', state: 'finished', updatedAt: iso(2) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), ['p1:busy'])
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(groups.pinned, [])
  assert.deepEqual(groups.recent, [])
})

test('archived threads are excluded everywhere, closed roots leave recent', () => {
  const projects = [
    project('p1', [
      thread('archived', { archivedAt: iso(1), bookmarked: true }),
      thread('closed', { closedAt: iso(1), lastPromptAt: iso(2) }),
      thread('open', { lastPromptAt: iso(3) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'archived', state: 'finished', updatedAt: iso(1) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(groups.working, [])
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(groups.pinned, [])
  assert.deepEqual(entryIds(groups.recent), ['p1:open'])
})

test('activities for unknown threads are ignored', () => {
  const projects = [project('p1', [thread('known')])]
  const activities = [
    { projectId: 'p1', threadId: 'missing', state: 'working', updatedAt: iso(1) },
    { projectId: 'p2', threadId: 'known', state: 'working', updatedAt: iso(1) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(groups.working, [])
})

test('sections sort newest first across projects', () => {
  const projects = [
    project('p1', [thread('older', { lastPromptAt: iso(50) })]),
    project('p2', [thread('newer', { lastPromptAt: iso(5) })]),
  ]

  const groups = activityViewGroups(projects, [])
  assert.deepEqual(entryIds(groups.recent), ['p2:newer', 'p1:older'])
})

test('recent caps at the limit and reports the overflow', () => {
  const threads = Array.from({ length: recentThreadLimit + 3 }, (_, index) =>
    thread(`t${index}`, { lastPromptAt: iso(index + 1) }))
  const groups = activityViewGroups([project('p1', threads)], [])

  assert.equal(groups.recent.length, recentThreadLimit)
  assert.equal(groups.hiddenRecentCount, 3)
  assert.deepEqual(entryIds(groups.recent).at(0), 'p1:t0')
})

test('working entries prefer the activity timestamp for display', () => {
  const projects = [project('p1', [thread('busy', { lastPromptAt: iso(30) })])]
  const activities = [
    { projectId: 'p1', threadId: 'busy', state: 'working', updatedAt: iso(2) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.equal(groups.working[0].at, base - 2 * minute)
})

test('formatRelativeShort compresses elapsed time', () => {
  assert.equal(formatRelativeShort(iso(0), base), 'now')
  assert.equal(formatRelativeShort(iso(5), base), '5m')
  assert.equal(formatRelativeShort(iso(3 * 60), base), '3h')
  assert.equal(formatRelativeShort(iso(2 * 24 * 60), base), '2d')
  assert.equal(formatRelativeShort(iso(9 * 24 * 60), base), '1w')
  assert.equal(formatRelativeShort(base - 5 * minute, base), '5m')
  assert.equal(formatRelativeShort(undefined, base), '')
  assert.equal(formatRelativeShort('not a date', base), '')
  assert.equal(formatRelativeShort(0, base), '')
})
