export const collapsedRootThreadLimit = 5

function parsedTime(value) {
  if (typeof value !== 'string' || !value) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

function threadRecency(thread) {
  return parsedTime(thread.lastPromptAt) ?? parsedTime(thread.createdAt) ?? 0
}

function rootThreadId(threadsById, threadId) {
  let current = threadsById.get(threadId)
  if (!current) return null

  const visited = new Set()
  while (current.parentThreadId) {
    if (visited.has(current.id)) return null
    visited.add(current.id)
    const parent = threadsById.get(current.parentThreadId)
    if (!parent) return null
    current = parent
  }
  return current.id
}

export function defaultVisibleRootThreadIds(
  threads,
  activities,
  projectId,
  limit = collapsedRootThreadLimit,
) {
  const activeRoots = threads.filter((thread) => !thread.parentThreadId && !thread.archivedAt)
  const boundedLimit = Number.isInteger(limit) && limit > 0 ? limit : 0
  const recentIds = new Set(
    activeRoots
      .map((thread, index) => ({ thread, index }))
      .sort((left, right) => threadRecency(right.thread) - threadRecency(left.thread) || left.index - right.index)
      .slice(0, boundedLimit)
      .map(({ thread }) => thread.id),
  )

  const activeRootIds = new Set(activeRoots.map((thread) => thread.id))
  const threadsById = new Map(threads.map((thread) => [thread.id, thread]))
  const attentionRootIds = new Set()
  for (const activity of activities) {
    if (
      activity.projectId !== projectId
      || (activity.state !== 'working' && activity.state !== 'finished')
    ) continue
    const rootId = rootThreadId(threadsById, activity.threadId)
    if (rootId && activeRootIds.has(rootId)) attentionRootIds.add(rootId)
  }

  return activeRoots
    .filter((thread) => recentIds.has(thread.id) || attentionRootIds.has(thread.id))
    .map((thread) => thread.id)
}
