'use strict'

const { contextBridge, ipcRenderer } = require('electron')

const appIpcChannels = {
  reloadFrontend: 'kiwi-code-desktop-app:reload-frontend',
}
const legacyAppIpcChannels = {
  reloadFrontend: 'dire-mux-desktop-app:reload-frontend',
}
const browserIpcChannels = {
  show: 'kiwi-code-desktop-browser:show',
  hide: 'kiwi-code-desktop-browser:hide',
  setBounds: 'kiwi-code-desktop-browser:set-bounds',
  setBackendOrigin: 'kiwi-code-desktop-browser:set-backend-origin',
  state: 'kiwi-code-desktop-browser:state',
  workspaceShortcut: 'kiwi-code-desktop-browser:workspace-shortcut',
}
const legacyBrowserIpcChannels = {
  show: 'dire-mux-desktop-browser:show',
  hide: 'dire-mux-desktop-browser:hide',
  setBounds: 'dire-mux-desktop-browser:set-bounds',
  setBackendOrigin: 'dire-mux-desktop-browser:set-backend-origin',
  state: 'dire-mux-desktop-browser:state',
  workspaceShortcut: 'dire-mux-desktop-browser:workspace-shortcut',
}
function compatibleChannelSets(current, legacy) {
  const environment = process.env || {}
  const legacyRuntime = !environment.KIWI_CODE_BROWSER_PROVIDER_CONFIG && Boolean(
    environment.DIRE_MUX_BROWSER_PROVIDER_CONFIG || environment.DIRE_MUX_DESKTOP_URL,
  )
  return legacyRuntime ? [legacy, current] : [current, legacy]
}

const browserChannelSets = compatibleChannelSets(browserIpcChannels, legacyBrowserIpcChannels)
const appChannelSets = compatibleChannelSets(appIpcChannels, legacyAppIpcChannels)

function missingHandler(error) {
  const message = error instanceof Error ? error.message : String(error)
  return message.includes('No handler registered for')
}

async function invokeCompatible(channelSets, operation, payload) {
  let unavailableError
  for (const channels of channelSets) {
    try {
      return await ipcRenderer.invoke(channels[operation], payload)
    } catch (error) {
      if (!missingHandler(error)) throw error
      unavailableError = error
    }
  }
  throw unavailableError || new Error('Desktop IPC handler is unavailable.')
}

function multiplexedListener(channelSets, operation, callback) {
  if (typeof callback !== 'function') throw new TypeError('callback must be a function')
  let pendingSignature = null
  let resetScheduled = false
  const listener = (_event, value) => {
    const signature = JSON.stringify(value)
    if (resetScheduled && signature === pendingSignature) return
    pendingSignature = signature
    if (!resetScheduled) {
      resetScheduled = true
      queueMicrotask(() => {
        pendingSignature = null
        resetScheduled = false
      })
    }
    callback(value)
  }
  for (const channels of channelSets) ipcRenderer.on(channels[operation], listener)
  return () => {
    for (const channels of channelSets) ipcRenderer.removeListener(channels[operation], listener)
  }
}

function stateListener(channelSets, callback) {
  return multiplexedListener(channelSets, 'state', callback)
}

function shortcutListener(channelSets, callback) {
  return multiplexedListener(channelSets, 'workspaceShortcut', (index) => {
    if (Number.isInteger(index) && index >= 1 && index <= 6) callback(index)
  })
}

function createBrowserBridge() {
  return Object.freeze({
    show(options) {
      return invokeCompatible(browserChannelSets, 'show', options)
    },
    hide(options) {
      return invokeCompatible(browserChannelSets, 'hide', options)
    },
    setBounds(options) {
      return invokeCompatible(browserChannelSets, 'setBounds', options)
    },
    setBackendOrigin(origin) {
      return invokeCompatible(browserChannelSets, 'setBackendOrigin', origin)
    },
    onState(callback) {
      return stateListener(browserChannelSets, callback)
    },
    onWorkspaceShortcut(callback) {
      return shortcutListener(browserChannelSets, callback)
    },
  })
}

function createAppBridge() {
  return Object.freeze({
    platform: process.platform,
    reloadFrontend() {
      return invokeCompatible(appChannelSets, 'reloadFrontend')
    },
  })
}

if (process.isMainFrame) {
  contextBridge.exposeInMainWorld('kiwiCodeDesktopApp', createAppBridge())
  contextBridge.exposeInMainWorld('direMuxDesktopApp', createAppBridge())
  contextBridge.exposeInMainWorld('kiwiCodeDesktopBrowser', createBrowserBridge())
  contextBridge.exposeInMainWorld('direMuxDesktopBrowser', createBrowserBridge())
}
