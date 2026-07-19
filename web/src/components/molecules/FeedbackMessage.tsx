import type { ComponentPropsWithoutRef } from 'react'
import { classNames } from '../../lib/classNames'

export type FeedbackTone = 'error' | 'success'
export type FeedbackSize = 'sm' | 'md' | 'status'

const toneStyles: Record<FeedbackTone, string> = {
  error: 'border-ghost-red/25 bg-ghost-red/[0.07] text-ghost-bright-red',
  success: 'border-ghost-green/25 bg-ghost-green/[0.07] text-ghost-green',
}

const sizeStyles: Record<FeedbackSize, string> = {
  sm: 'text-[10px] leading-4',
  md: 'text-[11px] leading-4',
  status: 'text-[11px]',
}

type FeedbackMessageProps = ComponentPropsWithoutRef<'p'> & {
  tone: FeedbackTone
  size?: FeedbackSize
}

export function FeedbackMessage({
  tone,
  size = 'md',
  className,
  ...props
}: FeedbackMessageProps) {
  return (
    <p
      className={classNames(
        'rounded-lg border px-3 py-2',
        toneStyles[tone],
        sizeStyles[size],
        className,
      )}
      {...props}
    />
  )
}
