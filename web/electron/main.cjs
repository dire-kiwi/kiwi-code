'use strict'

const path = require('node:path')
const { app, BaseWindow, WebContentsView, ipcMain, session: electronSession, shell } = require('electron')
const { BrowserProviderServer } = require('./browser-provider.cjs')
const { BrowserWorkspaceManager } = require('./browser-sessions.cjs')
const {
  CODE_SERVER_PARTITION,
  CodeServerWorkspaceManager,
} = require('./code-server-manager.cjs')
const {
  BrowserProviderError,
  isRecord,
  navigationOrigin,
  requireLoopbackHttpUrl,
} = require('./browser-helpers.cjs')

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

const browserIpcChannels = {
  show: 'kiwi-code-desktop-browser:show',
  hide: 'kiwi-code-desktop-browser:hide',
  setBounds: 'kiwi-code-desktop-browser:set-bounds',
  setBackendOrigin: 'kiwi-code-desktop-browser:set-backend-origin',
  state: 'kiwi-code-desktop-browser:state',
  workspaceShortcut: 'kiwi-code-desktop-browser:workspace-shortcut',
}
const codeServerIpcChannels = {
  show: 'kiwi-code-desktop-code-server:show',
  hide: 'kiwi-code-desktop-code-server:hide',
  setBounds: 'kiwi-code-desktop-code-server:set-bounds',
  close: 'kiwi-code-desktop-code-server:close',
  state: 'kiwi-code-desktop-code-server:state',
  workspaceShortcut: 'kiwi-code-desktop-code-server:workspace-shortcut',
}

let primaryWindow = null
let trustedView = null
let workspace = null
let codeServerWorkspace = null
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

function validateSessionPayload(value, requireBounds, requireWorkspacePath = false) {
  if (!isRecord(value)) throw new BrowserProviderError('invalid_request', 'Desktop view options must be an object.')
  const allowed = new Set(['projectId', 'threadId', ...(requireBounds ? ['bounds'] : []), ...(requireWorkspacePath ? ['workspacePath'] : [])])
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
  if (
    requireWorkspacePath &&
    (typeof value.workspacePath !== 'string' || value.workspacePath.length < 1 || value.workspacePath.length > 32_768)
  ) {
    throw new BrowserProviderError('invalid_request', 'workspacePath must be a nonempty string.')
  }
  return value
}

function validateBackendOrigin(value) {
  if (typeof value !== 'string') {
    throw new BrowserProviderError('invalid_request', 'Backend origin must be a string.')
  }
  let url
  try {
    url = new URL(value)
  } catch {
    throw new BrowserProviderError('invalid_request', 'Backend origin must be a valid URL.')
  }
  if (
    (url.protocol !== 'http:' && url.protocol !== 'https:') ||
    url.username ||
    url.password ||
    url.pathname !== '/' ||
    url.search ||
    url.hash
  ) {
    throw new BrowserProviderError('invalid_request', 'Backend origin must be an HTTP or HTTPS origin.')
  }
  return url.origin
}

function registerIpc() {
  const trustedInvoke = (target, unavailableMessage, handler) => async (event, payload) => {
    const contents = trustedView?.webContents
    if (
      !contents ||
      event.sender !== contents ||
      event.senderFrame !== contents.mainFrame ||
      navigationOrigin(event.senderFrame?.url) !== desktopOrigin
    ) {
      throw new BrowserProviderError('unauthorized', 'Untrusted IPC sender.', 403)
    }
    const current = target()
    if (!current) throw new BrowserProviderError('session_not_found', unavailableMessage, 404)
    return handler(current, payload)
  }
  const browserInvoke = (handler) => trustedInvoke(
    () => workspace,
    'Desktop browser is unavailable.',
    handler,
  )
  const codeServerInvoke = (handler) => trustedInvoke(
    () => codeServerWorkspace,
    'Desktop Code workspace is unavailable.',
    handler,
  )

  ipcMain.handle(browserIpcChannels.show, browserInvoke((current, payload) => {
    codeServerWorkspace?.detachActiveView()
    return current.show(validateSessionPayload(payload, true))
  }))
  ipcMain.handle(browserIpcChannels.hide, browserInvoke((current, payload) => current.hide(validateSessionPayload(payload, false))))
  ipcMain.handle(browserIpcChannels.setBounds, browserInvoke((current, payload) => current.setBounds(validateSessionPayload(payload, true))))
  ipcMain.handle(browserIpcChannels.setBackendOrigin, browserInvoke((current, payload) => {
    const origin = validateBackendOrigin(payload)
    codeServerWorkspace?.addProtectedOrigin(origin)
    codeServerWorkspace?.detachActiveView()
    return current.setRendererBackendOrigin(origin)
  }))
  ipcMain.handle(codeServerIpcChannels.show, codeServerInvoke((current, payload) => {
    workspace?.detachActiveView()
    return current.show(validateSessionPayload(payload, true, true))
  }))
  ipcMain.handle(codeServerIpcChannels.hide, codeServerInvoke((current, payload) => current.hide(validateSessionPayload(payload, false))))
  ipcMain.handle(codeServerIpcChannels.setBounds, codeServerInvoke((current, payload) => current.setBounds(validateSessionPayload(payload, true))))
  ipcMain.handle(codeServerIpcChannels.close, codeServerInvoke((current, payload) => current.close(validateSessionPayload(payload, false))))
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

  const currentWorkspace = new BrowserWorkspaceManager({
    WebContentsView,
    hostWindow: window,
    appView,
    desktopOrigin,
    apiOrigin,
    onState: (state) => {
      if (!appView.webContents.isDestroyed()) appView.webContents.send(browserIpcChannels.state, state)
    },
    onWorkspaceShortcut: (index) => {
      if (!appView.webContents.isDestroyed()) {
        appView.webContents.send(browserIpcChannels.workspaceShortcut, index)
      }
    },
  })
  const currentCodeServerWorkspace = new CodeServerWorkspaceManager({
    app,
    WebContentsView,
    hostWindow: window,
    appView,
    electronSession: electronSession.fromPartition(CODE_SERVER_PARTITION),
    protectedOrigins: [desktopOrigin, apiOrigin],
    openExternal,
    onBeforeAttach: () => currentWorkspace.detachActiveView(),
    onServiceOrigin: (origin) => currentWorkspace.addProtectedOrigin(origin),
    onState: (state) => {
      if (!appView.webContents.isDestroyed()) appView.webContents.send(codeServerIpcChannels.state, state)
    },
    onWorkspaceShortcut: (index) => {
      if (!appView.webContents.isDestroyed()) {
        appView.webContents.send(codeServerIpcChannels.workspaceShortcut, index)
      }
    },
  })
  workspace = currentWorkspace
  codeServerWorkspace = currentCodeServerWorkspace
  currentWorkspace.resize()
  currentCodeServerWorkspace.resize()
  window.on('resize', () => {
    currentWorkspace.resize()
    currentCodeServerWorkspace.resize()
  })

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
    if (details.isMainFrame && !details.isSameDocument) {
      currentWorkspace.detachActiveView()
      currentCodeServerWorkspace.detachActiveView()
    }
  })
  appView.webContents.on('render-process-gone', () => {
    currentWorkspace.detachActiveView()
    currentCodeServerWorkspace.detachActiveView()
  })

  const currentProvider = new BrowserProviderServer({ app, workspace: currentWorkspace })
  provider = currentProvider
  try {
    const providerConfig = await currentProvider.start()
    currentCodeServerWorkspace.addProtectedOrigin(`http://127.0.0.1:${providerConfig.port}`)
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
      currentCodeServerWorkspace.dispose(),
      currentProvider.stop(),
    ]).catch((error) => {
      console.error('Could not clean up desktop workspaces:', error)
    }).finally(() => {
      if (!appView.webContents.isDestroyed()) appView.webContents.close({ waitForBeforeUnload: false })
      if (workspace === currentWorkspace) workspace = null
      if (codeServerWorkspace === currentCodeServerWorkspace) codeServerWorkspace = null
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
    const activeCodeServerWorkspace = codeServerWorkspace
    void Promise.all([
      cleanupPromise,
      activeProvider?.stop(),
      activeWorkspace?.dispose(),
      activeCodeServerWorkspace?.dispose(),
    ]).finally(() => app.quit())
  })

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit()
  })
}
