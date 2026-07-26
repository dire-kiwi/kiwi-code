import assert from 'node:assert/strict'
import test from 'node:test'
import {
  piActivityKey,
  piActivityVersion,
  reconcileFailedPiAcknowledgements,
  reconcilePiActivities,
  restoreAcknowledgedPiActivity,
  samePiActivities,
} from '../src/pi-activity-reconciliation.mjs'

function activity(threadId, state, second) {
  return {
    projectId: 'project',
    threadId,
    state,
    updatedAt: new Date(Date.UTC(2026, 6, 26, 0, 0, second)).toISOString(),
  }
}

test('activity equality observes timestamps and array order', () => {
  const first = activity('first', 'working', 1)
  const second = activity('second', 'finished', 2)

  assert.equal(samePiActivities([first, second], [first, second]), true)
  assert.equal(samePiActivities([first, second], [{ ...first, updatedAt: activity('first', 'working', 3).updatedAt }, second]), false)
  assert.equal(samePiActivities([first, second], [second, first]), false)
})

test('acknowledged activity stays hidden through repeated and older snapshots', () => {
  const finished = activity('thread', 'finished', 5)
  const acknowledgements = new Map([[
    piActivityKey(finished.projectId, finished.threadId),
    { activity: finished, index: 0 },
  ]])

  assert.deepEqual(reconcilePiActivities([finished], acknowledgements), [])
  assert.equal(acknowledgements.size, 1)
  assert.deepEqual(reconcilePiActivities([activity('thread', 'working', 4)], acknowledgements), [])
  assert.equal(acknowledgements.size, 1)
})

test('a newer run supersedes an acknowledgement tombstone', () => {
  const finished = activity('thread', 'finished', 5)
  const working = activity('thread', 'working', 6)
  const acknowledgements = new Map([[
    piActivityKey(finished.projectId, finished.threadId),
    { activity: finished, index: 0 },
  ]])

  assert.deepEqual(reconcilePiActivities([working], acknowledgements), [working])
  assert.equal(acknowledgements.size, 0)
})

test('snapshot omission confirms an acknowledgement', () => {
  const finished = activity('thread', 'finished', 5)
  const acknowledgements = new Map([[
    piActivityKey(finished.projectId, finished.threadId),
    { activity: finished, index: 0 },
  ]])

  assert.deepEqual(reconcilePiActivities([], acknowledgements), [])
  assert.equal(acknowledgements.size, 0)
})

test('failed acknowledgement guard lasts only for the same activity generation', () => {
  const finished = activity('thread', 'finished', 5)
  const key = piActivityKey(finished.projectId, finished.threadId)
  const failed = new Map([[key, piActivityVersion(finished)]])

  reconcileFailedPiAcknowledgements([finished], failed)
  assert.equal(failed.size, 1)
  reconcileFailedPiAcknowledgements([activity('thread', 'working', 6)], failed)
  assert.equal(failed.size, 0)

  failed.set(key, piActivityVersion(finished))
  reconcileFailedPiAcknowledgements([], failed)
  assert.equal(failed.size, 0)
})

test('failed acknowledgement rollback restores the original array position', () => {
  const first = activity('first', 'working', 1)
  const restored = activity('restored', 'finished', 2)
  const last = activity('last', 'working', 3)

  assert.deepEqual(
    restoreAcknowledgedPiActivity([first, last], { activity: restored, index: 1 }),
    [first, restored, last],
  )
})

test('failed acknowledgement rollback never overwrites newer activity', () => {
  const finished = activity('thread', 'finished', 2)
  const newerWorking = activity('thread', 'working', 3)

  assert.deepEqual(
    restoreAcknowledgedPiActivity([newerWorking], { activity: finished, index: 0 }),
    [newerWorking],
  )
})
