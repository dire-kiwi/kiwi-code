import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  Archive,
  Bookmark,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Clock3,
  CornerDownRight,
  Folder,
  FolderOpen,
  GitBranch,
  GitFork,
  Globe2,
  GripVertical,
  ListTree,
  LoaderCircle,
  PanelLeftClose,
  Plus,
  Search,
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
  selectActiveProjectIds,
  selectActiveProjects,
  selectActiveThreadIndex,
} from '@/store/selectors/workspace'
import { selectActiveProfileId } from '@/store/slices/preferences'
import { selectPiActivities } from '@/store/slices/agentActivity'
import { selectProfiles } from '@/store/slices/profiles'
import {
  selectArchivingThreadId,
  selectBookmarkingThreadId,
  selectDeletingProjectId,
  selectDeletingThreadId,
  threadBookmarked,
} from '@/store/slices/projects'
import {
  bookmarksOnlyChanged,
  childThreadsCollapseToggled,
  moreThreadsToggled,
  projectCollapseToggled,
  selectBookmarksOnly,
  selectCollapsedChildThreadIds,
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
import { projectFinderOpened, selectSidebarOpen, sidebarClosed } from '@/store/slices/ui'
import type { Profile, Project, Thread } from '@/types'
import { useProcessWebServers, useThreadUsage } from '@/wire/serverData'
import { Button, IconButton, SelectionButton } from '@/ui/buttons'
import { SidebarActivityView } from './SidebarActivityView'
import { SidebarAddProjectForm } from './SidebarAddProjectForm'
import { SidebarFooterNav } from './SidebarFooterNav'
import { SidebarProfileSwitcher } from './SidebarProfileSwitcher'
import { SidebarWebServers } from './SidebarWebServers'
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
  const activeProjectIds = useAppSelector(selectActiveProjectIds)
  const deletingProjectId = useAppSelector(selectDeletingProjectId)
  const deletingThreadId = useAppSelector(selectDeletingThreadId)
  const archivingThreadId = useAppSelector(selectArchivingThreadId)
  const bookmarkingThreadId = useAppSelector(selectBookmarkingThreadId)
  const usageSnapshots = useThreadUsage()
  const allWebServers = useProcessWebServers()
  // Only servers belonging to a project the current profile can see.
  const processWebServers = useMemo(
    () => allWebServers.filter((server) => activeProjectIds.has(server.projectId)),
    [activeProjectIds, allWebServers],
  )
  const isOpen = useAppSelector(selectSidebarOpen)
  const viewMode = useAppSelector(selectSidebarView)
  const sidebarWidth = useAppSelector(selectSidebarWidth)
  const collapsedProjectIds = useAppSelector(selectCollapsedProjectIds)
  const expandedMoreProjectIds = useAppSelector(selectExpandedMoreProjectIds)
  const collapsedChildThreadIds = useAppSelector(selectCollapsedChildThreadIds)
  const bookmarksOnly = useAppSelector(selectBookmarksOnly)
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
  const bookmarkThreadIdsByProject = useMemo(() => new Map(
    projects.map((project) => [
      project.id,
      new Set(threadIndex.tree(project.id)?.bookmarkedPathIds() ?? []),
    ]),
  ), [projects, threadIndex])
  const visibleProjects = bookmarksOnly
    ? projects.filter((project) => (bookmarkThreadIdsByProject.get(project.id)?.size ?? 0) > 0)
    : projects

  // The thunk carries the failure message; something has to surface it, or a
  // refused bookmark just stops the spinner and says nothing.
  async function bookmarkThread(project: Project, thread: Thread) {
    const result = await dispatch(threadBookmarked({
      projectId: project.id,
      threadId: thread.id,
      bookmarked: !thread.bookmarked,
    }))
    if (threadBookmarked.rejected.match(result)) window.alert(result.payload)
  }

  const openNewThread = (projectId: string) => navigateAndClose(newThreadPath(projectId))
  const openProjectSettings = (projectId: string) =>
    navigateAndClose(projectSettingsPath(projectId, DEFAULT_PROJECT_SETTINGS_SECTION))

  useEffect(() => {
    if (!selectedThreadId) return
    const project = threadIndex.projectByThreadId.get(selectedThreadId)
    const tree = project ? threadIndex.tree(project.id) : null
    const selected = tree?.byId.get(selectedThreadId)
    if (!project || !tree || !selected) return

    const ancestors = tree.ancestors(selected.id)
    const root = ancestors.at(-1) ?? selected
    dispatch(threadRevealed({
      projectId: project.id,
      ancestorIds: ancestors.map((ancestor) => ancestor.id),
      expandArchived: Boolean(root?.archivedAt),
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
    const closed = Boolean(thread.closedAt)
    const isChild = Boolean(thread.parentThreadId)
    const canReorder = !bookmarksOnly && !isChild && !archived && activeThreadCount > 1 && !savingOrder
    const selected = thread.id === selectedThreadId
    const tree = threadIndex.tree(project.id)
    const children = (tree?.children(thread.id) ?? []).filter((candidate) =>
      (!visibleThreadIds || visibleThreadIds.has(candidate.id))
        && (!candidate.closedAt || bookmarksOnly),
    )
    const hasChildren = children.length > 0
    const childrenExpanded = bookmarksOnly ? hasChildren : !collapsedChildThreadIds.has(thread.id)
    const usage = usageByThread.get(`${project.id}\0${thread.id}`)
    const hasDescendantUsageScope = (tree?.descendants(thread.id).length ?? 0) > 0
    const displayedUsage = usage && (hasDescendantUsageScope ? usage.total : usage.own)
    const usageScope = hasDescendantUsageScope
      ? 'Total usage including all descendant threads'
      : 'Usage for this thread'
    const usageTitle = displayedUsage
      ? `\n${usageScope}: ${usageDescription(displayedUsage)}${usage?.limitReached ? ' — limit reached' : ''}`
      : ''
    const { activity: piActivity, childActivity } = threadIndex.threadActivity(project.id, thread.id)
    const locationTitle = thread.worktree && thread.branch
      ? `${thread.branch}\n${thread.cwd}`
      : thread.cwd
    const activityTitle = piActivity
      ? `\n${childActivity ? 'Child coding agent' : 'Coding agent'} is ${piActivity.state === 'working' ? 'working' : 'finished'}`
      : ''
    const archivedTitle = thread.archivedAt
      ? `\nArchived ${new Date(thread.archivedAt).toLocaleString()}`
      : ''
    const closedTitle = thread.closedAt
      ? `\nCompleted ${new Date(thread.closedAt).toLocaleString()}`
      : ''
    const selectionPadding = isChild
      ? hasChildren ? 'pl-8' : 'pl-3'
      : hasChildren ? 'pl-12' : 'pl-8'
    const menuOpen = threadMenuId === thread.id
    // While the actions menu is open the row must stay fully opaque: any opacity below 1 turns the
    // row into a stacking context, which traps the menu behind the rows rendered after it.

    return (
      <li
        key={thread.id}
        data-thread-row
        data-project-id={project.id}
        data-thread-id={thread.id}
        data-parent-thread-id={thread.parentThreadId}
        onDragOver={bookmarksOnly || isChild || archived ? undefined : (event) => handleThreadDragOver(event, project.id, thread.id)}
        onDrop={bookmarksOnly || isChild || archived ? undefined : (event) => handleThreadDrop(event, project, thread.id)}
        className={`${menuOpen ? 'relative z-40' : ''} ${!menuOpen && (archived || closed) ? 'opacity-75' : ''} ${
          !menuOpen && bookmarksOnly && !thread.bookmarked ? 'opacity-65' : ''
        } ${draggedItem?.kind === 'thread' && draggedItem.id === thread.id ? 'opacity-45' : ''}`}
      >
        <div className="group/thread relative transition-opacity">
          {!bookmarksOnly && !isChild && !archived && dropTarget?.kind === 'thread' && dropTarget.projectId === project.id && dropTarget.id === thread.id && dropTarget.position === 'before' && (
            <span className="pointer-events-none absolute inset-x-2 top-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
          )}
          {!bookmarksOnly && !isChild && !archived && (
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
          {hasChildren && !bookmarksOnly && (
            <button
              type="button"
              onClick={() => dispatch(childThreadsCollapseToggled(thread.id))}
              aria-expanded={childrenExpanded}
              aria-controls={`thread-${thread.id}-children`}
              aria-label={`${childrenExpanded ? 'Collapse' : 'Expand'} ${children.length} child ${children.length === 1 ? 'thread' : 'threads'} for ${thread.title}`}
              title={`${childrenExpanded ? 'Hide' : 'Show'} ${children.length} child ${children.length === 1 ? 'thread' : 'threads'}`}
              className={`absolute top-1/2 z-10 grid size-5 -translate-y-1/2 place-items-center rounded text-ghost-faint transition hover:bg-ghost-raised hover:text-ghost-white ${isChild ? 'left-1.5' : 'left-6'}`}
            >
              <ChevronRight size={11} className={`transition-transform ${childrenExpanded ? 'rotate-90' : ''}`} />
            </button>
          )}
          <SelectionButton
            type="button"
            selected={selected}
            selectionVariant="navigation"
            onClick={() => onSelectThread(project.id, thread.id)}
            aria-current={selected ? 'page' : undefined}
            title={`${locationTitle}${archivedTitle}${closedTitle}${activityTitle}${usageTitle}`}
            className={`${selectionPadding} pr-12`}
          >
            {isChild && <CornerDownRight size={11} className="shrink-0 text-ghost-cyan" aria-hidden="true" />}
            {thread.worktree && <GitBranch size={11} className="shrink-0 text-ghost-green" />}
            {archived && !thread.worktree && <Archive size={11} className="shrink-0 text-ghost-faint" />}
            {closed && <Clock3 size={11} className="shrink-0 text-ghost-faint" aria-hidden="true" />}
            <span className="min-w-0 flex-1 truncate">{thread.title}</span>
            {thread.closedAt && <span className="sr-only">Completed {new Date(thread.closedAt).toLocaleString()}</span>}
            {hasChildren && (
              <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full border border-ghost-border/65 px-1 py-0.5 font-mono text-[9px] leading-none text-ghost-faint" aria-label={`${children.length} child ${children.length === 1 ? 'thread' : 'threads'}`}>
                <GitFork size={8} aria-hidden="true" />
                {children.length}
              </span>
            )}
            {piActivity?.state === 'working' ? (
              <>
                <LoaderCircle size={11} className="shrink-0 animate-spin text-ghost-green" aria-hidden="true" />
                <span className="sr-only">{childActivity ? 'Child coding agent' : 'Coding agent'} is working</span>
              </>
            ) : piActivity?.state === 'finished' ? (
              <>
                <span className="size-1.5 shrink-0 rounded-full bg-ghost-green shadow-[0_0_6px_rgba(181,189,104,0.7)]" aria-hidden="true" />
                <span className="sr-only">{childActivity ? 'Child coding agent' : 'Coding agent'} finished</span>
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
            <IconButton
              type="button"
              size="xs"
              variant="subtle"
              disabled={Boolean(bookmarkingThreadId || archivingThreadId || deletingThreadId)}
              onClick={() => void bookmarkThread(project, thread)}
              aria-pressed={Boolean(thread.bookmarked)}
              aria-label={thread.bookmarked ? `Remove bookmark from ${thread.title}` : `Bookmark ${thread.title}`}
              title={thread.bookmarked ? 'Remove bookmark' : 'Bookmark thread'}
              className={thread.bookmarked
                ? 'text-ghost-green hover:text-ghost-bright-green'
                : 'text-ghost-faint opacity-0 transition group-hover/thread:opacity-100 group-focus-within/thread:opacity-100'}
            >
              {bookmarkingThreadId === thread.id
                ? <LoaderCircle size={11} className="animate-spin" />
                : <Bookmark size={11} fill={thread.bookmarked ? 'currentColor' : 'none'} />}
            </IconButton>
            <ThreadActionsMenu
              threadTitle={thread.title}
              archived={archived}
              archiving={archivingThreadId === thread.id}
              deleting={deletingThreadId === thread.id}
              disabled={Boolean(archivingThreadId || deletingThreadId || bookmarkingThreadId)}
              open={menuOpen}
              onOpenChange={(open) => setThreadMenuId(open ? thread.id : null)}
              onArchive={() => onArchiveThread(project, thread, !archived)}
              onDelete={() => onDeleteThread(project, thread)}
              triggerClassName={menuOpen || selected
                ? undefined
                : 'opacity-0 transition group-hover/thread:opacity-100 group-focus-within/thread:opacity-100'}
            />
          </div>
          {!bookmarksOnly && !isChild && !archived && dropTarget?.kind === 'thread' && dropTarget.projectId === project.id && dropTarget.id === thread.id && dropTarget.position === 'after' && (
            <span className="pointer-events-none absolute inset-x-2 bottom-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
          )}
        </div>
        {hasChildren && childrenExpanded && (
          <ul id={`thread-${thread.id}-children`} className="ml-5 space-y-0.5 border-l border-ghost-border/55 pl-1">
            {children.map((child) => renderThreadRow(project, child, activeThreadCount, visibleThreadIds))}
          </ul>
        )}
      </li>
    )
  }

  function renderProjectThreadRows(project: Project) {
    const tree = threadIndex.tree(project.id)
    if (!tree) return null
    const roots = tree.roots
    const activeThreads = roots.filter((thread) => !thread.archivedAt)
    const archivedThreads = roots.filter((thread) => thread.archivedAt)
    if (bookmarksOnly) {
      const visibleThreadIds = bookmarkThreadIdsByProject.get(project.id) ?? new Set<string>()
      return roots
        .filter((thread) => visibleThreadIds.has(thread.id))
        .map((thread) => renderThreadRow(project, thread, activeThreads.length, visibleThreadIds))
    }
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
                title="Activity view: working, needs review, pinned, recent"
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
            {viewMode === 'tree' && (
              <IconButton
                type="button"
                size="sm"
                variant="subtle"
                onClick={() => dispatch(bookmarksOnlyChanged(!bookmarksOnly))}
                aria-pressed={bookmarksOnly}
                aria-label={bookmarksOnly ? 'Show all threads' : 'Show bookmarked threads only'}
                title={bookmarksOnly ? 'Show all threads' : 'Show bookmarked threads only'}
                className={bookmarksOnly ? 'bg-ghost-green/10 text-ghost-green' : undefined}
              >
                <Bookmark size={14} fill={bookmarksOnly ? 'currentColor' : 'none'} />
              </IconButton>
            )}
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => dispatch(projectFinderOpened())}
              aria-label="Find a project or thread"
              title="Find projects and threads (Ctrl+F)"
            >
              <Search size={14} />
            </IconButton>
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
          ) : bookmarksOnly && visibleProjects.length === 0 ? (
            <div className="mx-1 mt-2 rounded-lg border border-dashed border-ghost-border/70 px-3 py-6 text-center">
              <Bookmark size={17} className="mx-auto text-ghost-faint" />
              <p className="mt-2.5 text-[10px] text-ghost-muted">No bookmarked threads</p>
              <Button
                type="button"
                variant="text"
                onClick={() => dispatch(bookmarksOnlyChanged(false))}
                className="mt-2 text-[9px] text-ghost-green"
              >
                Show all threads
              </Button>
            </div>
          ) : (
            <ul className="space-y-2.5">
              {visibleProjects.map((project) => (
                <li
                  key={project.id}
                  data-project-row
                  data-project-id={project.id}
                  onDragOver={bookmarksOnly ? undefined : (event) => handleProjectDragOver(event, project.id)}
                  onDrop={bookmarksOnly ? undefined : (event) => handleProjectDrop(event, project.id)}
                  className={`group/project relative transition-opacity ${
                    draggedItem?.kind === 'project' && draggedItem.id === project.id ? 'opacity-45' : ''
                  }`}
                >
                  {!bookmarksOnly && dropTarget?.kind === 'project' && dropTarget.id === project.id && dropTarget.position === 'before' && (
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
                      draggable={!bookmarksOnly && !savingOrder && projects.length > 1}
                      disabled={bookmarksOnly || savingOrder || projects.length < 2}
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
                      onClick={() => {
                        if (!bookmarksOnly) dispatch(projectCollapseToggled(project.id))
                      }}
                      disabled={bookmarksOnly}
                      aria-expanded={bookmarksOnly || !collapsedProjectIds.has(project.id)}
                      aria-controls={`project-${project.id}-threads`}
                      title={bookmarksOnly ? 'Matching projects stay expanded while filtering' : collapsedProjectIds.has(project.id) ? `Expand ${project.name}` : `Collapse ${project.name}`}
                      className="flex h-7 min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-md text-left outline-none transition hover:text-ghost-foreground focus-visible:ring-1 focus-visible:ring-ghost-green/45"
                    >
                      <span className={`relative grid size-5 shrink-0 place-items-center ${
                        project.threads.some((thread) => thread.id === selectedThreadId)
                          ? 'text-ghost-green'
                          : 'text-ghost-dim'
                      }`}>
                        {!bookmarksOnly && collapsedProjectIds.has(project.id)
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
                    hidden={!bookmarksOnly && collapsedProjectIds.has(project.id)}
                    className="mt-0.5 space-y-0.5"
                  >
                    {renderProjectThreadRows(project)}
                  </ul>
                  {!bookmarksOnly && dropTarget?.kind === 'project' && dropTarget.id === project.id && dropTarget.position === 'after' && (
                    <span className="pointer-events-none absolute inset-x-2 bottom-0 z-20 h-0.5 rounded-full bg-ghost-green shadow-[0_0_7px_rgba(181,189,104,0.8)]" />
                  )}
                </li>
              ))}
            </ul>
          )}

          <SidebarWebServers
            webServers={processWebServers}
            onNavigate={() => dispatch(sidebarClosed())}
          />
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
