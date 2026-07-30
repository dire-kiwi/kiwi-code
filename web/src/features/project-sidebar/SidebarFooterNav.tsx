import { Clock3, History, LoaderCircle, PanelsTopLeft, RotateCw, Settings2 } from 'lucide-react'
import { restartApplication, waitForApplicationRestart } from '@/api'
import { reloadFrontend } from '@/frontend-reload.mjs'
import { useState } from 'react'
import { Button, SelectionButton } from '@/ui/buttons'

export type SidebarFooterNavProps = {
  cleanupSelected: boolean
  sessionLogSelected: boolean
  tmuxSelected: boolean
  settingsSelected: boolean
  onOpenCleanup: () => void
  onOpenSessionLog: () => void
  onOpenTmux: () => void
  onOpenSettings: () => void
}

export function SidebarFooterNav({
  cleanupSelected,
  sessionLogSelected,
  tmuxSelected,
  settingsSelected,
  onOpenCleanup,
  onOpenSessionLog,
  onOpenTmux,
  onOpenSettings,
}: SidebarFooterNavProps) {
  const [restarting, setRestarting] = useState(false)

  async function handleRestart() {
    if (restarting || !window.confirm('Restart Kiwi Code?\n\nThe application will fully exit before a fresh instance starts. Your tmux sessions and running tools will keep running.')) return

    setRestarting(true)
    try {
      const response = await restartApplication()
      // The new instance reports a different id; waiting for it is what tells
      // us the reload will land on the replacement rather than the corpse.
      await waitForApplicationRestart(response.instanceId)
      await reloadFrontend()
    } catch (reason) {
      setRestarting(false)
      window.alert(reason instanceof Error ? reason.message : 'Could not restart Kiwi Code.')
    }
  }

  return (
    <div className="shrink-0 space-y-0.5 border-t border-ghost-border/70 bg-ghost-panel/25 p-2">
      <SelectionButton
        type="button"
        selected={cleanupSelected}
        selectionVariant="navigation-compact"
        onClick={onOpenCleanup}
        aria-current={cleanupSelected ? 'page' : undefined}
      >
        <Clock3 size={13} className={cleanupSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
        <span>Cleanup</span>
      </SelectionButton>
      <SelectionButton
        type="button"
        selected={sessionLogSelected}
        selectionVariant="navigation-compact"
        onClick={onOpenSessionLog}
        aria-current={sessionLogSelected ? 'page' : undefined}
      >
        <History size={13} className={sessionLogSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
        <span>Session log</span>
      </SelectionButton>
      <SelectionButton
        type="button"
        selected={tmuxSelected}
        selectionVariant="navigation-compact"
        onClick={onOpenTmux}
        aria-current={tmuxSelected ? 'page' : undefined}
      >
        <PanelsTopLeft size={13} className={tmuxSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
        <span>tmux</span>
      </SelectionButton>
      <div className="flex items-center gap-0.5">
        <div className="min-w-0 flex-1">
          <SelectionButton
            type="button"
            selected={settingsSelected}
            selectionVariant="navigation-compact"
            onClick={onOpenSettings}
            aria-current={settingsSelected ? 'page' : undefined}
          >
            <Settings2 size={13} className={settingsSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
            <span>Settings</span>
          </SelectionButton>
        </div>
        <Button
          type="button"
          variant="subtle"
          onClick={() => void handleRestart()}
          disabled={restarting}
          className="grid size-8 shrink-0 place-items-center rounded-md disabled:cursor-wait disabled:opacity-60"
          aria-label={restarting ? 'Restarting Kiwi Code' : 'Restart Kiwi Code'}
          title={restarting ? 'Restarting Kiwi Code…' : 'Restart Kiwi Code'}
        >
          {restarting
            ? <LoaderCircle size={13} className="animate-spin text-ghost-green" />
            : <RotateCw size={13} className="text-ghost-dim" />}
        </Button>
      </div>
    </div>
  )
}
