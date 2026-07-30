import { createListenerMiddleware, type Middleware } from '@reduxjs/toolkit'
import {
  readStoredState,
  writeStoredState,
  type StorageLike,
  type StoredStateCodec,
} from '../lib/storedState'
import type { RootState } from './rootReducer'

// Dynamic-key slices need to discover their keys at boot, which plain
// StorageLike cannot express.
export type EnumerableStorage = StorageLike & Pick<Storage, 'key' | 'length' | 'removeItem'>

// Maps slice fields onto the localStorage keys they have always used. These key
// strings are a compatibility contract with every installed browser: renaming
// one silently resets that preference for every existing user.
export type PersistedFields<State> = {
  [Field in keyof State]?: {
    key: string
    codec: StoredStateCodec<State[Field]>
  }
}

// `encode` stays lazy so seeding, which only needs keys, never pays for it.
export type PersistWriter = {
  key: string
  encode: () => string
}

const rawCodec: StoredStateCodec<string> = {
  decode: (raw) => raw,
  encode: (value) => value,
}

const persistDelay = 150
// A busy stream of unrelated actions -- agent activity arriving over the socket
// restarts the debounce on every push -- must not be able to hold a real
// preference change unwritten indefinitely.
const persistMaxDelay = 1000

export function hydrateFields<State extends object>(
  initial: State,
  fields: PersistedFields<State>,
  storage: StorageLike | null,
): State {
  const next = { ...initial }
  for (const field of Object.keys(fields) as Array<keyof State>) {
    const entry = fields[field]
    if (entry) next[field] = readStoredState(entry.key, initial[field], entry.codec, storage)
  }
  return next
}

export function fieldWriters<State extends object>(
  fields: PersistedFields<State>,
  state: State,
): PersistWriter[] {
  return (Object.keys(fields) as Array<keyof State>).flatMap((field) => {
    const entry = fields[field]
    if (!entry) return []
    return [{ key: entry.key, encode: () => entry.codec.encode(state[field]) }]
  })
}

export function scanStoredKeys(
  storage: EnumerableStorage | null,
  visit: (key: string, raw: string) => void,
) {
  // StorageLike only promises getItem/setItem, so enumeration stays optional.
  if (!storage || typeof storage.key !== 'function') return
  try {
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index)
      if (key === null) continue
      const raw = storage.getItem(key)
      if (raw !== null) visit(key, raw)
    }
  } catch {
    // Restrictive storage policies must not prevent the UI from loading.
  }
}

export type Persistence = {
  flush: (state: RootState) => void
  seed: (state: RootState) => void
  middleware: Middleware
}

export function createPersistence(
  collectWriters: (state: RootState) => PersistWriter[],
  storage: StorageLike | null,
): Persistence {
  const lastWritten = new Map<string, string>()

  function flush(state: RootState) {
    for (const writer of collectWriters(state)) {
      const encoded = writer.encode()
      if (lastWritten.get(writer.key) === encoded) continue
      lastWritten.set(writer.key, encoded)
      writeStoredState(writer.key, encoded, rawCodec, storage)
    }
  }

  // Seeding from the hydrated state, not from storage, is what keeps an
  // untouched value from being written straight back out the way the old hook
  // did on mount. Hydration already made the state equal what storage holds, so
  // a key is only written once something actually changes it -- including keys
  // that storage does not have yet, whose value is still the default.
  function seed(state: RootState) {
    for (const writer of collectWriters(state)) {
      lastWritten.set(writer.key, writer.encode())
    }
  }

  let pendingSince = 0

  function flushPending(state: RootState) {
    pendingSince = 0
    flush(state)
  }

  const listener = createListenerMiddleware()
  listener.startListening({
    predicate: () => true,
    effect: async (_action, api) => {
      const now = Date.now()
      if (pendingSince === 0) pendingSince = now
      if (now - pendingSince >= persistMaxDelay) {
        flushPending(api.getState() as RootState)
        return
      }
      api.cancelActiveListeners()
      // Cancellation rejects here, so pendingSince survives until a flush.
      await api.delay(persistDelay)
      flushPending(api.getState() as RootState)
    },
  })

  return {
    flush: (state) => flushPending(state),
    seed,
    middleware: listener.middleware as Middleware,
  }
}
