import { useEffect, useState, type Dispatch, type SetStateAction } from 'react'

export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>

export type StoredStateCodec<Value> = {
  decode: (raw: string) => Value | undefined
  encode: (value: Value) => string
}

type StoredStateOptions = {
  storage?: StorageLike | null
  load?: boolean
  save?: boolean
}

function fallbackValue<Value>(fallback: Value | (() => Value)): Value {
  return typeof fallback === 'function'
    ? (fallback as () => Value)()
    : fallback
}

function browserStorage(): StorageLike | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function guardedStoredStateCodec<Value>(
  parse: (raw: string) => unknown,
  isValue: (value: unknown) => value is Value,
  stringify: (value: Value) => string = String,
): StoredStateCodec<Value> {
  return {
    decode(raw) {
      try {
        const parsed = parse(raw)
        return isValue(parsed) ? parsed : undefined
      } catch {
        return undefined
      }
    },
    encode: stringify,
  }
}

export function readStoredState<Value>(
  key: string,
  fallback: Value | (() => Value),
  codec: StoredStateCodec<Value>,
  storage: StorageLike | null = browserStorage(),
): Value {
  if (!storage) return fallbackValue(fallback)
  try {
    const raw = storage.getItem(key)
    if (raw !== null) {
      const decoded = codec.decode(raw)
      if (decoded !== undefined) return decoded
    }
  } catch {
    // Restrictive storage policies must not prevent the UI from loading.
  }
  return fallbackValue(fallback)
}

export function writeStoredState<Value>(
  key: string,
  value: Value,
  codec: StoredStateCodec<Value>,
  storage: StorageLike | null = browserStorage(),
): boolean {
  if (!storage) return false
  try {
    storage.setItem(key, codec.encode(value))
    return true
  } catch {
    return false
  }
}

export function useStoredState<Value>(
  key: string,
  fallback: Value | (() => Value),
  codec: StoredStateCodec<Value>,
  options: StoredStateOptions = {},
): [Value, Dispatch<SetStateAction<Value>>] {
  const storage = options.storage === undefined ? browserStorage() : options.storage
  const load = options.load !== false
  const save = options.save !== false
  const [value, setValue] = useState<Value>(() =>
    load ? readStoredState(key, fallback, codec, storage) : fallbackValue(fallback),
  )

  useEffect(() => {
    if (save) writeStoredState(key, value, codec, storage)
  }, [codec, key, save, storage, value])

  return [value, setValue]
}

export const booleanStoredState = guardedStoredStateCodec(
  (raw) => raw === 'true' ? true : raw === 'false' ? false : raw,
  (value): value is boolean => typeof value === 'boolean',
  String,
)
