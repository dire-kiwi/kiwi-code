import type { EnumerableStorage } from '../store/persistence'

export type MemoryStorage = EnumerableStorage & {
  values: Map<string, string>
  writes: string[]
}

// Test double for anything that persists through StorageLike. Records the keys
// it was asked to write so tests can assert that untouched values stay untouched.
export function memoryStorage(initial: Record<string, string> = {}): MemoryStorage {
  const values = new Map(Object.entries(initial))
  const writes: string[] = []
  return {
    values,
    writes,
    get length() {
      return values.size
    },
    key: (index) => [...values.keys()][index] ?? null,
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value)
      writes.push(key)
    },
    removeItem: (key) => {
      values.delete(key)
    },
  }
}
