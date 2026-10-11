import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectPinningThreadId, selectSettlingThreadId, threadPinned, threadSettled } from '@/store/slices/projects'
import type { Project, Thread } from '@/types'

export function useThreadSettlement() {
  const dispatch = useAppDispatch()
  const settlingThreadId = useAppSelector(selectSettlingThreadId)
  const pinningThreadId = useAppSelector(selectPinningThreadId)

  async function toggleSettlement(project: Project, thread: Thread) {
    if (settlingThreadId) return
    const result = await dispatch(threadSettled({
      projectId: project.id, threadId: thread.id, settled: !thread.settledAt,
    }))
    if (threadSettled.rejected.match(result)) window.alert(result.payload)
  }

  /** Pinned threads are exempt from manual and idle settlement. */
  async function togglePin(project: Project, thread: Thread) {
    if (pinningThreadId) return
    const result = await dispatch(threadPinned({
      projectId: project.id, threadId: thread.id, pinned: !thread.pinned,
    }))
    if (threadPinned.rejected.match(result)) window.alert(result.payload)
  }

  return { settlingThreadId, toggleSettlement, pinningThreadId, togglePin }
}
