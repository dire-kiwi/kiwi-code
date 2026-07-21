'use strict'

const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const http = require('node:http')
const os = require('node:os')
const path = require('node:path')
const test = require('node:test')
const {
  BrowserProviderError,
  assertAllowedGuestUrl,
  clampBounds,
  formatAccessibilityTree,
  normalizeNavigationUrl,
  parseKeyChord,
  requireLoopbackHttpUrl,
  validateActionBody,
  validateBounds,
} = require('./browser-helpers.cjs')
const {
  BrowserProviderServer,
  atomicWriteConfig,
  configPathFor,
  removeMatchingConfig,
} = require('./browser-provider.cjs')
const { BrowserSession, BrowserWorkspaceManager, isProtectedRequest } = require('./browser-sessions.cjs')

function request(port, { token, body = '{}', contentType = 'application/json', method = 'POST', pathname = '/v1/action' }) {
  return new Promise((resolve, reject) => {
    const request = http.request({
      host: '127.0.0.1',
      port,
      method,
      path: pathname,
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': contentType,
      },
    }, (response) => {
      const chunks = []
      response.on('data', (chunk) => chunks.push(chunk))
      response.on('end', () => resolve({ status: response.statusCode, body: JSON.parse(Buffer.concat(chunks)) }))
    })
    request.on('error', reject)
    request.end(body)
  })
}

test('normalizes common URLs and blocks script or protected navigation', () => {
  assert.equal(normalizeNavigationUrl('example.com/docs'), 'https://example.com/docs')
  assert.equal(normalizeNavigationUrl('localhost:3000/app'), 'http://localhost:3000/app')
  assert.equal(normalizeNavigationUrl('example.com:8443/app'), 'https://example.com:8443/app')
  assert.equal(normalizeNavigationUrl('//example.org/a'), 'https://example.org/a')
  assert.equal(normalizeNavigationUrl('about:blank'), 'about:blank')
  assert.throws(() => normalizeNavigationUrl('javascript:alert(1)'), /blocked/)
  assert.throws(() => assertAllowedGuestUrl('file:///tmp/private', new Set()), /Only HTTP/)
  assert.throws(
    () => assertAllowedGuestUrl('http://127.0.0.1:4000/api/projects', new Set(['http://127.0.0.1:4000'])),
    /protected/,
  )
  assert.throws(
    () => assertAllowedGuestUrl('http://localhost:4000/api/projects', new Set(['http://127.0.0.1:4000'])),
    /protected/,
  )
  assert.equal(
    assertAllowedGuestUrl('http://example.com/', new Set(['http://127.0.0.1'])),
    'http://example.com/',
  )
})

test('accepts only loopback HTTP URLs for the trusted desktop renderer', () => {
  assert.equal(requireLoopbackHttpUrl('http://127.0.0.1:5173/app').origin, 'http://127.0.0.1:5173')
  assert.equal(requireLoopbackHttpUrl('https://[::1]:8443/').hostname, '[::1]')
  assert.throws(() => requireLoopbackHttpUrl('https://example.com/'), /loopback/)
  assert.throws(() => requireLoopbackHttpUrl('file:///tmp/app.html'), /loopback/)
  assert.throws(() => requireLoopbackHttpUrl('http://user:secret@localhost:4000/'), /loopback/)
})

test('parses named keys and modifier chords for CDP Input', () => {
  assert.deepEqual(parseKeyChord('CTRL+A'), {
    code: 'KeyA', key: 'a', modifiers: 2, windowsVirtualKeyCode: 65,
  })
  assert.deepEqual(parseKeyChord('Enter'), {
    code: 'Enter', key: 'Enter', modifiers: 0, windowsVirtualKeyCode: 13,
  })
  assert.deepEqual(parseKeyChord('Shift+x'), {
    code: 'KeyX', key: 'X', modifiers: 8, text: 'X', windowsVirtualKeyCode: 88,
  })
  assert.throws(() => parseKeyChord('CTRL+NotAKey'), /Unknown key/)
})

test('validates and clamps finite nonnegative DIP bounds', () => {
  assert.deepEqual(validateBounds({ x: 10.4, y: 5.6, width: 100.2, height: 80.8 }), {
    x: 10, y: 6, width: 100, height: 81,
  })
  assert.deepEqual(clampBounds({ x: 90, y: 70, width: 50, height: 50 }, { width: 100, height: 80 }), {
    x: 90, y: 70, width: 10, height: 10,
  })
  assert.throws(() => validateBounds({ x: -1, y: 0, width: 1, height: 1 }), /nonnegative/)
  assert.throws(() => validateBounds({ x: 0, y: 0, width: Infinity, height: 1 }), /finite/)
})

test('strictly validates provider action bodies', () => {
  assert.deepEqual(validateActionBody({ projectId: 'p', threadId: 't', operation: 'tabs.list' }), {
    projectId: 'p', threadId: 't', operation: 'tabs.list', params: {},
  })
  assert.throws(() => validateActionBody({ projectId: 'p', threadId: 't', operation: 'x', extra: true }), /Unknown/)
  assert.throws(() => validateActionBody({ projectId: 'p', threadId: 't', operation: 'x', params: [] }), /params/)
})

test('formats accessibility trees with ephemeral actionable refs', () => {
  const snapshot = formatAccessibilityTree([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: 'Page' }, childIds: ['2'] },
    { nodeId: '2', parentId: '1', role: { value: 'button' }, name: { value: 'Save' }, backendDOMNodeId: 42 },
  ])
  assert.match(snapshot.text, /button "Save" \[ref=e1\]/)
  assert.deepEqual(snapshot.refs.get('e1'), { backendNodeId: 42, name: 'Save', role: 'button' })
})

test('guest request policy blocks host-file schemes and protected origins', () => {
  const protectedOrigins = new Set(['http://127.0.0.1:4000'])
  assert.equal(isProtectedRequest('file:///Users/example/.ssh/id_rsa', protectedOrigins), true)
  assert.equal(isProtectedRequest('http://localhost:4000/api/projects', protectedOrigins), true)
  assert.equal(isProtectedRequest('https://example.com/app.js', protectedOrigins), false)
  assert.equal(isProtectedRequest('data:text/plain,hello', protectedOrigins), false)
})

test('background tab close preserves the selected tab', async () => {
  const removed = []
  const session = new BrowserSession({
    key: 'p\\0t', projectId: 'p', threadId: 't', partition: 'guest',
    WebContentsView: class {},
    hostWindow: { contentView: { removeChildView(view) { removed.push(view) } } },
    appView: { webContents: { isDestroyed: () => false } },
    protectedOrigins: new Set(), onChanged() {}, onPopup() {}, onWorkspaceShortcut() {},
    isViewActive: () => false,
  })
  const makeTab = (id) => ({
    id,
    view: {
      webContents: {
        close() {}, debugger: { isAttached: () => false }, getTitle: () => id,
        getURL: () => 'about:blank', isDestroyed: () => false,
      },
    },
  })
  session.tabs.set('one', makeTab('one'))
  session.tabs.set('two', makeTab('two'))
  session.currentTargetId = 'one'
  await session.tabsOperation('tabs.close', { targetId: 'two' })
  assert.equal(session.currentTargetId, 'one')
  assert.equal(session.tabs.has('two'), false)
  assert.equal(removed.length, 1)
})

test('no-session status and preview preserve the provider compatibility contract', async () => {
  const manager = new BrowserWorkspaceManager({
    WebContentsView: class {},
    hostWindow: { getContentSize: () => [1440, 900], contentView: { addChildView() {}, removeChildView() {} } },
    appView: { setBounds() {} },
    desktopOrigin: 'http://127.0.0.1:4000',
    apiOrigin: 'http://127.0.0.1:4000',
    onState() {},
  })
  assert.deepEqual(manager.setRendererBackendOrigin('http://remote-host:4000'), {
    origin: 'http://remote-host:4000',
  })
  assert.equal(manager.protectedOrigins.has('http://remote-host:4000'), true)
  assert.deepEqual(manager.show({
    projectId: 'p', threadId: 't', bounds: { x: 0, y: 0, width: 800, height: 600 },
  }), {
    visible: false,
    bounds: { x: 0, y: 0, width: 800, height: 600 },
  })
  const status = await manager.perform({ projectId: 'p', threadId: 't', operation: 'session.status', params: {} })
  assert.deepEqual(status, {
    message: 'Electron browser session is not running.',
    status: {
      endpoint: '', reachable: false, product: '', protocolVersion: '', pages: 0,
      currentTargetId: null, owned: true,
    },
    backend: 'electron', running: false, pages: [], pageList: [], currentTargetId: null,
  })
  await assert.rejects(
    manager.perform({ projectId: 'p', threadId: 't', operation: 'preview', params: { format: 'jpeg', quality: 70 } }),
    (error) => error.code === 'frame_unavailable' && error.status === 404,
  )
})

test('each restarted thread session receives a fresh ephemeral partition', () => {
  const manager = new BrowserWorkspaceManager({
    WebContentsView: class {},
    hostWindow: { getContentSize: () => [1, 1], contentView: {} },
    appView: {}, desktopOrigin: 'http://127.0.0.1:4000',
    apiOrigin: 'http://127.0.0.1:4000', onState() {},
  })
  const first = manager.partitionFor('project\\0thread')
  const second = manager.partitionFor('project\\0thread')
  assert.match(first, /^kiwi-code-guest-[a-f0-9]{24}$/)
  assert.notEqual(first, second)
})

test('atomically writes mode-0600 config and removes only a matching config', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-provider-test-'))
  const configPath = path.join(directory, 'nested', 'provider.json')
  const config = { version: 1, pid: 10, port: 20, token: 'a'.repeat(64) }
  try {
    await atomicWriteConfig(configPath, config)
    assert.deepEqual(JSON.parse(await fs.readFile(configPath, 'utf8')), config)
    assert.equal((await fs.stat(configPath)).mode & 0o777, 0o600)
    await removeMatchingConfig(configPath, { ...config, token: 'b'.repeat(64) })
    assert.deepEqual(JSON.parse(await fs.readFile(configPath, 'utf8')), config)
    await removeMatchingConfig(configPath, config)
    await assert.rejects(fs.stat(configPath), { code: 'ENOENT' })
  } finally {
    await fs.rm(directory, { recursive: true, force: true })
  }
})

test('provider binds loopback dynamically, authenticates, and uses the action envelope', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-provider-http-'))
  const configPath = path.join(directory, 'exact-provider.json')
  const actions = []
  const workspace = {
    addProtectedOrigin(origin) { this.origin = origin },
    async perform(action) {
      actions.push(action)
      if (action.operation === 'preview') throw new BrowserProviderError('frame_unavailable', '', 404, false)
      return { echoed: action.operation }
    },
  }
  const app = { getPath() { throw new Error('exact config path should win') } }
  const provider = new BrowserProviderServer({
    app,
    workspace,
    environment: { KIWI_CODE_BROWSER_PROVIDER_CONFIG: configPath },
  })
  try {
    const config = await provider.start()
    assert.equal(config.version, 1)
    assert.equal(config.token.length, 64)
    assert.ok(config.port > 0 && config.port !== 4000)
    assert.equal(workspace.origin, `http://127.0.0.1:${config.port}`)
    assert.equal(configPathFor(app, { KIWI_CODE_BROWSER_PROVIDER_CONFIG: configPath }), configPath)

    const denied = await request(config.port, { token: '0'.repeat(64) })
    assert.equal(denied.status, 401)
    assert.deepEqual(denied.body, { ok: false, error: { code: 'unauthorized' } })

    const accepted = await request(config.port, {
      token: config.token,
      body: JSON.stringify({ projectId: 'p', threadId: 't', operation: 'session.status', params: {} }),
    })
    assert.equal(accepted.status, 200)
    assert.deepEqual(accepted.body, { ok: true, result: { echoed: 'session.status' } })
    assert.equal(actions.length, 1)

    const unavailableFrame = await request(config.port, {
      token: config.token,
      body: JSON.stringify({
        projectId: 'p', threadId: 't', operation: 'preview',
        params: { format: 'jpeg', quality: 70 },
      }),
    })
    assert.equal(unavailableFrame.status, 404)
    assert.deepEqual(unavailableFrame.body, { ok: false, error: { code: 'frame_unavailable' } })

    const invalid = await request(config.port, { token: config.token, body: '{' })
    assert.equal(invalid.status, 400)
    assert.equal(invalid.body.error.code, 'invalid_json')
  } finally {
    await provider.stop()
    await fs.rm(directory, { recursive: true, force: true })
  }
})
