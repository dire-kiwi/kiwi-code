import { useState } from 'react'
import { Plus, RefreshCw } from 'lucide-react'
import { createShellWindow, selectShellWindow } from '../../api'
import type { TmuxWindow } from '../../types'
import { Button } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { SelectionButton } from '../atoms/SelectionButton'

type TmuxWindowTabsProps = {
  projectId: string
  threadId: string
  windows: TmuxWindow[]
  loading: boolean
  error: string
  onWindowsChange: (windows: TmuxWindow[]) => void
  onRetry: () => void
}

type PendingAction = number | 'create' | null

const copy = {
  label: 'tmux',
  tabListLabel: 'Shell tmux tabs',
  loadingCopy: 'Loading shell tabs…',
  emptyCopy: 'No shell tabs open',
  newTabLabel: 'New shell tab',
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : 'Could not update tmux tabs.'
}

export function TmuxWindowTabs({
  projectId,
  threadId,
  windows,
  loading,
  error,
  onWindowsChange,
  onRetry,
}: TmuxWindowTabsProps) {
  const [pending, setPending] = useState<PendingAction>(null)
  const [actionError, setActionError] = useState('')
  const visibleError = actionError || error

  async function createWindow() {
    if (pending !== null) return
    setPending('create')
    setActionError('')
    try {
      onWindowsChange(await createShellWindow(projectId, threadId))
    } catch (reason) {
      setActionError(errorMessage(reason))
    } finally {
      setPending(null)
    }
  }

  async function selectWindow(index: number) {
    if (pending !== null || windows.find((item) => item.index === index)?.active) return
    setPending(index)
    setActionError('')
    try {
      onWindowsChange(await selectShellWindow(projectId, threadId, index))
    } catch (reason) {
      setActionError(errorMessage(reason))
    } finally {
      setPending(null)
    }
  }

  return (
    <div className="flex h-9 shrink-0 items-center gap-2 bg-ghost-background px-3 sm:px-5">
      <span className="hidden shrink-0 font-mono text-[8px] font-medium uppercase tracking-[0.16em] text-ghost-faint sm:inline">
        {copy.label}
      </span>

      <div
        className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
        role="tablist"
        aria-label={copy.tabListLabel}
        aria-busy={loading}
      >
        {windows.map((item) => {
          const active = item.active
          const selecting = pending === item.index
          return (
            <SelectionButton
              key={item.index}
              type="button"
              selected={active}
              selectionVariant="compact-tab"
              role="tab"
              aria-selected={active}
              disabled={pending !== null}
              onClick={() => void selectWindow(item.index)}
            >
              <span className={active ? 'text-ghost-green/80' : 'text-ghost-faint'}>{item.index}</span>
              <span className="max-w-32 truncate">{item.name}</span>
              {selecting && <RefreshCw size={9} className="animate-spin" />}
            </SelectionButton>
          )
        })}
        {windows.length === 0 && !visibleError && (
          <span className="font-mono text-[9px] text-ghost-faint">
            {loading ? copy.loadingCopy : copy.emptyCopy}
          </span>
        )}
      </div>

      {visibleError && (
        <Button
          type="button"
          onClick={() => {
            setActionError('')
            onRetry()
          }}
          className="shrink-0 font-mono text-[8px] text-ghost-bright-red/80 transition hover:text-ghost-red"
          title={visibleError}
        >
          retry
        </Button>
      )}
      <IconButton
        type="button"
        size="xs"
        shrink
        variant="accent-outline"
        onClick={() => void createWindow()}
        disabled={pending !== null}
        className="disabled:cursor-wait disabled:opacity-60"
        aria-label={copy.newTabLabel}
        title={copy.newTabLabel}
      >
        {pending === 'create' ? <RefreshCw size={11} className="animate-spin" /> : <Plus size={12} />}
      </IconButton>
    </div>
  )
}
