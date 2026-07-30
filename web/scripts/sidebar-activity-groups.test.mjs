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
      thread('newer-recent', { lastPromptAt: iso(30) }),
      thread('older-recent', { lastPromptAt: iso(40) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'agent', state: 'working', updatedAt: iso(1) },
    { projectId: 'p1', threadId: 'done-agent', state: 'finished', updatedAt: iso(2) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), ['p1:agent'])
  assert.deepEqual(entryIds(groups.needsReview), ['p1:root'])
  assert.deepEqual(entryIds(groups.recent), ['p1:newer-recent', 'p1:older-recent'])
  assert.equal(groups.hiddenRecentCount, 0)
})

test('finished descendants share one root entry with the newest completion time', () => {
  const projects = [
    project('p1', [
      thread('root', { lastPromptAt: iso(10) }),
      thread('older-child', { parentThreadId: 'root' }),
      thread('newer-child', { parentThreadId: 'root' }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'older-child', state: 'finished', updatedAt: iso(5) },
    { projectId: 'p1', threadId: 'newer-child', state: 'finished', updatedAt: iso(1) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.needsReview), ['p1:root'])
  assert.equal(groups.needsReview[0].at, base - minute)
  assert.deepEqual(groups.recent, [])
})

test('archived child activity cannot keep its active root in needs review', () => {
  const projects = [
    project('p1', [
      thread('root', { lastPromptAt: iso(10) }),
      thread('archived-child', { parentThreadId: 'root', archivedAt: iso(1) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'archived-child', state: 'finished', updatedAt: iso(1) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(entryIds(groups.recent), ['p1:root'])
})

test('grandchild activity cannot roll through an archived child to an active root', () => {
  const projects = [
    project('p1', [
      thread('root', { lastPromptAt: iso(10) }),
      thread('archived-child', { parentThreadId: 'root', archivedAt: iso(1) }),
      thread('grandchild', { parentThreadId: 'archived-child' }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'grandchild', state: 'finished', updatedAt: iso(1) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(entryIds(groups.recent), ['p1:root'])
})

test('a working thread never duplicates into needs review or recent', () => {
  const projects = [
    project('p1', [thread('busy', { lastPromptAt: iso(3) })]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'busy', state: 'working', updatedAt: iso(1) },
    { projectId: 'p1', threadId: 'busy', state: 'finished', updatedAt: iso(2) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), ['p1:busy'])
  assert.deepEqual(groups.needsReview, [])
  assert.deepEqual(groups.recent, [])
})

test('archived threads are excluded everywhere, closed roots leave recent', () => {
  const projects = [
    project('p1', [
      thread('archived', { archivedAt: iso(1) }),
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

test('subthreads stay directly below their parent in activity sections', () => {
  const projects = [
    project('p1', [
      thread('working-parent', { lastPromptAt: iso(30) }),
      thread('working-child', { parentThreadId: 'working-parent', lastPromptAt: iso(3) }),
      thread('working-grandchild', { parentThreadId: 'working-child', lastPromptAt: iso(1) }),
      thread('other-working', { lastPromptAt: iso(2) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'working-parent', state: 'working', updatedAt: iso(10) },
    { projectId: 'p1', threadId: 'working-child', state: 'working', updatedAt: iso(8) },
    { projectId: 'p1', threadId: 'working-grandchild', state: 'working', updatedAt: iso(7) },
    { projectId: 'p1', threadId: 'other-working', state: 'working', updatedAt: iso(0) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), [
    'p1:working-parent',
    'p1:working-child',
    'p1:working-grandchild',
    'p1:other-working',
  ])
})

test('recent caps at the limit and reports the overflow', () => {
  const threads = Array.from({ length: recentThreadLimit + 3 }, (_, index) =>
    thread(`t${index}`, { lastPromptAt: iso(index + 1) }))
  const groups = activityViewGroups([project('p1', threads)], [])

  assert.equal(groups.recent.length, recentThreadLimit)
  assert.equal(groups.hiddenRecentCount, 3)
  assert.deepEqual(entryIds(groups.recent).at(0), 'p1:t0')
})

test('working entries reorder by user prompts instead of LLM activity', () => {
  const projects = [
    project('p1', [
      thread('newer-prompt', { lastPromptAt: iso(1) }),
      thread('newer-response', { lastPromptAt: iso(10) }),
    ]),
  ]
  const activities = [
    { projectId: 'p1', threadId: 'newer-prompt', state: 'working', updatedAt: iso(2) },
    { projectId: 'p1', threadId: 'newer-response', state: 'working', updatedAt: iso(0) },
  ]

  const groups = activityViewGroups(projects, activities)
  assert.deepEqual(entryIds(groups.working), ['p1:newer-prompt', 'p1:newer-response'])
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
