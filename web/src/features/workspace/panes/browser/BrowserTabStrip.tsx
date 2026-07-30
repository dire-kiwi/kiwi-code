import { Circle, Globe2, Plus, X } from 'lucide-react'
import { Button, IconButton } from '@/ui/buttons'
import type { BrowserPage } from '@/wire/domain'
import { pageLabel } from './browserHelpers'

export type BrowserTabStripProps = {
  pages: BrowserPage[]
  selectedTargetId: string | undefined
  /** The tab being recorded, which must not be closed out from under the recorder. */
  recordingTargetId: string | undefined
  busy: boolean
  statusLoading: boolean
  providerUnavailable: boolean
  sessionRunning: boolean
  onSelectTab: (targetId: string) => void
  onCloseTab: (targetId: string) => void
  onNewTab: () => void
}

export function BrowserTabStrip({
  pages,
  selectedTargetId,
  recordingTargetId,
  busy,
  statusLoading,
  providerUnavailable,
  sessionRunning,
  onSelectTab,
  onCloseTab,
  onNewTab,
}: BrowserTabStripProps) {
  return (
    <div className="flex h-9 shrink-0 items-center border-b border-ghost-border/65 bg-ghost-panel/80 px-2">
      <div
        className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
        role="toolbar"
        aria-label="Browser tabs"
      >
        {pages.map((page) => {
          const selected = page.id === selectedTargetId
          const recorded = page.id === recordingTargetId
          const label = pageLabel(page)
          return (
            <div
              key={page.id}
              className={`group flex h-7 min-w-[8rem] max-w-[15rem] shrink-0 items-center rounded-md border ${
                selected
                  ? 'border-ghost-border/85 bg-ghost-raised text-ghost-bright-white'
                  : 'border-transparent text-ghost-dim hover:bg-ghost-raised/55 hover:text-ghost-white'
              }`}
            >
              <Button
                type="button"
                aria-pressed={selected}
                aria-controls="browser-guest-rectangle"
                aria-label={`Select tab ${label}`}
                disabled={busy || providerUnavailable}
                onClick={() => {
                  if (!selected) onSelectTab(page.id)
                }}
                className="flex h-full min-w-0 flex-1 items-center gap-2 pl-2.5 text-left disabled:cursor-wait"
                title={page.url || label}
              >
                {recorded
                  ? <Circle size={10} fill="currentColor" className="shrink-0 animate-pulse text-ghost-bright-red" />
                  : <Globe2 size={11} className={selected ? 'shrink-0 text-ghost-green' : 'shrink-0'} />}
                <span className="truncate text-[10px] font-medium">{label}</span>
              </Button>
              <IconButton
                type="button"
                size="xs"
                variant="subtle"
                disabled={busy || providerUnavailable || recorded}
                onClick={() => onCloseTab(page.id)}
                aria-label={recorded ? `Stop recording before closing tab ${label}` : `Close tab ${label}`}
                title={recorded ? 'Stop recording before closing this tab' : `Close ${label}`}
                className="mr-0.5 opacity-70 group-hover:opacity-100 focus:opacity-100 disabled:cursor-wait"
              >
                <X size={10} />
              </IconButton>
            </div>
          )
        })}
        <IconButton
          type="button"
          size="sm"
          variant="subtle"
          shrink
          disabled={busy || statusLoading || providerUnavailable}
          onClick={onNewTab}
          aria-label="New browser tab"
          title="New browser tab"
        >
          <Plus size={13} />
        </IconButton>
      </div>
    </div>
  )
}
