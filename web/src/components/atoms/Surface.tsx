import type { ComponentPropsWithoutRef, ElementType } from 'react'
import { classNames } from '../../lib/classNames'

export type SurfaceVariant = 'panel' | 'elevated-panel' | 'dialog' | 'raised-panel'

const surfaceStyles: Record<SurfaceVariant, string> = {
  panel: 'rounded-2xl border border-ghost-border/70 bg-ghost-panel/95',
  'elevated-panel': 'rounded-2xl border border-ghost-border/70 bg-ghost-panel/95 shadow-[0_24px_80px_rgba(29,31,33,0.42)]',
  dialog: 'rounded-xl border border-ghost-border/80 bg-ghost-panel shadow-[0_24px_80px_rgba(0,0,0,0.55)]',
  'raised-panel': 'rounded-2xl border border-ghost-border/80 bg-ghost-panel shadow-2xl shadow-ghost-black/55',
}

type SurfaceOwnProps<T extends ElementType> = {
  as?: T
  variant?: SurfaceVariant
  className?: string
}

export type SurfaceProps<T extends ElementType = 'div'> = SurfaceOwnProps<T>
  & Omit<ComponentPropsWithoutRef<T>, keyof SurfaceOwnProps<T>>

/** Polymorphic styled surface that can remain a div, form, or other semantic element. */
export function Surface<T extends ElementType = 'div'>({
  as,
  variant = 'panel',
  className,
  ...props
}: SurfaceProps<T>) {
  const Component = as ?? 'div'
  return <Component className={classNames(surfaceStyles[variant], className)} {...props} />
}
