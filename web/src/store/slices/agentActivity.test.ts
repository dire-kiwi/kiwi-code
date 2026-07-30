import { describe, expect, it } from 'vitest'
import {
  acknowledgementConfirmed,
  acknowledgementFailed,
  acknowledgementInFlight,
  acknowledgementQueued,
  activitiesReceived,
  agentActivitySlice,
  lastFailedAcknowledgement,
  selectPiActivities,
  type AgentActivityState,
} from './agentActivity'
import type { RootState } from '@/store/rootReducer'
import type { PiThreadActivity } from '@/types'

const reduce = agentActivitySlice.reducer

function activity(threadId: string, updatedAt: string, state: 'working' | 'finished' = 'finished'): PiThreadActivity {
  return { projectId: 'p', threadId, state, updatedAt } as PiThreadActivity
}

function root(agentActivity: AgentActivityState) {
  return { agentActivity } as RootState
}

describe('agentActivity slice', () => {
  it('hides an acknowledged row and keeps it hidden across snapshots', () => {
    const item = activity('t1', '2026-07-27T00:01:00Z')
    const received = reduce(undefined, activitiesReceived([item]))
    expect(selectPiActivities(root(received))).toHaveLength(1)

    const queued = reduce(received, acknowledgementQueued({ projectId: 'p', activity: item }))
    expect(selectPiActivities(root(queued))).toHaveLength(0)
    expect(acknowledgementInFlight(root(queued), 'p', 't1')).toBe(true)

    // The server keeps sending the same activity until it processes the delete.
    const resent = reduce(queued, activitiesReceived([item]))
    expect(selectPiActivities(root(resent))).toHaveLength(0)
  })

  it('restores the row and blocks a passive retry at the same version', () => {
    const item = activity('t1', '2026-07-27T00:01:00Z')
    const queued = reduce(
      reduce(undefined, activitiesReceived([item])),
      acknowledgementQueued({ projectId: 'p', activity: item }),
    )
    const failed = reduce(queued, acknowledgementFailed({ projectId: 'p', activity: item }))

    expect(selectPiActivities(root(failed))).toHaveLength(1)
    expect(acknowledgementInFlight(root(failed), 'p', 't1')).toBe(false)
    expect(lastFailedAcknowledgement(root(failed), 'p', 't1')).toBeTruthy()
  })

  it('releases the failed marker once the activity advances', () => {
    const first = activity('t1', '2026-07-27T00:01:00Z')
    const queued = reduce(
      reduce(undefined, activitiesReceived([first])),
      acknowledgementQueued({ projectId: 'p', activity: first }),
    )
    const failed = reduce(queued, acknowledgementFailed({ projectId: 'p', activity: first }))

    const newer = activity('t1', '2026-07-27T00:02:00Z')
    const advanced = reduce(failed, activitiesReceived([newer]))

    // A genuinely newer finish is a new thing to review, so the guard lifts.
    expect(lastFailedAcknowledgement(root(advanced), 'p', 't1')).toBeUndefined()
  })

  it('ignores a failure that belongs to a superseded acknowledgement', () => {
    // The hazard: acknowledgement A is out, a newer snapshot releases its
    // tombstone, the user acknowledges again as B, and only then does A's
    // request fail. Rolling back on A would restore a stale row and discard B's
    // in-flight bookkeeping.
    const first = activity('t1', '2026-07-27T00:01:00Z')
    const newer = activity('t1', '2026-07-27T00:02:00Z')

    const queuedFirst = reduce(
      reduce(undefined, activitiesReceived([first])),
      acknowledgementQueued({ projectId: 'p', activity: first }),
    )
    const released = reduce(queuedFirst, activitiesReceived([newer]))
    const queuedSecond = reduce(released, acknowledgementQueued({ projectId: 'p', activity: newer }))

    const lateFailure = reduce(queuedSecond, acknowledgementFailed({ projectId: 'p', activity: first }))

    expect(acknowledgementInFlight(root(lateFailure), 'p', 't1')).toBe(true)
    expect(selectPiActivities(root(lateFailure))).toHaveLength(0)
    expect(lastFailedAcknowledgement(root(lateFailure), 'p', 't1')).toBeUndefined()
  })

  it('clears the pending entry when the server confirms', () => {
    const item = activity('t1', '2026-07-27T00:01:00Z')
    const queued = reduce(
      reduce(undefined, activitiesReceived([item])),
      acknowledgementQueued({ projectId: 'p', activity: item }),
    )
    const confirmed = reduce(queued, acknowledgementConfirmed({ projectId: 'p', threadId: 't1' }))

    expect(acknowledgementInFlight(root(confirmed), 'p', 't1')).toBe(false)
  })

  it('keeps the same array when a push changes nothing', () => {
    const item = activity('t1', '2026-07-27T00:01:00Z', 'working')
    const first = reduce(undefined, activitiesReceived([item]))
    const again = reduce(first, activitiesReceived([activity('t1', '2026-07-27T00:01:00Z', 'working')]))

    expect(selectPiActivities(root(again))).toBe(selectPiActivities(root(first)))
  })
})
