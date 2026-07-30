import assert from 'node:assert/strict'
import test from 'node:test'
import {
  bookmarkedThreadPathIds,
  defaultVisibleRootThreadIds,
} from '../src/sidebar-thread-visibility.mjs'

const thread = (id, createdAt, extras = {}) => ({ id, createdAt, ...extras })

test('collapsed projects keep the most recently prompted threads', () => {
  const threads = [
    thread('old', '2026-01-01T00:00:00Z'),
    thread('new', '2026-01-02T00:00:00Z'),
    thread('prompted', '2026-01-01T00:00:00Z', { lastPromptAt: '2026-01-03T00:00:00Z' }),
  ]
  assert.deepEqual(defaultVisibleRootThreadIds(threads, [], 'p1', 2), ['new', 'prompted'])
})

test('working, finished, and bookmarked threads remain visible', () => {
  const threads = [
    thread('recent', '2026-01-03T00:00:00Z'),
    thread('working', '2026-01-01T00:00:00Z'),
    thread('bookmarked', '2026-01-01T00:00:00Z', { bookmarked: true }),
  ]
  const activities = [{ projectId: 'p1', threadId: 'working', state: 'working' }]
  assert.deepEqual(defaultVisibleRootThreadIds(threads, activities, 'p1', 1), [
    'recent',
    'working',
    'bookmarked',
  ])
})

test('bookmark filtering returns only bookmarked threads', () => {
  assert.deepEqual(bookmarkedThreadPathIds([
    thread('one', '2026-01-01T00:00:00Z', { bookmarked: true }),
    thread('two', '2026-01-02T00:00:00Z'),
  ]), ['one'])
})
