import type { ReactNode } from 'react'

type PageIntroProps = {
  icon: ReactNode
  title: string
  children: ReactNode
}

export function PageIntro({ icon, title, children }: PageIntroProps) {
  return (
    <div className="mb-8">
      <div className="grid size-11 place-items-center rounded-xl border border-ghost-green/25 bg-ghost-green/[0.08] text-ghost-green shadow-[0_0_30px_rgba(181,189,104,0.08)]">
        {icon}
      </div>
      <h1 className="mt-5 text-xl font-semibold tracking-[-0.025em] text-ghost-foreground">
        {title}
      </h1>
      <p className="mt-2 max-w-lg text-xs leading-5 text-ghost-muted">
        {children}
      </p>
    </div>
  )
}
