'use strict'

const assert = require('node:assert/strict')
const { EventEmitter } = require('node:events')
const fs = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { PassThrough } = require('node:stream')
const test = require('node:test')
const {
  CODE_SERVER_COOKIE_NAME,
  CODE_SERVER_PARTITION,
  CodeServerWorkspaceManager,
  codeServerArguments,
  codeServerCommand,
  codeServerPaths,
  codeServerWorkspaceUrl,
  isBlockedCodeServerRequest,
  parseCodeServerListeningUrl,
} = require('./code-server-manager.cjs')

function fakeElectronSession() {
  const session = new EventEmitter()
  session.fetchCalls = []
  session.permissionCheckHandler = undefined
  session.permissionRequestHandler = undefined
  session.webRequest = {
    listener: undefined,
    onBeforeRequest(_filter, listener) {
      this.listener = listener
    },
  }
  session.setPermissionCheckHandler = (handler) => { session.permissionCheckHandler = handler }
  session.setPermissionRequestHandler = (handler) => { session.permissionRequestHandler = handler }
  session.cookies = {
    async remove() {},
    async get() { return [{ name: CODE_SERVER_COOKIE_NAME, value: 'private' }] },
  }
  session.fetch = async (url, options) => {
    session.fetchCalls.push({ url, options })
    return { status: 200 }
  }
  return session
}

class FakeChild extends EventEmitter {
  constructor() {
    super()
    this.stdout = new PassThrough()
    this.stderr = new PassThrough()
    this.exitCode = null
    this.kills = []
  }

  kill(signal) {
    this.kills.push(signal)
    if (this.exitCode === null) {
      this.exitCode = 0
      queueMicrotask(() => this.emit('exit', 0, signal))
    }
    return true
  }
}

function fakeViewClass(electronSession, createdViews) {
  return class FakeWebContentsView {
    constructor(options) {
      this.options = options
      this.bounds = null
      const contents = new EventEmitter()
      contents.destroyed = false
      contents.focused = false
      contents.session = electronSession
      contents.isDestroyed = () => contents.destroyed
      contents.isFocused = () => contents.focused
      contents.focus = () => { contents.focused = true }
      contents.close = () => {
        if (contents.destroyed) return
        contents.destroyed = true
        contents.emit('destroyed')
      }
      contents.setWindowOpenHandler = (handler) => { contents.windowOpenHandler = handler }
      contents.loadURL = (url) => {
        contents.loadedURL = url
        contents.emit('did-start-loading')
        return Promise.resolve().then(() => contents.emit('did-finish-load'))
      }
      this.webContents = contents
      createdViews.push(this)
    }

    setBounds(bounds) {
      this.bounds = bounds
    }
  }
}

function fakeHostWindow(appView) {
  const children = [appView]
  return {
    contentView: {
      children,
      addChildView(view) {
        if (!children.includes(view)) children.push(view)
      },
      removeChildView(view) {
        const index = children.indexOf(view)
        if (index >= 0) children.splice(index, 1)
      },
    },
    getContentSize: () => [1200, 800],
  }
}

test('builds a private dynamic code-server launch and folder URL', () => {
  const paths = codeServerPaths({ getPath: () => '/tmp/kiwi code profile' }, 'nonce')
  assert.equal(paths.config, '/tmp/kiwi code profile/code-server/config.yaml')
  assert.deepEqual(codeServerArguments(paths).slice(0, 4), [
    '--bind-addr', '127.0.0.1:0', '--auth', 'password',
  ])
  assert.ok(codeServerArguments(paths).includes('--disable-telemetry'))
  assert.ok(codeServerArguments(paths).includes('--disable-update-check'))
  assert.equal(codeServerCommand({ KIWI_CODE_CODE_SERVER_BIN: ' /custom/code-server ' }), '/custom/code-server')
  assert.equal(codeServerCommand({}), 'code-server')

  assert.equal(
    parseCodeServerListeningUrl('[info] HTTP server listening on http://127.0.0.1:43127/'),
    'http://127.0.0.1:43127/',
  )
  assert.equal(parseCodeServerListeningUrl('HTTP server listening on http://0.0.0.0:43127/'), null)
  assert.equal(parseCodeServerListeningUrl('HTTP server listening on http://127.0.0.1:0/'), null)
  assert.equal(parseCodeServerListeningUrl('HTTP server listening on http://127.0.0.1:4000/'), null)
  const workspaceUrl = new URL(codeServerWorkspaceUrl('http://127.0.0.1:43127/', '/tmp/a folder'))
  assert.equal(workspaceUrl.origin, 'http://127.0.0.1:43127')
  assert.equal(workspaceUrl.searchParams.get('folder'), '/tmp/a folder')
})

test('blocks host files and Kiwi Code control origins from the Code partition', () => {
  const protectedOrigins = new Set(['http://127.0.0.1:4000'])
  assert.equal(isBlockedCodeServerRequest('file:///Users/example/.ssh/id_ed25519', protectedOrigins), true)
  assert.equal(isBlockedCodeServerRequest('http://localhost:4000/api/projects', protectedOrigins), true)
  assert.equal(isBlockedCodeServerRequest('http://127.0.0.1:43127/', protectedOrigins), false)
  assert.equal(isBlockedCodeServerRequest('https://open-vsx.org/', protectedOrigins), false)
  assert.equal(isBlockedCodeServerRequest('blob:http://127.0.0.1:43127/id', protectedOrigins), false)
})

test('starts, authenticates, embeds, reuses, and disposes code-server', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-code-manager-'))
  const electronSession = fakeElectronSession()
  const appView = {
    webContents: {
      focused: false,
      isDestroyed: () => false,
      focus() { this.focused = true },
    },
  }
  const hostWindow = fakeHostWindow(appView)
  const createdViews = []
  const children = []
  const spawnCalls = []
  const states = []
  const serviceOrigins = []
  const manager = new CodeServerWorkspaceManager({
    app: {
      getPath(name) {
        if (name === 'userData') return directory
        if (name === 'home') return directory
        throw new Error(`unexpected path ${name}`)
      },
    },
    WebContentsView: fakeViewClass(electronSession, createdViews),
    hostWindow,
    appView,
    electronSession,
    protectedOrigins: ['http://127.0.0.1:4000'],
    environment: { PATH: '/test', KIWI_CODE_CODE_SERVER_BIN: '/test/code-server' },
    spawn(command, args, options) {
      const child = new FakeChild()
      children.push(child)
      spawnCalls.push({ command, args, options })
      queueMicrotask(() => child.stderr.write('[info] HTTP server listening on http://127.0.0.1:43127/\n'))
      return child
    },
    onState: (state) => states.push(state),
    onServiceOrigin: (origin) => serviceOrigins.push(origin),
  })

  try {
    const first = await manager.show({
      projectId: 'project',
      threadId: 'thread',
      workspacePath: directory,
      bounds: { x: 10, y: 20, width: 900, height: 700 },
    })
    await new Promise((resolve) => setImmediate(resolve))

    assert.equal(first.visible, true)
    assert.equal(spawnCalls.length, 1)
    assert.equal(spawnCalls[0].command, '/test/code-server')
    assert.equal(spawnCalls[0].options.env.PASSWORD.length, 64)
    assert.equal(spawnCalls[0].options.cwd, directory)
    assert.deepEqual(serviceOrigins, ['http://127.0.0.1:43127/'])
    assert.equal(electronSession.fetchCalls.length, 1)
    assert.equal(electronSession.fetchCalls[0].options.redirect, 'follow')
    assert.equal(electronSession.fetchCalls[0].options.credentials, 'include')
    assert.match(electronSession.fetchCalls[0].options.body, /password=/)
    assert.equal(createdViews.length, 1)
    assert.equal(createdViews[0].options.webPreferences.partition, CODE_SERVER_PARTITION)
    assert.equal(createdViews[0].options.webPreferences.contextIsolation, true)
    assert.equal(createdViews[0].options.webPreferences.nodeIntegration, false)
    assert.equal(createdViews[0].options.webPreferences.sandbox, true)
    assert.equal(new URL(createdViews[0].webContents.loadedURL).searchParams.get('folder'), directory)
    assert.deepEqual(createdViews[0].bounds, { x: 10, y: 20, width: 900, height: 700 })
    assert.ok(hostWindow.contentView.children.includes(createdViews[0]))
    assert.equal(manager.stateFor('project', 'thread').status, 'ready')
    assert.equal(electronSession.permissionCheckHandler(), false)

    manager.hide({ projectId: 'project', threadId: 'thread' })
    assert.ok(!hostWindow.contentView.children.includes(createdViews[0]))
    const second = await manager.show({
      projectId: 'project',
      threadId: 'thread',
      workspacePath: directory,
      bounds: { x: 0, y: 0, width: 500, height: 400 },
    })
    assert.equal(second.visible, true)
    assert.equal(spawnCalls.length, 1)
    assert.equal(createdViews.length, 1)

    manager.close({ projectId: 'project', threadId: 'thread' })
    assert.equal(createdViews[0].webContents.isDestroyed(), true)
    assert.equal(manager.sessions.size, 0)
    assert.ok(states.some((state) => state.status === 'starting'))
    assert.ok(states.some((state) => state.status === 'ready'))

    assert.equal((await fs.stat(manager.paths.config)).mode & 0o777, 0o600)

    const extensionDirectories = await fs.readdir(manager.paths.extensions)
    const themeDirectory = extensionDirectories.find((name) => name.startsWith('kiwi-code.kiwi-code-theme-'))
    assert.ok(themeDirectory, 'the Kiwi Code theme extension is installed')
    const installedTheme = JSON.parse(await fs.readFile(
      path.join(manager.paths.extensions, themeDirectory, 'themes', 'kiwi-code-color-theme.json'),
      'utf8',
    ))
    assert.equal(installedTheme.name, 'Kiwi Code')
    const seededSettings = JSON.parse(await fs.readFile(
      path.join(manager.paths.userData, 'User', 'settings.json'),
      'utf8',
    ))
    assert.equal(seededSettings['workbench.colorTheme'], 'Kiwi Code')
  } finally {
    await manager.dispose()
    assert.ok(children[0].kills.includes('SIGTERM'))
    await fs.rm(directory, { recursive: true, force: true })
  }
})

test('preparePaths keeps existing code-server user settings intact', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-code-settings-'))
  const electronSession = fakeElectronSession()
  const appView = { webContents: { isDestroyed: () => false, focus() {} } }
  const manager = new CodeServerWorkspaceManager({
    app: { getPath: () => directory },
    WebContentsView: fakeViewClass(electronSession, []),
    hostWindow: fakeHostWindow(appView),
    appView,
    electronSession,
    environment: {},
    spawn() { throw new Error('not spawned') },
  })
  const settingsPath = path.join(manager.paths.userData, 'User', 'settings.json')
  try {
    await manager.preparePaths()
    assert.equal(
      JSON.parse(await fs.readFile(settingsPath, 'utf8'))['workbench.colorTheme'],
      'Kiwi Code',
    )
    const custom = '{\n  "workbench.colorTheme": "Custom"\n}\n'
    await fs.writeFile(settingsPath, custom)
    await manager.preparePaths()
    assert.equal(await fs.readFile(settingsPath, 'utf8'), custom)
  } finally {
    await manager.dispose()
    await fs.rm(directory, { recursive: true, force: true })
  }
})

test('reports a missing code-server executable without attaching a guest', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-code-missing-'))
  const electronSession = fakeElectronSession()
  const appView = { webContents: { isDestroyed: () => false, focus() {} } }
  const hostWindow = fakeHostWindow(appView)
  const manager = new CodeServerWorkspaceManager({
    app: { getPath: () => directory },
    WebContentsView: fakeViewClass(electronSession, []),
    hostWindow,
    appView,
    electronSession,
    environment: {},
    spawn() {
      const child = new FakeChild()
      queueMicrotask(() => {
        const error = new Error('spawn code-server ENOENT')
        error.code = 'ENOENT'
        child.emit('error', error)
      })
      return child
    },
    startTimeoutMs: 100,
  })
  try {
    const state = await manager.show({
      projectId: 'project',
      threadId: 'thread',
      workspacePath: directory,
      bounds: { x: 0, y: 0, width: 500, height: 400 },
    })
    assert.equal(state.visible, false)
    assert.equal(state.status, 'error')
    assert.equal(state.error, 'code-server is not installed or not on PATH.')
    assert.equal(manager.sessions.size, 0)
  } finally {
    await manager.dispose()
    await fs.rm(directory, { recursive: true, force: true })
  }
})
