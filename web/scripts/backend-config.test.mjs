import assert from 'node:assert/strict'
import test from 'node:test'
import {
  backendStorageKeys,
  forgetBackendOrigin,
  listBackendOrigins,
  normalizeBackendOrigin,
  readActiveBackendOrigin,
  rememberBackendOrigin,
  selectBackendOrigin,
} from '../src/backend-config.mjs'

class MemoryStorage {
  constructor(entries = {}) {
    this.values = new Map(Object.entries(entries))
  }

  getItem(key) {
    return this.values.get(key) ?? null
  }

  setItem(key, value) {
    this.values.set(key, String(value))
  }
}

const localOrigin = 'http://127.0.0.1:4000'

test('backend origins accept convenient LAN names and canonical HTTP URLs', () => {
  assert.equal(normalizeBackendOrigin('devbox'), 'http://devbox:4000')
  assert.equal(normalizeBackendOrigin('devbox:4400'), 'http://devbox:4400')
  assert.equal(normalizeBackendOrigin('[::1]'), 'http://[::1]:4000')
  assert.equal(normalizeBackendOrigin('https://mux.example.test/'), 'https://mux.example.test')
  assert.throws(() => normalizeBackendOrigin('ssh://devbox:4000'), /HTTP or HTTPS/)
  assert.throws(() => normalizeBackendOrigin('http://devbox:4000/api'), /without a path/)
  assert.throws(() => normalizeBackendOrigin('http://user:secret@devbox:4000'), /username or password/)
})

test('saved backend choices retain the default and active selection', () => {
  const storage = new MemoryStorage()
  const remote = rememberBackendOrigin(storage, localOrigin, 'workstation:4100')

  assert.equal(remote, 'http://workstation:4100')
  assert.deepEqual(listBackendOrigins(storage, localOrigin), [localOrigin, remote])
  assert.equal(readActiveBackendOrigin(storage, localOrigin), remote)

  assert.equal(selectBackendOrigin(storage, localOrigin, localOrigin), localOrigin)
  assert.equal(readActiveBackendOrigin(storage, localOrigin), localOrigin)
  assert.deepEqual(listBackendOrigins(storage, localOrigin), [localOrigin, remote])
  assert.throws(
    () => selectBackendOrigin(storage, localOrigin, 'http://unknown:4000'),
    /not in the saved backend list/,
  )
})

test('forgetting the active backend switches back to the default', () => {
  const storage = new MemoryStorage()
  const remote = rememberBackendOrigin(storage, localOrigin, 'remote-host')

  assert.equal(forgetBackendOrigin(storage, localOrigin, remote), localOrigin)
  assert.deepEqual(listBackendOrigins(storage, localOrigin), [localOrigin])
  assert.equal(readActiveBackendOrigin(storage, localOrigin), localOrigin)
})

test('malformed persisted backend data fails closed to the frontend default', () => {
  const storage = new MemoryStorage({
    [backendStorageKeys.origins]: JSON.stringify(['javascript:alert(1)', 'remote:4500', 'remote:4500']),
    [backendStorageKeys.active]: 'http://missing:4000',
  })

  assert.deepEqual(listBackendOrigins(storage, localOrigin), [localOrigin, 'http://remote:4500'])
  assert.equal(readActiveBackendOrigin(storage, localOrigin), localOrigin)

  storage.setItem(backendStorageKeys.origins, '{')
  assert.deepEqual(listBackendOrigins(storage, localOrigin), [localOrigin])
})
