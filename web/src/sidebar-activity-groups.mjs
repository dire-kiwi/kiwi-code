import { activityDisplayThread } from './sidebar-thread-activity.mjs'

export const recentThreadLimit = 8

function parsedTime(value) {
  if (typeof value !== 'string' || !value) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

function threadRecency(thread) {
  return parsedTime(thread.lastPromptAt) ?? parsedTime(thread.createdAt) ?? 0
}

function entryKey(projectId, threadId) {
  return `${projectId}\u0000${threadId}`
}

function byNewestFirst(left, right) {
  return right.at - left.at || left.order - right.order
}

function orderThreadFamilies(entries, threadsByKey) {
  if (entries.length < 2) return entries

  const entriesByKey = new Map(entries.map((entry) => [entryKey(entry.projectId, entry.threadId), entry]))
  const childrenByKey = new Map()
  const childKeys = new Set()

  for (const entry of entries) {
    const key = entryKey(entry.projectId, entry.threadId)
    let parentId = threadsByKey.get(key)?.thread.parentThreadId
    const visited = new Set([entry.threadId])
    while (parentId && !visited.has(parentId)) {
      visited.add(parentId)
      const parentKey = entryKey(entry.projectId, parentId)
      if (entriesByKey.has(parentKey)) {
        const children = childrenByKey.get(parentKey) ?? []
        children.push(entry)
        childrenByKey.set(parentKey, children)
        childKeys.add(key)
        break
      }
      parentId = threadsByKey.get(parentKey)?.thread.parentThreadId
    }
  }

  const newestInFamily = new Map()
  const familyRecency = (entry, visiting = new Set()) => {
    const key = entryKey(entry.projectId, entry.threadId)
    const cached = newestInFamily.get(key)
    if (cached !== undefined) return cached
    if (visiting.has(key)) return entry.at
    const nextVisiting = new Set(visiting).add(key)
    let newest = entry.at
    for (const child of childrenByKey.get(key) ?? []) {
      newest = Math.max(newest, familyRecency(child, nextVisiting))
    }
    newestInFamily.set(key, newest)
    return newest
  }
  const byFamilyRecency = (left, right) =>
    familyRecency(right) - familyRecency(left) || byNewestFirst(left, right)

  const ordered = []
  const appended = new Set()
  const appendFamily = (entry) => {
    const key = entryKey(entry.projectId, entry.threadId)
    if (appended.has(key)) return
    appended.add(key)
    ordered.push(entry)
    for (const child of (childrenByKey.get(key) ?? []).sort(byFamilyRecency)) appendFamily(child)
  }

  const roots = entries.filter((entry) => !childKeys.has(entryKey(entry.projectId, entry.threadId)))
  for (const root of roots.sort(byFamilyRecency)) appendFamily(root)
  // Cyclic relationships are rejected by the backend, but keep every row
  // visible if malformed data ever reaches the client.
  for (const entry of entries.sort(byNewestFirst)) appendFamily(entry)
  return ordered
}

function publicEntry({ projectId, threadId, at }) {
  return { projectId, threadId, at }
}

/**
 * Groups every thread across all projects into the activity view's sections,
 * in priority order: working, needs review, pinned, recent. A thread appears
 * in the first section that claims it. Entries within a section keep each
 * visible subthread directly below its parent, while thread families remain
 * ordered by their newest member. Recent holds only active root threads and is
 * capped at recentLimit; the overflow is reported as hiddenRecentCount.
 */
export function activityViewGroups(projects, activities, recentLimit = recentThreadLimit) {
  const threadsByKey = new Map()
  const threadsByProject = new Map()
  let order = 0
  for (const project of projects) {
    threadsByProject.set(project.id, project.threads)
    for (const thread of project.threads) {
      threadsByKey.set(entryKey(project.id, thread.id), { projectId: project.id, thread, order: order++ })
    }
  }

  const included = new Set()
  const collectActivityEntries = (state) => {
    const entriesByKey = new Map()
    for (const activity of activities) {
      if (activity.state !== state) continue
      const projectThreads = threadsByProject.get(activity.projectId)
      const displayThread = projectThreads
        ? activityDisplayThread(projectThreads, activity)
        : null
      if (!displayThread) continue
      const key = entryKey(activity.projectId, displayThread.id)
      const found = threadsByKey.get(key)
      if (!found || found.thread.archivedAt || included.has(key)) continue
      const entry = {
        projectId: found.projectId,
        threadId: found.thread.id,
        at: parsedTime(activity.updatedAt) ?? threadRecency(found.thread),
        order: found.order,
      }
      const current = entriesByKey.get(key)
      if (!current || entry.at > current.at) entriesByKey.set(key, entry)
    }
    const entries = [...entriesByKey.values()]
    for (const entry of entries) included.add(entryKey(entry.projectId, entry.threadId))
    return orderThreadFamilies(entries, threadsByKey)
  }

  const working = collectActivityEntries('working')
  const needsReview = collectActivityEntries('finished')

  const pinned = []
  const remaining = []
  for (const [key, { projectId, thread, order: threadOrder }] of threadsByKey) {
    if (included.has(key) || thread.archivedAt) continue
    if (thread.bookmarked) {
      pinned.push({ projectId, threadId: thread.id, at: threadRecency(thread), order: threadOrder })
    } else if (!thread.parentThreadId && !thread.closedAt) {
      remaining.push({ projectId, threadId: thread.id, at: threadRecency(thread), order: threadOrder })
    }
  }
  const orderedPinned = orderThreadFamilies(pinned, threadsByKey)
  remaining.sort(byNewestFirst)
  const boundedLimit = Number.isInteger(recentLimit) && recentLimit > 0 ? recentLimit : 0
  const recent = remaining.slice(0, boundedLimit)

  return {
    working: working.map(publicEntry),
    needsReview: needsReview.map(publicEntry),
    pinned: orderedPinned.map(publicEntry),
    recent: recent.map(publicEntry),
    hiddenRecentCount: remaining.length - recent.length,
  }
}

/** Compact elapsed time for sidebar rows: now, 5m, 3h, 2d, 1w. */
export function formatRelativeShort(value, nowMs) {
  const timestamp = typeof value === 'number' ? value : parsedTime(value)
  if (timestamp === null || timestamp <= 0 || !Number.isFinite(nowMs)) return ''
  const seconds = Math.max(0, Math.floor((nowMs - timestamp) / 1000))
  if (seconds < 60) return 'now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  return `${Math.floor(days / 7)}w`
}
