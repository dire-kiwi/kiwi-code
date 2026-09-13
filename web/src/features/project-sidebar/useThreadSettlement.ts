import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectSettlingThreadId, threadSettled } from '@/store/slices/projects'
import type { Project, Thread } from '@/types'

export function useThreadSettlement() {
  const dispatch = useAppDispatch()
  const settlingThreadId = useAppSelector(selectSettlingThreadId)

  async function toggleSettlement(project: Project, thread: Thread) {
    if (settlingThreadId) return
    const result = await dispatch(threadSettled({
      projectId: project.id, threadId: thread.id, settled: !thread.settledAt,
    }))
    if (threadSettled.rejected.match(result)) window.alert(result.payload)
  }

  return { settlingThreadId, toggleSettlement }
}
