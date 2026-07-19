import { forwardRef } from 'react'
import { classNames } from '../../lib/classNames'
import { Button, type ButtonProps } from './Button'

export type SelectionButtonVariant =
  | 'navigation'
  | 'navigation-compact'
  | 'menu-item'
  | 'compact-tab'

type SelectionStyle = {
  base: string
  selected: string
  idle: string
}

const selectionStyles: Record<SelectionButtonVariant, SelectionStyle> = {
  navigation: {
    base: 'flex h-8 w-full items-center gap-1.5 rounded-md py-1 pl-8 pr-9 text-left text-[11px] leading-4 transition-colors',
    selected: 'bg-ghost-selected/80 text-ghost-bright-white ring-1 ring-inset ring-ghost-border/60',
    idle: 'text-ghost-muted hover:bg-ghost-raised/45 hover:text-ghost-white',
  },
  'navigation-compact': {
    base: 'flex h-8 w-full items-center gap-2.5 rounded-md px-2 text-[10px] font-medium transition-colors',
    selected: 'bg-ghost-selected/80 text-ghost-bright-white ring-1 ring-inset ring-ghost-border/60',
    idle: 'text-ghost-dim hover:bg-ghost-raised/45 hover:text-ghost-white',
  },
  'menu-item': {
    base: 'flex h-9 w-full items-center gap-2 rounded-lg px-2.5 text-left font-mono text-[10px] transition disabled:cursor-wait',
    selected: 'bg-ghost-green/[0.09] text-ghost-bright-white',
    idle: 'text-ghost-muted hover:bg-ghost-raised hover:text-ghost-bright-white',
  },
  'compact-tab': {
    base: 'flex h-6 shrink-0 items-center gap-1.5 rounded-md px-2 font-mono text-[9px] transition disabled:cursor-wait disabled:opacity-60',
    selected: 'bg-ghost-green/[0.13] text-ghost-bright-green shadow-[inset_0_0_0_1px_rgba(181,189,104,0.22)]',
    idle: 'text-ghost-dim hover:bg-ghost-raised/75 hover:text-ghost-bright-white',
  },
}

export type SelectionButtonProps = ButtonProps & {
  selected: boolean
  selectionVariant: SelectionButtonVariant
}

export const SelectionButton = forwardRef<HTMLButtonElement, SelectionButtonProps>(
  function SelectionButton({
    selected,
    selectionVariant,
    className,
    ...props
  }, ref) {
    const styles = selectionStyles[selectionVariant]
    return (
      <Button
        ref={ref}
        className={classNames(
          styles.base,
          selected ? styles.selected : styles.idle,
          className,
        )}
        {...props}
      />
    )
  },
)
