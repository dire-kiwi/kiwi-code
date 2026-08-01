'use strict'

const path = require('node:path')
const { app, BaseWindow, WebContentsView, ipcMain, shell } = require('electron')
const { BrowserProviderServer } = require('./browser-provider.cjs')
const { BrowserWorkspaceManager } = require('./browser-sessions.cjs')
const {
  BrowserProviderError,
  isRecord,
  navigationOrigin,
  requireLoopbackHttpUrl,
} = require('./browser-helpers.cjs')
const {
  interceptFrontendReloadShortcut,
  reloadFrontend,
} = require('./frontend-reload.cjs')

const desktopUrl = requireLoopbackHttpUrl(
  process.env.KIWI_CODE_DESKTOP_URL || 'http://127.0.0.1:5173',
  'KIWI_CODE_DESKTOP_URL',
).toString()
const desktopOrigin = new URL(desktopUrl).origin
const apiOrigin = process.env.KIWI_CODE_API_ORIGIN
  ? requireLoopbackHttpUrl(process.env.KIWI_CODE_API_ORIGIN, 'KIWI_CODE_API_ORIGIN').origin
  : desktopOrigin
const appIconPath = path.join(__dirname, 'icon.png')
const preloadPath = path.join(__dirname, 'preload.cjs')

const appIpcChannels = {
  reloadFrontend: 'kiwi-code-desktop-app:reload-frontend',
}
const legacyAppIpcChannels = {
  reloadFrontend: 'dire-mux-desktop-app:reload-frontend',
}
const appIpcChannelSets = [appIpcChannels, legacyAppIpcChannels]
const browserIpcChannels = {
  show: 'kiwi-code-desktop-browser:show',
  hide: 'kiwi-code-desktop-browser:hide',
  setBounds: 'kiwi-code-desktop-browser:set-bounds',
  state: 'kiwi-code-desktop-browser:state',
  workspaceShortcut: 'kiwi-code-desktop-browser:workspace-shortcut',
}
const legacyBrowserIpcChannels = {
  show: 'dire-mux-desktop-browser:show',
  hide: 'dire-mux-desktop-browser:hide',
  setBounds: 'dire-mux-desktop-browser:set-bounds',
  state: 'dire-mux-desktop-browser:state',
  workspaceShortcut: 'dire-mux-desktop-browser:workspace-shortcut',
}
const browserIpcChannelSets = [browserIpcChannels, legacyBrowserIpcChannels]
let primaryWindow = null
let trustedView = null
let workspace = null
let provider = null
let cleanupPromise = Promise.resolve()
let quitAfterCleanup = false

app.setName('kiwi-code')
if (process.env.KIWI_CODE_ELECTRON_USER_DATA) {
  app.setPath('userData', path.resolve(process.env.KIWI_CODE_ELECTRON_USER_DATA))
}
const hasSingleInstanceLock = app.requestSingleInstanceLock()

function openExternal(url) {
  try {
    const target = new URL(url)
    if (target.protocol === 'http:' || target.protocol === 'https:') {
      void shell.openExternal(target.toString()).catch((error) => {
        console.error(`Could not open ${target.toString()}:`, error)
      })
    }
  } catch {
    // Ignore malformed links rather than handing them to the operating system.
  }
}

function validateSessionPayload(value, requireBounds) {
  if (!isRecord(value)) throw new BrowserProviderError('invalid_request', 'Desktop view options must be an object.')
  const allowed = new Set(['projectId', 'threadId', ...(requireBounds ? ['bounds'] : [])])
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new BrowserProviderError('invalid_request', `Unknown desktop view field ${key}.`)
  }
  for (const key of ['projectId', 'threadId']) {
    if (
      typeof value[key] !== 'string' ||
      value[key].length < 1 ||
      value[key].length > 256 ||
      value[key].includes('\u0000')
    ) {
      throw new BrowserProviderError('invalid_request', `${key} must be a nonempty string.`)
    }
  }
  if (requireBounds && value.bounds === undefined) throw new BrowserProviderError('invalid_bounds', 'bounds are required.')
  return value
}

function registerIpc() {
  const trustedContents = (event) => {
    const contents = trustedView?.webContents
    if (
      !contents ||
      event.sender !== contents ||
      event.senderFrame !== contents.mainFrame ||
      navigationOrigin(event.senderFrame?.url) !== desktopOrigin
    ) {
      return null
    }
    return contents
  }
  const trustedInvoke = (target, unavailableMessage, handler) => async (event, payload) => {
    if (!trustedContents(event)) {
      throw new BrowserProviderError('unauthorized', 'Untrusted IPC sender.', 403)
    }
    const current = target()
    if (!current) throw new BrowserProviderError('session_not_found', unavailableMessage, 404)
    return handler(current, payload)
  }
  for (const channels of appIpcChannelSets) {
    ipcMain.handle(channels.reloadFrontend, (event) => {
      if (!trustedContents(event)) {
        throw new BrowserProviderError('unauthorized', 'Untrusted IPC sender.', 403)
      }
      const currentView = trustedView
      const currentBrowserWorkspace = workspace
      setImmediate(() => reloadFrontend(currentView, [currentBrowserWorkspace]))
      return { reloading: true }
    })
  }
  const browserInvoke = (handler) => trustedInvoke(
    () => workspace,
    'Desktop browser is unavailable.',
    handler,
  )

  for (const channels of browserIpcChannelSets) {
    ipcMain.handle(channels.show, browserInvoke((current, payload) => current.show(validateSessionPayload(payload, true))))
    ipcMain.handle(channels.hide, browserInvoke((current, payload) => current.hide(validateSessionPayload(payload, false))))
    ipcMain.handle(channels.setBounds, browserInvoke((current, payload) => current.setBounds(validateSessionPayload(payload, true))))
  }
}

function showWhenReady(window, view) {
  let shown = false
  const show = () => {
    if (shown || window.isDestroyed()) return
    shown = true
    window.show()
  }
  view.webContents.once('did-finish-load', show)
  view.webContents.once('did-fail-load', show)
}

async function createWindow() {
  await cleanupPromise.catch(() => {})
  if (primaryWindow && !primaryWindow.isDestroyed()) return

  const window = new BaseWindow({
    width: 1440,
    height: 900,
    minWidth: 960,
    minHeight: 640,
    backgroundColor: '#090b0f',
    titleBarStyle: 'hidden',
    ...(process.platform !== 'darwin' ? {
      titleBarOverlay: {
        color: '#00000000',
        symbolColor: '#e8e8e8',
      },
    } : {}),
    icon: appIconPath,
    show: false,
  })
  const appView = new WebContentsView({
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload: preloadPath,
      sandbox: true,
    },
  })
  window.contentView.addChildView(appView)
  primaryWindow = window
  trustedView = appView

  let currentWorkspace = null
  const requestFrontendReload = () => reloadFrontend(appView, [currentWorkspace])
  currentWorkspace = new BrowserWorkspaceManager({
    WebContentsView,
    hostWindow: window,
    appView,
    desktopOrigin,
    apiOrigin,
    onState: (state) => {
      if (!appView.webContents.isDestroyed()) {
        for (const channels of browserIpcChannelSets) appView.webContents.send(channels.state, state)
      }
    },
    onWorkspaceShortcut: (index) => {
      if (!appView.webContents.isDestroyed()) {
        for (const channels of browserIpcChannelSets) appView.webContents.send(channels.workspaceShortcut, index)
      }
    },
    onFrontendReload: requestFrontendReload,
  })
  workspace = currentWorkspace
  currentWorkspace.resize()
  window.on('resize', () => currentWorkspace.resize())

  appView.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url)
    return { action: 'deny' }
  })
  const handleTrustedNavigation = (details) => {
    if (navigationOrigin(details.url) === desktopOrigin) return
    details.preventDefault()
    openExternal(details.url)
  }
  appView.webContents.on('will-navigate', handleTrustedNavigation)
  appView.webContents.on('will-redirect', handleTrustedNavigation)
  appView.webContents.on('did-start-navigation', (details) => {
    if (details.isMainFrame && !details.isSameDocument) currentWorkspace.detachActiveView()
  })
  appView.webContents.on('render-process-gone', () => currentWorkspace.detachActiveView())
  appView.webContents.on('before-input-event', (event, input) => {
    interceptFrontendReloadShortcut(event, input, requestFrontendReload)
  })

  const currentProvider = new BrowserProviderServer({ app, workspace: currentWorkspace })
  provider = currentProvider
  try {
    await currentProvider.start()
  } catch (error) {
    console.error('Could not start Electron browser provider:', error)
    if (!window.isDestroyed()) window.close()
    throw error
  }

  showWhenReady(window, appView)
  void appView.webContents.loadURL(desktopUrl).catch((error) => {
    console.error(`Could not load ${desktopUrl}:`, error)
    if (!window.isDestroyed()) window.show()
  })

  let cleanupStarted = false
  const beginWindowCleanup = () => {
    if (cleanupStarted) return
    cleanupStarted = true
    if (trustedView === appView) trustedView = null
    cleanupPromise = Promise.all([
      currentWorkspace.dispose(),
      currentProvider.stop(),
    ]).catch((error) => {
      console.error('Could not clean up desktop workspaces:', error)
    }).finally(() => {
      if (!appView.webContents.isDestroyed()) appView.webContents.close({ waitForBeforeUnload: false })
      if (workspace === currentWorkspace) workspace = null
      if (provider === currentProvider) provider = null
    })
  }
  window.once('close', beginWindowCleanup)
  window.once('closed', () => {
    if (primaryWindow === window) primaryWindow = null
    beginWindowCleanup()
  })
}

if (!hasSingleInstanceLock) {
  app.quit()
} else {
  registerIpc()

  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.once(signal, () => app.quit())
  }

  app.on('second-instance', () => {
    if (primaryWindow && !primaryWindow.isDestroyed()) {
      if (primaryWindow.isMinimized()) primaryWindow.restore()
      primaryWindow.focus()
    }
  })

  app.whenReady().then(async () => {
    if (process.platform === 'darwin') app.dock.setIcon(appIconPath)
    await createWindow()
    app.on('activate', () => {
      if (!primaryWindow || primaryWindow.isDestroyed()) void createWindow().catch((error) => console.error('Could not recreate desktop window:', error))
    })
  }).catch((error) => {
    console.error('Could not start Kiwi Code desktop:', error)
    app.quit()
  })

  app.on('before-quit', (event) => {
    if (quitAfterCleanup) return
    event.preventDefault()
    quitAfterCleanup = true
    const activeProvider = provider
    const activeWorkspace = workspace
    void Promise.all([
      cleanupPromise,
      activeProvider?.stop(),
      activeWorkspace?.dispose(),
    ]).finally(() => app.quit())
  })

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit()
  })
}
