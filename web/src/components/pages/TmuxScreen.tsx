import { useEffect, useState } from 'react'
import {
  Layers3,
  LoaderCircle,
  MonitorUp,
  RefreshCw,
  SquareTerminal,
} from 'lucide-react'
import { listTmuxSessions } from '../../api'
import type { ConnectionStatus, TmuxBrowserSession, TmuxBrowserWindow } from '../../types'
import { IconButton } from '../atoms/IconButton'
import { Surface } from '../atoms/Surface'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { TerminalSession } from '../organisms/TerminalSession'

type TmuxScreenProps = {
  onOpenSidebar: () => void
  onBack: () => void
}

type SelectedWindow = {
  session: TmuxBrowserSession
  window: TmuxBrowserWindow
}

function sameTarget(selection: SelectedWindow | null, session: TmuxBrowserSession, window: TmuxBrowserWindow) {
  return selection?.session.name === session.name && selection.window.id === window.id
}

export function TmuxScreen({ onOpenSidebar, onBack }: TmuxScreenProps) {
  const [sessions, setSessions] = useState<TmuxBrowserSession[]>([])
  const [selection, setSelection] = useState<SelectedWindow | null>(null)
  const [terminalStatus, setTerminalStatus] = useState<ConnectionStatus>('closed')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [loadKey, setLoadKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    listTmuxSessions(controller.signal)
      .then((next) => {
        setSessions(next)
        setSelection((current) => {
          if (!current) return null
          const session = next.find((item) => item.name === current.session.name)
          const window = session?.windows.find((item) => item.id === current.window.id)
          return session && window ? { session, window } : null
        })
      })
      .catch((reason) => {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : 'Could not load tmux sessions.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [loadKey])

  function attachWindow(session: TmuxBrowserSession, window: TmuxBrowserWindow) {
    setTerminalStatus('connecting')
    setSelection({ session, window })
  }

  function attachSession(session: TmuxBrowserSession) {
    const window = session.windows.find((item) => item.active) ?? session.windows[0]
    if (window) attachWindow(session, window)
  }

  const targetLabel = selection
    ? `${selection.session.name}:${selection.window.index}`
    : 'Select a window to attach'

  return (
    <div className="flex h-full min-w-0 flex-col bg-ghost-black">
      <ScreenHeader
        title="tmux sessions"
        subtitle="dire-mux server"
        backLabel="Back to workspace"
        onOpenSidebar={onOpenSidebar}
        onBack={onBack}
      />

      <main className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <aside className="flex max-h-[45%] min-h-52 shrink-0 flex-col border-b border-ghost-border/70 bg-ghost-panel/75 lg:max-h-none lg:w-[23rem] lg:border-b-0 lg:border-r">
          <div className="flex h-12 shrink-0 items-center gap-3 border-b border-ghost-border/60 px-3.5">
            <span className="grid size-7 place-items-center rounded-lg bg-ghost-raised text-ghost-green">
              <Layers3 size={14} />
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-[10px] font-semibold uppercase tracking-[0.13em] text-ghost-muted">Sessions</p>
              <p className="mt-0.5 font-mono text-[8px] text-ghost-faint">
                {loading ? 'Loading…' : `${sessions.length} persistent session${sessions.length === 1 ? '' : 's'}`}
              </p>
            </div>
            <IconButton
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => setLoadKey((value) => value + 1)}
              disabled={loading}
              aria-label="Refresh tmux sessions"
              title="Refresh sessions"
            >
              <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
            </IconButton>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto p-2.5">
            {loading && sessions.length === 0 ? (
              <div className="grid min-h-32 place-items-center text-center">
                <div>
                  <LoaderCircle size={17} className="mx-auto animate-spin text-ghost-green" />
                  <p className="mt-2 text-[10px] text-ghost-dim">Loading tmux sessions</p>
                </div>
              </div>
            ) : error ? (
              <Surface variant="raised-panel" className="p-4 text-center">
                <p className="text-[10px] leading-4 text-ghost-bright-red">{error}</p>
                <button
                  type="button"
                  onClick={() => setLoadKey((value) => value + 1)}
                  className="mt-3 text-[10px] font-medium text-ghost-green hover:text-ghost-bright-green"
                >
                  Try again
                </button>
              </Surface>
            ) : sessions.length === 0 ? (
              <div className="grid min-h-32 place-items-center px-4 text-center">
                <div>
                  <SquareTerminal size={19} className="mx-auto text-ghost-faint" />
                  <p className="mt-3 text-xs font-medium text-ghost-muted">No tmux sessions</p>
                  <p className="mt-1 text-[9px] leading-4 text-ghost-faint">Open a thread tool to create one.</p>
                </div>
              </div>
            ) : (
              <ul className="space-y-2.5">
                {sessions.map((session) => {
                  const sessionSelected = selection?.session.name === session.name
                  return (
                    <li key={session.name} className="overflow-hidden rounded-xl border border-ghost-border/65 bg-ghost-black/25">
                      <button
                        type="button"
                        onClick={() => attachSession(session)}
                        className={`flex w-full items-start gap-2.5 px-3 py-2.5 text-left transition ${
                          sessionSelected ? 'bg-ghost-green/[0.06]' : 'hover:bg-ghost-raised/55'
                        }`}
                      >
                        <span className={`mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg ${
                          sessionSelected ? 'bg-ghost-green/15 text-ghost-green' : 'bg-ghost-raised text-ghost-dim'
                        }`}>
                          <MonitorUp size={13} />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="flex min-w-0 items-center gap-2">
                            <span className="truncate text-[11px] font-semibold text-ghost-bright-white">
                              {session.threadTitle || session.name}
                            </span>
                            {session.attached && (
                              <span className="size-1.5 shrink-0 rounded-full bg-ghost-green" title="Attached tmux client" />
                            )}
                          </span>
                          {session.projectName && (
                            <span className="mt-0.5 block truncate text-[9px] text-ghost-dim">
                              {session.projectName} · {session.kind}
                            </span>
                          )}
                          <span className="mt-1 block truncate font-mono text-[8px] text-ghost-faint" title={session.name}>
                            {session.name}
                          </span>
                        </span>
                        <span className="mt-1 rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[8px] text-ghost-faint">
                          {session.windows.length}
                        </span>
                      </button>

                      <ul className="border-t border-ghost-border/50 p-1.5">
                        {session.windows.map((window) => {
                          const active = sameTarget(selection, session, window)
                          return (
                            <li key={window.id}>
                              <button
                                type="button"
                                onClick={() => attachWindow(session, window)}
                                className={`flex h-9 w-full items-center gap-2 rounded-lg px-2.5 text-left transition ${
                                  active
                                    ? 'bg-ghost-green/[0.11] text-ghost-bright-white'
                                    : 'text-ghost-muted hover:bg-ghost-raised/65 hover:text-ghost-bright-white'
                                }`}
                              >
                                <span className={`font-mono text-[8px] ${active ? 'text-ghost-green' : 'text-ghost-faint'}`}>
                                  {window.index}
                                </span>
                                <span className="min-w-0 flex-1 truncate text-[10px] font-medium">{window.name}</span>
                                {window.currentCommand && (
                                  <span className="max-w-20 truncate font-mono text-[8px] text-ghost-faint" title={window.currentCommand}>
                                    {window.currentCommand}
                                  </span>
                                )}
                                {window.paneCount > 1 && (
                                  <span className="font-mono text-[8px] text-ghost-faint">{window.paneCount}p</span>
                                )}
                              </button>
                            </li>
                          )
                        })}
                      </ul>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
        </aside>

        <section className="flex min-h-0 min-w-0 flex-1 flex-col bg-ghost-background">
          <div className="flex h-10 shrink-0 items-center gap-2 border-b border-ghost-border/65 bg-ghost-panel/70 px-3.5">
            <SquareTerminal size={13} className={selection ? 'text-ghost-green' : 'text-ghost-faint'} />
            <span className="min-w-0 flex-1 truncate font-mono text-[9px] text-ghost-muted" title={targetLabel}>
              {targetLabel}
            </span>
            {selection && (
              <span className={`size-1.5 rounded-full ${
                terminalStatus === 'open'
                  ? 'bg-ghost-green'
                  : terminalStatus === 'connecting'
                    ? 'animate-pulse bg-ghost-yellow'
                    : terminalStatus === 'error'
                      ? 'bg-ghost-bright-red'
                      : 'bg-ghost-faint'
              }`} />
            )}
          </div>
          <div className="relative min-h-0 flex-1 overflow-hidden">
            {selection ? (
              <TerminalSession
                key={`${selection.session.name}:${selection.window.id}`}
                projectId=""
                threadId=""
                threadTitle={`${selection.session.name}:${selection.window.index}`}
                tool="terminal"
                tmuxSession={selection.session.name}
                tmuxWindow={selection.window.id}
                terminalLabel="tmux"
                active
                onStatusChange={setTerminalStatus}
              />
            ) : (
              <div className="absolute inset-0 grid place-items-center px-6 text-center">
                <div className="max-w-sm">
                  <span className="mx-auto grid size-12 place-items-center rounded-xl border border-ghost-border/80 bg-ghost-panel text-ghost-dim">
                    <MonitorUp size={20} />
                  </span>
                  <p className="mt-4 text-sm font-medium text-ghost-bright-white">Choose a tmux window</p>
                  <p className="mt-1.5 text-[11px] leading-5 text-ghost-muted">
                    Select a session or one of its windows to attach without stopping its running process.
                  </p>
                </div>
              </div>
            )}
          </div>
        </section>
      </main>
    </div>
  )
}
