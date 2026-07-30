import { describe, expect, it } from 'vitest'
import { memoryStorage } from './memoryStorage'
import {
  guardedStoredStateCodec,
  readStoredState,
  writeStoredState,
  type StorageLike,
} from './storedState'

const viewCodec = guardedStoredStateCodec(
  (raw) => raw,
  (value): value is 'activity' | 'tree' => value === 'activity' || value === 'tree',
)

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
})
