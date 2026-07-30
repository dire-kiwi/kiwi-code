import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import type { RootState } from '@/store/rootReducer'

// Chrome that is open right now rather than remembered across reloads.
export type UiState = {
  sidebarOpen: boolean
  detailsSidebarExpanded: boolean
}

export const initialUiState: UiState = {
  sidebarOpen: false,
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
    sidebarDismissed(state) {
      state.sidebarOpen = false
    },
    detailsSidebarExpandedChanged(state, action: PayloadAction<boolean>) {
      state.detailsSidebarExpanded = action.payload
    },
  },
})

export const {
  detailsSidebarExpandedChanged,
  sidebarClosed,
  sidebarDismissed,
  sidebarOpened,
  sidebarToggled,
} = uiSlice.actions

export const selectSidebarOpen = (state: RootState) => state.ui.sidebarOpen
export const selectDetailsSidebarExpanded = (state: RootState) =>
  state.ui.detailsSidebarExpanded
