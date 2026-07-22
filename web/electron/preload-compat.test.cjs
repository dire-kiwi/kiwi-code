'use strict'

const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const test = require('node:test')
const vm = require('node:vm')

const preloadSource = fs.readFileSync(path.join(__dirname, 'preload.cjs'), 'utf8')

function loadPreload(environment, invoke) {
  const bridges = new Map()
  const listeners = new Map()
  const ipcRenderer = {
    invoke,
    on(channel, listener) {
      const current = listeners.get(channel) || []
      current.push(listener)
      listeners.set(channel, current)
    },
    removeListener(channel, listener) {
      listeners.set(channel, (listeners.get(channel) || []).filter((candidate) => candidate !== listener))
    },
  }
  const contextBridge = {
    exposeInMainWorld(name, value) {
      bridges.set(name, value)
    },
  }
  vm.runInNewContext(preloadSource, {
    process: { isMainFrame: true, env: environment },
    queueMicrotask,
    require(name) {
      assert.equal(name, 'electron')
      return { contextBridge, ipcRenderer }
    },
  }, { filename: 'preload.cjs' })
  return {
    bridges,
    emit(channel, value) {
      for (const listener of listeners.get(channel) || []) listener({}, value)
    },
  }
}

test('preload prefers the legacy IPC handlers in a pre-rename Electron process', async () => {
  const invoked = []
  const runtime = loadPreload({ DIRE_MUX_DESKTOP_URL: 'http://127.0.0.1:4000' }, async (channel) => {
    invoked.push(channel)
    if (!channel.startsWith('dire-mux-')) throw new Error(`No handler registered for '${channel}'`)
    return 'legacy-result'
  })

  assert.deepEqual([...runtime.bridges.keys()].sort(), [
    'direMuxDesktopBrowser',
    'direMuxDesktopCodeServer',
    'kiwiCodeDesktopBrowser',
    'kiwiCodeDesktopCodeServer',
  ])
  assert.equal(await runtime.bridges.get('kiwiCodeDesktopBrowser').hide({ projectId: 'p', threadId: 't' }), 'legacy-result')
  assert.deepEqual(invoked, ['dire-mux-desktop-browser:hide'])
})

test('preload falls back when only the other desktop channel generation is registered', async () => {
  const invoked = []
  const runtime = loadPreload({ KIWI_CODE_DESKTOP_URL: 'http://127.0.0.1:4000' }, async (channel) => {
    invoked.push(channel)
    if (channel.startsWith('kiwi-code-')) throw new Error(`No handler registered for '${channel}'`)
    return 'fallback-result'
  })

  assert.equal(await runtime.bridges.get('kiwiCodeDesktopBrowser').setBackendOrigin('http://127.0.0.1:4000'), 'fallback-result')
  assert.deepEqual(invoked, [
    'kiwi-code-desktop-browser:set-backend-origin',
    'dire-mux-desktop-browser:set-backend-origin',
  ])
})

test('preload deduplicates state emitted on current and legacy aliases', () => {
  const runtime = loadPreload({}, async () => undefined)
  const states = []
  runtime.bridges.get('kiwiCodeDesktopBrowser').onState((state) => states.push(state))
  const state = { projectId: 'p', threadId: 't', visible: true, currentTargetId: 'page' }

  runtime.emit('kiwi-code-desktop-browser:state', state)
  runtime.emit('dire-mux-desktop-browser:state', state)

  assert.deepEqual(states, [state])
})
