import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import type { RootState } from '../rootReducer'

// Chrome that is open right now rather than remembered across reloads. It lives
// in the store, not in App, so a global shortcut can toggle it without being
// defined inside the component that happens to render it.
export type UiState = {
  sidebarOpen: boolean
  projectFinderOpen: boolean
  detailsSidebarExpanded: boolean
}

export const initialUiState: UiState = {
  sidebarOpen: false,
  projectFinderOpen: false,
  detailsSidebarExpanded: false,
}

export const uiSlice = createSlice({
  name: 'ui',
  initialState: initialUiState,
  reducers: {
    sidebarOpened(state) {
      state.sidebarOpen = true
    },
    sidebarClosed(state) {
      state.sidebarOpen = false
    },
    sidebarToggled(state) {
      state.sidebarOpen = !state.sidebarOpen
    },
    projectFinderOpened(state) {
      state.projectFinderOpen = true
    },
    projectFinderClosed(state) {
      state.projectFinderOpen = false
    },
    // Navigating away from the sidebar dismisses both at once.
    sidebarDismissed(state) {
      state.sidebarOpen = false
      state.projectFinderOpen = false
    },
    detailsSidebarExpandedChanged(state, action: PayloadAction<boolean>) {
      state.detailsSidebarExpanded = action.payload
    },
  },
})

export const {
  detailsSidebarExpandedChanged,
  projectFinderClosed,
  projectFinderOpened,
  sidebarClosed,
  sidebarDismissed,
  sidebarOpened,
  sidebarToggled,
} = uiSlice.actions

export const selectSidebarOpen = (state: RootState) => state.ui.sidebarOpen
export const selectProjectFinderOpen = (state: RootState) => state.ui.projectFinderOpen
export const selectDetailsSidebarExpanded = (state: RootState) =>
  state.ui.detailsSidebarExpanded
