export function activityDisplayThreadId(threads, activity) {
  if (activity.state !== 'finished') return activity.threadId

  const threadsById = new Map(threads.map((thread) => [thread.id, thread]))
  let displayThreadId = activity.threadId
  let thread = threadsById.get(displayThreadId)
  const visited = new Set()
  while (thread?.parentThreadId && !visited.has(thread.id)) {
    visited.add(thread.id)
    const parent = threadsById.get(thread.parentThreadId)
    if (!parent) break
    displayThreadId = parent.id
    thread = parent
  }
  return displayThreadId
}

export function sidebarThreadActivity(threads, activities, projectId, threadId) {
  const displayed = activities.filter((activity) =>
    activity.projectId === projectId
      && activityDisplayThreadId(threads, activity) === threadId,
  )
  const activity = displayed.find((candidate) => candidate.state === 'working')
    ?? displayed.find((candidate) => candidate.state === 'finished')
    ?? null

  return {
    activity,
    childActivity: Boolean(activity && activity.threadId !== threadId),
  }
}
