import assert from 'node:assert/strict'
import test from 'node:test'
import {
  createSidebarThreadIndex,
  createThreadTreeIndex,
} from '../src/sidebar-thread-index.mjs'

const threads = [
  { id: 'root', title: 'Root' },
  { id: 'child', title: 'Child', parentThreadId: 'root' },
  { id: 'grandchild', title: 'Grandchild', parentThreadId: 'child' },
]

test('thread tree index resolves families once and preserves source order', () => {
  const index = createThreadTreeIndex(threads)
  assert.deepEqual(index.roots.map((thread) => thread.id), ['root'])
  assert.deepEqual(index.children('root').map((thread) => thread.id), ['child'])
  assert.deepEqual(index.ancestors('grandchild').map((thread) => thread.id), ['child', 'root'])
  assert.deepEqual(index.descendants('root').map((thread) => thread.id), ['child', 'grandchild'])
  assert.deepEqual(index.orderedTreeIds(['root']), ['root', 'child', 'grandchild'])
})

test('sidebar index rolls finished activity up and keeps working child activity local', () => {
  const projects = [{ id: 'project', threads }]
  const working = { projectId: 'project', threadId: 'child', state: 'working' }
  const finished = { projectId: 'project', threadId: 'grandchild', state: 'finished' }
  const index = createSidebarThreadIndex(projects, [finished, working])

  assert.deepEqual(index.threadActivity('project', 'root'), {
    activity: finished,
    childActivity: true,
  })
  assert.deepEqual(index.threadActivity('project', 'child'), {
    activity: working,
    childActivity: false,
  })
  assert.deepEqual(index.finishedActivities('project', 'root'), [finished])
  assert.deepEqual(index.projectActivityCounts.get('project'), { working: 1, finished: 1 })
})

test('cycles terminate without inventing roots or dropping indexed rows', () => {
  const cyclic = [
    { id: 'a', parentThreadId: 'b' },
    { id: 'b', parentThreadId: 'a' },
  ]
  const tree = createThreadTreeIndex(cyclic)
  assert.equal(tree.rootId('a'), null)
  assert.deepEqual(tree.descendants('a').map((thread) => thread.id), ['b'])
  assert.deepEqual(tree.orderedTreeIds([]), ['a', 'b'])
})
