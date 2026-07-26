import assert from 'node:assert/strict'
import test from 'node:test'
import { projectsByMostRecentThread } from '../src/new-thread-project-order.mjs'

function thread(id, createdAt, overrides = {}) {
  return { id, createdAt, ...overrides }
}

function project(id, threads) {
  return { id, threads }
}

test('projects are ordered by their most recently created thread', () => {
  const projects = [
    project('older', [thread('older-thread', '2026-01-01T00:00:00Z')]),
    project('newest', [thread('newest-thread', '2026-03-01T00:00:00Z')]),
    project('middle', [
      thread('middle-older', '2025-12-01T00:00:00Z'),
      thread('middle-newest', '2026-02-01T00:00:00Z'),
    ]),
  ]

  assert.deepEqual(
    projectsByMostRecentThread(projects).map(({ id }) => id),
    ['newest', 'middle', 'older'],
  )
  assert.deepEqual(projects.map(({ id }) => id), ['older', 'newest', 'middle'])
})

test('thread creation time, not recent activity, determines project order', () => {
  const projects = [
    project('recently-used', [thread('old', '2026-01-01T00:00:00Z', {
      lastPromptAt: '2026-04-01T00:00:00Z',
    })]),
    project('recently-created', [thread('new', '2026-03-01T00:00:00Z')]),
  ]

  assert.deepEqual(
    projectsByMostRecentThread(projects).map(({ id }) => id),
    ['recently-created', 'recently-used'],
  )
})

test('projects with equal or missing thread dates keep their existing order', () => {
  const projects = [
    project('first-tie', [thread('first', '2026-01-01T00:00:00Z')]),
    project('second-tie', [thread('second', '2026-01-01T00:00:00Z')]),
    project('no-threads', []),
    project('invalid-date', [thread('invalid', 'not-a-date')]),
  ]

  assert.deepEqual(
    projectsByMostRecentThread(projects).map(({ id }) => id),
    ['first-tie', 'second-tie', 'no-threads', 'invalid-date'],
  )
})
