import { describe, expect, it } from 'vitest'
import {
  childThreadsCollapseToggled,
  defaultSidebarWidth,
  initialSidebarState,
  projectCollapseToggled,
  selectCollapsedProjectIds,
  sidebarSlice,
  sidebarWidthChanged,
  sidebarWidthNudged,
  sidebarWidthReset,
  threadRevealed,
  webServersCollapseToggled,
  type SidebarState,
} from './sidebar'
import type { RootState } from '@/store/rootReducer'

const reduce = sidebarSlice.reducer

function sidebarRoot(sidebar: SidebarState) {
  return { sidebar } as RootState
}

describe('sidebar slice', () => {
  it('clamps width on write, not only on decode', () => {
    expect(reduce(undefined, sidebarWidthChanged(9000)).width).toBe(384)
    expect(reduce(undefined, sidebarWidthChanged(10)).width).toBe(288)
    expect(reduce(undefined, sidebarWidthChanged(320.4)).width).toBe(320)
  })

  it('nudges and resets width within the clamp', () => {
    const widened = reduce(undefined, sidebarWidthNudged(16))
    expect(widened.width).toBe(defaultSidebarWidth + 16)
    expect(reduce(widened, sidebarWidthNudged(-1000)).width).toBe(288)
    expect(reduce(widened, sidebarWidthReset()).width).toBe(defaultSidebarWidth)
  })

  it('toggles membership for collapse sets', () => {
    const collapsed = reduce(undefined, projectCollapseToggled('project-1'))
    expect(collapsed.collapsedProjectIds).toEqual(['project-1'])
    expect(reduce(collapsed, projectCollapseToggled('project-1')).collapsedProjectIds).toEqual([])

    const threads = reduce(undefined, childThreadsCollapseToggled('thread-1'))
    expect(threads.collapsedChildThreadIds).toEqual(['thread-1'])
  })

  it('toggles the web servers section', () => {
    const collapsed = reduce(undefined, webServersCollapseToggled())
    expect(collapsed.webServersCollapsed).toBe(true)
    expect(reduce(collapsed, webServersCollapseToggled()).webServersCollapsed).toBe(false)
  })

  it('reveals a selected thread by expanding whatever hides it', () => {
    const hidden: SidebarState = {
      ...initialSidebarState,
      collapsedProjectIds: ['project-1', 'project-2'],
      collapsedChildThreadIds: ['root', 'middle', 'unrelated'],
    }
    const revealed = reduce(hidden, threadRevealed({
      projectId: 'project-1',
      ancestorIds: ['middle', 'root'],
      expandArchived: true,
    }))

    expect(revealed.collapsedChildThreadIds).toEqual(['unrelated'])
    expect(revealed.collapsedProjectIds).toEqual(['project-2'])
    expect(revealed.expandedMoreProjectIds).toEqual(['project-1'])
  })

  it('keeps array identity when a reveal changes nothing', () => {
    // This action fires on every selection change. Handing the memoised Set
    // selectors a fresh array each time would re-render the whole sidebar.
    const state = reduce(undefined, threadRevealed({
      projectId: 'project-1',
      ancestorIds: ['root'],
      expandArchived: false,
    }))

    expect(state.collapsedProjectIds).toBe(initialSidebarState.collapsedProjectIds)
    expect(state.collapsedChildThreadIds).toBe(initialSidebarState.collapsedChildThreadIds)
    expect(selectCollapsedProjectIds(sidebarRoot(state)))
      .toBe(selectCollapsedProjectIds(sidebarRoot(state)))
  })
})
