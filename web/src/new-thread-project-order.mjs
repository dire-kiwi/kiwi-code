function parsedTime(value) {
  if (typeof value !== 'string' || !value) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

function mostRecentThreadCreation(project) {
  let mostRecent = Number.NEGATIVE_INFINITY
  for (const thread of project.threads) {
    const createdAt = parsedTime(thread.createdAt)
    if (createdAt !== null && createdAt > mostRecent) mostRecent = createdAt
  }
  return mostRecent
}

/** Orders projects by their newest thread without mutating the provided project list. */
export function projectsByMostRecentThread(projects) {
  return projects
    .map((project, index) => ({ project, index, createdAt: mostRecentThreadCreation(project) }))
    .sort((left, right) => right.createdAt - left.createdAt || left.index - right.index)
    .map(({ project }) => project)
}
