const keySeparator = '\u0000'

export function sidebarThreadKey(projectId, threadId) {
  return `${projectId}${keySeparator}${threadId}`
}

export function createThreadTreeIndex(threads) {
  const byId = new Map()
  const childrenByParentId = new Map()
  const roots = []

  for (const thread of threads) {
    byId.set(thread.id, thread)
    if (!thread.parentThreadId) {
      roots.push(thread)
      continue
    }
    const children = childrenByParentId.get(thread.parentThreadId) ?? []
    children.push(thread)
    childrenByParentId.set(thread.parentThreadId, children)
  }

  function children(threadId) {
    return childrenByParentId.get(threadId) ?? []
  }

  function ancestors(threadId) {
    const found = []
    const visited = new Set([threadId])
    let parentId = byId.get(threadId)?.parentThreadId
    while (parentId && !visited.has(parentId)) {
      visited.add(parentId)
      const parent = byId.get(parentId)
      if (!parent) break
      found.push(parent)
      parentId = parent.parentThreadId
    }
    return found
  }

  function descendants(threadId) {
    const found = []
    const visited = new Set([threadId])
    const visit = (parentId) => {
      for (const child of children(parentId)) {
        if (visited.has(child.id)) continue
        visited.add(child.id)
        found.push(child)
        visit(child.id)
      }
    }
    visit(threadId)
    return found
  }

  function rootId(threadId) {
    let current = byId.get(threadId)
    if (!current) return null
    const visited = new Set()
    while (current.parentThreadId) {
      if (visited.has(current.id)) return null
      visited.add(current.id)
      const parent = byId.get(current.parentThreadId)
      if (!parent) return null
      current = parent
    }
    return current.id
  }

  function activityDisplayThread(activity, rejectArchived = true) {
    let thread = byId.get(activity.threadId)
    if (!thread || (rejectArchived && thread.archivedAt)) return null
    if (activity.state !== 'finished') return thread

    const visited = new Set()
    while (thread?.parentThreadId && !visited.has(thread.id)) {
      visited.add(thread.id)
      const parent = byId.get(thread.parentThreadId)
      if (!parent) break
      if (rejectArchived && parent.archivedAt) return null
      thread = parent
    }
    return thread
  }

  function orderedTreeIds(rootIds) {
    const ordered = []
    const seen = new Set()
    const append = (threadId) => {
      if (seen.has(threadId)) return
      seen.add(threadId)
      ordered.push(threadId)
      for (const child of children(threadId)) append(child.id)
    }
    for (const rootId of rootIds) append(rootId)
    for (const thread of threads) append(thread.id)
    return ordered
  }

  return {
    threads,
    byId,
    roots,
    children,
    ancestors,
    descendants,
    rootId,
    activityDisplayThread,
    orderedTreeIds,
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

  const activitiesByDisplayKey = new Map()
  const finishedActivitiesByDisplayKey = new Map()
  for (const activity of activities) {
    const tree = treeByProjectId.get(activity.projectId)
    if (!tree) continue

    const visibleDisplay = tree.activityDisplayThread(activity, true)
    if (visibleDisplay) {
      const key = sidebarThreadKey(activity.projectId, visibleDisplay.id)
      const displayed = activitiesByDisplayKey.get(key) ?? []
      displayed.push(activity)
      activitiesByDisplayKey.set(key, displayed)
    }

    if (activity.state === 'finished') {
      const display = tree.activityDisplayThread(activity, false)
      if (!display) continue
      const key = sidebarThreadKey(activity.projectId, display.id)
      const finished = finishedActivitiesByDisplayKey.get(key) ?? []
      finished.push(activity)
      finishedActivitiesByDisplayKey.set(key, finished)
    }
  }

  const preferredActivityByKey = new Map()
  for (const [key, displayed] of activitiesByDisplayKey) {
    const preferred = displayed.find((activity) => activity.state === 'working')
      ?? displayed.find((activity) => activity.state === 'finished')
    if (preferred) preferredActivityByKey.set(key, preferred)
  }

  const projectActivityCounts = new Map()
  for (const [key, activity] of preferredActivityByKey) {
    if (activity.state !== 'working' && activity.state !== 'finished') continue
    const separator = key.indexOf(keySeparator)
    const projectId = key.slice(0, separator)
    const current = projectActivityCounts.get(projectId) ?? { working: 0, finished: 0 }
    if (activity.state === 'working') current.working += 1
    else current.finished += 1
    projectActivityCounts.set(projectId, current)
  }

  function tree(projectId) {
    return treeByProjectId.get(projectId) ?? null
  }

  function entry(projectId, threadId) {
    return entryByKey.get(sidebarThreadKey(projectId, threadId)) ?? null
  }

  function threadActivity(projectId, threadId) {
    const activity = preferredActivityByKey.get(sidebarThreadKey(projectId, threadId)) ?? null
    return {
      activity,
      childActivity: Boolean(activity && activity.threadId !== threadId),
    }
  }

  function finishedActivities(projectId, threadId) {
    return finishedActivitiesByDisplayKey.get(sidebarThreadKey(projectId, threadId)) ?? []
  }

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
