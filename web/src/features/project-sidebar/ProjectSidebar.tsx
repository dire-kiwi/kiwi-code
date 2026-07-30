import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  Archive,
  ChevronDown,
  ChevronUp,
  Folder,
  FolderOpen,
  GitBranch,
  Globe2,
  GripVertical,
  ListTree,
  LoaderCircle,
  PanelLeftClose,
  Plus,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useMatch } from 'react-router-dom'
import { WORKSPACE_ROUTE, newThreadPath, projectSettingsPath } from '@/app/routes'
import { DEFAULT_PROJECT_SETTINGS_SECTION } from '@/features/settings/registry'
import { formatCompactTokens, formatCompactUsd, usageDescription } from '@/lib/formatUsage'
import { defaultVisibleRootThreadIds } from '@/sidebar-thread-visibility.mjs'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import {
  selectActiveProjects,
  selectActiveThreadIndex,
} from '@/store/selectors/workspace'
import { selectActiveProfileId } from '@/store/slices/preferences'
import { selectPiActivities } from '@/store/slices/agentActivity'
import { selectProfiles } from '@/store/slices/profiles'
import {
  selectArchivingThreadId,
  selectDeletingProjectId,
  selectDeletingThreadId,
} from '@/store/slices/projects'
import {
  moreThreadsToggled,
  projectCollapseToggled,
  selectCollapsedProjectIds,
  selectExpandedMoreProjectIds,
  selectSidebarView,
  selectSidebarWidth,
  sidebarViewChanged,
  sidebarWidthChanged,
  sidebarWidthKeyboardStep,
  sidebarWidthNudged,
  sidebarWidthReset,
  threadRevealed,
} from '@/store/slices/sidebar'
import { selectSidebarOpen, sidebarClosed } from '@/store/slices/ui'
import type { Profile, Project, Thread } from '@/types'
import { useThreadUsage } from '@/wire/serverData'
import { Button, IconButton, SelectionButton } from '@/ui/buttons'
import { SidebarActivityView } from './SidebarActivityView'
import { SidebarAddProjectForm } from './SidebarAddProjectForm'
import { SidebarFooterNav } from './SidebarFooterNav'
import { SidebarProfileSwitcher } from './SidebarProfileSwitcher'
import { ThreadActionsMenu } from './ThreadActionsMenu'
import { useSidebarNavigation } from './useSidebarNavigation'
import { useSidebarReorder } from './useSidebarReorder'

// What is left as props are the four actions that are not purely data work:
// each either confirms with the user first or navigates afterwards, and neither
// belongs inside a thunk. Everything else the sidebar shows it now selects.
type ProjectSidebarProps = {
  onSelectProfile: (profileId: string) => void
  onProfileCreated: (profile: Profile) => void
  onSelectThread: (projectId: string, threadId: string) => void
  onProjectCreated: (project: Project) => void
  onDeleteProject: (project: Project) => void
  onArchiveThread: (project: Project, thread: Thread, archived: boolean) => void
  onDeleteThread: (project: Project, thread: Thread) => void
}


export function ProjectSidebar({
  onSelectProfile,
  onProfileCreated,
  onSelectThread,
  onProjectCreated,
  onDeleteProject,
  onArchiveThread,
  onDeleteThread,
}: ProjectSidebarProps) {
  const [showProjectForm, setShowProjectForm] = useState(false)
  const dispatch = useAppDispatch()
  const { navigateAndClose } = useSidebarNavigation()
  // The router already holds which thread is open; App used to re-derive this
  // with useMatch and pass it down, which made the URL a two-copy fact.
  const selectedThreadId = useMatch(WORKSPACE_ROUTE)?.params.threadId ?? null
  const activeProfileId = useAppSelector(selectActiveProfileId)
  const profiles = useAppSelector(selectProfiles)
  const projects = useAppSelector(selectActiveProjects)
  const threadIndex = useAppSelector(selectActiveThreadIndex)
  const deletingProjectId = useAppSelector(selectDeletingProjectId)
  const deletingThreadId = useAppSelector(selectDeletingThreadId)
  const archivingThreadId = useAppSelector(selectArchivingThreadId)
  const usageSnapshots = useThreadUsage()
  const isOpen = useAppSelector(selectSidebarOpen)
  const viewMode = useAppSelector(selectSidebarView)
  const sidebarWidth = useAppSelector(selectSidebarWidth)
  const collapsedProjectIds = useAppSelector(selectCollapsedProjectIds)
  const expandedMoreProjectIds = useAppSelector(selectExpandedMoreProjectIds)
  // Only for the default-visible-roots calculation below; the tree itself comes
  // from the memoised index.
  const piActivities = useAppSelector(selectPiActivities)
  const [threadMenuId, setThreadMenuId] = useState<string | null>(null)
  const asideRef = useRef<HTMLElement>(null)
  const activeProfile = profiles.find((profile) => profile.id === activeProfileId) ?? profiles[0]
  const usageByThread = useMemo(() => new Map(
    usageSnapshots.map((snapshot) => [`${snapshot.projectId}\0${snapshot.threadId}`, snapshot]),
  ), [usageSnapshots])
  const projectActivityCounts = threadIndex.projectActivityCounts

  const openNewThread = (projectId: string) => navigateAndClose(newThreadPath(projectId))
  const openProjectSettings = (projectId: string) =>
    navigateAndClose(projectSettingsPath(projectId, DEFAULT_PROJECT_SETTINGS_SECTION))

  useEffect(() => {
    if (!selectedThreadId) return
    const project = threadIndex.projectByThreadId.get(selectedThreadId)
    const tree = project ? threadIndex.tree(project.id) : null
    const selected = tree?.byId.get(selectedThreadId)
    if (!project || !tree || !selected) return

    dispatch(threadRevealed({
      projectId: project.id,
      expandArchived: Boolean(selected.archivedAt),
    }))
  }, [dispatch, selectedThreadId, threadIndex])


  const {
    draggedItem,
    dropTarget,
    savingOrder,
    clearDrag,
    startProjectDrag,
    startThreadDrag,
    handleProjectDragOver,
    handleProjectDrop,
    handleThreadDragOver,
    handleThreadDrop,
    handleProjectHandleKeyDown,
    handleThreadHandleKeyDown,
  } = useSidebarReorder({
    projects,
    treeFor: (projectId) => threadIndex.tree(projectId),
  })

  function renderThreadRow(
    project: Project,
    thread: Thread,
    activeThreadCount: number,
    visibleThreadIds?: ReadonlySet<string>,
  ) {
    const archived = Boolean(thread.archivedAt)
    const canReorder = !archived && activeThreadCount > 1 && !savingOrder
    const selected = thread.id === selectedThreadId
    const usage = usageByThread.get(`${project.id}\0${thread.id}`)
    const displayedUsage = usage?.own
    const usageScope = 'Usage for this thread'
    const usageTitle = displayedUsage
      ? `\n${usageScope}: ${usageDescription(displayedUsage)}${usage?.limitReached ? ' — limit reached' : ''}`
      : ''
    const { activity: piActivity } = threadIndex.threadActivity(project.id, thread.id)
    const locationTitle = thread.worktree && thread.branch
      ? `${thread.branch}\n${thread.cwd}`
      : thread.cwd
    const activityTitle = piActivity
      ? `\nCoding agent is ${piActivity.state === 'working' ? 'working' : 'finished'}`
      : ''
    const archivedTitle = thread.archivedAt
      ? `\nArchived ${new Date(thread.archivedAt).toLocaleString()}`
      : ''
    const selectionPadding = 'pl-8'
    const menuOpen = threadMenuId === thread.id
    // While the actions menu is open the row must stay fully opaque: any opacity below 1 turns the
    // row into a stacking context, which traps the menu behind the rows rendered after it.

    return (
      <li
        key={thread.id}
        data-thread-row
        data-project-id={project.id}
        data-thread-id={thread.id}
        onDragOver={archived ? undefined : (event) => handleThreadDragOver(event, project.id, thread.id)}
        onDrop={archived ? undefined : (event) => handleThreadDrop(event, project, thread.id)}
        className={`${menuOpen ? 'relative z-40' : ''} ${!menuOpen && archived ? 'opacity-75' : ''} ${
          draggedItem?.kind === 'thread' && draggedItem.id === thread.id ? 'opacity-45' : ''
        }`}
      >
        <div className="group/thread relative transition-opacity">
          {!archived && dropTarget?.kind === 'thread' && dropTarget.projectId === project.id && dropTarget.id === thread.id && dropTarget.position === 'before' && (
            <span className="pointer-events-none absolute inset-x-2 top-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
          )}
          {!archived && (
            <button
              type="button"
              draggable={canReorder}
              disabled={!canReorder}
              data-reorder-handle="thread"
              data-project-id={project.id}
              data-thread-id={thread.id}
              onDragStart={(event) => startThreadDrag(event, project.id, thread.id)}
              onDragEnd={() => clearDrag()}
              onKeyDown={(event) => handleThreadHandleKeyDown(event, project, thread.id)}
              className="absolute left-1.5 top-1/2 z-10 grid size-5 -translate-y-1/2 cursor-grab place-items-center rounded text-ghost-faint opacity-0 transition hover:bg-ghost-raised hover:text-ghost-white group-hover/thread:opacity-100 focus:opacity-100 active:cursor-grabbing disabled:pointer-events-none"
              aria-label={`Reorder thread ${thread.title}; drag or use the arrow keys`}
              title="Drag to reorder; arrow keys also work"
            >
              <GripVertical size={11} />
            </button>
          )}
          <SelectionButton
            type="button"
            selected={selected}
            selectionVariant="navigation"
            onClick={() => onSelectThread(project.id, thread.id)}
            aria-current={selected ? 'page' : undefined}
            title={`${locationTitle}${archivedTitle}${activityTitle}${usageTitle}`}
            className={`${selectionPadding} pr-12`}
          >
            {thread.worktree && <GitBranch size={11} className="shrink-0 text-ghost-green" />}
            {archived && !thread.worktree && <Archive size={11} className="shrink-0 text-ghost-faint" />}
            <span className="min-w-0 flex-1 truncate">{thread.title}</span>
            {piActivity?.state === 'working' ? (
              <>
                <LoaderCircle size={11} className="shrink-0 animate-spin text-ghost-green" aria-hidden="true" />
                <span className="sr-only">Coding agent is working</span>
              </>
            ) : piActivity?.state === 'finished' ? (
              <>
                <span className="size-1.5 shrink-0 rounded-full bg-ghost-green shadow-[0_0_6px_rgba(181,189,104,0.7)]" aria-hidden="true" />
                <span className="sr-only">Coding agent finished</span>
              </>
            ) : null}
            {displayedUsage && (
              <span
                aria-hidden="true"
                className={`shrink-0 font-mono text-[9px] leading-none ${
                  usage?.limitReached ? 'text-ghost-bright-red' : 'text-ghost-faint'
                }`}
              >
                {formatCompactTokens(displayedUsage.totalTokens)} · {formatCompactUsd(displayedUsage.costUsd)}
              </span>
            )}
            {displayedUsage && <span className="sr-only">{usageScope}: {usageDescription(displayedUsage)}{usage?.limitReached ? '. Limit reached.' : ''}</span>}
          </SelectionButton>
          <div className="absolute right-1 top-1/2 flex -translate-y-1/2 items-center">
            <ThreadActionsMenu
              threadTitle={thread.title}
              archived={archived}
              archiving={archivingThreadId === thread.id}
              deleting={deletingThreadId === thread.id}
              disabled={Boolean(archivingThreadId || deletingThreadId)}
              open={menuOpen}
              onOpenChange={(open) => setThreadMenuId(open ? thread.id : null)}
              onArchive={() => onArchiveThread(project, thread, !archived)}
              onDelete={() => onDeleteThread(project, thread)}
              triggerClassName={menuOpen || selected
                ? undefined
                : 'opacity-0 transition group-hover/thread:opacity-100 group-focus-within/thread:opacity-100'}
            />
          </div>
          {!archived && dropTarget?.kind === 'thread' && dropTarget.projectId === project.id && dropTarget.id === thread.id && dropTarget.position === 'after' && (
            <span className="pointer-events-none absolute inset-x-2 bottom-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
          )}
        </div>
      </li>
    )
  }

  function renderProjectThreadRows(project: Project) {
    const tree = threadIndex.tree(project.id)
    if (!tree) return null
    const roots = tree.roots
    const activeThreads = roots.filter((thread) => !thread.archivedAt)
    const archivedThreads = roots.filter((thread) => thread.archivedAt)
    const expanded = expandedMoreProjectIds.has(project.id)
    const defaultVisibleIds = new Set(defaultVisibleRootThreadIds(
      project.threads,
      piActivities,
      project.id,
      undefined,
      tree,
    ))
    const displayedActiveThreads = expanded
      ? activeThreads
      : activeThreads.filter((thread) => defaultVisibleIds.has(thread.id))
    const hiddenActiveCount = activeThreads.length - defaultVisibleIds.size
    const hasMoreThreads = hiddenActiveCount > 0 || archivedThreads.length > 0

    return (
      <>
        {displayedActiveThreads.map((thread) => renderThreadRow(project, thread, activeThreads.length))}
        {hasMoreThreads && (
          <li className="px-2 pt-0.5">
            <Button
              type="button"
              variant="text"
              onClick={() => dispatch(moreThreadsToggled(project.id))}
              aria-expanded={expanded}
              className="flex h-7 w-full items-center gap-1.5 rounded-md px-1.5 text-left font-mono text-[10px] text-ghost-faint transition hover:bg-ghost-raised/45 hover:text-ghost-muted"
            >
              {expanded ? <ChevronUp size={10} /> : <ChevronDown size={10} />}
              <span>{expanded ? 'Show less' : 'Show more'}</span>
              <span className="ml-auto flex items-center gap-1">
                {hiddenActiveCount > 0 && (
                  <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 text-[9px]">
                    {hiddenActiveCount} older
                  </span>
                )}
                {archivedThreads.length > 0 && (
                  <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 text-[9px]">
                    {archivedThreads.length} archived
                  </span>
                )}
              </span>
            </Button>
          </li>
        )}
        {expanded && archivedThreads.map((thread) => renderThreadRow(
          project,
          thread,
          activeThreads.length,
        ))}
      </>
    )
  }

  return (
    <>
      <Button
        type="button"
        aria-label="Close project navigation"
        className={`fixed inset-0 z-30 bg-ghost-black/80 backdrop-blur-sm transition-opacity md:hidden ${
          isOpen ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'
        }`}
        onClick={() => dispatch(sidebarClosed())}
      />

      <aside
        ref={asideRef}
        style={{ width: sidebarWidth }}
        className={`fixed inset-y-0 left-0 z-40 flex max-w-[calc(100vw-2rem)] shrink-0 flex-col border-r border-ghost-border/70 bg-ghost-sidebar shadow-2xl transition-[transform,visibility] duration-300 md:relative md:z-auto md:visible md:max-w-none md:translate-x-0 md:shadow-none ${
          isOpen ? 'visible translate-x-0' : 'invisible -translate-x-full'
        }`}
      >
        <header className="desktop-titlebar-drag-region flex h-[4.5rem] shrink-0 flex-col justify-center gap-1 border-b border-ghost-border/70 px-3">
          <div className="desktop-titlebar-left-safe flex items-center justify-between gap-2">
            <h1 className="min-w-0 truncate text-xs font-semibold text-ghost-bright-white">
              {viewMode === 'activity' ? 'Threads' : 'Projects'}
            </h1>
            <div className="flex shrink-0 items-center gap-0.5">
            <div
              role="group"
              aria-label="Sidebar view"
              className="mr-0.5 flex items-center gap-0.5 rounded-md border border-ghost-border/55 p-0.5"
            >
              <IconButton
                type="button"
                size="xs"
                variant="subtle"
                onClick={() => dispatch(sidebarViewChanged('activity'))}
                aria-pressed={viewMode === 'activity'}
                aria-label="Activity view"
                title="Activity view: working, needs review, recent"
                className={viewMode === 'activity' ? 'bg-ghost-green/10 text-ghost-green' : undefined}
              >
                <Activity size={12} />
              </IconButton>
              <IconButton
                type="button"
                size="xs"
                variant="subtle"
                onClick={() => dispatch(sidebarViewChanged('tree'))}
                aria-pressed={viewMode === 'tree'}
                aria-label="Projects view"
                title="Projects view: the full project and thread tree"
                className={viewMode === 'tree' ? 'bg-ghost-green/10 text-ghost-green' : undefined}
              >
                <ListTree size={12} />
              </IconButton>
            </div>
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => setShowProjectForm(true)}
              aria-label="Add a project"
              title="Add project"
            >
              <Plus size={15} />
            </IconButton>
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => dispatch(sidebarClosed())}
              className="md:hidden"
              aria-label="Close sidebar"
            >
              <PanelLeftClose size={15} />
            </IconButton>
            </div>
          </div>
          <SidebarProfileSwitcher
            profiles={profiles}
            activeProfileId={activeProfileId}
            onSelectProfile={onSelectProfile}
            onProfileCreated={onProfileCreated}
          />
        </header>

        {showProjectForm && (
          <SidebarAddProjectForm
            activeProfile={activeProfile}
            activeProfileId={activeProfileId}
            onProjectCreated={onProjectCreated}
            onCancel={() => setShowProjectForm(false)}
          />
        )}

        <nav className="min-h-0 flex-1 overflow-y-auto px-2 py-2" aria-label="Projects and threads">
          {projects.length === 0 ? (
            <div className="mx-1 mt-2 rounded-lg border border-dashed border-ghost-border/70 px-3 py-6 text-center">
              <Folder size={17} className="mx-auto text-ghost-faint" />
              <p className="mt-2.5 text-[10px] text-ghost-muted">
                No projects{activeProfile ? ` in ${activeProfile.name}` : ' yet'}
              </p>
            </div>
          ) : viewMode === 'activity' ? (
            <SidebarActivityView
              onSelectThread={onSelectThread}
              onNewThread={openNewThread}
              onArchiveThread={onArchiveThread}
              onDeleteThread={onDeleteThread}
            />
          ) : (
            <ul className="space-y-2.5">
              {projects.map((project) => (
                <li
                  key={project.id}
                  data-project-row
                  data-project-id={project.id}
                  onDragOver={(event) => handleProjectDragOver(event, project.id)}
                  onDrop={(event) => handleProjectDrop(event, project.id)}
                  className={`group/project relative transition-opacity ${
                    draggedItem?.kind === 'project' && draggedItem.id === project.id ? 'opacity-45' : ''
                  }`}
                >
                  {dropTarget?.kind === 'project' && dropTarget.id === project.id && dropTarget.position === 'before' && (
                    <span className="pointer-events-none absolute inset-x-2 top-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
                  )}
                  <div
                    data-project-drag-image
                    className={`flex h-8 items-center gap-1 px-1.5 ${
                      project.threads.some((thread) => thread.id === selectedThreadId)
                        ? 'text-ghost-bright-white'
                        : 'text-ghost-muted'
                    }`}
                  >
                    <button
                      type="button"
                      draggable={!savingOrder && projects.length > 1}
                      disabled={savingOrder || projects.length < 2}
                      data-reorder-handle="project"
                      data-project-id={project.id}
                      onDragStart={(event) => startProjectDrag(event, project.id)}
                      onDragEnd={() => clearDrag()}
                      onKeyDown={(event) => handleProjectHandleKeyDown(event, project.id)}
                      className="-ml-1 grid size-5 shrink-0 cursor-grab place-items-center rounded text-ghost-faint opacity-0 transition hover:bg-ghost-raised hover:text-ghost-white group-hover/project:opacity-100 focus:opacity-100 active:cursor-grabbing disabled:cursor-default disabled:opacity-0"
                      aria-label={`Reorder project ${project.name}; drag or use the arrow keys`}
                      title="Drag to reorder; arrow keys also work"
                    >
                      <GripVertical size={11} />
                    </button>
                    <Button
                      type="button"
                      onClick={() => dispatch(projectCollapseToggled(project.id))}
                      aria-expanded={!collapsedProjectIds.has(project.id)}
                      aria-controls={`project-${project.id}-threads`}
                      title={collapsedProjectIds.has(project.id) ? `Expand ${project.name}` : `Collapse ${project.name}`}
                      className="flex h-7 min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-md text-left outline-none transition hover:text-ghost-foreground focus-visible:ring-1 focus-visible:ring-ghost-green/45"
                    >
                      <span className={`relative grid size-5 shrink-0 place-items-center ${
                        project.threads.some((thread) => thread.id === selectedThreadId)
                          ? 'text-ghost-green'
                          : 'text-ghost-dim'
                      }`}>
                        {collapsedProjectIds.has(project.id)
                          ? <Folder size={16} strokeWidth={1.7} />
                          : <FolderOpen size={16} strokeWidth={1.7} />}
                        <Globe2 size={7} strokeWidth={1.9} className="absolute bottom-0 right-0 rounded-full bg-ghost-sidebar" />
                      </span>
                      <span className="min-w-0 flex-1 truncate text-xs font-semibold">{project.name}</span>
                    </Button>
                    {(() => {
                      const counts = projectActivityCounts.get(project.id)
                      if (!counts || (counts.working === 0 && counts.finished === 0)) return null
                      return (
                        <span className="flex shrink-0 items-center gap-1 group-hover/project:hidden">
                          {counts.working > 0 && (
                            <span
                              className="rounded-full border border-ghost-green/40 px-1.5 py-0.5 font-mono text-[9px] leading-none text-ghost-green"
                              title={`${counts.working} coding ${counts.working === 1 ? 'agent is' : 'agents are'} working`}
                            >
                              {counts.working} live
                            </span>
                          )}
                          {counts.finished > 0 && (
                            <span
                              className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[9px] leading-none text-ghost-green"
                              title={`${counts.finished} ${counts.finished === 1 ? 'thread needs' : 'threads need'} review`}
                            >
                              {counts.finished} done
                            </span>
                          )}
                        </span>
                      )
                    })()}
                    <IconButton
                      type="button"
                      size="xs"
                      shrink
                      variant="subtle-white"
                      onClick={() => openNewThread(project.id)}
                      className="opacity-0 group-hover/project:opacity-100 focus:opacity-100"
                      aria-label={`New thread in ${project.name}`}
                      title="New thread"
                    >
                      <Plus size={12} />
                    </IconButton>
                    <IconButton
                      type="button"
                      size="xs"
                      shrink
                      variant="subtle-white"
                      onClick={() => openProjectSettings(project.id)}
                      className="opacity-0 group-hover/project:opacity-100 focus:opacity-100"
                      aria-label={`Settings for ${project.name}`}
                      title="Project settings"
                    >
                      <Settings2 size={12} />
                    </IconButton>
                    <IconButton
                      type="button"
                      size="xs"
                      shrink
                      variant="danger"
                      onClick={() => onDeleteProject(project)}
                      className="opacity-0 group-hover/project:opacity-100 focus:opacity-100"
                      aria-label={`Remove ${project.name}`}
                    >
                      {deletingProjectId === project.id ? <LoaderCircle size={11} className="animate-spin" /> : <Trash2 size={11} />}
                    </IconButton>
                  </div>

                  <ul
                    id={`project-${project.id}-threads`}
                    hidden={collapsedProjectIds.has(project.id)}
                    className="mt-0.5 space-y-0.5"
                  >
                    {renderProjectThreadRows(project)}
                  </ul>
                  {dropTarget?.kind === 'project' && dropTarget.id === project.id && dropTarget.position === 'after' && (
                    <span className="pointer-events-none absolute inset-x-2 bottom-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
                  )}
                </li>
              ))}
            </ul>
          )}
        </nav>

        <SidebarFooterNav />

        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize sidebar"
          tabIndex={0}
          title="Drag to resize; arrow keys also work. Double-click to reset."
          onPointerDown={(event) => {
            event.preventDefault()
            event.currentTarget.setPointerCapture(event.pointerId)
          }}
          onPointerMove={(event) => {
            if (!event.currentTarget.hasPointerCapture(event.pointerId)) return
            const left = asideRef.current?.getBoundingClientRect().left ?? 0
            dispatch(sidebarWidthChanged(event.clientX - left))
          }}
          onDoubleClick={() => dispatch(sidebarWidthReset())}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
            event.preventDefault()
            const delta = event.key === 'ArrowLeft' ? -sidebarWidthKeyboardStep : sidebarWidthKeyboardStep
            dispatch(sidebarWidthNudged(delta))
          }}
          className="absolute inset-y-0 right-0 z-50 hidden w-1 cursor-col-resize transition-colors hover:bg-ghost-green/30 focus-visible:bg-ghost-green/40 active:bg-ghost-green/40 md:block"
        />
      </aside>
    </>
  )
}
