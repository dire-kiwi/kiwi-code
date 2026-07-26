export type NewThreadProject = {
  threads: readonly {
    createdAt: string
  }[]
}

export function projectsByMostRecentThread<Project extends NewThreadProject>(
  projects: readonly Project[],
): Project[]
