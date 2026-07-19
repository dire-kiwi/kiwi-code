import type { ReactNode } from 'react'

type FormScreenTemplateProps = {
  header: ReactNode
  children: ReactNode
}

export function FormScreenTemplate({ header, children }: FormScreenTemplateProps) {
  return (
    <div className="flex h-full min-w-0 flex-col bg-ghost-black">
      {header}
      <main className="relative min-h-0 flex-1 overflow-y-auto px-5 py-10 sm:px-8 sm:py-14">
        <div className="empty-grid pointer-events-none absolute inset-0 opacity-35" />
        {children}
      </main>
    </div>
  )
}
