import assert from 'node:assert/strict'
import test from 'node:test'
import { createSidebarThreadIndex, createThreadTreeIndex } from '../src/sidebar-thread-index.mjs'

test('thread index keeps every thread at the top level in source order', () => {
  const threads = [
    { id: 'a', bookmarked: true },
    { id: 'b' },
  ]
  const tree = createThreadTreeIndex(threads)
  assert.deepEqual(tree.roots.map((thread) => thread.id), ['a', 'b'])
  assert.deepEqual(tree.bookmarkedPathIds(), ['a'])
})

test('sidebar index keeps activity on its owning thread', () => {
  const projects = [{ id: 'p1', threads: [{ id: 'a' }, { id: 'b' }] }]
  const activities = [
    { projectId: 'p1', threadId: 'a', state: 'finished' },
    { projectId: 'p1', threadId: 'b', state: 'working' },
  ]
  const index = createSidebarThreadIndex(projects, activities)
  assert.equal(index.threadActivity('p1', 'a').activity?.state, 'finished')
  assert.equal(index.threadActivity('p1', 'b').activity?.state, 'working')
  assert.deepEqual(index.projectActivityCounts.get('p1'), { working: 1, finished: 1 })
})
