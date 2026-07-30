import { configureStore } from '@reduxjs/toolkit'
import { browserStorage } from '@/lib/storedState'
import {
  createPersistence,
  hydrateFields,
  type EnumerableStorage,
  type PersistWriter,
} from './persistence'
import { rootReducer, type RootState } from './rootReducer'
import { initialAgentActivityState } from '@/store/slices/agentActivity'
import {
  hydrateNewThreadPreferences,
  newThreadPreferencesWriters,
} from '@/store/slices/newThreadPreferences'
import {
  initialPreferencesState,
  preferencesPersistence,
  preferencesWriters,
} from '@/store/slices/preferences'
import { initialProfilesState } from '@/store/slices/profiles'
import { initialProjectsState } from '@/store/slices/projects'
import { initialSettingsState } from '@/store/slices/settings'
import { initialSidebarState, sidebarPersistence, sidebarWriters } from '@/store/slices/sidebar'
import { hydrateThreadWorkspace, threadWorkspaceWriters } from '@/store/slices/threadWorkspace'
import { initialThreadWorkspaceRuntimeState } from '@/store/slices/threadWorkspaceRuntime'
import { initialUiState } from '@/store/slices/ui'

export type CreateAppStoreOptions = {
  storage?: EnumerableStorage | null
  persist?: boolean
}

function collectPersistWriters(state: RootState): PersistWriter[] {
  return [
    ...newThreadPreferencesWriters(state),
    ...preferencesWriters(state),
    ...sidebarWriters(state),
    ...threadWorkspaceWriters(state),
  ]
}

export function createAppStore(options: CreateAppStoreOptions = {}) {
  const storage = options.storage === undefined ? browserStorage() : options.storage
  const persist = options.persist !== false

  // Hydrating into preloadedState keeps the first render correct, so nothing
  // mounts against a default that a later effect would have to correct.
  const preloadedState: RootState = {
    newThreadPreferences: hydrateNewThreadPreferences(storage),
    preferences: hydrateFields(initialPreferencesState, preferencesPersistence, storage),
    sidebar: hydrateFields(initialSidebarState, sidebarPersistence, storage),
    threadWorkspace: hydrateThreadWorkspace(storage),
    // Not persisted: server state, re-fetched over the socket on every connect.
    agentActivity: initialAgentActivityState,
    profiles: initialProfilesState,
    projects: initialProjectsState,
    settings: initialSettingsState,
    // Not persisted: live state for whichever workspace is open right now.
    threadWorkspaceRuntime: initialThreadWorkspaceRuntimeState,
    // Not persisted: this is chrome that is open now, not a remembered preference.
    ui: initialUiState,
  }

  const persistence = createPersistence(collectPersistWriters, storage)
  persistence.seed(preloadedState)

  const store = configureStore({
    reducer: rootReducer,
    preloadedState,
    middleware: (getDefaultMiddleware) => persist
      ? getDefaultMiddleware().prepend(persistence.middleware)
      : getDefaultMiddleware(),
  })

  if (persist && typeof window !== 'undefined') {
    // Writes are debounced, so a change made just before the tab goes away has
    // to be forced out or it is lost.
    const flushNow = () => persistence.flush(store.getState())
    window.addEventListener('pagehide', flushNow)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') flushNow()
    })
  }

  return store
}

export const store = createAppStore()

export type AppStore = ReturnType<typeof createAppStore>
export type AppDispatch = AppStore['dispatch']
export type { RootState }
