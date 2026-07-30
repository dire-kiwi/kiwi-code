import { combineReducers } from '@reduxjs/toolkit'
import { newThreadPreferencesSlice } from '@/store/slices/newThreadPreferences'
import { preferencesSlice } from '@/store/slices/preferences'
import { sidebarSlice } from '@/store/slices/sidebar'
import { threadWorkspaceSlice } from '@/store/slices/threadWorkspace'
import { uiSlice } from '@/store/slices/ui'

// RootState lives here rather than in ./index so slices and the persistence
// layer can import the type without pulling in the singleton store.
export const rootReducer = combineReducers({
  newThreadPreferences: newThreadPreferencesSlice.reducer,
  preferences: preferencesSlice.reducer,
  sidebar: sidebarSlice.reducer,
  threadWorkspace: threadWorkspaceSlice.reducer,
  ui: uiSlice.reducer,
})

export type RootState = ReturnType<typeof rootReducer>
