import { Activity, RefreshCw } from 'lucide-react'
import type { ProcessWindow } from '../../types'
import { Button } from '../atoms/Button'
import { SelectionButton } from '../atoms/SelectionButton'

type ProcessWindowTabsProps = {
  windows: ProcessWindow[]
  selectedId: string | null
  loading: boolean
  error: string
  onSelect: (id: string) => void
  onRetry: () => void
}

export function ProcessWindowTabs({
  windows,
  selectedId,
  loading,
  error,
  onSelect,
  onRetry,
}: ProcessWindowTabsProps) {
  return (
    <div className="flex h-9 shrink-0 items-center gap-2 bg-ghost-background px-3 sm:px-5">
      <span className="hidden shrink-0 items-center gap-1.5 font-mono text-[8px] font-medium uppercase tracking-[0.16em] text-ghost-faint sm:flex">
        <Activity size={10} />
        processes
      </span>

      <div
        className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
        role="tablist"
        aria-label="Agent process shells"
        aria-busy={loading}
      >
        {windows.map((window) => {
          const active = window.id === selectedId
          return (
            <SelectionButton
              key={window.id}
              type="button"
              selected={active}
              selectionVariant="compact-tab"
              role="tab"
              aria-selected={active}
              onClick={() => onSelect(window.id)}
              title={window.currentCommand || window.name}
            >
              <span className={active ? 'text-ghost-green/80' : 'text-ghost-faint'}>{window.index}</span>
              <span className="max-w-36 truncate">{window.name}</span>
              {window.currentCommand && (
                <span className="max-w-24 truncate text-[8px] text-ghost-faint">{window.currentCommand}</span>
              )}
            </SelectionButton>
          )
        })}
        {windows.length === 0 && !error && (
          <span className="font-mono text-[9px] text-ghost-faint">
            {loading ? 'Loading process shells…' : 'No agent-created process shells'}
          </span>
        )}
      </div>

      {error && (
        <Button
          type="button"
          onClick={onRetry}
          className="flex shrink-0 items-center gap-1 font-mono text-[8px] text-ghost-bright-red/80 transition hover:text-ghost-red"
          title={error}
        >
          <RefreshCw size={9} />
          retry
        </Button>
      )}
    </div>
  )
}
