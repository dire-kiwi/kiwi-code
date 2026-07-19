import type { ReactNode } from 'react'
import { classNames } from '../../lib/classNames'

export type StatusBadgeTone = 'neutral' | 'success' | 'warning' | 'error' | 'info'

const toneStyles: Record<StatusBadgeTone, string> = {
  neutral: 'border-ghost-border/70 text-ghost-dim',
  success: 'border-ghost-green/30 bg-ghost-green/[0.07] text-ghost-green',
  warning: 'border-ghost-yellow/30 bg-ghost-yellow/[0.07] text-ghost-yellow',
  error: 'border-ghost-bright-red/30 bg-ghost-bright-red/[0.07] text-ghost-bright-red',
  info: 'border-ghost-blue/30 bg-ghost-blue/[0.07] text-ghost-blue',
}

type StatusBadgeProps = {
  children: ReactNode
  tone?: StatusBadgeTone
  monospace?: boolean
  className?: string
}

export function StatusBadge({
  children,
  tone = 'neutral',
  monospace = false,
  className,
}: StatusBadgeProps) {
  return (
    <span className={classNames(
      'rounded-full border px-2 py-0.5 text-[8px]',
      monospace
        ? 'font-mono'
        : 'font-semibold uppercase tracking-[0.1em]',
      toneStyles[tone],
      className,
    )}>
      {children}
    </span>
  )
}
