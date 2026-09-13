import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, Inbox, LoaderCircle } from 'lucide-react'
import { useMatch } from 'react-router-dom'
import { WORKSPACE_ROUTE } from '@/app/routes'
import { usageDescription } from '@/lib/formatUsage'
import { activityViewGroups, formatRelativeShort, type ActivityGroupEntry } from '@/sidebar-activity-groups.mjs'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectActiveProjects, selectActiveThreadIndex } from '@/store/selectors/workspace'
import { selectPiActivities } from '@/store/slices/agentActivity'
import {
  selectDeletingThreadId,
} from '@/store/slices/projects'
import { sidebarViewChanged } from '@/store/slices/sidebar'
import type { Project, Thread } from '@/types'
import { Button, SelectionButton } from '@/ui/buttons'
import { useThreadUsage } from '@/wire/serverData'
import { useThreadSettlement } from './useThreadSettlement'
import { ThreadActionsMenu } from './ThreadActionsMenu'

type SectionKind = 'working' | 'needsReview' | 'recent' | 'settled'

// Sibling of ProjectSidebar, rendered in its place when the view is switched.
// It was handed eight of the sidebar's own props; it selects the same state.
type SidebarActivityViewProps = {
  onSelectThread: (projectId: string, threadId: string) => void
  projectScope?: string
  onDeleteThread: (project: Project, thread: Thread) => void
}

const sectionStateDescriptions: Record<SectionKind, string> = {
  working: 'Coding agent is working',
  needsReview: 'Coding agent finished — needs review',
  recent: '',
  settled: 'Settled thread',
}

export function SidebarActivityView({
  onSelectThread,
  projectScope,
  onDeleteThread,
}: SidebarActivityViewProps) {
  const dispatch = useAppDispatch()
  const { settlingThreadId, toggleSettlement } = useThreadSettlement()
  const [settledOpen, setSettledOpen] = useState(false)
  const projects = useAppSelector(selectActiveProjects)
  const piActivities = useAppSelector(selectPiActivities)
  const threadIndex = useAppSelector(selectActiveThreadIndex)
  const usageSnapshots = useThreadUsage()
  const selectedThreadId = useMatch(WORKSPACE_ROUTE)?.params.threadId ?? null
  const deletingThreadId = useAppSelector(selectDeletingThreadId)
  const onShowAllThreads = () => dispatch(sidebarViewChanged('tree'))
  const [now, setNow] = useState(() => Date.now())
  const [threadMenuKey, setThreadMenuKey] = useState<string | null>(null)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const groups = useMemo(
    () => activityViewGroups(projectScope ? projects.filter((project) => project.id === projectScope) : projects, piActivities, undefined, threadIndex),
    [piActivities, projects, projectScope, threadIndex],
  )
  useEffect(() => {
    if (groups.settled.some((entry) => entry.threadId === selectedThreadId)) setSettledOpen(true)
  }, [groups.settled, selectedThreadId])
  const usageByKey = useMemo(() => new Map(
    usageSnapshots.map((snapshot) => [`${snapshot.projectId}\0${snapshot.threadId}`, snapshot]),
  ), [usageSnapshots])

  const isEmpty = groups.working.length === 0
    && groups.needsReview.length === 0
    && groups.recent.length === 0

  function renderEntry(kind: SectionKind, entry: ActivityGroupEntry) {
    const key = `${entry.projectId}\0${entry.threadId}`
    const found = threadIndex.entry(entry.projectId, entry.threadId)
    if (!found) return null
    const { project, thread } = found
    const selected = thread.id === selectedThreadId
    const usage = usageByKey.get(key)
    const stateDescription = sectionStateDescriptions[kind]
    const title = [
      project.name,
      thread.cwd,
      stateDescription,
      usage ? `Usage: ${usageDescription(usage.own)}${usage.limitReached ? ' — limit reached' : ''}` : '',
    ].filter(Boolean).join('\n')
    const elapsed = formatRelativeShort(entry.at, now)
    const menuOpen = threadMenuKey === key
    // The open menu needs its row on top; an opacity below 1 would create a
    // stacking context that traps the menu behind later rows.

    return (
      <li key={key} className={menuOpen ? 'relative z-40' : undefined}>
        <div className="group/thread relative">
          <SelectionButton
            type="button"
            selected={selected}
            selectionVariant="navigation"
            onClick={() => onSelectThread(project.id, thread.id)}
            aria-current={selected ? 'page' : undefined}
            title={title}
            className="!h-auto min-h-14 pl-3 pr-14"
          >
            <span className="pointer-events-none absolute right-2.5 top-2.5 flex h-3 items-center justify-end">
              {kind === 'working' ? (
                <LoaderCircle size={11} className="animate-spin text-ghost-green" aria-hidden="true" />
              ) : elapsed ? (
                <span className="font-mono text-[9px] leading-none text-ghost-faint">{elapsed}</span>
              ) : null}
            </span>
            {kind === 'needsReview' && (
              <span
                className="size-1.5 shrink-0 rounded-full bg-ghost-green shadow-[0_0_6px_rgba(181,189,104,0.7)]"
                aria-hidden="true"
              />
            )}
            <span className="min-w-0 flex-1">
              <span className="mb-1 block truncate text-[10px] text-ghost-dim">{project.name}</span>
              <span className="block truncate text-xs text-ghost-white">{thread.title}</span>
            </span>
            {stateDescription && <span className="sr-only">{stateDescription}</span>}
          </SelectionButton>
          <div className="absolute bottom-1 right-1 flex items-center">
            <ThreadActionsMenu
              threadTitle={thread.title}
              settled={Boolean(thread.settledAt)}
              working={kind === 'working'}
              settling={settlingThreadId === thread.id}
              onSettle={() => void toggleSettlement(project, thread)}
              deleting={deletingThreadId === thread.id}
              disabled={Boolean(deletingThreadId || settlingThreadId)}
              open={menuOpen}
              onOpenChange={(open) => setThreadMenuKey(open ? key : null)}
              onDelete={() => onDeleteThread(project, thread)}
              triggerClassName={menuOpen || selected
                ? undefined
                : 'opacity-0 transition group-hover/thread:opacity-100 group-focus-within/thread:opacity-100'}
            />
          </div>
        </div>
      </li>
    )
  }

  function renderSection(kind: SectionKind, label: string, entries: ActivityGroupEntry[], showCount: boolean) {
    if (entries.length === 0) return null
    return (
      <section aria-label={label}>
        <h3 className="flex items-center gap-1.5 px-2 pb-1 pt-3 text-[11px] font-medium text-ghost-dim">
          {label}
          {showCount && <span className="text-ghost-green">· {entries.length}</span>}
        </h3>
        <ul className="space-y-0.5">
          {entries.map((entry) => renderEntry(kind, entry))}
        </ul>
      </section>
    )
  }

  return (
    <div>
      {isEmpty ? (
        <div className="mx-1 mt-3 rounded-lg border border-dashed border-ghost-border/70 px-3 py-6 text-center">
          <Inbox size={17} className="mx-auto text-ghost-faint" aria-hidden="true" />
          <p className="mt-2.5 text-[10px] text-ghost-muted">All quiet — no active threads</p>
          <Button
            type="button"
            variant="text"
            onClick={onShowAllThreads}
            className="mt-2 text-[9px] text-ghost-green"
          >
            Browse projects
          </Button>
        </div>
      ) : (
        <>
          {renderSection('working', 'Working', groups.working, true)}
          {renderSection('needsReview', 'Needs review', groups.needsReview, true)}
          {renderSection('recent', 'Recent', groups.recent, false)}
          {groups.hiddenRecentCount > 0 && (
            <Button
              type="button"
              variant="text"
              onClick={onShowAllThreads}
              className="mt-1 flex h-7 w-full items-center rounded-md px-2 font-mono text-[10px] text-ghost-faint transition hover:bg-ghost-raised/45 hover:text-ghost-muted"
            >
              {groups.hiddenRecentCount} older — browse in Projects view
            </Button>
          )}
        </>
      )}
      <section aria-label="Settled threads" className="mt-3 border-t border-ghost-border/45 pt-1">
        <button type="button" aria-expanded={settledOpen} onClick={() => setSettledOpen((open) => !open)}
          className="flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[11px] text-ghost-dim hover:bg-ghost-raised/40">
          <span className="flex-1">Settled ({groups.settled.length})</span>
          {settledOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </button>
        {settledOpen && (
          groups.settled.length ? <ul className="space-y-0.5">{groups.settled.map((entry) => renderEntry('settled', entry))}</ul>
            : <p className="px-2 py-3 text-xs text-ghost-faint">No settled threads</p>
        )}
      </section>
    </div>
  )
}
