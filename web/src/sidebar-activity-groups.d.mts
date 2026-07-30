import type { SidebarThreadIndex } from './sidebar-thread-index.mjs'

export type ActivityGroupThread = {
  id: string
  createdAt: string
  lastPromptAt?: string
  archivedAt?: string
  bookmarked?: boolean
}

export type ActivityGroupProject = {
  id: string
  threads: readonly ActivityGroupThread[]
}

export type ActivityGroupActivity = {
  projectId: string
  threadId: string
  state: string
  updatedAt?: string
}

export type ActivityGroupEntry = {
  projectId: string
  threadId: string
  /** Milliseconds timestamp used for the row's elapsed-time display. */
  at: number
}

export type ActivityViewGroups = {
  working: ActivityGroupEntry[]
  needsReview: ActivityGroupEntry[]
  pinned: ActivityGroupEntry[]
  recent: ActivityGroupEntry[]
  hiddenRecentCount: number
}

export const recentThreadLimit: number

export function activityViewGroups(
  projects: readonly ActivityGroupProject[],
  activities: readonly ActivityGroupActivity[],
  recentLimit?: number,
  index?: SidebarThreadIndex<
    ActivityGroupThread,
    ActivityGroupProject,
    ActivityGroupActivity
  >,
): ActivityViewGroups

export function formatRelativeShort(
  value: string | number | null | undefined,
  nowMs: number,
): string
