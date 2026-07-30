import {
  createSidebarThreadIndex,
  sidebarThreadKey,
} from './sidebar-thread-index.mjs'

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

function byPromptNewestFirst(left, right) {
  return right.promptAt - left.promptAt || left.order - right.order
}

function orderEntries(entries, byEntryRecency = byNewestFirst) {
  return [...entries].sort(byEntryRecency)
}

function publicEntry({ projectId, threadId, at }) {
  return { projectId, threadId, at }
}

/**
 * Groups every thread across all projects into the activity view's sections,
 * in priority order: working, needs review, recent. A thread appears
 * in the first section that claims it. Entries within a section keep each
 * recency order. Recent holds active threads and is
 * capped at recentLimit; the overflow is reported as hiddenRecentCount.
 */
export function activityViewGroups(
  projects,
  activities,
  recentLimit = recentThreadLimit,
  index = createSidebarThreadIndex(projects, activities),
) {
  const threadsByKey = new Map()
  let order = 0
  for (const [key, { project, thread }] of index.entryByKey) {
    threadsByKey.set(key, { projectId: project.id, thread, order: order++ })
  }

  const included = new Set()
  const collectActivityEntries = (state) => {
    const entriesByKey = new Map()
    for (const activity of activities) {
      if (activity.state !== state) continue
      const displayThread = index.tree(activity.projectId)?.activityDisplayThread(activity, true)
      if (!displayThread) continue
      const key = sidebarThreadKey(activity.projectId, displayThread.id)
      const found = threadsByKey.get(key)
      if (!found || found.thread.archivedAt || included.has(key)) continue
      const promptAt = threadRecency(found.thread)
      const entry = {
        projectId: found.projectId,
        threadId: found.thread.id,
        at: parsedTime(activity.updatedAt) ?? promptAt,
        promptAt,
        order: found.order,
      }
      const current = entriesByKey.get(key)
      if (!current || entry.at > current.at) entriesByKey.set(key, entry)
    }
    const entries = [...entriesByKey.values()]
    for (const entry of entries) included.add(entryKey(entry.projectId, entry.threadId))
    // Working heartbeats refresh while the LLM responds. Keep that timestamp
    // for the row display, but only let a user's latest prompt change ordering.
    return state === 'working'
      ? orderEntries(entries, byPromptNewestFirst)
      : orderEntries(entries)
  }

  const working = collectActivityEntries('working')
  const needsReview = collectActivityEntries('finished')

  const remaining = []
  for (const [key, { projectId, thread, order: threadOrder }] of threadsByKey) {
    if (included.has(key) || thread.archivedAt) continue
    remaining.push({ projectId, threadId: thread.id, at: threadRecency(thread), order: threadOrder })
  }
  remaining.sort(byNewestFirst)
  const boundedLimit = Number.isInteger(recentLimit) && recentLimit > 0 ? recentLimit : 0
  const recent = remaining.slice(0, boundedLimit)

  return {
    working: working.map(publicEntry),
    needsReview: needsReview.map(publicEntry),
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
