// Everything involved in dragging or arrow-keying a project or thread row into
// a new position: the in-flight drag, the drop indicator, the save-in-progress
// latch, and the six handlers the rows attach.
//
// Threads reorder within their active roots only. Archived roots keep their
// relative order at the end, and the tree flattens the result back into the
// parent-child order the server stores.
import { useRef, useState, type DragEvent, type KeyboardEvent } from 'react'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectActiveProfileId } from '@/store/slices/preferences'
import { projectsReordered, threadsReordered } from '@/store/slices/projects'
import type { Project } from '@/types'
import {
  projectDropPosition,
  reorderedIds,
  sameOrder,
  verticalDropPosition,
  type DragItem,
  type DropTarget,
} from './sidebarDragAndDrop'

type ThreadTree = {
  roots: Array<{ id: string; archivedAt?: string | null }>
  orderedTreeIds: (rootIds: string[]) => string[]
}

export type SidebarReorderOptions = {
  projects: Project[]
  treeFor: (projectId: string) => ThreadTree | null | undefined
}

export function useSidebarReorder({
  projects,
  treeFor,
}: SidebarReorderOptions) {
  const dispatch = useAppDispatch()
  const activeProfileId = useAppSelector(selectActiveProfileId)
  const [draggedItem, setDraggedItem] = useState<DragItem | null>(null)
  const [dropTarget, setDropTarget] = useState<DropTarget | null>(null)
  const [savingOrder, setSavingOrder] = useState(false)
  // Drag handlers fire from the DOM between renders, so they read the ref
  // rather than the state snapshot they closed over.
  const draggedItemRef = useRef<DragItem | null>(null)

  function setActiveDrag(item: DragItem | null) {
    draggedItemRef.current = item
    setDraggedItem(item)
    if (!item) setDropTarget(null)
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
    // The thunk owns the optimistic move and the rollback; the latch here is
    // only about blocking a second drag while one is in flight.
    void dispatch(projectsReordered({ profileId: activeProfileId, projectIds }))
      .then((result) => {
        if (projectsReordered.rejected.match(result)) window.alert(result.payload)
      })
      .finally(() => setSavingOrder(false))
  }

  function saveThreadOrder(project: Project, threadIds: string[]) {
    const currentIds = project.threads.map((thread) => thread.id)
    if (sameOrder(currentIds, threadIds)) return
    setSavingOrder(true)
    void dispatch(threadsReordered({ projectId: project.id, threadIds }))
      .then((result) => {
        if (threadsReordered.rejected.match(result)) window.alert(result.payload)
      })
      .finally(() => setSavingOrder(false))
  }

  /** Active roots first, archived roots after, both in their current order. */
  function partitionedRootIds(projectId: string) {
    const tree = treeFor(projectId)
    if (!tree) return null
    return {
      tree,
      active: tree.roots.filter((thread) => !thread.archivedAt).map((thread) => thread.id),
      archived: tree.roots.filter((thread) => thread.archivedAt).map((thread) => thread.id),
    }
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
    const partitioned = project ? partitionedRootIds(project.id) : null
    const activeThreadIds = partitioned?.active ?? []
    if (savingOrder || !project || activeThreadIds.length < 2 || !activeThreadIds.includes(threadId)) {
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
    const partitioned = partitionedRootIds(project.id)
    if (!partitioned) return
    const threadIds = reorderedIds(
      partitioned.active,
      item.id,
      threadId,
      verticalDropPosition(event),
    )
    setActiveDrag(null)
    saveThreadOrder(project, partitioned.tree.orderedTreeIds([...threadIds, ...partitioned.archived]))
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

  function handleThreadHandleKeyDown(
    event: KeyboardEvent<HTMLButtonElement>,
    project: Project,
    threadId: string,
  ) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    if (savingOrder) return
    const partitioned = partitionedRootIds(project.id)
    if (!partitioned) return
    const index = partitioned.active.indexOf(threadId)
    const targetIndex = index + (event.key === 'ArrowUp' ? -1 : 1)
    if (index < 0 || targetIndex < 0 || targetIndex >= partitioned.active.length) return
    const reordered = [...partitioned.active]
    ;[reordered[index], reordered[targetIndex]] = [reordered[targetIndex], reordered[index]]
    saveThreadOrder(project, partitioned.tree.orderedTreeIds([...reordered, ...partitioned.archived]))
  }

  return {
    draggedItem,
    dropTarget,
    savingOrder,
    clearDrag: () => setActiveDrag(null),
    startProjectDrag,
    startThreadDrag,
    handleProjectDragOver,
    handleProjectDrop,
    handleThreadDragOver,
    handleThreadDrop,
    handleProjectHandleKeyDown,
    handleThreadHandleKeyDown,
  }
}
