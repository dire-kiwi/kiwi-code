import { createSelector } from '@reduxjs/toolkit'
import { createSidebarThreadIndex } from '@/sidebar-thread-index.mjs'
import { selectPiActivities } from '@/store/slices/agentActivity'
import { selectActiveProfileId } from '@/store/slices/preferences'
import { selectProjects } from '@/store/slices/projects'

// Cross-slice derivations that used to be useMemo calls in App. They must stay
// memoised: each returns a fresh array or object, and an unmemoised selector
// re-renders every subscriber on every unrelated dispatch.

/** Projects belonging to the profile currently selected in the sidebar. */
export const selectActiveProjects = createSelector(
  [selectProjects, selectActiveProfileId],
  (projects, activeProfileId) => projects.filter((project) => project.profileId === activeProfileId),
)

/**
 * The sidebar's lookup structure over projects x threads x activity. Rebuilt
 * only when one of those two actually changes -- it walks every thread in every
 * project, so rebuilding it per render was never affordable.
 */
export const selectThreadIndex = createSelector(
  [selectProjects, selectPiActivities],
  (projects, piActivities) => createSidebarThreadIndex(projects, piActivities),
)

/** The same index narrowed to the active profile, which is what the sidebar shows. */
export const selectActiveThreadIndex = createSelector(
  [selectActiveProjects, selectPiActivities],
  (projects, piActivities) => createSidebarThreadIndex(projects, piActivities),
)

/** Project ids visible under the current profile, for filtering socket lists. */
export const selectActiveProjectIds = createSelector(
  [selectActiveProjects],
  (projects) => new Set(projects.map((project) => project.id)),
)
