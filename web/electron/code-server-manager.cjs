'use strict'

const { spawn: spawnProcess } = require('node:child_process')
const { randomBytes } = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')
const {
  clampBounds,
  isProtectedUrl,
  navigationOrigin,
  requireLoopbackHttpUrl,
  validateBounds,
} = require('./browser-helpers.cjs')

const CODE_SERVER_PARTITION = 'dire-mux-code-server'
const CODE_SERVER_COOKIE_NAME = 'code-server-session-dire-mux'
const DEFAULT_START_TIMEOUT_MS = 30_000
const DEFAULT_STOP_TIMEOUT_MS = 3_000
const MAX_STARTUP_OUTPUT_BYTES = 16 * 1024
const MAX_WORKSPACE_PATH_BYTES = 32 * 1024

class CodeServerManagerError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined)
    this.name = 'CodeServerManagerError'
    this.code = code
  }
}

function codeServerPaths(app, nonce = randomBytes(6).toString('hex')) {
  const root = path.join(app.getPath('userData'), 'code-server')
  return {
    root,
    config: path.join(root, 'config.yaml'),
    extensions: path.join(root, 'extensions'),
    runtimeSocket: path.join(root, `session-${process.pid}-${nonce}.sock`),
    userData: path.join(root, 'data'),
  }
}

function codeServerArguments(paths) {
  return [
    '--bind-addr', '127.0.0.1:0',
    '--auth', 'password',
    '--config', paths.config,
    '--user-data-dir', paths.userData,
    '--extensions-dir', paths.extensions,
    '--session-socket', paths.runtimeSocket,
    '--cookie-suffix', 'dire-mux',
    '--app-name', 'Dire Mux Code',
    '--idle-timeout-seconds', '600',
    '--disable-telemetry',
    '--disable-update-check',
  ]
}

function codeServerCommand(environment = process.env) {
  const configured = environment.DIRE_MUX_CODE_SERVER_BIN?.trim()
  return configured || 'code-server'
}

function parseCodeServerListeningUrl(output) {
  if (typeof output !== 'string') return null
  const match = /HTTP server listening on (http:\/\/127\.0\.0\.1:(\d+)(?:\/[^\s]*)?)/i.exec(output)
  if (!match || Number(match[2]) < 1 || Number(match[2]) > 65_535 || Number(match[2]) === 4_000) return null
  try {
    const url = requireLoopbackHttpUrl(match[1], 'code-server URL')
    url.pathname = '/'
    url.search = ''
    url.hash = ''
    return url.toString()
  } catch {
    return null
  }
}

function codeServerWorkspaceUrl(origin, workspacePath) {
  const url = requireLoopbackHttpUrl(origin, 'code-server URL')
  url.pathname = '/'
  url.search = ''
  url.hash = ''
  url.searchParams.set('folder', workspacePath)
  return url.toString()
}

function isBlockedCodeServerRequest(raw, protectedOrigins) {
  try {
    const url = new URL(raw)
    if (['file:', 'javascript:', 'vbscript:'].includes(url.protocol)) return true
    if (['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) {
      return isProtectedUrl(url, protectedOrigins)
    }
    return false
  } catch {
    return true
  }
}

function boundedOutput(current, chunk) {
  const next = current + String(chunk)
  return Buffer.byteLength(next) <= MAX_STARTUP_OUTPUT_BYTES
    ? next
    : Buffer.from(next).subarray(-MAX_STARTUP_OUTPUT_BYTES).toString('utf8')
}

function isMissingExecutableError(error) {
  return error?.code === 'ENOENT' || /(?:spawn|executable).*code-server.*(?:ENOENT|not found)/i.test(error?.message || '')
}

class CodeServerWorkspaceManager {
  constructor({
    app,
    WebContentsView,
    hostWindow,
    appView,
    electronSession,
    protectedOrigins = [],
    onState = () => {},
    onWorkspaceShortcut = () => {},
    onBeforeAttach = () => {},
    onServiceOrigin = () => {},
    openExternal = () => {},
    environment = process.env,
    spawn = spawnProcess,
    fileSystem = fs.promises,
    logger = console,
    startTimeoutMs = DEFAULT_START_TIMEOUT_MS,
    stopTimeoutMs = DEFAULT_STOP_TIMEOUT_MS,
  }) {
    this.app = app
    this.WebContentsView = WebContentsView
    this.hostWindow = hostWindow
    this.appView = appView
    this.electronSession = electronSession
    this.protectedOrigins = new Set(protectedOrigins.filter(Boolean))
    this.onState = onState
    this.onWorkspaceShortcut = onWorkspaceShortcut
    this.onBeforeAttach = onBeforeAttach
    this.onServiceOrigin = onServiceOrigin
    this.openExternal = openExternal
    this.environment = environment
    this.spawn = spawn
    this.fileSystem = fileSystem
    this.logger = logger
    this.startTimeoutMs = startTimeoutMs
    this.stopTimeoutMs = stopTimeoutMs
    this.paths = codeServerPaths(app)
    this.sessions = new Map()
    this.service = null
    this.serviceStartPromise = null
    this.startingChild = null
    this.expectedExits = new WeakSet()
    this.activeKey = null
    this.activeProjectId = null
    this.activeThreadId = null
    this.activeBounds = null
    this.activeStatus = 'idle'
    this.activeError = ''
    this.attachedView = null
    this.activationGeneration = 0
    this.disposed = false
    this.disposePromise = null
    this.partitionConfigured = false
    this.willDownloadHandler = (event) => event.preventDefault()
    this.configurePartition()
  }

  key(projectId, threadId) {
    return `${projectId}\u0000${threadId}`
  }

  addProtectedOrigin(origin) {
    if (origin) this.protectedOrigins.add(origin)
  }

  configurePartition() {
    if (this.partitionConfigured) return
    this.partitionConfigured = true
    this.electronSession.setPermissionCheckHandler(() => false)
    this.electronSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false))
    this.electronSession.on('will-download', this.willDownloadHandler)
    this.electronSession.webRequest.onBeforeRequest({ urls: ['<all_urls>'] }, (details, callback) => {
      callback({ cancel: isBlockedCodeServerRequest(details.url, this.protectedOrigins) })
    })
  }

  async preparePaths() {
    await Promise.all([
      this.fileSystem.mkdir(this.paths.root, { recursive: true, mode: 0o700 }),
      this.fileSystem.mkdir(this.paths.userData, { recursive: true, mode: 0o700 }),
      this.fileSystem.mkdir(this.paths.extensions, { recursive: true, mode: 0o700 }),
    ])
    const config = [
      'bind-addr: 127.0.0.1:0',
      'auth: password',
      'cert: false',
      'disable-telemetry: true',
      'disable-update-check: true',
      '',
    ].join('\n')
    const temporaryConfig = `${this.paths.config}.${process.pid}.${randomBytes(6).toString('hex')}.tmp`
    try {
      await this.fileSystem.writeFile(temporaryConfig, config, { flag: 'wx', mode: 0o600 })
      await this.fileSystem.rename(temporaryConfig, this.paths.config)
    } finally {
      await this.fileSystem.unlink(temporaryConfig).catch(() => {})
    }
    await this.fileSystem.chmod(this.paths.config, 0o600)
    await this.fileSystem.unlink(this.paths.runtimeSocket).catch((error) => {
      if (error?.code !== 'ENOENT') throw error
    })
  }

  async authenticate(origin, password) {
    await this.electronSession.cookies.remove(origin, CODE_SERVER_COOKIE_NAME).catch(() => {})
    const body = new URLSearchParams({
      base: '/',
      href: new URL('/login/', origin).toString(),
      password,
    }).toString()
    const response = await this.electronSession.fetch(new URL('/login/', origin).toString(), {
      method: 'POST',
      credentials: 'include',
      redirect: 'follow',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body,
    })
    const cookies = await this.electronSession.cookies.get({ url: origin, name: CODE_SERVER_COOKIE_NAME })
    if (!cookies.some((cookie) => cookie.name === CODE_SERVER_COOKIE_NAME)) {
      throw new CodeServerManagerError(
        'authentication_failed',
        `code-server rejected the private desktop login (HTTP ${response.status}).`,
      )
    }
  }

  startService() {
    if (this.service) return Promise.resolve(this.service)
    if (this.serviceStartPromise) return this.serviceStartPromise
    const pending = this.launchService()
    this.serviceStartPromise = pending
    void pending.finally(() => {
      if (this.serviceStartPromise === pending) this.serviceStartPromise = null
    }).catch(() => {})
    return pending
  }

  async launchService() {
    if (this.disposed) throw new CodeServerManagerError('disposed', 'The desktop Code workspace is shutting down.')
    await this.preparePaths()
    const password = randomBytes(32).toString('hex')
    const command = codeServerCommand(this.environment)
    const args = codeServerArguments(this.paths)
    let child
    try {
      child = this.spawn(command, args, {
        cwd: this.app.getPath('home'),
        env: { ...this.environment, PASSWORD: password },
        stdio: ['ignore', 'pipe', 'pipe'],
        windowsHide: true,
      })
    } catch (error) {
      if (isMissingExecutableError(error)) {
        throw new CodeServerManagerError('not_installed', 'code-server is not installed or not on PATH.', error)
      }
      throw new CodeServerManagerError('start_failed', 'Could not start code-server.', error)
    }
    this.startingChild = child

    return new Promise((resolve, reject) => {
      let startupOutput = ''
      let settled = false
      let addressDetected = false
      let timer

      const stopListening = () => {
        clearTimeout(timer)
        // Keep consuming the piped streams after startup so a chatty extension
        // cannot fill code-server's stdout/stderr buffers and stall the editor.
      }
      const fail = (error) => {
        if (settled) return
        settled = true
        stopListening()
        if (this.startingChild === child) this.startingChild = null
        this.expectedExits.add(child)
        try { child.kill('SIGTERM') } catch { /* The process may already be gone. */ }
        reject(error)
      }
      const serviceExited = (code, signal) => {
        if (!settled) {
          const suffix = signal ? ` with signal ${signal}` : ` with status ${code ?? 'unknown'}`
          const error = new CodeServerManagerError(
            'start_failed',
            `code-server exited before it was ready${suffix}.`,
          )
          if (startupOutput.trim()) this.logger.error('code-server startup output:', startupOutput.trim())
          fail(error)
          return
        }
        if (!this.expectedExits.has(child)) this.handleServiceExit(child, code, signal)
      }
      const ready = async (origin) => {
        try {
          await this.authenticate(origin, password)
          if (settled) return
          if (this.disposed) {
            fail(new CodeServerManagerError('disposed', 'The desktop Code workspace is shutting down.'))
            return
          }
          settled = true
          stopListening()
          const service = { child, origin }
          this.service = service
          if (this.startingChild === child) this.startingChild = null
          this.onServiceOrigin(origin)
          resolve(service)
        } catch (error) {
          if (!(error instanceof CodeServerManagerError)) {
            this.logger.error('Could not authenticate the desktop Code workspace:', error)
          }
          fail(error instanceof CodeServerManagerError
            ? error
            : new CodeServerManagerError('authentication_failed', 'Could not authenticate the desktop Code workspace.', error))
        }
      }
      const consumeOutput = (chunk) => {
        startupOutput = boundedOutput(startupOutput, chunk)
        if (addressDetected) return
        const origin = parseCodeServerListeningUrl(startupOutput)
        if (!origin) return
        addressDetected = true
        void ready(origin)
      }

      child.stdout?.on('data', consumeOutput)
      child.stderr?.on('data', consumeOutput)
      child.once('error', (error) => {
        if (isMissingExecutableError(error)) {
          fail(new CodeServerManagerError('not_installed', 'code-server is not installed or not on PATH.', error))
        } else {
          fail(new CodeServerManagerError('start_failed', 'Could not start code-server.', error))
        }
      })
      child.once('exit', serviceExited)
      timer = setTimeout(() => {
        fail(new CodeServerManagerError('start_timeout', 'code-server did not become ready within 30 seconds.'))
      }, this.startTimeoutMs)
    })
  }

  handleServiceExit(child, code, signal) {
    if (this.service?.child !== child || this.disposed) return
    this.service = null
    const suffix = signal ? ` (${signal})` : Number.isInteger(code) ? ` (status ${code})` : ''
    const identities = [...this.sessions.values()].map((session) => ({
      projectId: session.projectId,
      threadId: session.threadId,
    }))
    for (const session of [...this.sessions.values()]) this.destroySession(session)
    this.activeStatus = 'error'
    this.activeError = `code-server stopped unexpectedly${suffix}.`
    for (const identity of identities) {
      this.onState({
        ...identity,
        visible: false,
        status: 'error',
        error: this.activeError,
      })
    }
  }

  async resolveWorkspacePath(workspacePath) {
    if (
      typeof workspacePath !== 'string' ||
      !path.isAbsolute(workspacePath) ||
      Buffer.byteLength(workspacePath) > MAX_WORKSPACE_PATH_BYTES
    ) {
      throw new CodeServerManagerError('invalid_workspace', 'The Code workspace requires a valid absolute folder path.')
    }
    try {
      const resolved = await this.fileSystem.realpath(workspacePath)
      const stat = await this.fileSystem.stat(resolved)
      if (!stat.isDirectory()) throw new Error('not a directory')
      return resolved
    } catch (error) {
      throw new CodeServerManagerError('workspace_unavailable', 'The thread workspace folder is no longer available.', error)
    }
  }

  createSession({ key, projectId, threadId, workspacePath, service }) {
    const view = new this.WebContentsView({
      webPreferences: {
        backgroundThrottling: false,
        contextIsolation: true,
        disableDialogs: true,
        nodeIntegration: false,
        partition: CODE_SERVER_PARTITION,
        sandbox: true,
      },
    })
    const session = {
      key,
      projectId,
      threadId,
      workspacePath,
      serviceOrigin: service.origin,
      status: 'loading',
      error: '',
      view,
      destroyed: false,
      loadPromise: null,
    }
    this.sessions.set(key, session)

    const update = (status, error = '') => {
      if (session.destroyed) return
      session.status = status
      session.error = error
      if (this.activeKey === key) this.emitState(projectId, threadId)
    }
    const blockNavigation = (details) => {
      if (navigationOrigin(details.url) === service.origin) return
      details.preventDefault()
      this.openExternal(details.url)
    }
    view.webContents.on('will-navigate', blockNavigation)
    view.webContents.on('will-redirect', blockNavigation)
    view.webContents.setWindowOpenHandler(({ url }) => {
      this.openExternal(url)
      return { action: 'deny' }
    })
    view.webContents.on('before-input-event', (event, input) => {
      const index = Number(input.key)
      if (
        input.type === 'keyDown' &&
        (input.control || input.meta) &&
        !input.alt &&
        !input.shift &&
        Number.isInteger(index) &&
        index >= 1 &&
        index <= 7
      ) {
        event.preventDefault()
        this.onWorkspaceShortcut(index)
      }
    })
    view.webContents.on('did-start-loading', () => update('loading'))
    view.webContents.on('did-finish-load', () => update('ready'))
    view.webContents.on('did-fail-load', (_event, errorCode, errorDescription, _url, isMainFrame) => {
      if (isMainFrame === false || errorCode === -3) return
      update('error', errorDescription || 'The Code workspace could not be loaded.')
    })
    view.webContents.on('render-process-gone', () => {
      update('error', 'The Code workspace renderer stopped unexpectedly.')
      if (this.attachedView === view) this.suspendView(false)
    })
    view.webContents.on('destroyed', () => {
      if (session.destroyed) return
      session.destroyed = true
      this.sessions.delete(key)
      if (this.attachedView === view) this.attachedView = null
      if (this.activeKey === key) {
        this.activeStatus = 'error'
        this.activeError = 'The Code workspace renderer was closed.'
        this.emitState(projectId, threadId)
      }
    })

    const url = codeServerWorkspaceUrl(service.origin, workspacePath)
    try {
      session.loadPromise = Promise.resolve(view.webContents.loadURL(url)).catch((error) => {
        update('error', error?.message || 'The Code workspace could not be loaded.')
      })
    } catch (error) {
      update('error', error?.message || 'The Code workspace could not be loaded.')
      session.loadPromise = Promise.resolve()
    }
    return session
  }

  stateFor(projectId, threadId) {
    const key = this.key(projectId, threadId)
    const session = this.sessions.get(key)
    const active = this.activeKey === key
    return {
      projectId,
      threadId,
      visible: Boolean(active && session && this.attachedView === session.view),
      status: session?.status ?? (active ? this.activeStatus : 'idle'),
      error: session?.error ?? (active ? this.activeError : ''),
    }
  }

  emitState(projectId = this.activeProjectId, threadId = this.activeThreadId) {
    if (!projectId || !threadId) return
    this.onState(this.stateFor(projectId, threadId))
  }

  attachSession(session) {
    if (session.destroyed || this.disposed) return
    const previous = this.attachedView
    const restoreGuestFocus = Boolean(
      previous &&
      !previous.webContents.isDestroyed() &&
      previous.webContents.isFocused(),
    )
    if (previous && previous !== session.view) this.hostWindow.contentView.removeChildView(previous)
    this.onBeforeAttach()
    this.attachedView = session.view
    if (!this.hostWindow.contentView.children.includes(session.view)) {
      this.hostWindow.contentView.addChildView(session.view)
    }
    if (this.activeBounds) session.view.setBounds(clampBounds(this.activeBounds, this.contentSize()))
    if (restoreGuestFocus && !session.view.webContents.isDestroyed()) session.view.webContents.focus()
  }

  suspendView(restoreAppFocus = true) {
    const previous = this.attachedView
    const shouldRestoreFocus = Boolean(
      restoreAppFocus &&
      previous &&
      !previous.webContents.isDestroyed() &&
      previous.webContents.isFocused(),
    )
    if (previous && this.hostWindow.contentView.children.includes(previous)) {
      this.hostWindow.contentView.removeChildView(previous)
    }
    this.attachedView = null
    if (shouldRestoreFocus && !this.appView.webContents.isDestroyed()) this.appView.webContents.focus()
  }

  detachActiveView() {
    const projectId = this.activeProjectId
    const threadId = this.activeThreadId
    this.activationGeneration += 1
    this.suspendView()
    this.activeKey = null
    this.activeProjectId = null
    this.activeThreadId = null
    this.activeBounds = null
    this.activeStatus = 'idle'
    this.activeError = ''
    if (projectId && threadId) this.emitState(projectId, threadId)
  }

  async show(payload) {
    if (this.disposed) {
      return {
        projectId: payload.projectId,
        threadId: payload.threadId,
        visible: false,
        status: 'error',
        error: 'The desktop Code workspace is shutting down.',
      }
    }
    const key = this.key(payload.projectId, payload.threadId)
    const bounds = validateBounds(payload.bounds)
    const generation = ++this.activationGeneration
    this.activeKey = key
    this.activeProjectId = payload.projectId
    this.activeThreadId = payload.threadId
    this.activeBounds = bounds
    this.activeStatus = 'starting'
    this.activeError = ''
    this.emitState()

    try {
      const [workspacePath, service] = await Promise.all([
        this.resolveWorkspacePath(payload.workspacePath),
        this.startService(),
      ])
      if (this.disposed || this.activationGeneration !== generation || this.activeKey !== key) {
        return this.stateFor(payload.projectId, payload.threadId)
      }
      let session = this.sessions.get(key)
      if (
        session &&
        (session.workspacePath !== workspacePath || session.serviceOrigin !== service.origin || session.destroyed)
      ) {
        this.destroySession(session)
        session = null
      }
      if (!session) {
        session = this.createSession({
          key,
          projectId: payload.projectId,
          threadId: payload.threadId,
          workspacePath,
          service,
        })
      }
      this.attachSession(session)
      this.emitState()
      return this.stateFor(payload.projectId, payload.threadId)
    } catch (error) {
      if (this.activationGeneration === generation && this.activeKey === key) {
        this.activeStatus = 'error'
        this.activeError = error instanceof CodeServerManagerError
          ? error.message
          : 'Could not open the desktop Code workspace.'
        this.suspendView()
        this.emitState()
      }
      if (!(error instanceof CodeServerManagerError)) this.logger.error('Could not open code-server:', error)
      return this.stateFor(payload.projectId, payload.threadId)
    }
  }

  hide(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    if (this.activeKey === key) this.detachActiveView()
    return this.stateFor(payload.projectId, payload.threadId)
  }

  setBounds(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    const bounds = validateBounds(payload.bounds)
    if (this.activeKey === key) {
      this.activeBounds = bounds
      if (this.attachedView) this.attachedView.setBounds(clampBounds(bounds, this.contentSize()))
    }
    return this.stateFor(payload.projectId, payload.threadId)
  }

  close(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    if (this.activeKey === key) this.detachActiveView()
    const session = this.sessions.get(key)
    if (session) this.destroySession(session)
    return this.stateFor(payload.projectId, payload.threadId)
  }

  destroySession(session) {
    if (!session || session.destroyed) return
    session.destroyed = true
    if (this.attachedView === session.view) this.suspendView(false)
    if (this.hostWindow.contentView.children.includes(session.view)) {
      this.hostWindow.contentView.removeChildView(session.view)
    }
    this.sessions.delete(session.key)
    if (!session.view.webContents.isDestroyed()) {
      session.view.webContents.close({ waitForBeforeUnload: false })
    }
  }

  contentSize() {
    const [width, height] = this.hostWindow.getContentSize()
    return { width, height }
  }

  resize() {
    if (this.attachedView && this.activeBounds) {
      this.attachedView.setBounds(clampBounds(this.activeBounds, this.contentSize()))
    }
  }

  async stopChild(child) {
    if (!child || child.exitCode !== null && child.exitCode !== undefined) return
    this.expectedExits.add(child)
    await new Promise((resolve) => {
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        child.removeListener('exit', finish)
        resolve()
      }
      child.once('exit', finish)
      const timer = setTimeout(() => {
        try { child.kill('SIGKILL') } catch { /* The process may already be gone. */ }
        finish()
      }, this.stopTimeoutMs)
      try {
        if (!child.kill('SIGTERM')) finish()
      } catch {
        finish()
      }
    })
  }

  dispose() {
    if (this.disposePromise) return this.disposePromise
    this.disposed = true
    this.activationGeneration += 1
    this.disposePromise = (async () => {
      this.suspendView(false)
      for (const session of [...this.sessions.values()]) this.destroySession(session)
      const children = [...new Set([this.service?.child, this.startingChild].filter(Boolean))]
      await Promise.all(children.map((child) => this.stopChild(child)))
      await this.serviceStartPromise?.catch(() => {})
      this.service = null
      this.startingChild = null
      this.electronSession.removeListener('will-download', this.willDownloadHandler)
      this.electronSession.webRequest.onBeforeRequest(null)
      this.electronSession.setPermissionCheckHandler(null)
      this.electronSession.setPermissionRequestHandler(null)
      await this.fileSystem.unlink(this.paths.runtimeSocket).catch(() => {})
    })()
    return this.disposePromise
  }
}

module.exports = {
  CODE_SERVER_COOKIE_NAME,
  CODE_SERVER_PARTITION,
  CodeServerManagerError,
  CodeServerWorkspaceManager,
  codeServerArguments,
  codeServerCommand,
  codeServerPaths,
  codeServerWorkspaceUrl,
  isBlockedCodeServerRequest,
  parseCodeServerListeningUrl,
}
