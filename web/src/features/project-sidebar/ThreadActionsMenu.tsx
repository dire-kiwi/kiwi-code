import { useEffect, useRef } from 'react'
import { CircleCheck, RotateCcw, EllipsisVertical, LoaderCircle, Trash2 } from 'lucide-react'
import { Button, IconButton } from '@/ui/buttons'

type ThreadActionsMenuProps = {
  threadTitle: string
  settled?: boolean
  working?: boolean
  settling?: boolean
  onSettle?: () => void
  deleting: boolean
  /** Blocks every action while another thread mutation is still in flight. */
  disabled: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
  onDelete: () => void
  triggerClassName?: string
}

/**
 * Settle/un-settle and delete actions for a single sidebar thread row. The open
 * row is owned by the caller so only one menu can be open across a view.
 */
export function ThreadActionsMenu({
  threadTitle,
  settled,
  working,
  settling,
  onSettle,
  deleting,
  disabled,
  open,
  onOpenChange,
  onDelete,
  triggerClassName,
}: ThreadActionsMenuProps) {
  const onOpenChangeRef = useRef(onOpenChange)
  onOpenChangeRef.current = onOpenChange

  useEffect(() => {
    if (!open) return

    function handlePointerDown(event: PointerEvent) {
      const target = event.target
      if (target instanceof Element && target.closest('[data-thread-menu]')) return
      onOpenChangeRef.current(false)
    }

    document.addEventListener('pointerdown', handlePointerDown, true)
    return () => document.removeEventListener('pointerdown', handlePointerDown, true)
  }, [open])

  return (
    <div className="relative flex items-center gap-0.5" data-thread-menu>
      {onSettle && (
        <IconButton type="button" size="xs" variant="subtle" disabled={disabled || (!settled && working)}
          onClick={onSettle} aria-label={`${settled ? 'Un-settle' : 'Settle'} ${threadTitle}`}
          title={!settled && working ? 'Wait for the agent to finish' : settled ? 'Un-settle thread' : 'Settle thread'}
          className={triggerClassName}>
          {settling ? <LoaderCircle size={12} className="animate-spin" /> : settled ? <RotateCcw size={12} /> : <CircleCheck size={12} />}
        </IconButton>
      )}
      <IconButton
        type="button"
        size="xs"
        variant="subtle"
        onClick={() => onOpenChange(!open)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Actions for ${threadTitle}`}
        title="Thread actions"
        className={triggerClassName}
      >
        <EllipsisVertical size={12} />
      </IconButton>
      {open && (
        <div
          role="menu"
          aria-label={`Actions for ${threadTitle}`}
          onKeyDown={(event) => {
            if (event.key !== 'Escape') return
            event.stopPropagation()
            onOpenChange(false)
          }}
          className="absolute right-0 top-[calc(100%+2px)] z-30 w-40 rounded-lg border border-ghost-border/90 bg-ghost-panel p-1 shadow-2xl"
        >
          {onSettle && (
            <Button role="menuitem" type="button" variant="subtle" disabled={disabled || (!settled && working)}
              onClick={() => { onOpenChange(false); onSettle() }}
              className="flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[11px]">
              {settled ? <RotateCcw size={12} /> : <CircleCheck size={12} />}
              {settled ? 'Un-settle thread' : 'Settle thread'}
            </Button>
          )}
          <Button
            role="menuitem"
            type="button"
            variant="danger"
            disabled={disabled}
            onClick={() => {
              onOpenChange(false)
              onDelete()
            }}
            className="flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[11px]"
          >
            {deleting ? <LoaderCircle size={12} className="animate-spin" /> : <Trash2 size={12} />}
            Delete thread
          </Button>
        </div>
      )}
    </div>
  )
}
