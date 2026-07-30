import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import { guardedStoredStateCodec } from '@/lib/storedState'
import { fieldWriters, type PersistedFields } from '@/store/persistence'
import type { RootState } from '@/store/rootReducer'

export type PreferencesState = {
  activeProfileId: string
}

export const initialPreferencesState: PreferencesState = {
  activeProfileId: 'personal',
}

const activeProfileCodec = guardedStoredStateCodec(
  (raw) => raw,
  (value): value is string => typeof value === 'string' && value.length > 0,
)

// Storage keys are a compatibility contract with installed browsers, and the
// end-to-end suite seeds this one directly. Never rename it.
export const preferencesPersistence: PersistedFields<PreferencesState> = {
  activeProfileId: { key: 'kiwi-code-active-profile', codec: activeProfileCodec },
}

export const preferencesSlice = createSlice({
  name: 'preferences',
  initialState: initialPreferencesState,
  reducers: {
    activeProfileSelected(state, action: PayloadAction<string>) {
      state.activeProfileId = action.payload
    },
  },
})

export const { activeProfileSelected } = preferencesSlice.actions

export const selectActiveProfileId = (state: RootState) => state.preferences.activeProfileId

export function preferencesWriters(state: RootState) {
  return fieldWriters(preferencesPersistence, state.preferences)
}
