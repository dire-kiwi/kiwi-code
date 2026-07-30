import { Check } from 'lucide-react'
import type { AsyncFeedback } from '@/lib/useAsyncFeedback'
import { FeedbackMessage } from './FeedbackMessage'

type ActionFeedbackProps = {
  feedback: AsyncFeedback | null
  id?: string
  className?: string
}

export function ActionFeedback({ feedback, id, className }: ActionFeedbackProps) {
  if (!feedback) return null
  const success = feedback.tone === 'success'
  return (
    <FeedbackMessage
      id={id}
      role={success ? 'status' : 'alert'}
      tone={feedback.tone}
      size={success ? 'status' : 'md'}
      className={success ? `flex items-center gap-2 ${className ?? ''}` : className}
    >
      {success && <Check size={13} className="shrink-0" />}
      {feedback.message}
    </FeedbackMessage>
  )
}
