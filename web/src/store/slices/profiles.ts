import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import type { RootState } from '@/store/rootReducer'
import type { Profile } from '@/types'

// Profiles are read by the sidebar, the profile switcher, the settings shell and
// the thread finder, and written by the socket plus "create profile". They were
// App state handed down; they are server state with one owner now.
//
// Not persisted -- but note that the *selected* profile id is, and lives in the
// preferences slice. This holds the list, not the choice.
export type ProfilesState = {
  profiles: Profile[]
  hydrated: boolean
}

export const initialProfilesState: ProfilesState = {
  profiles: [],
  hydrated: false,
}

/**
 * Identity guard. A socket push that changes nothing must return the same array,
 * or every subscriber re-renders on each snapshot.
 */
export function sameProfiles(current: readonly Profile[], next: readonly Profile[]) {
  if (current.length !== next.length) return false
  return current.every((profile, index) => {
    const candidate = next[index]
    return candidate && candidate.id === profile.id && candidate.name === profile.name
  })
}

export const profilesSlice = createSlice({
  name: 'profiles',
  initialState: initialProfilesState,
  reducers: {
    profilesReceived(state, action: PayloadAction<Profile[]>) {
      if (!sameProfiles(state.profiles, action.payload)) state.profiles = action.payload
      state.hydrated = true
    },
    // Optimistic: the creator navigates straight into the new profile rather
    // than waiting for the server to echo the list back.
    profileCreated(state, action: PayloadAction<Profile>) {
      if (state.profiles.some((profile) => profile.id === action.payload.id)) return
      state.profiles.push(action.payload)
    },
  },
})

export const { profileCreated, profilesReceived } = profilesSlice.actions

export const selectProfiles = (state: RootState) => state.profiles.profiles
export const selectProfilesHydrated = (state: RootState) => state.profiles.hydrated
