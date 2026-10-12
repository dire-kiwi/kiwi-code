import { isMacPlatform } from './profileShortcuts'

export type ThreadCycleDirection = 1 | -1

type ThreadCycleEvent = Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>

/**
 * Whether a key press asks for the next (1) or previous (-1) thread. These are
 * the keys browsers use to switch tabs: Ctrl+Tab and Ctrl+Shift+Tab or
 * Ctrl+PageDown and Ctrl+PageUp everywhere, plus Cmd+Option+Right/Left and
 * Cmd+Shift+]/[ on macOS. electron/thread-shortcuts.cjs mirrors this for keys
 * pressed inside the native browser view.
 */
export function threadCycleDirection(
  event: ThreadCycleEvent,
  mac = isMacPlatform(),
): ThreadCycleDirection | null {
  if (event.ctrlKey && !event.metaKey && !event.altKey) {
    if (event.key === 'Tab') return event.shiftKey ? -1 : 1
    if (!event.shiftKey && event.key === 'PageDown') return 1
    if (!event.shiftKey && event.key === 'PageUp') return -1
  }
  if (mac && event.metaKey && !event.ctrlKey) {
    if (event.altKey && !event.shiftKey && event.key === 'ArrowRight') return 1
    if (event.altKey && !event.shiftKey && event.key === 'ArrowLeft') return -1
    // Shift turns the key into a brace, so read the physical bracket.
    if (event.shiftKey && !event.altKey && event.code === 'BracketRight') return 1
    if (event.shiftKey && !event.altKey && event.code === 'BracketLeft') return -1
  }
  return null
}

export type ThreadRowRef = { projectId: string; threadId: string }

/**
 * Thread rows in the order the sidebar shows them, whichever view is active,
 * skipping rows inside collapsed projects and closed Settled sections.
 */
export function visibleThreadRows(container: ParentNode): ThreadRowRef[] {
  return Array.from(container.querySelectorAll<HTMLElement>('[data-thread-row]'))
    .filter((row) => !row.closest('[hidden], details:not([open])'))
    .flatMap((row) => {
      const { projectId, threadId } = row.dataset
      return projectId && threadId ? [{ projectId, threadId }] : []
    })
}

/**
 * The row one step from the selected thread, wrapping at the ends like browser
 * tabs. With no selected row visible, next starts at the top and previous at
 * the bottom.
 */
export function adjacentThread(
  rows: readonly ThreadRowRef[],
  selectedThreadId: string | null,
  direction: ThreadCycleDirection,
): ThreadRowRef | null {
  if (rows.length === 0) return null
  const current = rows.findIndex((row) => row.threadId === selectedThreadId)
  if (current === -1) return rows[direction === 1 ? 0 : rows.length - 1]
  return rows[(current + direction + rows.length) % rows.length]
}
