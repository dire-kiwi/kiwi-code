import {
  createSidebarThreadIndex,
  createThreadTreeIndex,
} from './sidebar-thread-index.mjs'

export function activityDisplayThreadId(threads, activity, tree = createThreadTreeIndex(threads)) {
  return tree.activityDisplayThread(activity, false)?.id ?? activity.threadId
}

export function activityDisplayThread(threads, activity, tree = createThreadTreeIndex(threads)) {
  return tree.activityDisplayThread(activity, true)
}

export function sidebarThreadActivity(threads, activities, projectId, threadId, index) {
  const resolved = index ?? createSidebarThreadIndex([{ id: projectId, threads }], activities)
  return resolved.threadActivity(projectId, threadId)
}

export function sidebarProjectActivityCounts(projects, activities, index) {
  return (index ?? createSidebarThreadIndex(projects, activities)).projectActivityCounts
}
