import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import {
  piActivityKey,
  piActivityVersion,
  reconcileFailedPiAcknowledgements,
  reconcilePiActivities,
  restoreAcknowledgedPiActivity,
  samePiActivities,
  type PiActivityAcknowledgement,
} from '@/pi-activity-reconciliation.mjs'
import type { RootState } from '@/store/rootReducer'
import type { PiThreadActivity } from '@/types'

// Which threads have a coding agent working or finished, and the bookkeeping for
// acknowledging a finished one.
//
// Acknowledgement is optimistic: the row disappears the moment you interact with
// the thread, and comes back if the server call fails. That needed two Maps and
// three refs in App to survive socket callbacks without going stale. As slice
// state the reducers see the current value by construction.
//
// Maps become plain records here: RTK's serializability check rejects Map, and
// these are keyed by `${projectId}\0${threadId}` anyway.
export type AgentActivityState = {
  activities: PiThreadActivity[]
  /** In flight: removed from the list, not yet confirmed by the server. */
  pending: Record<string, PiActivityAcknowledgement>
  /** Failed once at this version; not retried until the user asks again. */
  failed: Record<string, string>
}

export const initialAgentActivityState: AgentActivityState = {
  activities: [],
  pending: {},
  failed: {},
}

export const agentActivitySlice = createSlice({
  name: 'agentActivity',
  initialState: initialAgentActivityState,
  reducers: {
    // A socket push, filtered through whatever is currently in flight so an
    // acknowledged row does not flicker back before the server agrees.
    //
    // The reconciliation helpers take Maps and mutate them in place. That module
    // is shared and has its own tests under web/scripts, so rather than change
    // its contract the records are lifted to Maps here and written back.
    activitiesReceived(state, action: PayloadAction<PiThreadActivity[]>) {
      const next = action.payload

      const failed = new Map(Object.entries(state.failed))
      reconcileFailedPiAcknowledgements(next, failed)
      state.failed = Object.fromEntries(failed)

      const pending = new Map(Object.entries(state.pending))
      const visible = reconcilePiActivities(next, pending)
      state.pending = Object.fromEntries(pending)

      if (!samePiActivities(state.activities, visible)) state.activities = visible
    },

    // Returns nothing: the caller checks `pending` itself before dispatching so
    // it knows whether to fire the request.
    acknowledgementQueued(
      state,
      action: PayloadAction<{ projectId: string; activity: PiThreadActivity }>,
    ) {
      const { projectId, activity } = action.payload
      const key = piActivityKey(projectId, activity.threadId)
      if (state.pending[key]) return

      delete state.failed[key]
      state.pending[key] = {
        activity,
        index: state.activities.findIndex((item) =>
          item.projectId === projectId && item.threadId === activity.threadId),
      }
      state.activities = state.activities.filter((item) =>
        item.projectId !== projectId || item.threadId !== activity.threadId)
    },

    acknowledgementConfirmed(
      state,
      action: PayloadAction<{ projectId: string; threadId: string }>,
    ) {
      delete state.pending[piActivityKey(action.payload.projectId, action.payload.threadId)]
    },

    // Puts the row back where it was and records the version, so a passive
    // interaction does not retry the same failing call forever.
    acknowledgementFailed(
      state,
      action: PayloadAction<{ projectId: string; activity: PiThreadActivity }>,
    ) {
      const { projectId, activity } = action.payload
      const key = piActivityKey(projectId, activity.threadId)
      const acknowledgement = state.pending[key]
      if (!acknowledgement) return
      // The entry has to be the one this failure belongs to. A newer snapshot can
      // release the tombstone and a second acknowledgement can take its place
      // while the first request is still out; rolling that one back would restore
      // a stale row and drop a live request's bookkeeping.
      if (piActivityVersion(acknowledgement.activity) !== piActivityVersion(activity)) return

      delete state.pending[key]
      state.failed[key] = piActivityVersion(activity)
      const restored = restoreAcknowledgedPiActivity(state.activities, acknowledgement)
      if (!samePiActivities(state.activities, restored)) state.activities = restored
    },
  },
})

export const {
  acknowledgementConfirmed,
  acknowledgementFailed,
  acknowledgementQueued,
  activitiesReceived,
} = agentActivitySlice.actions

export const selectPiActivities = (state: RootState) => state.agentActivity.activities

/** True when this thread already has an acknowledgement in flight. */
export function acknowledgementInFlight(state: RootState, projectId: string, threadId: string) {
  return Boolean(state.agentActivity.pending[piActivityKey(projectId, threadId)])
}

/** The version that last failed, so a caller can decide whether to retry. */
export function lastFailedAcknowledgement(state: RootState, projectId: string, threadId: string) {
  return state.agentActivity.failed[piActivityKey(projectId, threadId)]
}
