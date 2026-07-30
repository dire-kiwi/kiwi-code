import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import type { RootState } from '@/store/rootReducer'
import type { AppSettings } from '@/types'

// Application settings were read independently in six places -- ThemeProvider,
// App, NewThreadScreen, SettingsShell, TerminalWorkspace and PiNativePane -- and
// written back through an onSettingsUpdated callback that only SettingsShell's
// local copy ever saw. One owner instead: the socket bridge and every save land
// here, and everyone selects.
//
// Not persisted. The server is the source of truth; this is a cache of the
// latest snapshot, hydrated by ServerStateBridge on every connect.
export type SettingsStatus = 'loading' | 'ready' | 'error'

export type SettingsState = {
  settings: AppSettings | null
  status: SettingsStatus
  error: string
}

export const initialSettingsState: SettingsState = {
  settings: null,
  status: 'loading',
  error: '',
}

export const settingsSlice = createSlice({
  name: 'settings',
  initialState: initialSettingsState,
  reducers: {
    // Dispatched both by the socket bridge and by a section that has just saved,
    // so a save is visible everywhere without waiting for the server to echo.
    settingsReceived(state, action: PayloadAction<AppSettings>) {
      state.settings = action.payload
      state.status = 'ready'
      state.error = ''
    },
    settingsFailed(state, action: PayloadAction<string>) {
      state.settings = null
      state.status = 'error'
      state.error = action.payload
    },
    settingsLoading(state) {
      state.status = 'loading'
      state.error = ''
    },
  },
})

export const { settingsFailed, settingsLoading, settingsReceived } = settingsSlice.actions

export const selectSettings = (state: RootState) => state.settings.settings
export const selectSettingsStatus = (state: RootState) => state.settings.status
export const selectSettingsError = (state: RootState) => state.settings.error
export const selectTheme = (state: RootState) => state.settings.settings?.theme ?? null
