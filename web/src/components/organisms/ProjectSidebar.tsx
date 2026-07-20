import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import {
  Archive,
  ArchiveRestore,
  Bookmark,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Clock3,
  CornerDownRight,
  ExternalLink,
  Folder,
  FolderGit2,
  FolderOpen,
  GitBranch,
  GitFork,
  Globe2,
  GripVertical,
  LoaderCircle,
  PanelLeftClose,
  PanelsTopLeft,
  Plus,
  RadioTower,
  RotateCw,
  Search,
  Settings2,
  Trash2,
  X,
} from 'lucide-react'
import {
  createProfile,
  createProject,
  restartApplication,
  waitForApplicationRestart,
} from '../../api'
import { formatCompactTokens, formatCompactUsd, usageDescription } from '../../lib/formatUsage'
import { sidebarThreadActivity } from '../../sidebar-thread-activity.mjs'
import { bookmarkedThreadPathIds, defaultVisibleRootThreadIds } from '../../sidebar-thread-visibility.mjs'
import type { PiThreadActivity, ProcessWebServer, Profile, Project, Thread, ThreadUsageSnapshot } from '../../types'
import { Button } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { TextInput } from '../atoms/Input'
import { SelectionButton } from '../atoms/SelectionButton'
import { Select } from '../atoms/Select'
import { BackendSwitcher } from '../molecules/BackendSwitcher'
import { ProjectPathAutocomplete } from '../molecules/ProjectPathAutocomplete'

type ProjectSidebarProps = {
  profiles: Profile[]
  activeProfileId: string
  projects: Project[]
  piActivities: PiThreadActivity[]
  processWebServers: ProcessWebServer[]
  usageSnapshots: ThreadUsageSnapshot[]
  selectedThreadId: string | null
  deletingProjectId: string | null
  deletingThreadId: string | null
  archivingThreadId: string | null
  bookmarkingThreadId: string | null
  cleanupSelected: boolean
  tmuxSelected: boolean
  settingsSelected: boolean
  isOpen: boolean
  onClose: () => void
  onOpenFinder: () => void
  onSelectProfile: (profileId: string) => void
  onProfileCreated: (profile: Profile) => void
  onSelectThread: (projectId: string, threadId: string) => void
  onNewThread: (projectId: string) => void
  onOpenCleanup: () => void
  onOpenTmux: () => void
  onOpenSettings: () => void
  onProjectCreated: (project: Project) => void
  onReorderProjects: (projectIds: string[]) => Promise<void>
  onReorderThreads: (projectId: string, threadIds: string[]) => Promise<void>
  onDeleteProject: (project: Project) => void
  onArchiveThread: (project: Project, thread: Thread, archived: boolean) => void
  onBookmarkThread: (project: Project, thread: Thread, bookmarked: boolean) => void
  onDeleteThread: (project: Project, thread: Thread) => void
}

const newProfileValue = '__new-profile__'

type DragItem =
  | { kind: 'project'; id: string }
  | { kind: 'thread'; projectId: string; id: string }

type DropPosition = 'before' | 'after'

type DropTarget =
  | { kind: 'project'; id: string; position: DropPosition }
  | { kind: 'thread'; projectId: string; id: string; position: DropPosition }

function reorderedIds(ids: string[], sourceId: string, targetId: string, position: DropPosition) {
  if (sourceId === targetId) return ids
  const withoutSource = ids.filter((id) => id !== sourceId)
  const targetIndex = withoutSource.indexOf(targetId)
  if (targetIndex < 0 || withoutSource.length === ids.length) return ids
  withoutSource.splice(targetIndex + (position === 'after' ? 1 : 0), 0, sourceId)
  return withoutSource
}

function verticalDropPosition(event: DragEvent<HTMLElement>): DropPosition {
  const bounds = event.currentTarget.getBoundingClientRect()
  return event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
}

function projectDropPosition(event: DragEvent<HTMLLIElement>): DropPosition {
  const header = event.currentTarget.querySelector<HTMLElement>('[data-project-drag-image]')
  const bounds = (header ?? event.currentTarget).getBoundingClientRect()
  return event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
}

function sameOrder(left: string[], right: string[]) {
  return left.length === right.length && left.every((id, index) => id === right[index])
}

function webServerAddress(value: string) {
  try {
    const url = new URL(value)
    return `${url.host}${url.pathname === '/' ? '' : url.pathname}`
  } catch {
    return value
  }
}

function rootThreads(project: Project) {
  return project.threads.filter((thread) => !thread.parentThreadId)
}

function orderedThreadTreeIds(project: Project, rootIds: string[]) {
  const childrenByParent = new Map<string, Thread[]>()
  for (const thread of project.threads) {
    if (!thread.parentThreadId) continue
    const children = childrenByParent.get(thread.parentThreadId) ?? []
    children.push(thread)
    childrenByParent.set(thread.parentThreadId, children)
  }
  const ordered: string[] = []
  const seen = new Set<string>()
  const appendTree = (threadId: string) => {
    if (seen.has(threadId)) return
    seen.add(threadId)
    ordered.push(threadId)
    for (const child of childrenByParent.get(threadId) ?? []) appendTree(child.id)
  }
  for (const rootId of rootIds) appendTree(rootId)
  // Keep malformed/orphaned data addressable instead of dropping IDs from the
  // complete-order request. The backend normally prevents this state.
  for (const thread of project.threads) appendTree(thread.id)
  return ordered
}

export function ProjectSidebar({
  profiles,
  activeProfileId,
  projects,
  piActivities,
  processWebServers,
  usageSnapshots,
  selectedThreadId,
  deletingProjectId,
  deletingThreadId,
  archivingThreadId,
  bookmarkingThreadId,
  cleanupSelected,
  tmuxSelected,
  settingsSelected,
  isOpen,
  onClose,
  onOpenFinder,
  onSelectProfile,
  onProfileCreated,
  onSelectThread,
  onNewThread,
  onOpenCleanup,
  onOpenTmux,
  onOpenSettings,
  onProjectCreated,
  onReorderProjects,
  onReorderThreads,
  onDeleteProject,
  onArchiveThread,
  onBookmarkThread,
  onDeleteThread,
}: ProjectSidebarProps) {
  const [showProjectForm, setShowProjectForm] = useState(false)
  const [name, setName] = useState('')
  const [path, setPath] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [creatingProfile, setCreatingProfile] = useState(false)
  const [draggedItem, setDraggedItem] = useState<DragItem | null>(null)
  const [dropTarget, setDropTarget] = useState<DropTarget | null>(null)
  const [savingOrder, setSavingOrder] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [collapsedProjectIds, setCollapsedProjectIds] = useState<ReadonlySet<string>>(() => new Set())
  const [expandedMoreProjectIds, setExpandedMoreProjectIds] = useState<ReadonlySet<string>>(() => new Set())
  const [collapsedChildThreadIds, setCollapsedChildThreadIds] = useState<ReadonlySet<string>>(() => new Set())
  const [bookmarksOnly, setBookmarksOnly] = useState(false)
  const draggedItemRef = useRef<DragItem | null>(null)
  const activeProfile = profiles.find((profile) => profile.id === activeProfileId) ?? profiles[0]
  const usageByThread = useMemo(() => new Map(
    usageSnapshots.map((snapshot) => [`${snapshot.projectId}\0${snapshot.threadId}`, snapshot]),
  ), [usageSnapshots])
  const bookmarkThreadIdsByProject = useMemo(() => new Map(
    projects.map((project) => [project.id, new Set(bookmarkedThreadPathIds(project.threads))]),
  ), [projects])
  const visibleProjects = bookmarksOnly
    ? projects.filter((project) => (bookmarkThreadIdsByProject.get(project.id)?.size ?? 0) > 0)
    : projects

  useEffect(() => {
    if (!selectedThreadId) return
    const project = projects.find((candidate) => candidate.threads.some((thread) => thread.id === selectedThreadId))
    const selected = project?.threads.find((thread) => thread.id === selectedThreadId)
    if (!project || !selected) return

    const byId = new Map(project.threads.map((thread) => [thread.id, thread]))
    const ancestors: string[] = []
    let parentId = selected.parentThreadId
    while (parentId) {
      ancestors.push(parentId)
      parentId = byId.get(parentId)?.parentThreadId
    }
    if (ancestors.length > 0) {
      setCollapsedChildThreadIds((current) => {
        const next = new Set(current)
        for (const id of ancestors) next.delete(id)
        return next.size === current.size ? current : next
      })
    }
    const root = ancestors.length > 0 ? byId.get(ancestors.at(-1)!) : selected
    if (root?.archivedAt) {
      setExpandedMoreProjectIds((current) => {
        if (current.has(project.id)) return current
        return new Set(current).add(project.id)
      })
    }
    setCollapsedProjectIds((current) => {
      if (!current.has(project.id)) return current
      const next = new Set(current)
      next.delete(project.id)
      return next
    })
  }, [projects, selectedThreadId])

  async function handleProfileSelection(profileId: string) {
    if (profileId !== newProfileValue) {
      onSelectProfile(profileId)
      return
    }

    const name = window.prompt('Name the new profile')?.trim()
    if (!name) return
    setCreatingProfile(true)
    try {
      const profile = await createProfile(name)
      onProfileCreated(profile)
    } catch (reason) {
      window.alert(reason instanceof Error ? reason.message : 'Could not create that profile.')
    } finally {
      setCreatingProfile(false)
    }
  }

  async function handleRestart() {
    if (restarting || !window.confirm('Restart dire/mux?\n\nThe application will fully exit before a fresh instance starts. Your tmux sessions and running tools will keep running.')) return

    setRestarting(true)
    try {
      const response = await restartApplication()
      await waitForApplicationRestart(response.instanceId)
      window.location.reload()
    } catch (reason) {
      setRestarting(false)
      window.alert(reason instanceof Error ? reason.message : 'Could not restart dire/mux.')
    }
  }

  async function handleProjectSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      const project = await createProject({ name, path, profileId: activeProfileId })
      setName('')
      setPath('')
      setShowProjectForm(false)
      onProjectCreated(project)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not add that project.')
    } finally {
      setSubmitting(false)
    }
  }

  function setActiveDrag(item: DragItem | null) {
    draggedItemRef.current = item
    setDraggedItem(item)
    if (!item) setDropTarget(null)
  }

  function toggleProject(projectId: string) {
    setCollapsedProjectIds((current) => {
      const next = new Set(current)
      if (next.has(projectId)) {
        next.delete(projectId)
      } else {
        next.add(projectId)
      }
      return next
    })
  }

  function toggleMoreThreads(projectId: string) {
    setExpandedMoreProjectIds((current) => {
      const next = new Set(current)
      if (next.has(projectId)) {
        next.delete(projectId)
      } else {
        next.add(projectId)
      }
      return next
    })
  }

  function toggleChildThreads(threadId: string) {
    setCollapsedChildThreadIds((current) => {
      const next = new Set(current)
      if (next.has(threadId)) next.delete(threadId)
      else next.add(threadId)
      return next
    })
  }

  function startProjectDrag(event: DragEvent<HTMLButtonElement>, projectId: string) {
    if (savingOrder || projects.length < 2) {
      event.preventDefault()
      return
    }
    setActiveDrag({ kind: 'project', id: projectId })
    setDropTarget(null)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', `project:${projectId}`)
    const row = event.currentTarget.closest<HTMLElement>('[data-project-drag-image]')
    if (row) event.dataTransfer.setDragImage(row, 16, 20)
  }

  function startThreadDrag(event: DragEvent<HTMLButtonElement>, projectId: string, threadId: string) {
    const project = projects.find((item) => item.id === projectId)
    const activeThreads = project ? rootThreads(project).filter((thread) => !thread.archivedAt) : []
    if (savingOrder || !project || activeThreads.length < 2 || !activeThreads.some((thread) => thread.id === threadId)) {
      event.preventDefault()
      return
    }
    setActiveDrag({ kind: 'thread', projectId, id: threadId })
    setDropTarget(null)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', `thread:${projectId}:${threadId}`)
    const row = event.currentTarget.closest<HTMLElement>('[data-thread-row]')
    if (row) event.dataTransfer.setDragImage(row, 16, 20)
  }

  function updateDropTarget(next: DropTarget) {
    setDropTarget((current) => {
      if (!current || current.kind !== next.kind) return next
      if (current.id !== next.id || current.position !== next.position) return next
      if (current.kind === 'thread' && next.kind === 'thread' && current.projectId !== next.projectId) return next
      return current
    })
  }

  function saveProjectOrder(projectIds: string[]) {
    const currentIds = projects.map((project) => project.id)
    if (sameOrder(currentIds, projectIds)) return
    setSavingOrder(true)
    void onReorderProjects(projectIds).finally(() => setSavingOrder(false))
  }

  function saveThreadOrder(project: Project, threadIds: string[]) {
    const currentIds = project.threads.map((thread) => thread.id)
    if (sameOrder(currentIds, threadIds)) return
    setSavingOrder(true)
    void onReorderThreads(project.id, threadIds).finally(() => setSavingOrder(false))
  }

  function handleProjectDragOver(event: DragEvent<HTMLLIElement>, projectId: string) {
    const item = draggedItemRef.current
    if (item?.kind !== 'project' || item.id === projectId) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = 'move'
    updateDropTarget({ kind: 'project', id: projectId, position: projectDropPosition(event) })
  }

  function handleProjectDrop(event: DragEvent<HTMLLIElement>, projectId: string) {
    const item = draggedItemRef.current
    if (item?.kind !== 'project' || item.id === projectId) return
    event.preventDefault()
    event.stopPropagation()
    const projectIds = reorderedIds(
      projects.map((project) => project.id),
      item.id,
      projectId,
      projectDropPosition(event),
    )
    setActiveDrag(null)
    saveProjectOrder(projectIds)
  }

  function handleThreadDragOver(event: DragEvent<HTMLLIElement>, projectId: string, threadId: string) {
    const item = draggedItemRef.current
    if (item?.kind !== 'thread' || item.projectId !== projectId || item.id === threadId) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = 'move'
    updateDropTarget({ kind: 'thread', projectId, id: threadId, position: verticalDropPosition(event) })
  }

  function handleThreadDrop(event: DragEvent<HTMLLIElement>, project: Project, threadId: string) {
    const item = draggedItemRef.current
    if (item?.kind !== 'thread' || item.projectId !== project.id || item.id === threadId) return
    event.preventDefault()
    event.stopPropagation()
    const roots = rootThreads(project)
    const activeThreadIds = roots.filter((thread) => !thread.archivedAt).map((thread) => thread.id)
    const archivedThreadIds = roots.filter((thread) => thread.archivedAt).map((thread) => thread.id)
    const threadIds = reorderedIds(
      activeThreadIds,
      item.id,
      threadId,
      verticalDropPosition(event),
    )
    setActiveDrag(null)
    saveThreadOrder(project, orderedThreadTreeIds(project, [...threadIds, ...archivedThreadIds]))
  }

  function handleProjectHandleKeyDown(event: KeyboardEvent<HTMLButtonElement>, projectId: string) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    if (savingOrder) return
    const projectIds = projects.map((project) => project.id)
    const index = projectIds.indexOf(projectId)
    const targetIndex = index + (event.key === 'ArrowUp' ? -1 : 1)
    if (index < 0 || targetIndex < 0 || targetIndex >= projectIds.length) return
    const reordered = [...projectIds]
    ;[reordered[index], reordered[targetIndex]] = [reordered[targetIndex], reordered[index]]
    saveProjectOrder(reordered)
  }

  function handleThreadHandleKeyDown(event: KeyboardEvent<HTMLButtonElement>, project: Project, threadId: string) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    if (savingOrder) return
    const roots = rootThreads(project)
    const activeThreadIds = roots.filter((thread) => !thread.archivedAt).map((thread) => thread.id)
    const archivedThreadIds = roots.filter((thread) => thread.archivedAt).map((thread) => thread.id)
    const index = activeThreadIds.indexOf(threadId)
    const targetIndex = index + (event.key === 'ArrowUp' ? -1 : 1)
    if (index < 0 || targetIndex < 0 || targetIndex >= activeThreadIds.length) return
    const reordered = [...activeThreadIds]
    ;[reordered[index], reordered[targetIndex]] = [reordered[targetIndex], reordered[index]]
    saveThreadOrder(project, orderedThreadTreeIds(project, [...reordered, ...archivedThreadIds]))
  }

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
    const children = project.threads.filter((candidate) =>
      candidate.parentThreadId === thread.id
        && (!visibleThreadIds || visibleThreadIds.has(candidate.id))
        && (!candidate.closedAt || bookmarksOnly),
    )
    const hasChildren = children.length > 0
    const childrenExpanded = bookmarksOnly ? hasChildren : !collapsedChildThreadIds.has(thread.id)
    const descendantIds = new Set<string>()
    const collectDescendants = (parentId: string) => {
      for (const candidate of project.threads) {
        if (candidate.parentThreadId !== parentId || descendantIds.has(candidate.id)) continue
        descendantIds.add(candidate.id)
        collectDescendants(candidate.id)
      }
    }
    collectDescendants(thread.id)
    const usage = usageByThread.get(`${project.id}\0${thread.id}`)
    const hasDescendantUsageScope = descendantIds.size > 0
    const displayedUsage = usage && (hasDescendantUsageScope ? usage.total : usage.own)
    const usageScope = hasDescendantUsageScope
      ? 'Total usage including all descendant threads'
      : 'Usage for this thread'
    const usageTitle = displayedUsage
      ? `\n${usageScope}: ${usageDescription(displayedUsage)}${usage?.limitReached ? ' — limit reached' : ''}`
      : ''
    const { activity: piActivity, childActivity } = sidebarThreadActivity(
      project.threads,
      piActivities,
      project.id,
      thread.id,
    )
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

    return (
      <li
        key={thread.id}
        data-thread-row
        data-project-id={project.id}
        data-thread-id={thread.id}
        data-parent-thread-id={thread.parentThreadId}
        onDragOver={bookmarksOnly || isChild || archived ? undefined : (event) => handleThreadDragOver(event, project.id, thread.id)}
        onDrop={bookmarksOnly || isChild || archived ? undefined : (event) => handleThreadDrop(event, project, thread.id)}
        className={`${archived || closed ? 'opacity-75' : ''} ${
          bookmarksOnly && !thread.bookmarked ? 'opacity-65' : ''
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
              onDragEnd={() => setActiveDrag(null)}
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
              onClick={() => toggleChildThreads(thread.id)}
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
            className={`${selectionPadding} pr-[4.75rem]`}
          >
            {isChild && <CornerDownRight size={11} className="shrink-0 text-ghost-cyan" aria-hidden="true" />}
            {thread.worktree && <GitBranch size={11} className="shrink-0 text-ghost-green" />}
            {archived && !thread.worktree && <Archive size={11} className="shrink-0 text-ghost-faint" />}
            {closed && <Clock3 size={11} className="shrink-0 text-ghost-faint" aria-hidden="true" />}
            <span className="min-w-0 flex-1 truncate">{thread.title}</span>
            {thread.closedAt && <span className="sr-only">Completed {new Date(thread.closedAt).toLocaleString()}</span>}
            {hasChildren && (
              <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full border border-ghost-border/65 px-1 py-0.5 font-mono text-[8px] text-ghost-faint" aria-label={`${children.length} child ${children.length === 1 ? 'thread' : 'threads'}`}>
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
            {displayedUsage && <span className="sr-only">{usageScope}: {usageDescription(displayedUsage)}{usage?.limitReached ? '. Limit reached.' : ''}</span>}
          </SelectionButton>
          {displayedUsage && (
            <span
              aria-hidden="true"
              className={`pointer-events-none absolute right-8 top-1/2 flex -translate-y-1/2 flex-col items-end font-mono leading-none transition group-hover/thread:opacity-0 group-focus-within/thread:opacity-0 ${
                usage?.limitReached ? 'text-ghost-bright-red' : 'text-ghost-faint'
              }`}
            >
              {hasDescendantUsageScope && (
                <span className="mb-0.5 text-[6px] font-semibold uppercase tracking-[0.12em]">all threads</span>
              )}
              <span className="text-[8px]">{formatCompactTokens(displayedUsage.totalTokens)} · {formatCompactUsd(displayedUsage.costUsd)}</span>
            </span>
          )}
          <div className="absolute right-1 top-1/2 flex -translate-y-1/2 items-center">
            <div className="pointer-events-none flex opacity-0 transition group-hover/thread:pointer-events-auto group-hover/thread:opacity-100 group-focus-within/thread:pointer-events-auto group-focus-within/thread:opacity-100">
              <IconButton
                type="button"
                size="xs"
                variant="subtle"
                disabled={Boolean(archivingThreadId || deletingThreadId || bookmarkingThreadId)}
                onClick={() => onArchiveThread(project, thread, !archived)}
                aria-label={`${archived ? 'Restore' : 'Archive'} ${thread.title}`}
                title={archived ? 'Restore thread' : 'Archive thread'}
              >
                {archivingThreadId === thread.id
                  ? <LoaderCircle size={11} className="animate-spin" />
                  : archived ? <ArchiveRestore size={11} /> : <Archive size={11} />}
              </IconButton>
              <IconButton
                type="button"
                size="xs"
                variant="danger"
                disabled={Boolean(deletingThreadId || archivingThreadId || bookmarkingThreadId)}
                onClick={() => onDeleteThread(project, thread)}
                aria-label={`Delete ${thread.title}`}
                title="Delete thread now"
              >
                {deletingThreadId === thread.id ? <LoaderCircle size={11} className="animate-spin" /> : <Trash2 size={11} />}
              </IconButton>
            </div>
            <IconButton
              type="button"
              size="xs"
              variant="subtle"
              disabled={Boolean(bookmarkingThreadId || archivingThreadId || deletingThreadId)}
              onClick={() => onBookmarkThread(project, thread, !thread.bookmarked)}
              aria-pressed={Boolean(thread.bookmarked)}
              aria-label={thread.bookmarked ? `Remove bookmark from ${thread.title}` : `Bookmark ${thread.title}`}
              title={thread.bookmarked ? 'Remove bookmark' : 'Bookmark thread'}
              className={thread.bookmarked ? 'text-ghost-green hover:text-ghost-bright-green' : 'text-ghost-faint'}
            >
              {bookmarkingThreadId === thread.id
                ? <LoaderCircle size={11} className="animate-spin" />
                : <Bookmark size={11} fill={thread.bookmarked ? 'currentColor' : 'none'} />}
            </IconButton>
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
    const roots = rootThreads(project)
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
              onClick={() => toggleMoreThreads(project.id)}
              aria-expanded={expanded}
              className="flex h-7 w-full items-center gap-1.5 rounded-md px-1.5 text-left font-mono text-[9px] text-ghost-faint transition hover:bg-ghost-raised/45 hover:text-ghost-muted"
            >
              {expanded ? <ChevronUp size={10} /> : <ChevronDown size={10} />}
              <span>{expanded ? 'Show less' : 'Show more'}</span>
              <span className="ml-auto flex items-center gap-1">
                {hiddenActiveCount > 0 && (
                  <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 text-[8px]">
                    {hiddenActiveCount} older
                  </span>
                )}
                {archivedThreads.length > 0 && (
                  <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 text-[8px]">
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
        onClick={onClose}
      />

      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-72 max-w-[calc(100vw-2rem)] shrink-0 flex-col border-r border-ghost-border/70 bg-ghost-sidebar shadow-2xl transition-[transform,visibility] duration-300 md:static md:z-auto md:visible md:max-w-none md:translate-x-0 md:shadow-none ${
          isOpen ? 'visible translate-x-0' : 'invisible -translate-x-full'
        }`}
      >
        <header className="flex h-[4.5rem] shrink-0 items-center justify-between gap-2 border-b border-ghost-border/70 px-3">
          <div className="min-w-0">
            <h1 className="text-xs font-semibold text-ghost-bright-white">Projects</h1>
            <label className="mt-0.5 inline-flex max-w-full items-center">
              <span className="sr-only">Current profile</span>
              <Select
                variant="inline"
                value={activeProfileId}
                options={[
                  ...profiles.map((profile) => ({ value: profile.id, label: profile.name })),
                  { value: newProfileValue, label: '＋ New profile…' },
                ]}
                onChange={(profileId) => void handleProfileSelection(profileId)}
                disabled={creatingProfile}
                className="min-w-0 max-w-36"
                aria-label="Current profile"
              />
            </label>
          </div>
          <div className="flex shrink-0 items-center gap-0.5">
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => setBookmarksOnly((current) => !current)}
              aria-pressed={bookmarksOnly}
              aria-label={bookmarksOnly ? 'Show all threads' : 'Show bookmarked threads only'}
              title={bookmarksOnly ? 'Show all threads' : 'Show bookmarked threads only'}
              className={bookmarksOnly ? 'bg-ghost-green/10 text-ghost-green' : undefined}
            >
              <Bookmark size={14} fill={bookmarksOnly ? 'currentColor' : 'none'} />
            </IconButton>
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={onOpenFinder}
              aria-label="Find a project or thread"
              title="Find projects and threads (Ctrl+F)"
            >
              <Search size={14} />
            </IconButton>
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => {
                setError('')
                setShowProjectForm(true)
              }}
              aria-label="Add a project"
              title="Add project"
            >
              <Plus size={15} />
            </IconButton>
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={onClose}
              className="md:hidden"
              aria-label="Close sidebar"
            >
              <PanelLeftClose size={15} />
            </IconButton>
          </div>
        </header>

        <BackendSwitcher />

        {showProjectForm && (
          <form onSubmit={handleProjectSubmit} className="relative z-10 mx-2 mt-2 rounded-lg border border-ghost-border/70 bg-ghost-panel p-3">
            <div className="mb-3 flex items-center justify-between">
              <p className="text-[10px] font-semibold text-ghost-bright-white">
                Add local project{activeProfile ? ` to ${activeProfile.name}` : ''}
              </p>
              <Button type="button" onClick={() => setShowProjectForm(false)} aria-label="Cancel" className="text-ghost-dim">
                <X size={12} />
              </Button>
            </div>
            <TextInput
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={80}
              placeholder="Project name (optional)"
            />
            <ProjectPathAutocomplete
              value={path}
              disabled={submitting}
              onChange={setPath}
            />
            {error && <p role="alert" className="mt-2 text-[11px] text-ghost-bright-red">{error}</p>}
            <Button
              type="submit"
              variant="primary-static"
              disabled={submitting || !path.trim()}
              className="mt-3 flex h-8 w-full items-center justify-center gap-2 rounded-md text-[10px]"
            >
              {submitting ? <LoaderCircle size={12} className="animate-spin" /> : <FolderGit2 size={12} />}
              Add project
            </Button>
          </form>
        )}

        <nav className="min-h-0 flex-1 overflow-y-auto px-2 py-2" aria-label="Projects and threads">
          {projects.length === 0 ? (
            <div className="mx-1 mt-2 rounded-lg border border-dashed border-ghost-border/70 px-3 py-6 text-center">
              <Folder size={17} className="mx-auto text-ghost-faint" />
              <p className="mt-2.5 text-[10px] text-ghost-muted">
                No projects{activeProfile ? ` in ${activeProfile.name}` : ' yet'}
              </p>
            </div>
          ) : bookmarksOnly && visibleProjects.length === 0 ? (
            <div className="mx-1 mt-2 rounded-lg border border-dashed border-ghost-border/70 px-3 py-6 text-center">
              <Bookmark size={17} className="mx-auto text-ghost-faint" />
              <p className="mt-2.5 text-[10px] text-ghost-muted">No bookmarked threads</p>
              <Button
                type="button"
                variant="text"
                onClick={() => setBookmarksOnly(false)}
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
                      onDragEnd={() => setActiveDrag(null)}
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
                        if (!bookmarksOnly) toggleProject(project.id)
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
                      <span className="min-w-0 flex-1 truncate text-[11px] font-semibold">{project.name}</span>
                    </Button>
                    <IconButton
                      type="button"
                      size="xs"
                      shrink
                      variant="subtle-white"
                      onClick={() => onNewThread(project.id)}
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
        </nav>

        <section className="max-h-40 shrink-0 overflow-y-auto border-t border-ghost-border/70 px-2 py-2" aria-labelledby="sidebar-processes-title">
          <div className="flex h-5 items-center gap-1.5 px-1.5">
            <RadioTower size={11} className={processWebServers.length > 0 ? 'text-ghost-green' : 'text-ghost-faint'} />
            <h2 id="sidebar-processes-title" className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
              Processes
            </h2>
            {processWebServers.length > 0 && (
              <span className="ml-auto rounded-full border border-ghost-border/70 px-1.5 font-mono text-[8px] text-ghost-faint">
                {processWebServers.length}
              </span>
            )}
          </div>
          {processWebServers.length === 0 ? (
            <p className="px-1.5 pt-1 font-mono text-[9px] text-ghost-faint">No web servers</p>
          ) : (
            <ul className="mt-1 space-y-0.5">
              {processWebServers.map((webServer) => (
                <li key={`${webServer.projectId}:${webServer.threadId}:${webServer.processId}:${webServer.url}`}>
                  <a
                    href={webServer.url}
                    target="_blank"
                    rel="noreferrer"
                    onClick={onClose}
                    title={`${webServer.projectName} / ${webServer.threadTitle} / ${webServer.processName}\n${webServer.url}`}
                    className="group/server flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1.5 text-ghost-muted transition hover:bg-ghost-raised/45 hover:text-ghost-bright-white focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ghost-green/45"
                  >
                    <span className="grid size-5 shrink-0 place-items-center rounded bg-ghost-green/[0.08] text-ghost-green">
                      <RadioTower size={11} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-[9px] text-ghost-white">{webServerAddress(webServer.url)}</span>
                      <span className="block truncate text-[8px] text-ghost-faint">
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
              aria-label={restarting ? 'Restarting dire/mux' : 'Restart dire/mux'}
              title={restarting ? 'Restarting dire/mux…' : 'Restart dire/mux'}
            >
              {restarting
                ? <LoaderCircle size={13} className="animate-spin text-ghost-green" />
                : <RotateCw size={13} className="text-ghost-dim" />}
            </Button>
          </div>
        </div>
      </aside>
    </>
  )
}
