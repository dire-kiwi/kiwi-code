import type { ReactNode } from 'react'
import { classNames } from '../../lib/classNames'

type SectionHeaderTone = 'green' | 'yellow' | 'blue' | 'magenta'

const iconToneStyles: Record<SectionHeaderTone, string> = {
  green: 'text-ghost-green',
  yellow: 'text-ghost-yellow',
  blue: 'text-ghost-blue',
  magenta: 'text-ghost-magenta',
}

type SectionHeaderProps = {
  icon: ReactNode
  title: string
  description: ReactNode
  tone: SectionHeaderTone
  badge?: ReactNode
}

export function SectionHeader({ icon, title, description, tone, badge }: SectionHeaderProps) {
  return (
    <div className="flex items-start gap-3 border-b border-ghost-border/60 p-4 sm:p-5">
      <span className={classNames(
        'grid size-9 shrink-0 place-items-center rounded-lg bg-ghost-raised',
        iconToneStyles[tone],
      )}>
        {icon}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-sm font-semibold text-ghost-bright-white">{title}</h2>
          {badge}
        </div>
        <p className="mt-1 text-[10px] leading-4 text-ghost-muted">{description}</p>
      </div>
    </div>
  )
}
