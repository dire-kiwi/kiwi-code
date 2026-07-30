import { acknowledgePiThreadActivity } from '@/api'
import { piActivityVersion } from '@/pi-activity-reconciliation.mjs'
import type { AppDispatch } from '@/store'
import type { RootState } from '@/store/rootReducer'
import { selectThreadIndex } from '@/store/selectors/workspace'
import {
  acknowledgementConfirmed,
  acknowledgementFailed,
  acknowledgementInFlight,
  acknowledgementQueued,
  lastFailedAcknowledgement,
} from '@/store/slices/agentActivity'

// Lives here rather than beside the slice because it needs selectThreadIndex,
// which is built from the projects slice as well -- importing that from inside
// agentActivity.ts would close an import cycle.

/**
 * Clears the "needs review" marker for a thread the user has just interacted
 * with. Optimistic: the row goes immediately and comes back if the server
 * refuses.
 *
 * `retryFailed` distinguishes an explicit act (clicking the thread) from a
 * passive one (typing in a pane that happens to be open). A passive interaction
 * must not retry a call that already failed at this version, or a broken server
 * produces one request per keystroke.
 */
// A plain thunk rather than createAsyncThunk on purpose. Nothing consumes a
// pending/fulfilled lifecycle here, and this fires from onWheelCapture and
// onKeyDownCapture -- emitting two actions per wheel tick would run every
// subscriber's selector dozens of times a second while scrolling a transcript.
// With nothing to acknowledge, this now dispatches nothing at all.
export function threadActivityAcknowledged({
  projectId,
  threadId,
  retryFailed = false,
}: {
  projectId: string
  threadId: string
  retryFailed?: boolean
}) {
  return (dispatch: AppDispatch, getState: () => RootState) => {
    const activities = selectThreadIndex(getState()).finishedActivities(projectId, threadId)

    for (const activity of activities) {
      const state = getState()
      if (acknowledgementInFlight(state, projectId, activity.threadId)) continue
      const version = piActivityVersion(activity)
      if (!retryFailed && lastFailedAcknowledgement(state, projectId, activity.threadId) === version) continue

      dispatch(acknowledgementQueued({ projectId, activity }))
      void acknowledgePiThreadActivity(projectId, activity.threadId).then(
        () => dispatch(acknowledgementConfirmed({ projectId, threadId: activity.threadId })),
        () => dispatch(acknowledgementFailed({ projectId, activity })),
      )
    }
  }
}
