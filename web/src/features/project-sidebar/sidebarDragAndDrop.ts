// Geometry and list arithmetic for reordering sidebar rows. Pure, so the
// "which half of the row is the pointer over" rules can be read without the
// drag handlers around them.
import type { DragEvent } from 'react'

export type DragItem =
  | { kind: 'project'; id: string }
  | { kind: 'thread'; projectId: string; id: string }

export type DropPosition = 'before' | 'after'

export type DropTarget =
  | { kind: 'project'; id: string; position: DropPosition }
  | { kind: 'thread'; projectId: string; id: string; position: DropPosition }

/** Returns `ids` unchanged when the move would be a no-op or the target is gone. */
export function reorderedIds(
  ids: string[],
  sourceId: string,
  targetId: string,
  position: DropPosition,
) {
  if (sourceId === targetId) return ids
  const withoutSource = ids.filter((id) => id !== sourceId)
  const targetIndex = withoutSource.indexOf(targetId)
  if (targetIndex < 0 || withoutSource.length === ids.length) return ids
  withoutSource.splice(targetIndex + (position === 'after' ? 1 : 0), 0, sourceId)
  return withoutSource
}

export function verticalDropPosition(event: DragEvent<HTMLElement>): DropPosition {
  const bounds = event.currentTarget.getBoundingClientRect()
  return event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
}

// A project row is as tall as its expanded thread list, so the midpoint that
// matters is the header's, not the whole row's.
export function projectDropPosition(event: DragEvent<HTMLLIElement>): DropPosition {
  const header = event.currentTarget.querySelector<HTMLElement>('[data-project-drag-image]')
  const bounds = (header ?? event.currentTarget).getBoundingClientRect()
  return event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
}

export function sameOrder(left: string[], right: string[]) {
  return left.length === right.length && left.every((id, index) => id === right[index])
}
