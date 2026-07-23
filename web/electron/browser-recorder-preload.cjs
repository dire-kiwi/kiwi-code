'use strict'

const { contextBridge, ipcRenderer } = require('electron')

const commandChannel = 'kiwi-code-browser-recorder:command'
const eventChannel = 'kiwi-code-browser-recorder:event'

if (process.isMainFrame) {
  contextBridge.exposeInMainWorld('kiwiCodeBrowserRecorder', Object.freeze({
    onCommand(callback) {
      if (typeof callback !== 'function') throw new TypeError('callback must be a function')
      const listener = (_event, command) => callback(command)
      ipcRenderer.on(commandChannel, listener)
      return () => ipcRenderer.removeListener(commandChannel, listener)
    },
    event(value) {
      return ipcRenderer.invoke(eventChannel, value)
    },
  }))
}
