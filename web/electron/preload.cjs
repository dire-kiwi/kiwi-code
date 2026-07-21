'use strict'

const { contextBridge, ipcRenderer } = require('electron')

const browserChannels = {
  show: 'kiwi-code-desktop-browser:show',
  hide: 'kiwi-code-desktop-browser:hide',
  setBounds: 'kiwi-code-desktop-browser:set-bounds',
  setBackendOrigin: 'kiwi-code-desktop-browser:set-backend-origin',
  state: 'kiwi-code-desktop-browser:state',
  workspaceShortcut: 'kiwi-code-desktop-browser:workspace-shortcut',
}
const codeServerChannels = {
  show: 'kiwi-code-desktop-code-server:show',
  hide: 'kiwi-code-desktop-code-server:hide',
  setBounds: 'kiwi-code-desktop-code-server:set-bounds',
  close: 'kiwi-code-desktop-code-server:close',
  state: 'kiwi-code-desktop-code-server:state',
  workspaceShortcut: 'kiwi-code-desktop-code-server:workspace-shortcut',
}

function stateListener(channels, callback) {
  if (typeof callback !== 'function') throw new TypeError('callback must be a function')
  const listener = (_event, state) => callback(state)
  ipcRenderer.on(channels.state, listener)
  return () => ipcRenderer.removeListener(channels.state, listener)
}

function shortcutListener(channels, callback) {
  if (typeof callback !== 'function') throw new TypeError('callback must be a function')
  const listener = (_event, index) => {
    if (Number.isInteger(index) && index >= 1 && index <= 7) callback(index)
  }
  ipcRenderer.on(channels.workspaceShortcut, listener)
  return () => ipcRenderer.removeListener(channels.workspaceShortcut, listener)
}

if (process.isMainFrame) {
  contextBridge.exposeInMainWorld('kiwiCodeDesktopBrowser', Object.freeze({
    show(options) {
      return ipcRenderer.invoke(browserChannels.show, options)
    },
    hide(options) {
      return ipcRenderer.invoke(browserChannels.hide, options)
    },
    setBounds(options) {
      return ipcRenderer.invoke(browserChannels.setBounds, options)
    },
    setBackendOrigin(origin) {
      return ipcRenderer.invoke(browserChannels.setBackendOrigin, origin)
    },
    onState(callback) {
      return stateListener(browserChannels, callback)
    },
    onWorkspaceShortcut(callback) {
      return shortcutListener(browserChannels, callback)
    },
  }))

  contextBridge.exposeInMainWorld('kiwiCodeDesktopCodeServer', Object.freeze({
    show(options) {
      return ipcRenderer.invoke(codeServerChannels.show, options)
    },
    hide(options) {
      return ipcRenderer.invoke(codeServerChannels.hide, options)
    },
    setBounds(options) {
      return ipcRenderer.invoke(codeServerChannels.setBounds, options)
    },
    close(options) {
      return ipcRenderer.invoke(codeServerChannels.close, options)
    },
    onState(callback) {
      return stateListener(codeServerChannels, callback)
    },
    onWorkspaceShortcut(callback) {
      return shortcutListener(codeServerChannels, callback)
    },
  }))
}
