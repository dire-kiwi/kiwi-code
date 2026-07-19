export type SidebarVisibilityThread = {
  id: string
  createdAt: string
  lastPromptAt?: string
  parentThreadId?: string
  archivedAt?: string
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
): string[]
