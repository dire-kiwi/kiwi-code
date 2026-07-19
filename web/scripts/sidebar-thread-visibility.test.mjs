import assert from 'node:assert/strict'
import test from 'node:test'
import {
  collapsedRootThreadLimit,
  defaultVisibleRootThreadIds,
} from '../src/sidebar-thread-visibility.mjs'

const projectId = 'project'

function thread(id, createdDay, lastPromptDay) {
  return {
    id,
    createdAt: `2026-01-${String(createdDay).padStart(2, '0')}T00:00:00Z`,
    ...(lastPromptDay
      ? { lastPromptAt: `2026-02-${String(lastPromptDay).padStart(2, '0')}T00:00:00Z` }
      : {}),
  }
}

test('collapsed projects keep the five most recently prompted roots in their saved order', () => {
  const threads = [
    thread('saved-first-but-old', 1, 1),
    thread('recent-5', 2, 5),
    thread('recent-7', 3, 7),
    thread('recent-3', 4, 3),
    thread('recent-6', 5, 6),
    thread('recent-2', 6, 2),
    thread('recent-4', 7, 4),
  ]

  assert.equal(collapsedRootThreadLimit, 5)
  assert.deepEqual(defaultVisibleRootThreadIds(threads, [], projectId), [
    'recent-5',
    'recent-7',
    'recent-3',
    'recent-6',
    'recent-4',
  ])
})

test('unprompted threads use creation time as their recency fallback', () => {
  const threads = [
    thread('created-first', 1),
    thread('created-third', 3),
    thread('created-second', 2),
  ]

  assert.deepEqual(defaultVisibleRootThreadIds(threads, [], projectId, 2), [
    'created-third',
    'created-second',
  ])
})

test('working and unread-finished roots stay visible beyond the recency limit', () => {
  const threads = [
    thread('old-working', 1, 1),
    thread('old-finished', 2, 2),
    thread('recent-a', 3, 3),
    thread('recent-b', 4, 4),
    thread('recent-c', 5, 5),
    thread('recent-d', 6, 6),
    thread('recent-e', 7, 7),
  ]
  const activities = [
    { projectId, threadId: 'old-working', state: 'working' },
    { projectId, threadId: 'old-finished', state: 'finished' },
    { projectId: 'other-project', threadId: 'old-working', state: 'working' },
  ]

  assert.deepEqual(defaultVisibleRootThreadIds(threads, activities, projectId), threads.map(({ id }) => id))
})

test('child activity keeps its active root visible while archived roots remain under Show more', () => {
  const roots = [
    thread('old-parent', 1, 1),
    thread('archived-parent', 2, 2),
    thread('recent-a', 3, 3),
    thread('recent-b', 4, 4),
    thread('recent-c', 5, 5),
    thread('recent-d', 6, 6),
    thread('recent-e', 7, 7),
  ]
  roots[1].archivedAt = '2026-03-01T00:00:00Z'
  const threads = [
    ...roots,
    { ...thread('active-child', 8, 8), parentThreadId: 'old-parent' },
    { ...thread('archived-child', 9, 9), parentThreadId: 'archived-parent' },
  ]
  const activities = [
    { projectId, threadId: 'active-child', state: 'working' },
    { projectId, threadId: 'archived-child', state: 'working' },
  ]

  const visible = defaultVisibleRootThreadIds(threads, activities, projectId)
  assert.equal(visible.includes('old-parent'), true)
  assert.equal(visible.includes('archived-parent'), false)
  assert.equal(visible.includes('active-child'), false)
})
