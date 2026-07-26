import assert from 'node:assert/strict'
import test from 'node:test'
import { reloadFrontend } from '../src/frontend-reload.mjs'

test('frontend reload uses the Electron hard-reload bridge when available', async () => {
  const calls = []
  const result = await reloadFrontend({
    kiwiCodeDesktopApp: { reloadFrontend: async () => { calls.push('desktop') } },
    location: { reload: () => calls.push('browser') },
  })

  assert.equal(result, 'desktop')
  assert.deepEqual(calls, ['desktop'])
})

test('frontend reload falls back to browser location reload', async () => {
  let reloads = 0
  const result = await reloadFrontend({
    location: { reload: () => { reloads += 1 } },
  })

  assert.equal(result, 'browser')
  assert.equal(reloads, 1)
})

test('frontend reload falls back when an older Electron main has no handler', async () => {
  const calls = []
  const result = await reloadFrontend({
    kiwiCodeDesktopApp: {
      reloadFrontend: async () => {
        calls.push('desktop')
        throw new Error('No handler registered for reload')
      },
    },
    location: { reload: () => calls.push('browser') },
  })

  assert.equal(result, 'browser')
  assert.deepEqual(calls, ['desktop', 'browser'])
})
