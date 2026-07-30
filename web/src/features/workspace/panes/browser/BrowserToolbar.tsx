import type { FormEvent, RefObject } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  Globe2,
  LoaderCircle,
  RefreshCw,
  RotateCw,
  X,
} from 'lucide-react'
import type { BrowserActionOperation } from '@/types'
import type { BrowserCurrentPage } from '@/wire/domain'
import { IconButton } from '@/ui/buttons'
import { StatusBadge, type StatusBadgeTone } from '@/ui/feedback'
import { BaseInput } from '@/ui/inputs'

export type BrowserToolbarProps = {
  /** The pane's single action dispatcher; every button here goes through it. */
  runAction: (operation: BrowserActionOperation, params?: Record<string, unknown>) => void
  busyOperation: BrowserActionOperation | null
  currentPage: BrowserCurrentPage | null
  currentLoading: boolean
  statusLoading: boolean
  frameLoading: boolean
  providerUnavailable: boolean
  noSession: boolean

  addressRef: RefObject<HTMLInputElement | null>
  address: string
  onAddressChange: (value: string) => void
  onAddressBlur: () => void
  onSubmitAddress: (event: FormEvent<HTMLFormElement>) => void


  backendTone: StatusBadgeTone
  backendLabel: string
  viewModeLabel: string
  viewModeActive: boolean

  onRetryAll: () => void
}

export function BrowserToolbar({
  runAction,
  busyOperation,
  currentPage,
  currentLoading,
  statusLoading,
  frameLoading,
  providerUnavailable,
  noSession,
  addressRef,
  address,
  onAddressChange,
  onAddressBlur,
  onSubmitAddress,
  backendTone,
  backendLabel,
  viewModeLabel,
  viewModeActive,
  onRetryAll,
}: BrowserToolbarProps) {
  const busy = Boolean(busyOperation)

  return (
    <div
      className="flex min-h-12 shrink-0 items-center gap-1.5 border-b border-ghost-border/65 bg-ghost-panel/95 px-2 py-1.5 sm:px-3"
      role="toolbar"
      aria-label="Browser navigation"
    >
      <div className="flex shrink-0 items-center gap-0.5">
        <IconButton
          type="button"
          size="md"
          variant="subtle"
          disabled={busy || !currentPage || currentPage.canGoBack === false}
          onClick={() => runAction('navigate.back')}
          aria-label="Go back"
          title="Back"
        >
          <ArrowLeft size={15} />
        </IconButton>
        <IconButton
          type="button"
          size="md"
          variant="subtle"
          disabled={busy || !currentPage || currentPage.canGoForward === false}
          onClick={() => runAction('navigate.forward')}
          aria-label="Go forward"
          title="Forward"
        >
          <ArrowRight size={15} />
        </IconButton>
        <IconButton
          type="button"
          size="md"
          variant="subtle"
          disabled={busy || !currentPage}
          onClick={() => runAction('navigate.reload')}
          aria-label="Reload page"
          title="Reload"
        >
          <RotateCw size={14} className={busyOperation === 'navigate.reload' ? 'animate-spin' : ''} />
        </IconButton>
      </div>

      <form onSubmit={onSubmitAddress} className="relative min-w-0 flex-1">
        <Globe2
          size={13}
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ghost-dim"
        />
        <BaseInput
          ref={addressRef}
          type="text"
          inputMode="url"
          value={address}
          onChange={(event) => onAddressChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' || event.nativeEvent.isComposing) return
            event.preventDefault()
            event.currentTarget.form?.requestSubmit()
          }}
          onBlur={onAddressBlur}
          disabled={busy || statusLoading || providerUnavailable}
          aria-label="Browser address"
          autoCapitalize="none"
          autoCorrect="off"
          autoComplete="off"
          spellCheck={false}
          placeholder="Enter a URL or search"
          className="h-8 w-full rounded-lg border border-ghost-border/80 bg-ghost-black/45 pl-8 pr-8 font-mono text-[10px] text-ghost-bright-white outline-none transition placeholder:text-ghost-faint focus:border-ghost-green/55 focus:ring-2 focus:ring-ghost-green/10 disabled:cursor-wait disabled:opacity-70"
        />
        {(busyOperation === 'navigate.goto' || busyOperation === 'session.start' || currentLoading) && (
          <LoaderCircle
            size={12}
            className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 animate-spin text-ghost-green"
          />
        )}
      </form>

      <div className="hidden shrink-0 items-center gap-1 lg:flex" aria-label="Browser backend status">
        <StatusBadge tone={backendTone}>{backendLabel}</StatusBadge>
        <StatusBadge tone={viewModeActive ? 'info' : 'neutral'}>{viewModeLabel}</StatusBadge>
      </div>
      <IconButton
        type="button"
        size="md"
        variant="subtle"
        shrink
        onClick={onRetryAll}
        disabled={statusLoading || frameLoading}
        aria-label="Refresh browser status and preview"
        title="Refresh browser status"
      >
        <RefreshCw size={13} className={statusLoading || frameLoading ? 'animate-spin' : ''} />
      </IconButton>
      <IconButton
        type="button"
        size="md"
        variant="danger"
        shrink
        disabled={busy || noSession || providerUnavailable}
        onClick={() => {
          if (window.confirm('Close this thread’s browser session?')) runAction('session.stop')
        }}
        aria-label="Close browser session"
        title="Close browser session"
      >
        <X size={14} />
      </IconButton>
    </div>
  )
}
