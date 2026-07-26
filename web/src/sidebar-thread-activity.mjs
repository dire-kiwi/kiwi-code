function resolveActivityDisplayThread(threadsById, activity, rejectArchived) {
  let thread = threadsById.get(activity.threadId)
  if (!thread || (rejectArchived && thread.archivedAt)) return null
  if (activity.state !== 'finished') return thread

  const visited = new Set()
  while (thread?.parentThreadId && !visited.has(thread.id)) {
    visited.add(thread.id)
    const parent = threadsById.get(thread.parentThreadId)
    if (!parent) break
    if (rejectArchived && parent.archivedAt) return null
    thread = parent
  }
  return thread
}

export function activityDisplayThreadId(threads, activity) {
  const threadsById = new Map(threads.map((thread) => [thread.id, thread]))
  return resolveActivityDisplayThread(threadsById, activity, false)?.id ?? activity.threadId
}

export function activityDisplayThread(threads, activity) {
  const threadsById = new Map(threads.map((thread) => [thread.id, thread]))
  return resolveActivityDisplayThread(threadsById, activity, true)
}

export function sidebarThreadActivity(threads, activities, projectId, threadId) {
  const displayed = activities.filter((activity) =>
    activity.projectId === projectId
      && activityDisplayThread(threads, activity)?.id === threadId,
  )
  const activity = displayed.find((candidate) => candidate.state === 'working')
    ?? displayed.find((candidate) => candidate.state === 'finished')
    ?? null

  return {
    activity,
    childActivity: Boolean(activity && activity.threadId !== threadId),
  }
}

export function sidebarProjectActivityCounts(projects, activities) {
  const projectById = new Map(projects.map((project) => [project.id, project]))
  const displayedActivities = new Map()
  for (const activity of activities) {
    const project = projectById.get(activity.projectId)
    if (!project || (activity.state !== 'working' && activity.state !== 'finished')) continue
    const displayThread = activityDisplayThread(project.threads, activity)
    if (!displayThread) continue
    const key = `${activity.projectId}\u0000${displayThread.id}`
    const current = displayedActivities.get(key)
    if (!current || (activity.state === 'working' && current.state !== 'working')) {
      displayedActivities.set(key, activity)
    }
  }

  const counts = new Map()
  for (const activity of displayedActivities.values()) {
    const current = counts.get(activity.projectId) ?? { working: 0, finished: 0 }
    if (activity.state === 'working') current.working += 1
    else current.finished += 1
    counts.set(activity.projectId, current)
  }
  return counts
}
