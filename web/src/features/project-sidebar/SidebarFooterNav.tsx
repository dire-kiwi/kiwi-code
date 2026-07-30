import { Clock3, History, LoaderCircle, PanelsTopLeft, RotateCw, Settings2 } from 'lucide-react'
import { useMatch } from 'react-router-dom'
import { restartApplication, waitForApplicationRestart } from '@/api'
import {
  CLEANUP_ROUTE,
  SESSION_LOG_ROUTE,
  SETTINGS_ROUTE,
  SETTINGS_SECTION_ROUTE,
  TMUX_ROUTE,
  settingsPath,
} from '@/app/routes'
import { DEFAULT_GLOBAL_SETTINGS_SECTION } from '@/features/settings/registry'
import { reloadFrontend } from '@/frontend-reload.mjs'
import { useState } from 'react'
import { Button, SelectionButton } from '@/ui/buttons'
import { useSidebarNavigation } from './useSidebarNavigation'

// No props: this is rendered once, and both halves of what it needs -- which
// destination is current, and how to reach the others -- come from the router.
export function SidebarFooterNav() {
  const [restarting, setRestarting] = useState(false)
  const { navigateAndClose } = useSidebarNavigation()
  const cleanupSelected = Boolean(useMatch(CLEANUP_ROUTE))
  const sessionLogSelected = Boolean(useMatch(SESSION_LOG_ROUTE))
  const tmuxSelected = Boolean(useMatch(TMUX_ROUTE))
  const settingsSelected = Boolean(useMatch(SETTINGS_ROUTE)) || Boolean(useMatch(SETTINGS_SECTION_ROUTE))

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
        onClick={() => navigateAndClose(CLEANUP_ROUTE)}
        aria-current={cleanupSelected ? 'page' : undefined}
      >
        <Clock3 size={13} className={cleanupSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
        <span>Cleanup</span>
      </SelectionButton>
      <SelectionButton
        type="button"
        selected={sessionLogSelected}
        selectionVariant="navigation-compact"
        onClick={() => navigateAndClose(SESSION_LOG_ROUTE)}
        aria-current={sessionLogSelected ? 'page' : undefined}
      >
        <History size={13} className={sessionLogSelected ? 'text-ghost-green' : 'text-ghost-dim'} />
        <span>Session log</span>
      </SelectionButton>
      <SelectionButton
        type="button"
        selected={tmuxSelected}
        selectionVariant="navigation-compact"
        onClick={() => navigateAndClose(TMUX_ROUTE)}
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
            onClick={() => navigateAndClose(settingsPath(DEFAULT_GLOBAL_SETTINGS_SECTION))}
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
