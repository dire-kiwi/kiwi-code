export type SidebarActivityThread = {
  id: string
  parentThreadId?: string
  archivedAt?: string
}

export type SidebarThreadActivity = {
  projectId: string
  threadId: string
  state: string
}

export type SidebarActivityProject = {
  id: string
  threads: readonly SidebarActivityThread[]
}

export function activityDisplayThreadId(
  threads: readonly SidebarActivityThread[],
  activity: SidebarThreadActivity,
): string

export function activityDisplayThread<Thread extends SidebarActivityThread>(
  threads: readonly Thread[],
  activity: SidebarThreadActivity,
): Thread | null

export function sidebarThreadActivity<Activity extends SidebarThreadActivity>(
  threads: readonly SidebarActivityThread[],
  activities: readonly Activity[],
  projectId: string,
  threadId: string,
): {
  activity: Activity | null
  childActivity: boolean
}

export function sidebarProjectActivityCounts(
  projects: readonly SidebarActivityProject[],
  activities: readonly SidebarThreadActivity[],
): Map<string, { working: number; finished: number }>
