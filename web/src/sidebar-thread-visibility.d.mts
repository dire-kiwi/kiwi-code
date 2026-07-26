import type { ThreadTreeIndex } from './sidebar-thread-index.mjs'

export type SidebarVisibilityThread = {
  id: string
  createdAt: string
  lastPromptAt?: string
  parentThreadId?: string
  archivedAt?: string
  bookmarked?: boolean
}

export type SidebarVisibilityActivity = {
  projectId: string
  threadId: string
  state: string
}

export const collapsedRootThreadLimit: number

export function defaultVisibleRootThreadIds(
  threads: readonly SidebarVisibilityThread[],
  activities: readonly SidebarVisibilityActivity[],
  projectId: string,
  limit?: number,
  tree?: ThreadTreeIndex<SidebarVisibilityThread>,
): string[]

export function bookmarkedThreadPathIds(
  threads: readonly SidebarVisibilityThread[],
  tree?: ThreadTreeIndex<SidebarVisibilityThread>,
): string[]
