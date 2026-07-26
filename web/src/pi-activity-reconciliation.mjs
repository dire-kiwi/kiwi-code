export function piActivityKey(projectId, threadId) {
  return `${projectId}\u0000${threadId}`
}

export function piActivityVersion(activity) {
  return `${activity.state}\u0000${activity.updatedAt}`
}

export function samePiActivities(current, next) {
  if (current.length !== next.length) return false
  return current.every((activity, index) => {
    const candidate = next[index]
    return candidate
      && candidate.projectId === activity.projectId
      && candidate.threadId === activity.threadId
      && candidate.state === activity.state
      && candidate.updatedAt === activity.updatedAt
  })
}

function parsedActivityTime(value) {
  if (typeof value !== 'string' || !value) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

function isStrictlyNewerActivity(activity, acknowledgedActivity) {
  const activityTime = parsedActivityTime(activity.updatedAt)
  const acknowledgedTime = parsedActivityTime(acknowledgedActivity.updatedAt)
  return activityTime !== null && acknowledgedTime !== null && activityTime > acknowledgedTime
}

/**
 * Applies a full server snapshot while retaining optimistic acknowledgement
 * tombstones. A tombstone is only released once the server omits that thread or
 * reports activity with a strictly newer timestamp.
 */
export function reconcilePiActivities(nextActivities, acknowledgements) {
  const presentKeys = new Set()
  const visibleActivities = []

  for (const activity of nextActivities) {
    const key = piActivityKey(activity.projectId, activity.threadId)
    presentKeys.add(key)
    const acknowledgement = acknowledgements.get(key)
    if (!acknowledgement) {
      visibleActivities.push(activity)
      continue
    }
    if (isStrictlyNewerActivity(activity, acknowledgement.activity)) {
      acknowledgements.delete(key)
      visibleActivities.push(activity)
    }
  }

  for (const key of acknowledgements.keys()) {
    if (!presentKeys.has(key)) acknowledgements.delete(key)
  }
  return visibleActivities
}

/**
 * Drops failed-attempt guards when the authoritative snapshot advances. The
 * guard prevents passive workspace interaction from repeatedly flashing the
 * same completion after a failed DELETE; an explicit row selection can retry.
 */
export function reconcileFailedPiAcknowledgements(nextActivities, failedAcknowledgements) {
  const incoming = new Map(nextActivities.map((activity) => [
    piActivityKey(activity.projectId, activity.threadId),
    piActivityVersion(activity),
  ]))
  for (const [key, version] of failedAcknowledgements) {
    if (incoming.get(key) !== version) failedAcknowledgements.delete(key)
  }
}

export function restoreAcknowledgedPiActivity(current, acknowledgement) {
  const restored = acknowledgement.activity
  const key = piActivityKey(restored.projectId, restored.threadId)
  const existingIndex = current.findIndex((activity) =>
    piActivityKey(activity.projectId, activity.threadId) === key)
  if (existingIndex >= 0) {
    if (isStrictlyNewerActivity(current[existingIndex], restored)) return current
    const next = [...current]
    next[existingIndex] = restored
    return next
  }

  const next = [...current]
  const index = Math.max(0, Math.min(acknowledgement.index, next.length))
  next.splice(index, 0, restored)
  return next
}
