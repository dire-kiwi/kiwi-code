import { combineReducers } from '@reduxjs/toolkit'
import { agentActivitySlice } from '@/store/slices/agentActivity'
import { newThreadPreferencesSlice } from '@/store/slices/newThreadPreferences'
import { preferencesSlice } from '@/store/slices/preferences'
import { profilesSlice } from '@/store/slices/profiles'
import { projectsSlice } from '@/store/slices/projects'
import { settingsSlice } from '@/store/slices/settings'
import { sidebarSlice } from '@/store/slices/sidebar'
import { threadWorkspaceSlice } from '@/store/slices/threadWorkspace'
import { threadWorkspaceRuntimeSlice } from '@/store/slices/threadWorkspaceRuntime'
import { uiSlice } from '@/store/slices/ui'

// RootState lives here rather than in ./index so slices and the persistence
// layer can import the type without pulling in the singleton store.
export const rootReducer = combineReducers({
  agentActivity: agentActivitySlice.reducer,
  newThreadPreferences: newThreadPreferencesSlice.reducer,
  preferences: preferencesSlice.reducer,
  profiles: profilesSlice.reducer,
  projects: projectsSlice.reducer,
  settings: settingsSlice.reducer,
  sidebar: sidebarSlice.reducer,
  threadWorkspace: threadWorkspaceSlice.reducer,
  threadWorkspaceRuntime: threadWorkspaceRuntimeSlice.reducer,
  ui: uiSlice.reducer,
})

export type RootState = ReturnType<typeof rootReducer>
