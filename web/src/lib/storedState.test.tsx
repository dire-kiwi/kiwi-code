import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import {
  guardedStoredStateCodec,
  readStoredState,
  useStoredState,
  writeStoredState,
  type StorageLike,
} from './storedState'

const viewCodec = guardedStoredStateCodec(
  (raw) => raw,
  (value): value is 'activity' | 'tree' => value === 'activity' || value === 'tree',
)

function memoryStorage(initial: Record<string, string> = {}): StorageLike & { values: Map<string, string> } {
  const values = new Map(Object.entries(initial))
  return {
    values,
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value)
    },
  }
}

afterEach(cleanup)

describe('storedState', () => {
  it('accepts guarded values and falls back for malformed or rejected values', () => {
    const storage = memoryStorage({
      valid: 'tree',
      invalid: 'grid',
      broken: '{',
    })
    const jsonIds = guardedStoredStateCodec(
      JSON.parse,
      (value): value is string[] => Array.isArray(value)
        && value.every((item) => typeof item === 'string'),
      JSON.stringify,
    )

    expect(readStoredState('valid', 'activity', viewCodec, storage)).toBe('tree')
    expect(readStoredState('invalid', 'activity', viewCodec, storage)).toBe('activity')
    expect(readStoredState('broken', ['fallback'], jsonIds, storage)).toEqual(['fallback'])
  })

  it('fails closed when storage access throws', () => {
    const storage: StorageLike = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }

    expect(readStoredState('view', 'activity', viewCodec, storage)).toBe('activity')
    expect(writeStoredState('view', 'tree', viewCodec, storage)).toBe(false)
  })

  it('loads and persists hook updates, with loading independently suppressible', () => {
    const storage = memoryStorage({ view: 'tree' })
    const { result, unmount } = renderHook(() =>
      useStoredState('view', 'activity', viewCodec, { storage }),
    )
    expect(result.current[0]).toBe('tree')

    act(() => result.current[1]('activity'))
    expect(storage.values.get('view')).toBe('activity')
    unmount()

    const ignored = renderHook(() =>
      useStoredState('view', 'tree', viewCodec, { storage, load: false, save: false }),
    )
    expect(ignored.result.current[0]).toBe('tree')
  })
})
