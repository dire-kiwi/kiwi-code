import { useEffect, useMemo, useState } from 'react'
import { CornerDownRight, Folder, Inbox, LoaderCircle, Plus } from 'lucide-react'
import { useMatch } from 'react-router-dom'
import { WORKSPACE_ROUTE } from '@/app/routes'
import { usageDescription } from '@/lib/formatUsage'
import { projectsByMostRecentThread } from '@/new-thread-project-order.mjs'
import { activityViewGroups, formatRelativeShort, type ActivityGroupEntry } from '@/sidebar-activity-groups.mjs'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectActiveProjects, selectActiveThreadIndex } from '@/store/selectors/workspace'
import { selectPiActivities } from '@/store/slices/agentActivity'
import {
  selectArchivingThreadId,
  selectDeletingThreadId,
} from '@/store/slices/projects'
import { sidebarViewChanged } from '@/store/slices/sidebar'
import type { Project, Thread } from '@/types'
import { Button, IconButton, SelectionButton } from '@/ui/buttons'
import { useThreadUsage } from '@/wire/serverData'
import { ThreadActionsMenu } from './ThreadActionsMenu'

type SectionKind = 'working' | 'needsReview' | 'recent'

// Sibling of ProjectSidebar, rendered in its place when the view is switched.
// It was handed eight of the sidebar's own props; it selects the same state.
type SidebarActivityViewProps = {
  onSelectThread: (projectId: string, threadId: string) => void
  onNewThread: (projectId: string) => void
  onArchiveThread: (project: Project, thread: Thread, archived: boolean) => void
  onDeleteThread: (project: Project, thread: Thread) => void
}

const sectionStateDescriptions: Record<SectionKind, string> = {
  working: 'Coding agent is working',
  needsReview: 'Coding agent finished — needs review',
  recent: '',
}

export function SidebarActivityView({
  onSelectThread,
  onNewThread,
  onArchiveThread,
  onDeleteThread,
}: SidebarActivityViewProps) {
  const dispatch = useAppDispatch()
  const projects = useAppSelector(selectActiveProjects)
  const piActivities = useAppSelector(selectPiActivities)
  const threadIndex = useAppSelector(selectActiveThreadIndex)
  const usageSnapshots = useThreadUsage()
  const selectedThreadId = useMatch(WORKSPACE_ROUTE)?.params.threadId ?? null
  const deletingThreadId = useAppSelector(selectDeletingThreadId)
  const archivingThreadId = useAppSelector(selectArchivingThreadId)
  const onShowAllThreads = () => dispatch(sidebarViewChanged('tree'))
  const [now, setNow] = useState(() => Date.now())
  const [projectPickerOpen, setProjectPickerOpen] = useState(false)
  const [threadMenuKey, setThreadMenuKey] = useState<string | null>(null)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    if (!projectPickerOpen) return

    function handlePointerDown(event: PointerEvent) {
      const target = event.target
      if (target instanceof Element && target.closest('[data-new-thread-picker]')) return
      setProjectPickerOpen(false)
    }

    document.addEventListener('pointerdown', handlePointerDown, true)
    return () => document.removeEventListener('pointerdown', handlePointerDown, true)
  }, [projectPickerOpen])

  const groups = useMemo(
    () => activityViewGroups(projects, piActivities, undefined, threadIndex),
    [piActivities, projects, threadIndex],
  )
  const newThreadProjects = useMemo(() => projectsByMostRecentThread(projects), [projects])
  const usageByKey = useMemo(() => new Map(
    usageSnapshots.map((snapshot) => [`${snapshot.projectId}\0${snapshot.threadId}`, snapshot]),
  ), [usageSnapshots])
  const showProjectTags = projects.length > 1

  const isEmpty = groups.working.length === 0
    && groups.needsReview.length === 0
    && groups.recent.length === 0

  function renderEntry(kind: SectionKind, entry: ActivityGroupEntry) {
    const key = `${entry.projectId}\0${entry.threadId}`
    const found = threadIndex.entry(entry.projectId, entry.threadId)
    if (!found) return null
    const { project, thread } = found
    const selected = thread.id === selectedThreadId
    const parent = thread.parentThreadId
      ? threadIndex.tree(project.id)?.byId.get(thread.parentThreadId)
      : undefined
    const usage = usageByKey.get(key)
    const stateDescription = sectionStateDescriptions[kind]
    const title = [
      `${project.name}${parent ? ` · ${parent.title}` : ''}`,
      thread.cwd,
      stateDescription,
      usage ? `Usage: ${usageDescription(usage.own)}${usage.limitReached ? ' — limit reached' : ''}` : '',
    ].filter(Boolean).join('\n')
    const elapsed = formatRelativeShort(entry.at, now)
    const archived = Boolean(thread.archivedAt)
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
            className="pl-2 pr-8"
          >
            {thread.parentThreadId && (
              <CornerDownRight size={11} className="shrink-0 text-ghost-cyan" aria-hidden="true" />
            )}
            {kind === 'working' && (
              <LoaderCircle size={11} className="shrink-0 animate-spin text-ghost-green" aria-hidden="true" />
            )}
            {kind === 'needsReview' && (
              <span
                className="size-1.5 shrink-0 rounded-full bg-ghost-green shadow-[0_0_6px_rgba(181,189,104,0.7)]"
                aria-hidden="true"
              />
            )}
            <span className="min-w-0 flex-1 truncate">{thread.title}</span>
            {stateDescription && <span className="sr-only">{stateDescription}</span>}
            {showProjectTags && (
              <span className="max-w-20 shrink-0 truncate rounded border border-ghost-border/65 px-1 py-0.5 font-mono text-[9px] leading-none text-ghost-dim">
                {project.name}
              </span>
            )}
            {elapsed && (
              <span className="w-6 shrink-0 text-right font-mono text-[9px] leading-none text-ghost-faint">
                {elapsed}
              </span>
            )}
          </SelectionButton>
          <div className="absolute right-1 top-1/2 flex -translate-y-1/2 items-center">
            <ThreadActionsMenu
              threadTitle={thread.title}
              archived={archived}
              archiving={archivingThreadId === thread.id}
              deleting={deletingThreadId === thread.id}
              disabled={Boolean(archivingThreadId || deletingThreadId)}
              open={menuOpen}
              onOpenChange={(open) => setThreadMenuKey(open ? key : null)}
              onArchive={() => onArchiveThread(project, thread, !archived)}
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
        <h3 className="flex items-center gap-1.5 px-2 pb-1 pt-3 font-mono text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
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
      <div className="flex justify-end">
        <div className="relative" data-new-thread-picker>
          <IconButton
            type="button"
            size="sm"
            variant="subtle"
            onClick={() => setProjectPickerOpen((current) => !current)}
            aria-haspopup="menu"
            aria-expanded={projectPickerOpen}
            aria-label="New thread"
            title="New thread…"
            className={projectPickerOpen ? 'bg-ghost-raised text-ghost-bright-white' : undefined}
          >
            <Plus size={14} />
          </IconButton>
          {projectPickerOpen && (
            <div
              role="menu"
              aria-label="New thread in project"
              onKeyDown={(event) => {
                if (event.key !== 'Escape') return
                event.stopPropagation()
                setProjectPickerOpen(false)
              }}
              className="absolute right-0 top-[calc(100%+2px)] z-30 w-52 rounded-lg border border-ghost-border/90 bg-ghost-panel p-1 shadow-2xl"
            >
              <p className="px-2 pb-1 pt-1.5 font-mono text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                New thread in…
              </p>
              <div className="max-h-56 overflow-y-auto">
                {newThreadProjects.map((project) => (
                  <Button
                    key={project.id}
                    role="menuitem"
                    type="button"
                    variant="subtle"
                    onClick={() => {
                      setProjectPickerOpen(false)
                      onNewThread(project.id)
                    }}
                    className="flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[11px]"
                  >
                    <Folder size={13} className="shrink-0 text-ghost-dim" aria-hidden="true" />
                    <span className="min-w-0 flex-1 truncate">{project.name}</span>
                  </Button>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
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
    </div>
  )
}
