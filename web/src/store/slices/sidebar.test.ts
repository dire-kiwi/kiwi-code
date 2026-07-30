import { describe, expect, it } from 'vitest'
import {
  bookmarksOnlyChanged,
  initialSidebarState,
  moreThreadsToggled,
  projectCollapseToggled,
  sidebarSlice,
  sidebarWidthChanged,
  threadRevealed,
} from './sidebar'

const reduce = sidebarSlice.reducer

describe('sidebar slice', () => {
  it('toggles project and overflow visibility', () => {
    const collapsed = reduce(undefined, projectCollapseToggled('project'))
    expect(collapsed.collapsedProjectIds).toEqual(['project'])
    const expanded = reduce(collapsed, moreThreadsToggled('project'))
    expect(expanded.expandedMoreProjectIds).toEqual(['project'])
  })

  it('reveals a selected archived thread and its project', () => {
    const hidden = {
      ...initialSidebarState,
      collapsedProjectIds: ['project', 'other'],
    }
    const revealed = reduce(hidden, threadRevealed({ projectId: 'project', expandArchived: true }))
    expect(revealed.collapsedProjectIds).toEqual(['other'])
    expect(revealed.expandedMoreProjectIds).toEqual(['project'])
  })

  it('updates simple preferences', () => {
    expect(reduce(undefined, bookmarksOnlyChanged(true)).bookmarksOnly).toBe(true)
    expect(reduce(undefined, sidebarWidthChanged(320)).width).toBe(320)
  })
})
