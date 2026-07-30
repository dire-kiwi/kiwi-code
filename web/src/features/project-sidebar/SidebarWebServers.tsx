import { ChevronDown, ExternalLink, RadioTower } from 'lucide-react'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectWebServersCollapsed, webServersCollapseToggled } from '@/store/slices/sidebar'
import type { ProcessWebServer } from '@/types'
import { Button } from '@/ui/buttons'

/** Host and path only -- the scheme and a bare "/" are noise at this width. */
function webServerAddress(value: string) {
  try {
    const url = new URL(value)
    return `${url.host}${url.pathname === '/' ? '' : url.pathname}`
  } catch {
    return value
  }
}

export type SidebarWebServersProps = {
  webServers: ProcessWebServer[]
  onNavigate: () => void
}

export function SidebarWebServers({ webServers, onNavigate }: SidebarWebServersProps) {
  const dispatch = useAppDispatch()
  const collapsed = useAppSelector(selectWebServersCollapsed)

  if (webServers.length === 0) return null

  return (
    <section className="mt-4 border-t border-ghost-border/55 pt-1.5" aria-labelledby="sidebar-web-servers-title">
      <Button
        type="button"
        onClick={() => dispatch(webServersCollapseToggled())}
        aria-expanded={!collapsed}
        aria-controls="sidebar-web-servers-list"
        className="flex h-6 w-full items-center gap-1.5 rounded-md px-1.5 text-left transition hover:bg-ghost-raised/45"
      >
        <RadioTower size={11} className="text-ghost-green" aria-hidden="true" />
        <h2 id="sidebar-web-servers-title" className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
          Web servers
        </h2>
        <span className="rounded-full border border-ghost-border/70 px-1.5 font-mono text-[9px] text-ghost-faint">
          {webServers.length}
        </span>
        <ChevronDown
          size={10}
          className={`ml-auto text-ghost-faint transition-transform ${collapsed ? '-rotate-90' : ''}`}
          aria-hidden="true"
        />
      </Button>
      {!collapsed && (
        <ul id="sidebar-web-servers-list" className="mt-1 space-y-0.5">
          {webServers.map((webServer) => (
            <li key={`${webServer.projectId}:${webServer.threadId}:${webServer.processId}:${webServer.url}`}>
              <a
                href={webServer.url}
                target="_blank"
                rel="noreferrer"
                onClick={onNavigate}
                title={`${webServer.projectName} / ${webServer.threadTitle} / ${webServer.processName}\n${webServer.url}`}
                className="group/server flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1.5 text-ghost-muted transition hover:bg-ghost-raised/45 hover:text-ghost-bright-white focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ghost-green/45"
              >
                <span className="grid size-5 shrink-0 place-items-center rounded bg-ghost-green/[0.08] text-ghost-green">
                  <RadioTower size={11} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono text-[10px] text-ghost-white">{webServerAddress(webServer.url)}</span>
                  <span className="block truncate text-[9px] text-ghost-faint">
                    {webServer.processName} · {webServer.projectName}
                  </span>
                </span>
                <ExternalLink size={9} className="shrink-0 text-ghost-faint transition group-hover/server:text-ghost-green" />
              </a>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
