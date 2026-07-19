import type { ComponentPropsWithoutRef, ElementType } from 'react'
import { classNames } from '../../lib/classNames'

export type SelectableCardLayout = 'option' | 'container'

type SelectableCardOwnProps<T extends ElementType> = {
  as?: T
  selected: boolean
  layout?: SelectableCardLayout
  className?: string
}

export type SelectableCardProps<T extends ElementType = 'label'> = SelectableCardOwnProps<T>
  & Omit<ComponentPropsWithoutRef<T>, keyof SelectableCardOwnProps<T>>

const layoutStyles: Record<SelectableCardLayout, string> = {
  option: 'flex cursor-pointer items-start gap-3 rounded-xl border p-3.5 transition',
  container: 'rounded-xl border transition',
}

export function SelectableCard<T extends ElementType = 'label'>({
  as,
  selected,
  layout = 'option',
  className,
  ...props
}: SelectableCardProps<T>) {
  const Component = as ?? 'label'
  return (
    <Component
      className={classNames(
        layoutStyles[layout],
        selected
          ? 'border-ghost-green/45 bg-ghost-green/[0.07]'
          : 'border-ghost-border/70 bg-ghost-black/25 hover:border-ghost-border hover:bg-ghost-raised/45',
        className,
      )}
      {...props}
    />
  )
}
