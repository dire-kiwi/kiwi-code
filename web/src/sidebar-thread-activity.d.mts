import type { SidebarThreadIndex, ThreadTreeIndex } from './sidebar-thread-index.mjs'

export type SidebarActivityThread = {
  id: string
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
  tree?: ThreadTreeIndex<SidebarActivityThread>,
): string

export function activityDisplayThread<Thread extends SidebarActivityThread>(
  threads: readonly Thread[],
  activity: SidebarThreadActivity,
  tree?: ThreadTreeIndex<Thread>,
): Thread | null

export function sidebarThreadActivity<Activity extends SidebarThreadActivity>(
  threads: readonly SidebarActivityThread[],
  activities: readonly Activity[],
  projectId: string,
  threadId: string,
  index?: SidebarThreadIndex<
    SidebarActivityThread,
    SidebarActivityProject,
    Activity
  >,
): {
  activity: Activity | null
  childActivity: boolean
}

export function sidebarProjectActivityCounts(
  projects: readonly SidebarActivityProject[],
  activities: readonly SidebarThreadActivity[],
  index?: SidebarThreadIndex<
    SidebarActivityThread,
    SidebarActivityProject,
    SidebarThreadActivity
  >,
): Map<string, { working: number; finished: number }>
