export type IndexedThread = {
  id: string
  archivedAt?: string
  bookmarked?: boolean
}

export type IndexedProject<Thread extends IndexedThread = IndexedThread> = {
  id: string
  threads: readonly Thread[]
}

export type IndexedActivity = {
  projectId: string
  threadId: string
  state: string
}

export type ThreadTreeIndex<Thread extends IndexedThread = IndexedThread> = {
  threads: readonly Thread[]
  byId: Map<string, Thread>
  roots: Thread[]
  rootId: (threadId: string) => string | null
  activityDisplayThread: (activity: IndexedActivity, rejectArchived?: boolean) => Thread | null
  orderedTreeIds: (rootIds: readonly string[]) => string[]
  bookmarkedPathIds: () => string[]
}

export type SidebarThreadIndex<
  Thread extends IndexedThread = IndexedThread,
  Project extends IndexedProject<Thread> = IndexedProject<Thread>,
  Activity extends IndexedActivity = IndexedActivity,
> = {
  projects: readonly Project[]
  activities: readonly Activity[]
  projectById: Map<string, Project>
  treeByProjectId: Map<string, ThreadTreeIndex<Thread>>
  entryByKey: Map<string, { project: Project; thread: Thread }>
  projectByThreadId: Map<string, Project>
  projectActivityCounts: Map<string, { working: number; finished: number }>
  tree: (projectId: string) => ThreadTreeIndex<Thread> | null
  entry: (projectId: string, threadId: string) => { project: Project; thread: Thread } | null
  threadActivity: (projectId: string, threadId: string) => {
    activity: Activity | null
  }
  finishedActivities: (projectId: string, threadId: string) => Activity[]
}

export function sidebarThreadKey(projectId: string, threadId: string): string

export function createThreadTreeIndex<Thread extends IndexedThread>(
  threads: readonly Thread[],
): ThreadTreeIndex<Thread>

export function createSidebarThreadIndex<
  Project extends IndexedProject,
  Activity extends IndexedActivity,
>(
  projects: readonly Project[],
  activities: readonly Activity[],
): SidebarThreadIndex<Project['threads'][number], Project, Activity>
