import assert from 'node:assert/strict'
import test from 'node:test'
import {
  activityDisplayThread,
  activityDisplayThreadId,
  sidebarProjectActivityCounts,
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
  assert.equal(activityDisplayThread(malformedThreads, orphanFinished)?.id, 'orphan')
})

test('cyclic parent links terminate without inventing a display thread', () => {
  const cyclicThreads = [
    { id: 'cycle-a', parentThreadId: 'cycle-b' },
    { id: 'cycle-b', parentThreadId: 'cycle-a' },
  ]
  const cycleFinished = activity('cycle-a', 'finished')

  assert.equal(activityDisplayThreadId(cyclicThreads, cycleFinished), 'cycle-a')
  assert.equal(activityDisplayThread(cyclicThreads, cycleFinished)?.id, 'cycle-a')
})

test('archived or missing source threads cannot roll activity up to a visible root', () => {
  const archivedChildThreads = threads.map((thread) =>
    thread.id === 'child' ? { ...thread, archivedAt: '2026-07-26T00:00:00Z' } : thread)
  const childFinished = activity('child', 'finished')
  const missingFinished = activity('missing', 'finished')

  assert.equal(activityDisplayThread(archivedChildThreads, childFinished), null)
  assert.equal(activityDisplayThread(threads, missingFinished), null)
  assert.equal(sidebarThreadActivity(archivedChildThreads, [childFinished], projectId, 'root').activity, null)
  assert.equal(sidebarThreadActivity(threads, [missingFinished], projectId, 'root').activity, null)
})

test('activity is excluded when its rolled-up display thread is archived', () => {
  const archivedRootThreads = threads.map((thread) =>
    thread.id === 'root' ? { ...thread, archivedAt: '2026-07-26T00:00:00Z' } : thread)
  const childFinished = activity('child', 'finished')

  assert.equal(activityDisplayThread(archivedRootThreads, childFinished), null)
  assert.equal(sidebarThreadActivity(archivedRootThreads, [childFinished], projectId, 'root').activity, null)
})

test('activity cannot roll through an archived intermediate ancestor', () => {
  const archivedIntermediateThreads = threads.map((thread) =>
    thread.id === 'child' ? { ...thread, archivedAt: '2026-07-26T00:00:00Z' } : thread)
  const grandchildFinished = activity('grandchild', 'finished')

  assert.equal(activityDisplayThreadId(archivedIntermediateThreads, grandchildFinished), 'root')
  assert.equal(activityDisplayThread(archivedIntermediateThreads, grandchildFinished), null)
  assert.equal(
    sidebarThreadActivity(archivedIntermediateThreads, [grandchildFinished], projectId, 'root').activity,
    null,
  )
  assert.equal(
    sidebarProjectActivityCounts(
      [{ id: projectId, threads: archivedIntermediateThreads }],
      [grandchildFinished],
    ).has(projectId),
    false,
  )
})

test('project counts exclude archived and missing source or display threads', () => {
  const archivedChildThreads = threads.map((thread) =>
    thread.id === 'child' ? { ...thread, archivedAt: '2026-07-26T00:00:00Z' } : thread)
  const archivedRootThreads = threads.map((thread) =>
    thread.id === 'root' ? { ...thread, archivedAt: '2026-07-26T00:00:00Z' } : thread)
  const sourceCounts = sidebarProjectActivityCounts(
    [{ id: projectId, threads: archivedChildThreads }],
    [
      activity('child', 'finished'),
      activity('missing', 'working'),
    ],
  )
  const displayCounts = sidebarProjectActivityCounts(
    [{ id: projectId, threads: archivedRootThreads }],
    [activity('child', 'finished')],
  )

  assert.equal(sourceCounts.has(projectId), false)
  assert.equal(displayCounts.has(projectId), false)
})
