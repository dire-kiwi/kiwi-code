import type { ReactNode } from 'react'
import { Info } from 'lucide-react'
import { classNames } from '../../lib/classNames'

type InfoCalloutProps = {
  children: ReactNode
  className?: string
}

export function InfoCallout({ children, className }: InfoCalloutProps) {
  return (
    <div className={classNames(
      'flex items-start gap-2.5 rounded-xl border border-ghost-blue/20 bg-ghost-blue/[0.05] px-3.5 py-3 text-ghost-muted',
      className,
    )}>
      <Info size={14} className="mt-0.5 shrink-0 text-ghost-blue" />
      <div className="text-[10px] leading-4">{children}</div>
    </div>
  )
}
