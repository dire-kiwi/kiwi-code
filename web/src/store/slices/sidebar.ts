import { createSelector, createSlice, type PayloadAction } from '@reduxjs/toolkit'
import { guardedStoredStateCodec } from '@/lib/storedState'
import { fieldWriters, type PersistedFields } from '@/store/persistence'
import type { RootState } from '@/store/rootReducer'

export type SidebarViewMode = 'activity' | 'tree'

export const defaultSidebarWidth = 288
const minSidebarWidth = 288
const maxSidebarWidth = 384
export const sidebarWidthKeyboardStep = 16

export function clampSidebarWidth(value: number) {
  return Math.min(maxSidebarWidth, Math.max(minSidebarWidth, Math.round(value)))
}

export type SidebarState = {
  view: SidebarViewMode
  width: number
  collapsedProjectIds: string[]
  collapsedChildThreadIds: string[]
  // Ephemeral: the same "which rows are open" concern as the fields above, but
  // deliberately not persisted, matching the behaviour before the migration.
  expandedMoreProjectIds: string[]
}

export const initialSidebarState: SidebarState = {
  view: 'activity',
  width: defaultSidebarWidth,
  collapsedProjectIds: [],
  collapsedChildThreadIds: [],
  expandedMoreProjectIds: [],
}

const sidebarViewCodec = guardedStoredStateCodec(
  (raw) => raw,
  (value): value is SidebarViewMode => value === 'activity' || value === 'tree',
)

const sidebarWidthCodec = guardedStoredStateCodec(
  (raw) => {
    const value = Number(raw)
    return Number.isFinite(value) && value > 0 ? clampSidebarWidth(value) : value
  },
  (value): value is number => typeof value === 'number' && Number.isFinite(value) && value > 0,
)

// Historically a Set encoded as a JSON array. Storing the array directly keeps
// the encoding byte-identical while staying serialisable for Redux.
const storedIdListCodec = guardedStoredStateCodec<string[]>(
  JSON.parse,
  (value): value is string[] => Array.isArray(value)
    && value.every((id) => typeof id === 'string'),
  JSON.stringify,
)

// Storage keys are a compatibility contract with installed browsers. Never rename.
export const sidebarPersistence: PersistedFields<SidebarState> = {
  view: { key: 'kiwi-code.sidebar.view', codec: sidebarViewCodec },
  width: { key: 'kiwi-code.sidebar.width', codec: sidebarWidthCodec },
  collapsedProjectIds: {
    key: 'kiwi-code.sidebar.collapsed-projects',
    codec: storedIdListCodec,
  },
  collapsedChildThreadIds: {
    key: 'kiwi-code.sidebar.collapsed-child-threads',
    codec: storedIdListCodec,
  },
}

function toggleId(ids: string[], id: string) {
  const index = ids.indexOf(id)
  if (index < 0) ids.push(id)
  else ids.splice(index, 1)
}

export const sidebarSlice = createSlice({
  name: 'sidebar',
  initialState: initialSidebarState,
  reducers: {
    sidebarViewChanged(state, action: PayloadAction<SidebarViewMode>) {
      state.view = action.payload
    },
    // Clamping lives here rather than only in the codec so it guards writes as
    // well as decodes.
    sidebarWidthChanged(state, action: PayloadAction<number>) {
      state.width = clampSidebarWidth(action.payload)
    },
    sidebarWidthNudged(state, action: PayloadAction<number>) {
      state.width = clampSidebarWidth(state.width + action.payload)
    },
    sidebarWidthReset(state) {
      state.width = defaultSidebarWidth
    },
    projectCollapseToggled(state, action: PayloadAction<string>) {
      toggleId(state.collapsedProjectIds, action.payload)
    },
    childThreadsCollapseToggled(state, action: PayloadAction<string>) {
      toggleId(state.collapsedChildThreadIds, action.payload)
    },
    moreThreadsToggled(state, action: PayloadAction<string>) {
      toggleId(state.expandedMoreProjectIds, action.payload)
    },
    // Selecting a nested thread expands whatever hides it. This fires on every
    // selection change, so each field is only reassigned when it truly changed;
    // an unconditional filter would hand the memoised Set selectors a fresh
    // array every time and re-render the whole sidebar.
    threadRevealed(state, action: PayloadAction<{
      projectId: string
      ancestorIds: string[]
      expandArchived: boolean
    }>) {
      const { projectId, ancestorIds, expandArchived } = action.payload
      if (ancestorIds.length > 0) {
        const threads = state.collapsedChildThreadIds.filter((id) => !ancestorIds.includes(id))
        if (threads.length !== state.collapsedChildThreadIds.length) {
          state.collapsedChildThreadIds = threads
        }
      }
      if (expandArchived && !state.expandedMoreProjectIds.includes(projectId)) {
        state.expandedMoreProjectIds.push(projectId)
      }
      const projects = state.collapsedProjectIds.filter((id) => id !== projectId)
      if (projects.length !== state.collapsedProjectIds.length) {
        state.collapsedProjectIds = projects
      }
    },
  },
})

export const {
  childThreadsCollapseToggled,
  moreThreadsToggled,
  projectCollapseToggled,
  sidebarViewChanged,
  sidebarWidthChanged,
  sidebarWidthNudged,
  sidebarWidthReset,
  threadRevealed,
} = sidebarSlice.actions

export const selectSidebarView = (state: RootState) => state.sidebar.view
export const selectSidebarWidth = (state: RootState) => state.sidebar.width

// Call sites test membership, so hand them a Set. Reducers keep the source
// array's identity stable, so these rebuild only on a real membership change.
export const selectCollapsedProjectIds = createSelector(
  [(state: RootState) => state.sidebar.collapsedProjectIds],
  (ids): ReadonlySet<string> => new Set(ids),
)
export const selectCollapsedChildThreadIds = createSelector(
  [(state: RootState) => state.sidebar.collapsedChildThreadIds],
  (ids): ReadonlySet<string> => new Set(ids),
)
export const selectExpandedMoreProjectIds = createSelector(
  [(state: RootState) => state.sidebar.expandedMoreProjectIds],
  (ids): ReadonlySet<string> => new Set(ids),
)

export function sidebarWriters(state: RootState) {
  return fieldWriters(sidebarPersistence, state.sidebar)
}
