export type SidebarActivityThread = {
  id: string
  parentThreadId?: string
}

export type SidebarThreadActivity = {
  projectId: string
  threadId: string
  state: string
}

export function activityDisplayThreadId(
  threads: readonly SidebarActivityThread[],
  activity: SidebarThreadActivity,
): string

export function sidebarThreadActivity<Activity extends SidebarThreadActivity>(
  threads: readonly SidebarActivityThread[],
  activities: readonly Activity[],
  projectId: string,
  threadId: string,
): {
  activity: Activity | null
  childActivity: boolean
}
