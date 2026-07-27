import { useCallback, useRef, useState } from 'react'

export type AsyncFeedback = {
  tone: 'error' | 'success'
  message: string
}

type FeedbackCopy = {
  success: string
  failure: string
}

export function useAsyncFeedback<Action extends string = 'default'>() {
  const pendingRef = useRef<Action | null>(null)
  const [pendingAction, setPendingAction] = useState<Action | null>(null)
  const [feedback, setFeedback] = useState<AsyncFeedback | null>(null)

  const clearFeedback = useCallback(() => setFeedback(null), [])
  const showError = useCallback((message: string) => {
    setFeedback({ tone: 'error', message })
  }, [])
  const showSuccess = useCallback((message: string) => {
    setFeedback({ tone: 'success', message })
  }, [])

  const run = useCallback(async <Result>(
    action: Action,
    operation: () => Promise<Result>,
    copy: FeedbackCopy,
  ): Promise<Result | undefined> => {
    if (pendingRef.current !== null) return undefined
    pendingRef.current = action
    setPendingAction(action)
    setFeedback(null)
    try {
      const result = await operation()
      showSuccess(copy.success)
      return result
    } catch (reason) {
      showError(reason instanceof Error ? reason.message : copy.failure)
      return undefined
    } finally {
      pendingRef.current = null
      setPendingAction(null)
    }
  }, [showError, showSuccess])

  return {
    pendingAction,
    pending: pendingAction !== null,
    feedback,
    clearFeedback,
    showError,
    showSuccess,
    run,
  }
}
