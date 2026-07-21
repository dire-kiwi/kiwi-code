'use strict'

const { createHash, randomBytes } = require('node:crypto')
const {
  BrowserProviderError,
  assertAllowedGuestUrl,
  clampBounds,
  formatAccessibilityTree,
  isProtectedUrl,
  isRecord,
  parseKeyChord,
  validateBounds,
} = require('./browser-helpers.cjs')

const MAX_SCREENSHOT_BYTES = 15 * 1024 * 1024
const MAX_EXPRESSION_CHARS = 100_000
const MAX_TEXT_CHARS = 100_000
const MAX_CDP_RESULT_BYTES = 1024 * 1024
const MAX_WAIT_MS = 60_000
const SNAPSHOT_TEXT_BYTES = 51_200
const DEFAULT_TIMEOUT_MS = 30_000
const DEBUGGER_PROTOCOL_VERSION = '1.3'
const DEFAULT_VIEWPORT = { x: 0, y: 0, width: 1280, height: 800 }
const MAX_TABS = 32
const MAX_SCREENSHOT_DIMENSION = 16_384
const MAX_SCREENSHOT_PIXELS = 64 * 1024 * 1024
const SUPPORTED_OPERATIONS = new Set([
  'session.status', 'session.start', 'session.disconnect', 'session.stop',
  'tabs.list', 'tabs.new', 'tabs.select', 'tabs.close',
  'navigate.goto', 'navigate.back', 'navigate.forward', 'navigate.reload',
  'snapshot', 'click', 'fill', 'key', 'wait', 'evaluate', 'screenshot', 'cdp', 'preview',
])
const ALLOWED_CDP_DOMAINS = new Set([
  'Accessibility', 'Audits', 'CSS', 'DOM', 'DOMDebugger', 'DOMSnapshot',
  'Emulation', 'Fetch', 'FileSystem', 'IO', 'Input', 'LayerTree', 'Log',
  'Media', 'Network', 'Overlay', 'Page', 'Performance',
  'PerformanceTimeline', 'Profiler', 'Runtime', 'Schema', 'WebAudio',
])
const BLOCKED_CDP_METHODS = new Set([
  'DOM.setFileInputFiles',
  'Network.getAllCookies',
  'Page.crash',
  'Page.setDownloadBehavior',
  'Runtime.terminateExecution',
])
const ALLOWED_GUEST_REQUEST_PROTOCOLS = new Set([
  'about:', 'blob:', 'chrome-error:', 'data:', 'filesystem:',
  'http:', 'https:', 'ws:', 'wss:',
])

function requireString(value, name, maximum = MAX_EXPRESSION_CHARS) {
  if (typeof value !== 'string') throw new BrowserProviderError('invalid_params', `${name} must be a string.`)
  if (value.length > maximum) throw new BrowserProviderError('invalid_params', `${name} is too long.`)
  return value
}

function boundedInteger(value, name, fallback, minimum, maximum) {
  if (value === undefined) return fallback
  if (!Number.isInteger(value) || value < minimum || value > maximum) {
    throw new BrowserProviderError('invalid_params', `${name} must be an integer from ${minimum} to ${maximum}.`)
  }
  return value
}

function boundedNumber(value, name, fallback, minimum, maximum) {
  if (value === undefined) return fallback
  if (typeof value !== 'number' || !Number.isFinite(value) || value < minimum || value > maximum) {
    throw new BrowserProviderError('invalid_params', `${name} must be a finite number from ${minimum} to ${maximum}.`)
  }
  return value
}

function truncateUtf8(value, maximumBytes) {
  const buffer = Buffer.from(value)
  if (buffer.length <= maximumBytes) return { text: value, truncated: false }
  return {
    text: `${buffer.subarray(0, maximumBytes - 80).toString('utf8')}\n… output truncated at ${maximumBytes} bytes.`,
    truncated: true,
  }
}

function assertJsonSize(value, maximumBytes = MAX_CDP_RESULT_BYTES) {
  let encoded
  try {
    encoded = JSON.stringify(value)
  } catch {
    throw new BrowserProviderError('output_too_large', 'The browser result is not JSON serializable.', 422)
  }
  if (encoded === undefined) return
  if (Buffer.byteLength(encoded) > maximumBytes) {
    throw new BrowserProviderError('output_too_large', 'The browser result exceeds the output limit.', 413)
  }
}

function truncateMetadata(value, maximum = 2_048) {
  const text = typeof value === 'string' ? value : ''
  return text.length <= maximum ? text : `${text.slice(0, maximum - 1)}…`
}

function safeUrl(value) {
  try { return truncateMetadata(new URL(value).toString(), 16_384) } catch { return truncateMetadata(value || 'about:blank', 16_384) }
}

function isProtectedRequest(raw, protectedOrigins) {
  try {
    const url = new URL(raw)
    return !ALLOWED_GUEST_REQUEST_PROTOCOLS.has(url.protocol) || isProtectedUrl(url, protectedOrigins)
  } catch {
    return true
  }
}

async function debuggerSend(tab, method, params = {}, timeoutMs = DEFAULT_TIMEOUT_MS) {
  if (tab.view.webContents.isDestroyed()) {
    throw new BrowserProviderError('page_not_found', 'The selected page is closed.', 404)
  }
  const debug = tab.view.webContents.debugger
  if (!debug.isAttached()) {
    try {
      debug.attach(DEBUGGER_PROTOCOL_VERSION)
      tab.debuggerAttached = true
    } catch (error) {
      if (!debug.isAttached()) throw error
    }
  }
  const command = debug.sendCommand(method, params)
  let timer
  let cancel
  const interrupted = new Promise((_resolve, reject) => {
    cancel = () => reject(
      new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false),
    )
    tab.session?.waiters.add(cancel)
  })
  try {
    return await Promise.race([
      command,
      interrupted,
      new Promise((_resolve, reject) => {
        timer = setTimeout(() => {
          if (debug.isAttached()) debug.detach()
          reject(new BrowserProviderError('operation_timeout', 'Browser operation timed out.', 408))
        }, timeoutMs)
        timer.unref?.()
      }),
    ])
  } finally {
    clearTimeout(timer)
    tab.session?.waiters.delete(cancel)
  }
}

async function enablePage(tab) {
  await Promise.all([
    debuggerSend(tab, 'Page.enable'),
    debuggerSend(tab, 'Runtime.enable'),
    debuggerSend(tab, 'DOM.enable'),
  ])
}

function exceptionMessage(response) {
  const details = response?.exceptionDetails
  return details?.exception?.description || details?.exception?.className || details?.text
}

function remoteValue(response) {
  const exception = exceptionMessage(response)
  if (exception) throw new Error(exception)
  const result = response?.result
  if (!result) return undefined
  if (Object.hasOwn(result, 'value')) return result.value
  if (result.unserializableValue !== undefined) return result.unserializableValue
  if (result.type === 'undefined') return undefined
  return result.description || `[${result.type || 'object'}]`
}

async function evaluatePage(tab, expression, returnByValue = true) {
  const response = await debuggerSend(tab, 'Runtime.evaluate', {
    awaitPromise: true,
    expression,
    returnByValue,
    userGesture: true,
  })
  if (!returnByValue) {
    const exception = exceptionMessage(response)
    if (exception) throw new Error(exception)
    return response.result
  }
  return remoteValue(response)
}

async function callFunction(tab, objectId, functionDeclaration, args) {
  const response = await debuggerSend(tab, 'Runtime.callFunctionOn', {
    arguments: args.map((value) => ({ value })),
    awaitPromise: true,
    functionDeclaration,
    objectId,
    returnByValue: true,
    userGesture: true,
  })
  return remoteValue(response)
}

async function pageInfo(tab) {
  try {
    const info = await evaluatePage(tab, '({ title: document.title || "", url: location.href, readyState: document.readyState })')
    return {
      readyState: typeof info?.readyState === 'string' ? info.readyState : 'unknown',
      title: truncateMetadata(typeof info?.title === 'string' ? info.title : tab.view.webContents.getTitle()),
      url: safeUrl(typeof info?.url === 'string' ? info.url : tab.view.webContents.getURL()),
    }
  } catch {
    return {
      readyState: 'unknown',
      title: truncateMetadata(tab.view.webContents.getTitle() || ''),
      url: safeUrl(tab.view.webContents.getURL() || 'about:blank'),
    }
  }
}

async function loaderId(tab) {
  const response = await debuggerSend(tab, 'Page.getFrameTree')
  return response?.frameTree?.frame?.loaderId
}

async function pressKey(tab, chord) {
  const parsed = parseKeyChord(chord)
  const base = {
    code: parsed.code,
    key: parsed.key,
    modifiers: parsed.modifiers,
    nativeVirtualKeyCode: parsed.windowsVirtualKeyCode,
    windowsVirtualKeyCode: parsed.windowsVirtualKeyCode,
  }
  await debuggerSend(tab, 'Input.dispatchKeyEvent', {
    ...base,
    type: parsed.text === undefined ? 'rawKeyDown' : 'keyDown',
    ...(parsed.text === undefined ? {} : { text: parsed.text }),
  })
  await debuggerSend(tab, 'Input.dispatchKeyEvent', { ...base, type: 'keyUp' })
  return parsed
}

async function waitForDocumentReady(tab, timeoutMs, expectedLoaderId) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      if (expectedLoaderId && await loaderId(tab) !== expectedLoaderId) {
        await tab.session.pause(100)
        continue
      }
      if (await evaluatePage(tab, 'document.readyState') === 'complete') return
    } catch (error) {
      if (tab.session.stopped) throw error
      // Navigations can transiently invalidate the execution context.
    }
    await tab.session.pause(100)
  }
  throw new Error(`Page did not finish loading within ${timeoutMs}ms.`)
}

class BrowserSession {
  constructor({ key, projectId, threadId, partition, WebContentsView, hostWindow, appView, protectedOrigins, onChanged, onPopup, onWorkspaceShortcut, isViewActive }) {
    this.key = key
    this.projectId = projectId
    this.threadId = threadId
    this.partition = partition
    this.WebContentsView = WebContentsView
    this.hostWindow = hostWindow
    this.appView = appView
    this.protectedOrigins = protectedOrigins
    this.onChanged = onChanged
    this.onPopup = onPopup
    this.onWorkspaceShortcut = onWorkspaceShortcut
    this.isViewActive = isViewActive
    this.tabs = new Map()
    this.currentTargetId = null
    this.refs = new Map()
    this.partitionConfigured = false
    this.previewGeneration = 0
    this.stopped = false
    this.waiters = new Set()
  }

  pause(milliseconds) {
    if (this.stopped) {
      return Promise.reject(new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false))
    }
    if (milliseconds <= 0) return Promise.resolve()
    return new Promise((resolve, reject) => {
      const finish = (error) => {
        clearTimeout(timer)
        this.waiters.delete(cancel)
        if (error) reject(error)
        else resolve()
      }
      const timer = setTimeout(() => finish(), milliseconds)
      timer.unref?.()
      const cancel = () => finish(new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false))
      this.waiters.add(cancel)
    })
  }

  async loadTab(tab, url) {
    if (this.stopped) throw new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false)
    let timer
    let cancel
    const interrupted = new Promise((_resolve, reject) => {
      const finish = (error) => {
        clearTimeout(timer)
        this.waiters.delete(cancel)
        reject(error)
      }
      timer = setTimeout(() => finish(
        new BrowserProviderError('operation_timeout', 'Browser page loading timed out.', 408),
      ), DEFAULT_TIMEOUT_MS)
      timer.unref?.()
      cancel = () => finish(
        new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false),
      )
      this.waiters.add(cancel)
    })
    try {
      await Promise.race([tab.view.webContents.loadURL(url), interrupted])
    } finally {
      clearTimeout(timer)
      this.waiters.delete(cancel)
    }
  }

  tabList() {
    return [...this.tabs.values()].map((tab) => ({
      id: tab.id,
      title: tab.view.webContents.isDestroyed() ? '' : truncateMetadata(tab.view.webContents.getTitle()),
      type: 'page',
      url: tab.view.webContents.isDestroyed() ? '' : safeUrl(tab.view.webContents.getURL()),
    }))
  }

  selectedTab(required = true) {
    const tab = this.currentTargetId ? this.tabs.get(this.currentTargetId) : undefined
    if (!tab && required) throw new BrowserProviderError('page_not_found', 'No browser page is selected.', 404, false)
    return tab
  }

  matchingTab(targetId) {
    requireString(targetId, 'targetId', 128)
    const matches = [...this.tabs.keys()].filter((id) => id === targetId || id.startsWith(targetId))
    if (matches.length !== 1) {
      throw new BrowserProviderError('page_not_found', 'The requested browser page does not exist.', 404, false)
    }
    return this.tabs.get(matches[0])
  }

  selectMatching(targetId) {
    const tab = this.matchingTab(targetId)
    this.currentTargetId = tab.id
    this.refs.clear()
    this.onChanged(this)
    return tab
  }

  configurePartition(electronSession) {
    if (this.partitionConfigured) return
    this.partitionConfigured = true
    electronSession.setPermissionCheckHandler(() => false)
    electronSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false))
    electronSession.on('will-download', (event) => event.preventDefault())
    electronSession.webRequest.onBeforeRequest({ urls: ['<all_urls>'] }, (details, callback) => {
      callback({ cancel: isProtectedRequest(details.url, this.protectedOrigins) })
    })
  }

  async createTab(rawUrl = 'about:blank', select = true) {
    if (this.stopped) throw new BrowserProviderError('session_not_found', 'The browser session has stopped.', 404, false)
    if (this.tabs.size >= MAX_TABS) throw new BrowserProviderError('tab_limit_reached', `Browser sessions are limited to ${MAX_TABS} tabs.`, 409)
    const url = assertAllowedGuestUrl(rawUrl, this.protectedOrigins)
    const view = new this.WebContentsView({
      webPreferences: {
        backgroundThrottling: false,
        contextIsolation: true,
        disableDialogs: true,
        nodeIntegration: false,
        partition: this.partition,
        sandbox: true,
      },
    })
    view.setBounds(DEFAULT_VIEWPORT)
    const id = String(view.webContents.id)
    const tab = { id, view, debuggerAttached: false, session: this }
    this.tabs.set(id, tab)
    this.configurePartition(view.webContents.session)

    const blockNavigation = (details) => {
      try { assertAllowedGuestUrl(details.url, this.protectedOrigins) } catch { details.preventDefault() }
    }
    view.webContents.on('will-navigate', blockNavigation)
    view.webContents.on('will-redirect', blockNavigation)
    view.webContents.setWindowOpenHandler(({ url: popupUrl }) => {
      try {
        const normalized = assertAllowedGuestUrl(popupUrl, this.protectedOrigins)
        queueMicrotask(() => this.onPopup(this, normalized))
      } catch {
        // Invalid and protected popup targets are discarded.
      }
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
    view.webContents.debugger.on('detach', () => {
      tab.debuggerAttached = false
      this.refs.clear()
    })
    for (const eventName of ['did-start-loading', 'did-stop-loading', 'page-title-updated', 'did-navigate', 'did-navigate-in-page']) {
      view.webContents.on(eventName, () => this.onChanged(this))
    }
    view.webContents.on('destroyed', () => {
      this.tabs.delete(id)
      if (this.currentTargetId === id) this.currentTargetId = this.tabs.keys().next().value || null
      this.refs.clear()
      this.onChanged(this)
    })

    if (select) this.currentTargetId = id
    this.refs.clear()
    try {
      await this.loadTab(tab, url)
    } catch (error) {
      if (error instanceof BrowserProviderError || this.stopped) {
        if (this.tabs.has(tab.id)) this.destroyTab(tab)
        throw error
      }
      if (!view.webContents.isDestroyed() && view.webContents.getURL() !== url) {
        this.destroyTab(tab)
        throw error
      }
    }
    this.onChanged(this)
    return tab
  }

  destroyTab(tab) {
    this.hostWindow.contentView.removeChildView(tab.view)
    if (!tab.view.webContents.isDestroyed()) {
      if (tab.view.webContents.debugger.isAttached()) tab.view.webContents.debugger.detach()
      tab.view.webContents.close({ waitForBeforeUnload: false })
    }
    this.tabs.delete(tab.id)
    if (this.currentTargetId === tab.id) this.currentTargetId = this.tabs.keys().next().value || null
    this.refs.clear()
  }

  async stop() {
    if (this.stopped) return
    this.stopped = true
    for (const cancel of [...this.waiters]) cancel()
    this.waiters.clear()
    for (const tab of [...this.tabs.values()]) this.destroyTab(tab)
    this.currentTargetId = null
    this.refs.clear()
  }

  async withRenderableTab(tab, operation) {
    const children = this.hostWindow.contentView.children
    if (children.includes(tab.view)) return operation()

    // A hidden guest must never receive focus: previews and agent actions can
    // run while the user is typing in the trusted renderer or another guest.
    const appIndex = children.indexOf(this.appView)
    this.hostWindow.contentView.addChildView(tab.view, appIndex < 0 ? 0 : appIndex)
    tab.view.setBounds(DEFAULT_VIEWPORT)
    try {
      await this.pause(50)
      return await operation()
    } finally {
      if (!this.isViewActive(tab.view) && this.hostWindow.contentView.children.includes(tab.view)) {
        this.hostWindow.contentView.removeChildView(tab.view)
      }
    }
  }

  async withElement(tab, params, operation) {
    const ref = params.ref
    const selector = params.selector
    if ((typeof ref === 'string') === (typeof selector === 'string')) {
      throw new BrowserProviderError('invalid_params', 'Provide exactly one of ref or selector.')
    }
    let objectId
    try {
      if (typeof ref === 'string') {
        const stored = this.refs.get(ref)
        if (!stored) throw new BrowserProviderError('stale_ref', 'Unknown or stale accessibility ref.', 404)
        if (stored.targetId !== tab.id) throw new BrowserProviderError('stale_ref', 'The accessibility ref belongs to another page.', 404)
        const currentLoader = await loaderId(tab)
        if (stored.loaderId && currentLoader && stored.loaderId !== currentLoader) {
          this.refs.clear()
          throw new BrowserProviderError('stale_ref', 'The accessibility ref is stale after navigation.', 404)
        }
        const resolved = await debuggerSend(tab, 'DOM.resolveNode', {
          backendNodeId: stored.backendNodeId,
          objectGroup: 'kiwi-code-browser',
        })
        objectId = resolved?.object?.objectId
      } else {
        requireString(selector, 'selector', 10_000)
        const response = await debuggerSend(tab, 'Runtime.evaluate', {
          expression: `document.querySelector(${JSON.stringify(selector)})`,
          objectGroup: 'kiwi-code-browser',
          returnByValue: false,
        })
        const exception = exceptionMessage(response)
        if (exception) throw new Error(exception)
        if (response?.result?.subtype === 'null') throw new BrowserProviderError('element_not_found', 'No element matches the selector.', 404)
        objectId = response?.result?.objectId
      }
      if (!objectId) throw new BrowserProviderError('element_not_found', 'Could not resolve the browser element.', 404)
      return await operation(objectId)
    } finally {
      if (objectId && !tab.view.webContents.isDestroyed() && tab.view.webContents.debugger.isAttached()) {
        await debuggerSend(tab, 'Runtime.releaseObject', { objectId }).catch(() => {})
      }
    }
  }

  async status() {
    const pages = this.tabList()
    const tab = this.selectedTab(false)
    const selectedPage = pages.find((page) => page.id === this.currentTargetId)
    const currentPage = tab && selectedPage && !tab.view.webContents.isDestroyed() ? {
      ...selectedPage,
      canGoBack: tab.view.webContents.navigationHistory.canGoBack(),
      canGoForward: tab.view.webContents.navigationHistory.canGoForward(),
      loading: tab.view.webContents.isLoading(),
    } : undefined
    return {
      message: 'Electron browser session is running.',
      status: {
        endpoint: 'kiwi-code://electron',
        reachable: true,
        product: `Electron/${process.versions.electron || 'unknown'}`,
        protocolVersion: DEBUGGER_PROTOCOL_VERSION,
        pages: pages.length,
        currentTargetId: this.currentTargetId,
        owned: true,
      },
      backend: 'electron',
      running: true,
      pages,
      pageList: pages,
      currentTargetId: this.currentTargetId,
      ...(currentPage ? { current: currentPage, currentPage } : {}),
    }
  }

  async tabsOperation(operation, params) {
    let message = 'Listed browser tabs.'
    if (operation === 'tabs.new') {
      const tab = await this.createTab(params.url === undefined ? 'about:blank' : requireString(params.url, 'url', 16_384))
      message = `Opened and selected tab ${tab.id}.`
    } else if (operation === 'tabs.select') {
      const tab = this.selectMatching(params.targetId ?? params.id)
      message = `Selected tab ${tab.id}.`
    } else if (operation === 'tabs.close') {
      const tab = params.targetId || params.id ? this.matchingTab(params.targetId ?? params.id) : this.selectedTab()
      const id = tab.id
      this.destroyTab(tab)
      this.onChanged(this)
      message = `Closed tab ${id}.`
    }
    return { message, pages: this.tabList(), currentTargetId: this.currentTargetId }
  }

  async navigate(operation, params) {
    const tab = this.selectedTab()
    await enablePage(tab)
    const action = operation.slice('navigate.'.length)
    const timeoutMs = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS)
    if (action === 'goto') {
      const url = assertAllowedGuestUrl(requireString(params.url, 'url', 16_384), this.protectedOrigins)
      const result = await debuggerSend(tab, 'Page.navigate', { url })
      if (result?.errorText) throw new Error('Navigation failed.')
      if (!result?.isDownload) await waitForDocumentReady(tab, timeoutMs, result?.loaderId)
    } else if (action === 'reload') {
      await debuggerSend(tab, 'Page.reload', { ignoreCache: false })
      await this.pause(50)
      await waitForDocumentReady(tab, timeoutMs)
    } else {
      const history = await debuggerSend(tab, 'Page.getNavigationHistory')
      const index = history?.currentIndex ?? 0
      const wanted = action === 'back' ? index - 1 : index + 1
      const entry = history?.entries?.[wanted]
      if (!Number.isInteger(entry?.id)) throw new BrowserProviderError('navigation_unavailable', `Cannot navigate ${action}.`, 409)
      if (isProtectedRequest(entry.url, this.protectedOrigins)) throw new BrowserProviderError('blocked_origin', 'Navigation to a protected Kiwi Code origin is blocked.')
      await debuggerSend(tab, 'Page.navigateToHistoryEntry', { entryId: entry.id })
      await this.pause(50)
      await waitForDocumentReady(tab, timeoutMs)
    }
    this.refs.clear()
    this.onChanged(this)
    const info = await pageInfo(tab)
    return { action, targetId: tab.id, title: info.title, url: info.url }
  }

  async snapshot(params) {
    const tab = this.selectedTab()
    await enablePage(tab)
    await debuggerSend(tab, 'Accessibility.enable')
    const [tree, currentLoader, info] = await Promise.all([
      debuggerSend(tab, 'Accessibility.getFullAXTree'),
      loaderId(tab),
      pageInfo(tab),
    ])
    const formatted = formatAccessibilityTree(tree?.nodes || [], {
      interactiveOnly: params.interactiveOnly === true,
      maxDepth: boundedInteger(params.maxDepth, 'maxDepth', 30, 1, 50),
      maxNodes: boundedInteger(params.maxNodes, 'maxNodes', 300, 1, 1_000),
    })
    this.refs = new Map([...formatted.refs].map(([ref, value]) => [ref, {
      ...value,
      loaderId: currentLoader,
      targetId: tab.id,
    }]))
    const rendered = truncateUtf8([
      `Page: ${info.title || '(untitled)'}`,
      `URL: ${info.url}`,
      `Target: ${tab.id}`,
      '',
      formatted.text,
      '',
      `[${formatted.includedNodes} nodes shown; ${formatted.refs.size} actionable refs${formatted.omittedNodes ? `; ${formatted.omittedNodes} nodes omitted` : ''}.]`,
    ].join('\n'), SNAPSHOT_TEXT_BYTES)
    return {
      includedNodes: formatted.includedNodes,
      omittedNodes: formatted.omittedNodes,
      refs: formatted.refs.size,
      targetId: tab.id,
      text: rendered.text,
      title: info.title,
      truncated: rendered.truncated,
      url: info.url,
    }
  }

  async click(params) {
    const tab = this.selectedTab()
    await enablePage(tab)
    const before = new Set(this.tabs.keys())
    await this.withRenderableTab(tab, () => this.withElement(tab, params, async (objectId) => {
      const bounds = await callFunction(tab, objectId, `function () {
        const el = this && this.nodeType === 1 ? this : this?.parentElement;
        if (!el) return { error: "The referenced DOM node is not an element." };
        if (el.disabled) return { error: "The element is disabled." };
        el.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
        const rect = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        if (rect.width <= 0 || rect.height <= 0 || style.visibility === "hidden" || style.display === "none") return { error: "The element is not visible." };
        const x = rect.left + rect.width / 2, y = rect.top + rect.height / 2;
        const top = document.elementFromPoint(x, y);
        if (top && top !== el && !el.contains(top) && !top.contains(el)) return { error: "The element is covered." };
        return { x, y };
      }`, [])
      if (bounds?.error) throw new Error(bounds.error)
      if (typeof bounds?.x !== 'number' || typeof bounds?.y !== 'number') throw new Error('Could not determine a click point.')
      const button = params.button ?? 'left'
      if (!['left', 'middle', 'right'].includes(button)) throw new BrowserProviderError('invalid_params', 'button must be left, middle, or right.')
      const clickCount = boundedInteger(params.clickCount, 'clickCount', 1, 1, 3)
      const buttons = button === 'left' ? 1 : button === 'right' ? 2 : 4
      await debuggerSend(tab, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x: bounds.x, y: bounds.y })
      await debuggerSend(tab, 'Input.dispatchMouseEvent', { type: 'mousePressed', x: bounds.x, y: bounds.y, button, buttons, clickCount })
      await debuggerSend(tab, 'Input.dispatchMouseEvent', { type: 'mouseReleased', x: bounds.x, y: bounds.y, button, buttons: 0, clickCount })
    }))
    await this.pause(boundedInteger(params.waitMs, 'waitMs', 500, 0, 10_000))
    const info = await pageInfo(tab)
    return {
      clicked: params.ref ? `ref ${params.ref}` : `selector ${JSON.stringify(params.selector)}`,
      newTabs: this.tabList().filter((item) => !before.has(item.id)),
      targetId: tab.id,
      title: info.title,
      url: info.url,
    }
  }

  async fill(params) {
    const tab = this.selectedTab()
    const text = requireString(params.text, 'text', MAX_TEXT_CHARS)
    await enablePage(tab)
    await this.withElement(tab, params, async (objectId) => {
      const prepared = await callFunction(tab, objectId, `function (clear) {
        const el = this && this.nodeType === 1 ? this : this?.parentElement;
        if (!(el instanceof HTMLElement)) return { error: "The referenced node is not editable." };
        if (el.matches(":disabled, [readonly]")) return { error: "The element is disabled or readonly." };
        if (!(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el.isContentEditable)) return { error: "The element is not editable." };
        el.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
        el.focus();
        if (clear) {
          if (typeof el.select === "function") el.select();
          else { const range = document.createRange(); range.selectNodeContents(el); const selection = getSelection(); selection.removeAllRanges(); selection.addRange(range); }
        } else if (typeof el.setSelectionRange === "function") { const end = String(el.value ?? "").length; el.setSelectionRange(end, end); }
        return {};
      }`, [params.clear !== false])
      if (prepared?.error) throw new Error(prepared.error)
      if (text.length > 0) await debuggerSend(tab, 'Input.insertText', { text })
      else if (params.clear !== false) await pressKey(tab, 'Backspace')
      if (params.submit === true) await pressKey(tab, 'Enter')
    })
    await this.pause(params.submit === true ? 500 : 100)
    const info = await pageInfo(tab)
    return {
      filled: params.ref ? `ref ${params.ref}` : `selector ${JSON.stringify(params.selector)}`,
      submitted: params.submit === true,
      targetId: tab.id,
      textLength: text.length,
      title: info.title,
      url: info.url,
    }
  }

  async key(params) {
    const tab = this.selectedTab()
    const chord = requireString(params.key ?? params.chord, 'key', 128)
    await enablePage(tab)
    await pressKey(tab, chord)
    await this.pause(100)
    const info = await pageInfo(tab)
    return { chord, targetId: tab.id, title: info.title, url: info.url }
  }

  async wait(params) {
    const hasCondition = params.selector !== undefined || params.text !== undefined || params.urlContains !== undefined
    if (!hasCondition && params.timeMs === undefined) throw new BrowserProviderError('invalid_params', 'Provide timeMs or a wait condition.')
    if (params.state !== undefined && params.selector === undefined) throw new BrowserProviderError('invalid_params', 'state requires selector.')
    if (params.state !== undefined && !['hidden', 'visible'].includes(params.state)) throw new BrowserProviderError('invalid_params', 'state must be hidden or visible.')
    const started = Date.now()
    const timeMs = boundedNumber(params.timeMs, 'timeMs', 0, 0, MAX_WAIT_MS)
    const timeoutMs = hasCondition
      ? boundedInteger(params.timeoutMs, 'timeoutMs', 10_000, 100, MAX_WAIT_MS)
      : 0
    if (timeMs + timeoutMs > MAX_WAIT_MS) {
      throw new BrowserProviderError('invalid_params', `timeMs and timeoutMs may total at most ${MAX_WAIT_MS}.`)
    }
    if (timeMs > 0) await this.pause(timeMs)
    const tab = this.selectedTab()
    await enablePage(tab)
    if (hasCondition) {
      const selector = params.selector === undefined ? null : requireString(params.selector, 'selector', 10_000)
      const text = params.text === undefined ? null : requireString(params.text, 'text', MAX_TEXT_CHARS)
      const urlContains = params.urlContains === undefined ? null : requireString(params.urlContains, 'urlContains', 16_384)
      const deadline = Date.now() + timeoutMs
      let lastState
      while (Date.now() < deadline) {
        lastState = await evaluatePage(tab, `(() => {
          const selector = ${JSON.stringify(selector)}, expectedText = ${JSON.stringify(text)}, expectedUrl = ${JSON.stringify(urlContains)}, wantedState = ${JSON.stringify(params.state ?? 'visible')};
          let selectorMatches = true, found = null, visible = null;
          if (selector !== null) { const element = document.querySelector(selector); found = !!element; if (element) { const rect = element.getBoundingClientRect(), style = getComputedStyle(element); visible = rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden"; } else visible = false; selectorMatches = wantedState === "hidden" ? !visible : !!visible; }
          const textMatches = expectedText === null || (document.body?.innerText ?? "").includes(expectedText);
          const urlMatches = expectedUrl === null || location.href.includes(expectedUrl);
          return { done: selectorMatches && textMatches && urlMatches, found, visible, url: location.href };
        })()`)
        if (lastState?.done === true) break
        await this.pause(200)
      }
      if (lastState?.done !== true) throw new BrowserProviderError('wait_timeout', 'Browser wait timed out.', 408)
    }
    const info = await pageInfo(tab)
    return { elapsedMs: Date.now() - started, targetId: tab.id, title: info.title, url: info.url }
  }

  async evaluate(params) {
    const tab = this.selectedTab()
    const expression = requireString(params.expression ?? params.script, 'expression', MAX_EXPRESSION_CHARS)
    const timeoutMs = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS)
    await enablePage(tab)
    const response = await debuggerSend(tab, 'Runtime.evaluate', {
      awaitPromise: true,
      expression,
      returnByValue: true,
      userGesture: true,
    }, timeoutMs)
    const result = remoteValue(response)
    assertJsonSize(result)
    const info = await pageInfo(tab)
    return { result: result === undefined ? null : result, targetId: tab.id, title: info.title, url: info.url }
  }

  async capture(params, preview = false) {
    const tab = this.selectedTab(false)
    if (!tab && preview) throw new BrowserProviderError('frame_unavailable', '', 404, false)
    if (!tab) throw new BrowserProviderError('page_not_found', '', 404, false)

    const format = params.format ?? (preview ? 'jpeg' : 'png')
    if (!['jpeg', 'png'].includes(format)) {
      throw new BrowserProviderError('invalid_params', 'format must be jpeg or png.')
    }
    if (preview && (format !== 'jpeg' || (params.quality ?? 70) !== 70)) {
      throw new BrowserProviderError('invalid_params', 'preview requires JPEG quality 70.')
    }
    const quality = boundedInteger(params.quality, 'quality', preview ? 70 : 80, 0, 100)
    const fullPage = !preview && params.fullPage === true

    try {
      return await this.withRenderableTab(tab, async () => {
        let data
        let width
        let height
        let bytes
        if (!fullPage) {
          const image = await tab.view.webContents.capturePage(undefined, {
            stayAwake: true,
            stayHidden: true,
          })
          if (!image || image.isEmpty()) throw new Error('Browser did not return a viewport image.')
          const size = image.getSize()
          width = Math.round(size.width)
          height = Math.round(size.height)
          const contents = format === 'jpeg' ? image.toJPEG(quality) : image.toPNG()
          bytes = contents.length
          data = contents.toString('base64')
        } else {
          await enablePage(tab)
          const metrics = await debuggerSend(tab, 'Page.getLayoutMetrics')
          const size = metrics?.cssContentSize ?? metrics?.contentSize
          if (!(size?.width > 0) || !(size?.height > 0)) {
            throw new Error('Browser did not return valid screenshot dimensions.')
          }
          width = Math.round(size.width)
          height = Math.round(size.height)
          const response = await debuggerSend(tab, 'Page.captureScreenshot', {
            captureBeyondViewport: true,
            clip: { x: size.x ?? 0, y: size.y ?? 0, width: size.width, height: size.height, scale: 1 },
            format,
            fromSurface: true,
            ...(format === 'jpeg' ? { quality } : {}),
          }, MAX_WAIT_MS)
          if (typeof response?.data !== 'string') throw new Error('Browser did not return screenshot data.')
          data = response.data
          bytes = Buffer.from(data, 'base64').length
        }
        if (
          width <= 0 ||
          height <= 0 ||
          width > MAX_SCREENSHOT_DIMENSION ||
          height > MAX_SCREENSHOT_DIMENSION ||
          width * height > MAX_SCREENSHOT_PIXELS
        ) throw new BrowserProviderError('output_too_large', 'Screenshot dimensions exceed the safety limit.', 413)
        if (bytes <= 0 || bytes > MAX_SCREENSHOT_BYTES) {
          throw new BrowserProviderError('output_too_large', 'Screenshot exceeds the 15MB limit.', 413)
        }
        if (preview) {
          this.previewGeneration += 1
          return {
            data,
            mimeType: 'image/jpeg',
            width,
            height,
            generation: this.previewGeneration,
            capturedAt: new Date().toISOString(),
          }
        }
        const info = await pageInfo(tab)
        return {
          bytes,
          data,
          mimeType: format === 'jpeg' ? 'image/jpeg' : 'image/png',
          width,
          height,
          targetId: tab.id,
          title: info.title,
          url: info.url,
        }
      })
    } catch (error) {
      if (preview && (!(error instanceof BrowserProviderError) || error.code !== 'invalid_params')) {
        throw new BrowserProviderError('frame_unavailable', '', 404, false)
      }
      throw error
    }
  }

  async cdp(params) {
    const tab = this.selectedTab()
    const method = requireString(params.method, 'method', 256)
    if (!/^[A-Za-z][A-Za-z\d]*\.[A-Za-z][A-Za-z\d]*$/.test(method)) throw new BrowserProviderError('invalid_params', 'CDP method must look like Domain.method.')
    if (params.target !== undefined && params.target !== 'page') throw new BrowserProviderError('invalid_params', 'Only page-target CDP commands are available.')
    if (params.params !== undefined && !isRecord(params.params)) throw new BrowserProviderError('invalid_params', 'CDP params must be an object.')
    const commandParams = params.params === undefined ? {} : { ...params.params }
    const domain = method.slice(0, method.indexOf('.'))
    if (!ALLOWED_CDP_DOMAINS.has(domain) || BLOCKED_CDP_METHODS.has(method)) {
      throw new BrowserProviderError('blocked_command', 'This CDP command is outside the selected page boundary.')
    }
    if (
      method === 'Page.navigate' ||
      method === 'Fetch.continueRequest' ||
      method === 'Network.loadNetworkResource'
    ) {
      if (commandParams.url !== undefined) {
        commandParams.url = assertAllowedGuestUrl(
          requireString(commandParams.url, 'params.url', 16_384),
          this.protectedOrigins,
        )
      }
    }
    const timeoutMs = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS)
    const result = await debuggerSend(tab, method, commandParams, timeoutMs)
    assertJsonSize(result)
    if (['Page.navigate', 'Page.reload', 'Page.navigateToHistoryEntry'].includes(method)) this.refs.clear()
    return { method, result, target: 'page', targetId: tab.id }
  }

  async perform(operation, params) {
    if (operation === 'session.status') return this.status()
    if (['tabs.list', 'tabs.new', 'tabs.select', 'tabs.close'].includes(operation)) return this.tabsOperation(operation, params)
    if (['navigate.goto', 'navigate.back', 'navigate.forward', 'navigate.reload'].includes(operation)) return this.navigate(operation, params)
    if (operation === 'snapshot') return this.snapshot(params)
    if (operation === 'click') return this.click(params)
    if (operation === 'fill') return this.fill(params)
    if (operation === 'key') return this.key(params)
    if (operation === 'wait') return this.wait(params)
    if (operation === 'evaluate') return this.evaluate(params)
    if (operation === 'screenshot') return this.capture(params, false)
    if (operation === 'preview') return this.capture(params, true)
    if (operation === 'cdp') return this.cdp(params)
    throw new BrowserProviderError('unsupported_operation', 'Unsupported browser operation.')
  }
}

class BrowserWorkspaceManager {
  constructor({ WebContentsView, hostWindow, appView, desktopOrigin, apiOrigin, onState, onWorkspaceShortcut = () => {} }) {
    this.WebContentsView = WebContentsView
    this.hostWindow = hostWindow
    this.appView = appView
    this.onState = onState
    this.onWorkspaceShortcut = onWorkspaceShortcut
    this.sessions = new Map()
    this.queues = new Map()
    this.protectedOrigins = new Set([desktopOrigin, apiOrigin].filter(Boolean))
    this.rendererBackendOrigin = apiOrigin || desktopOrigin
    this.activeKey = null
    this.activeProjectId = null
    this.activeThreadId = null
    this.activeBounds = null
    this.attachedView = null
    this.disposed = false
    this.disposePromise = null
  }

  addProtectedOrigin(origin) {
    if (origin) this.protectedOrigins.add(origin)
  }

  setRendererBackendOrigin(origin) {
    this.addProtectedOrigin(origin)
    if (origin !== this.rendererBackendOrigin) {
      this.rendererBackendOrigin = origin
      this.detachActiveView()
    }
    return { origin }
  }

  key(projectId, threadId) {
    return `${projectId}\u0000${threadId}`
  }

  partitionFor(key) {
    const nonce = randomBytes(16)
    return `kiwi-code-guest-${createHash('sha256').update(key).update(nonce).digest('hex').slice(0, 24)}`
  }

  async serialized(projectId, threadId, operation) {
    const key = this.key(projectId, threadId)
    const previous = this.queues.get(key) || Promise.resolve()
    const current = previous.catch(() => {}).then(() => {
      if (this.disposed) throw new BrowserProviderError('session_not_found', 'Desktop browser is shutting down.', 503, false)
      return operation()
    })
    this.queues.set(key, current)
    try { return await current } finally { if (this.queues.get(key) === current) this.queues.delete(key) }
  }

  createSession(action, key) {
    const browserSession = new BrowserSession({
      key,
      projectId: action.projectId,
      threadId: action.threadId,
      partition: this.partitionFor(key),
      WebContentsView: this.WebContentsView,
      hostWindow: this.hostWindow,
      appView: this.appView,
      protectedOrigins: this.protectedOrigins,
      onChanged: () => this.sessionChanged(key),
      onPopup: (_session, url) => {
        void this.serialized(action.projectId, action.threadId, async () => {
          const current = this.sessions.get(key)
          if (!current) return
          await current.createTab(url, true)
        }).catch((error) => console.error('Could not open controlled browser tab:', error))
      },
      onWorkspaceShortcut: this.onWorkspaceShortcut,
      isViewActive: (view) => this.attachedView === view,
    })
    this.sessions.set(key, browserSession)
    return browserSession
  }

  noSessionStatus() {
    return {
      message: 'Electron browser session is not running.',
      status: {
        endpoint: '',
        reachable: false,
        product: '',
        protocolVersion: '',
        pages: 0,
        currentTargetId: null,
        owned: true,
      },
      backend: 'electron',
      running: false,
      pages: [],
      pageList: [],
      currentTargetId: null,
    }
  }

  async perform(action) {
    if (this.disposed) throw new BrowserProviderError('session_not_found', 'Desktop browser is shutting down.', 503, false)
    if (!SUPPORTED_OPERATIONS.has(action.operation)) {
      throw new BrowserProviderError('unsupported_operation', 'Unsupported browser operation.')
    }
    const key = this.key(action.projectId, action.threadId)
    if (action.operation === 'session.stop') {
      // Interrupt waits and in-flight WebContents operations before stop reaches
      // the head of the per-session queue.
      void this.sessions.get(key)?.stop()
    }
    return this.serialized(action.projectId, action.threadId, async () => {
      let browserSession = this.sessions.get(key)
      if (action.operation === 'session.status') {
        return browserSession ? browserSession.status() : this.noSessionStatus()
      }
      if (action.operation === 'session.disconnect') {
        if (!browserSession) {
          return { ...this.noSessionStatus(), message: 'No Electron browser connection was active.' }
        }
        return { ...(await browserSession.status()), message: 'Released the browser control connection; the session remains running.' }
      }
      if (action.operation === 'session.stop') {
        if (browserSession) {
          await browserSession.stop()
          this.sessions.delete(key)
        }
        if (this.activeKey === key) this.suspendView()
        this.emitState()
        return {
          ...this.noSessionStatus(),
          stopped: Boolean(browserSession),
          message: browserSession ? 'Stopped Electron browser session.' : 'No Electron browser session was running.',
        }
      }
      if (!browserSession && action.operation === 'preview') {
        throw new BrowserProviderError('frame_unavailable', '', 404, false)
      }

      const created = !browserSession
      if (!browserSession) browserSession = this.createSession(action, key)
      try {
        if (action.operation === 'session.start') {
          if (browserSession.tabs.size === 0) {
            await browserSession.createTab(action.params.url ?? 'about:blank')
          }
          this.sessionChanged(key)
          return browserSession.status()
        }
        if (created && action.operation !== 'tabs.new') {
          await browserSession.createTab('about:blank')
        }
        return await browserSession.perform(action.operation, action.params)
      } catch (error) {
        if (created && browserSession.tabs.size === 0) {
          await browserSession.stop()
          this.sessions.delete(key)
          this.sessionChanged(key)
        }
        if (error instanceof BrowserProviderError) throw error
        console.error(`Electron browser operation ${action.operation} failed:`, error)
        throw new BrowserProviderError('operation_failed', 'The browser operation failed.', 422, false)
      }
    })
  }

  sessionChanged(key) {
    if (this.activeKey === key) this.attachSelectedView()
    this.emitState()
  }

  emitState() {
    const current = this.activeKey ? this.sessions.get(this.activeKey) : undefined
    this.onState({
      visible: Boolean(this.attachedView),
      projectId: current?.projectId ?? this.activeProjectId,
      threadId: current?.threadId ?? this.activeThreadId,
      currentTargetId: current?.currentTargetId ?? null,
    })
  }

  attachSelectedView() {
    const browserSession = this.activeKey ? this.sessions.get(this.activeKey) : undefined
    const tab = browserSession?.selectedTab(false)
    const next = tab?.view || null
    const previous = this.attachedView
    const restoreGuestFocus = Boolean(
      previous &&
      !previous.webContents.isDestroyed() &&
      previous.webContents.isFocused(),
    )
    if (previous && previous !== next) this.hostWindow.contentView.removeChildView(previous)
    this.attachedView = next
    if (next) {
      this.hostWindow.contentView.addChildView(next)
      if (this.activeBounds) next.setBounds(clampBounds(this.activeBounds, this.contentSize()))
      if (restoreGuestFocus && !next.webContents.isDestroyed()) next.webContents.focus()
    } else if (restoreGuestFocus && !this.appView.webContents.isDestroyed()) {
      this.appView.webContents.focus()
    }
  }

  suspendView() {
    const previous = this.attachedView
    const restoreAppFocus = Boolean(
      previous &&
      !previous.webContents.isDestroyed() &&
      previous.webContents.isFocused(),
    )
    if (previous) this.hostWindow.contentView.removeChildView(previous)
    this.attachedView = null
    if (restoreAppFocus && !this.appView.webContents.isDestroyed()) this.appView.webContents.focus()
    this.emitState()
  }

  detachActiveView() {
    this.suspendView()
    this.activeKey = null
    this.activeProjectId = null
    this.activeThreadId = null
    this.activeBounds = null
  }

  contentSize() {
    const [width, height] = this.hostWindow.getContentSize()
    return { width, height }
  }

  show(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    const bounds = validateBounds(payload.bounds)
    this.activeKey = key
    this.activeProjectId = payload.projectId
    this.activeThreadId = payload.threadId
    this.activeBounds = bounds
    this.attachSelectedView()
    this.emitState()
    return { visible: Boolean(this.attachedView), bounds: clampBounds(this.activeBounds, this.contentSize()) }
  }

  hide(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    if (this.activeKey === key) this.detachActiveView()
    this.emitState()
    return { visible: false }
  }

  setBounds(payload) {
    const key = this.key(payload.projectId, payload.threadId)
    const bounds = validateBounds(payload.bounds)
    if (this.activeKey === key) {
      this.activeBounds = bounds
      if (this.attachedView) this.attachedView.setBounds(clampBounds(bounds, this.contentSize()))
    }
    return { bounds: clampBounds(bounds, this.contentSize()) }
  }

  resize() {
    const size = this.contentSize()
    this.appView.setBounds({ x: 0, y: 0, width: size.width, height: size.height })
    if (this.attachedView && this.activeBounds) this.attachedView.setBounds(clampBounds(this.activeBounds, size))
  }

  dispose() {
    if (this.disposePromise) return this.disposePromise
    this.disposed = true
    this.disposePromise = (async () => {
      this.detachActiveView()
      // Closing WebContents and cancelling waits first makes queued operations
      // settle instead of allowing a slow page to wedge application shutdown.
      await Promise.all([...this.sessions.values()].map((browserSession) => browserSession.stop()))
      await Promise.allSettled([...this.queues.values()])
      this.sessions.clear()
      this.queues.clear()
    })()
    return this.disposePromise
  }
}

module.exports = { BrowserSession, BrowserWorkspaceManager, isProtectedRequest }
