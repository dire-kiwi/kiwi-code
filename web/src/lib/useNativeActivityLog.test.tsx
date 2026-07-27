import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useNativeActivityLog } from './useNativeActivityLog'

describe('useNativeActivityLog', () => {
  it('coalesces immediate repeats and preserves the newest bounded history', () => {
    const { result } = renderHook(() => useNativeActivityLog(2))

    act(() => {
      result.current.appendActivity('ready', 'Connected.', 1_000)
      result.current.appendActivity('ready', 'Connected.', 2_000)
    })
    expect(result.current.activityLog).toEqual([{
      id: 1,
      at: 2_000,
      event: 'ready',
      summary: 'Connected.',
      repeats: 2,
    }])

    act(() => {
      result.current.appendActivity('working', 'Run started.', 4_000)
      result.current.appendActivity('settled', 'Run ended.', 5_000)
    })
    expect(result.current.activityLog.map(({ event }) => event)).toEqual([
      'settled',
      'working',
    ])
  })
})
