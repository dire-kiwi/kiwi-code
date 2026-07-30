import { describe, expect, it } from 'vitest'
import {
  initialSettingsState,
  selectSettings,
  selectSettingsError,
  selectSettingsStatus,
  selectTheme,
  settingsFailed,
  settingsLoading,
  settingsReceived,
  settingsSlice,
  type SettingsState,
} from './settings'
import type { RootState } from '@/store/rootReducer'
import type { AppSettings } from '@/types'

const reduce = settingsSlice.reducer

// Only the fields each assertion reads are populated; nothing here exercises the
// rest of AppSettings.
const settingsFixture = {
  theme: { fontFamily: 'JetBrains Mono', fontSize: 13 },
} as unknown as AppSettings

function settingsRoot(settings: SettingsState) {
  return { settings } as RootState
}

describe('settings slice', () => {
  it('starts empty and loading, so no reader sees a fabricated default', () => {
    expect(initialSettingsState).toEqual({ settings: null, status: 'loading', error: '' })
    expect(selectSettings(settingsRoot(initialSettingsState))).toBeNull()
    expect(selectTheme(settingsRoot(initialSettingsState))).toBeNull()
  })

  it('records a snapshot and clears any previous error', () => {
    const failed = reduce(undefined, settingsFailed('socket closed'))
    const recovered = reduce(failed, settingsReceived(settingsFixture))

    expect(selectSettingsStatus(settingsRoot(recovered))).toBe('ready')
    expect(selectSettingsError(settingsRoot(recovered))).toBe('')
    expect(selectSettings(settingsRoot(recovered))).toBe(settingsFixture)
  })

  it('drops the stale snapshot when the topic fails', () => {
    const ready = reduce(undefined, settingsReceived(settingsFixture))
    const failed = reduce(ready, settingsFailed('subscription error'))

    // Keeping the old snapshot would leave SettingsShell rendering editable
    // values it can no longer save against.
    expect(selectSettings(settingsRoot(failed))).toBeNull()
    expect(selectSettingsStatus(settingsRoot(failed))).toBe('error')
    expect(selectSettingsError(settingsRoot(failed))).toBe('subscription error')
  })

  it('reports loading again on reconnect without inventing an error', () => {
    const ready = reduce(undefined, settingsReceived(settingsFixture))
    const reconnecting = reduce(ready, settingsLoading())

    expect(selectSettingsStatus(settingsRoot(reconnecting))).toBe('loading')
    expect(selectSettingsError(settingsRoot(reconnecting))).toBe('')
  })

  it('makes a save visible to every reader at once', () => {
    // This is the point of the slice. Previously a section saved, called
    // onSettingsUpdated, and only SettingsShell's local copy moved -- the theme
    // provider and the agent panes kept a stale snapshot until the server echoed.
    const saved = {
      ...settingsFixture,
      theme: { fontFamily: 'Fira Code', fontSize: 15 },
    } as unknown as AppSettings
    const next = reduce(reduce(undefined, settingsReceived(settingsFixture)), settingsReceived(saved))

    expect(selectTheme(settingsRoot(next))).toEqual({ fontFamily: 'Fira Code', fontSize: 15 })
  })
})
