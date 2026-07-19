import assert from 'node:assert/strict'
import test from 'node:test'
import {
  activityDisplayThreadId,
  sidebarThreadActivity,
} from '../src/sidebar-thread-activity.mjs'

const projectId = 'project'
const threads = [
  { id: 'root' },
  { id: 'child', parentThreadId: 'root' },
  { id: 'grandchild', parentThreadId: 'child' },
]

function activity(threadId, state) {
  return { projectId, threadId, state }
}

test('working activity stays on the thread that is working', () => {
  const childWorking = activity('child', 'working')

  assert.equal(activityDisplayThreadId(threads, childWorking), 'child')
  assert.deepEqual(sidebarThreadActivity(threads, [childWorking], projectId, 'child'), {
    activity: childWorking,
    childActivity: false,
  })
  assert.equal(sidebarThreadActivity(threads, [childWorking], projectId, 'root').activity, null)
})

test('finished descendant activity appears only on the root parent', () => {
  const childFinished = activity('child', 'finished')
  const grandchildFinished = activity('grandchild', 'finished')

  assert.equal(activityDisplayThreadId(threads, childFinished), 'root')
  assert.equal(activityDisplayThreadId(threads, grandchildFinished), 'root')
  assert.deepEqual(sidebarThreadActivity(threads, [grandchildFinished], projectId, 'root'), {
    activity: grandchildFinished,
    childActivity: true,
  })
  assert.equal(sidebarThreadActivity(threads, [grandchildFinished], projectId, 'child').activity, null)
  assert.equal(sidebarThreadActivity(threads, [grandchildFinished], projectId, 'grandchild').activity, null)
})

test('finished root activity stays on the root thread', () => {
  const rootFinished = activity('root', 'finished')

  assert.equal(activityDisplayThreadId(threads, rootFinished), 'root')
  assert.deepEqual(sidebarThreadActivity(threads, [rootFinished], projectId, 'root'), {
    activity: rootFinished,
    childActivity: false,
  })
})

test('root activity takes priority over a completed descendant', () => {
  const rootWorking = activity('root', 'working')
  const grandchildFinished = activity('grandchild', 'finished')

  assert.deepEqual(
    sidebarThreadActivity(threads, [grandchildFinished, rootWorking], projectId, 'root'),
    { activity: rootWorking, childActivity: false },
  )
})

test('malformed parent links do not propagate completion to an unrelated thread', () => {
  const orphanFinished = activity('orphan', 'finished')
  const malformedThreads = [...threads, { id: 'orphan', parentThreadId: 'missing' }]

  assert.equal(activityDisplayThreadId(malformedThreads, orphanFinished), 'orphan')
})
