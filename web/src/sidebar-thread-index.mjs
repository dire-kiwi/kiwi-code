const keySeparator = '\u0000'

export function sidebarThreadKey(projectId, threadId) {
  return `${projectId}${keySeparator}${threadId}`
}

export function createThreadTreeIndex(threads) {
  const byId = new Map(threads.map((thread) => [thread.id, thread]))
  const roots = [...threads]
  const rootId = (threadId) => byId.has(threadId) ? threadId : null
  const activityDisplayThread = (activity, rejectArchived = true) => {
    const thread = byId.get(activity.threadId)
    return thread && (!rejectArchived || !thread.archivedAt) ? thread : null
  }
  const orderedTreeIds = (rootIds) => {
    const ordered = []
    const seen = new Set()
    for (const id of [...rootIds, ...threads.map((thread) => thread.id)]) {
      if (seen.has(id)) continue
      seen.add(id)
      ordered.push(id)
    }
    return ordered
  }
  const bookmarkedPathIds = () => threads
    .filter((thread) => thread.bookmarked)
    .map((thread) => thread.id)

  return {
    threads,
    byId,
    roots,
    rootId,
    activityDisplayThread,
    orderedTreeIds,
    bookmarkedPathIds,
  }
}

export function createSidebarThreadIndex(projects, activities) {
  const projectById = new Map()
  const treeByProjectId = new Map()
  const entryByKey = new Map()
  const projectByThreadId = new Map()

  for (const project of projects) {
    projectById.set(project.id, project)
    const tree = createThreadTreeIndex(project.threads)
    treeByProjectId.set(project.id, tree)
    for (const thread of project.threads) {
      entryByKey.set(sidebarThreadKey(project.id, thread.id), { project, thread })
      if (!projectByThreadId.has(thread.id)) projectByThreadId.set(thread.id, project)
    }
  }

  const activitiesByKey = new Map()
  const finishedActivitiesByKey = new Map()
  for (const activity of activities) {
    const entry = entryByKey.get(sidebarThreadKey(activity.projectId, activity.threadId))
    if (!entry || entry.thread.archivedAt) continue
    const key = sidebarThreadKey(activity.projectId, activity.threadId)
    const displayed = activitiesByKey.get(key) ?? []
    displayed.push(activity)
    activitiesByKey.set(key, displayed)
    if (activity.state === 'finished') {
      const finished = finishedActivitiesByKey.get(key) ?? []
      finished.push(activity)
      finishedActivitiesByKey.set(key, finished)
    }
  }

  const preferredActivityByKey = new Map()
  for (const [key, displayed] of activitiesByKey) {
    const preferred = displayed.find((activity) => activity.state === 'working')
      ?? displayed.find((activity) => activity.state === 'finished')
    if (preferred) preferredActivityByKey.set(key, preferred)
  }

  const projectActivityCounts = new Map()
  for (const [key, activity] of preferredActivityByKey) {
    const separator = key.indexOf(keySeparator)
    const projectId = key.slice(0, separator)
    const current = projectActivityCounts.get(projectId) ?? { working: 0, finished: 0 }
    if (activity.state === 'working') current.working += 1
    else if (activity.state === 'finished') current.finished += 1
    projectActivityCounts.set(projectId, current)
  }

  const tree = (projectId) => treeByProjectId.get(projectId) ?? null
  const entry = (projectId, threadId) => entryByKey.get(sidebarThreadKey(projectId, threadId)) ?? null
  const threadActivity = (projectId, threadId) => ({
    activity: preferredActivityByKey.get(sidebarThreadKey(projectId, threadId)) ?? null,
  })
  const finishedActivities = (projectId, threadId) =>
    finishedActivitiesByKey.get(sidebarThreadKey(projectId, threadId)) ?? []

  return {
    projects,
    activities,
    projectById,
    treeByProjectId,
    entryByKey,
    projectByThreadId,
    projectActivityCounts,
    tree,
    entry,
    threadActivity,
    finishedActivities,
  }
}
