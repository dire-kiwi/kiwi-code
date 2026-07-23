'use strict'

const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const readline = require('node:readline')
const { randomBytes } = require('node:crypto')
const puppeteer = require('puppeteer-core')
const { HeadlessRecordingManager } = require('./browser-recordings.cjs')

const MAX_TABS = 32
const MAX_WAIT_MS = 60_000
const DEFAULT_TIMEOUT_MS = 30_000
const MAX_TEXT_CHARS = 100_000
const MAX_EXPRESSION_CHARS = 100_000
const MAX_RESULT_BYTES = 1024 * 1024
const MAX_SCREENSHOT_BYTES = 15 * 1024 * 1024
const MAX_SCREENSHOT_DIMENSION = 16_384
const MAX_SCREENSHOT_PIXELS = 64 * 1024 * 1024
const SNAPSHOT_TEXT_BYTES = 51_200
const VIEWPORT = { width: 1280, height: 800 }
const ALLOWED_CDP_DOMAINS = new Set([
  'Accessibility', 'Audits', 'CSS', 'DOM', 'DOMDebugger', 'DOMSnapshot',
  'Emulation', 'Fetch', 'FileSystem', 'IO', 'Input', 'LayerTree', 'Log',
  'Media', 'Network', 'Overlay', 'Page', 'Performance', 'PerformanceTimeline',
  'Profiler', 'Runtime', 'Schema', 'WebAudio',
])
const BLOCKED_CDP_METHODS = new Set([
  'DOM.setFileInputFiles', 'Network.getAllCookies', 'Page.crash',
  'Page.setDownloadBehavior', 'Runtime.terminateExecution',
])

class ProviderError extends Error {
  constructor(code, message = '') { super(message); this.code = code }
}
function record(value) { return value !== null && typeof value === 'object' && !Array.isArray(value) }
function requireString(value, name, maximum = MAX_EXPRESSION_CHARS) {
  if (typeof value !== 'string' || value.length > maximum) throw new ProviderError('invalid_params', `${name} must be a bounded string`)
  return value
}
function boundedInteger(value, name, fallback, minimum, maximum) {
  if (value === undefined) return fallback
  if (!Number.isInteger(value) || value < minimum || value > maximum) throw new ProviderError('invalid_params', `${name} is out of range`)
  return value
}
function boundedNumber(value, name, fallback, minimum, maximum) {
  if (value === undefined) return fallback
  if (typeof value !== 'number' || !Number.isFinite(value) || value < minimum || value > maximum) throw new ProviderError('invalid_params', `${name} is out of range`)
  return value
}
function jsonSize(value, maximum = MAX_RESULT_BYTES) {
  let encoded
  try { encoded = JSON.stringify(value) } catch { throw new ProviderError('output_too_large') }
  if (encoded !== undefined && Buffer.byteLength(encoded) > maximum) throw new ProviderError('output_too_large')
}
function truncate(value, maximum = 2048) {
  const text = typeof value === 'string' ? value : ''
  return text.length <= maximum ? text : `${text.slice(0, maximum - 1)}…`
}
function truncateUtf8(value, maximum) {
  const bytes = Buffer.from(value)
  if (bytes.length <= maximum) return { text: value, truncated: false }
  return { text: `${bytes.subarray(0, maximum - 80).toString('utf8')}\n… output truncated at ${maximum} bytes.`, truncated: true }
}
function safeUrl(value) {
  try { return truncate(new URL(value).toString(), 16384) } catch { return truncate(value || 'about:blank', 16384) }
}
function normalizeUrl(raw) {
  requireString(raw, 'url', 16384)
  const input = raw.trim()
  if (!input) throw new ProviderError('invalid_params')
  if (/^(javascript|vbscript):/i.test(input)) throw new ProviderError('invalid_url')
  if (input.startsWith('//')) return `https:${input}`
  if (/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?(?:\/|$)/i.test(input)) return `http://${input}`
  if (/^(?:[a-z\d.-]+|\[[a-f\d:]+\]):\d+(?:\/|$)/i.test(input)) return `https://${input}`
  if (!/^[a-z][a-z\d+.-]*:/i.test(input)) return `https://${input}`
  if (/^about:blank$/i.test(input)) return 'about:blank'
  try { return new URL(input).toString() } catch { throw new ProviderError('invalid_url') }
}
function comparableOrigin(url) {
  const protocol = url.protocol === 'ws:' ? 'http:' : url.protocol === 'wss:' ? 'https:' : url.protocol
  return `${protocol}//${url.host}`
}
function loopback(hostname) {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, '')
  return host === 'localhost' || host === '::1' || host.startsWith('127.')
}
function privateHostname(hostname) {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, '')
  if (loopback(host) || host === '0.0.0.0' || host === '::') return true
  if (/^10\./.test(host) || /^192\.168\./.test(host) || /^169\.254\./.test(host)) return true
  const match = /^172\.(\d+)\./.exec(host)
  if (match && Number(match[1]) >= 16 && Number(match[1]) <= 31) return true
  return /^(fc|fd|fe8|fe9|fea|feb)/i.test(host)
}
function protectedUrl(raw, protectedOrigins) {
  let url
  try { url = new URL(raw) } catch { return true }
  if (!['about:', 'blob:', 'chrome-error:', 'data:', 'filesystem:', 'http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return true
  if (url.protocol === 'about:' && url.href === 'about:blank') return false
  if (protectedOrigins.has(url.origin) || protectedOrigins.has(comparableOrigin(url))) return true
  for (const rawOrigin of protectedOrigins) {
    try {
      const origin = new URL(rawOrigin)
      const protocol = url.protocol === 'ws:' ? 'http:' : url.protocol === 'wss:' ? 'https:' : url.protocol
      if (privateHostname(origin.hostname) && privateHostname(url.hostname) && origin.protocol === protocol && origin.port === url.port && (origin.port || loopback(url.hostname))) return true
    } catch {}
  }
  return false
}
function allowedUrl(raw, protectedOrigins) {
  const normalized = normalizeUrl(raw)
  if (normalized === 'about:blank') return normalized
  let url
  try { url = new URL(normalized) } catch { throw new ProviderError('invalid_url') }
  if (!['http:', 'https:'].includes(url.protocol)) throw new ProviderError('invalid_url')
  if (protectedUrl(url.toString(), protectedOrigins)) throw new ProviderError('blocked_origin')
  return url.toString()
}

const NAMED_KEYS = {
  BACKSPACE: ['Backspace', 'Backspace', 8], DELETE: ['Delete', 'Delete', 46], END: ['End', 'End', 35],
  ENTER: ['Enter', 'Enter', 13], ESC: ['Escape', 'Escape', 27], ESCAPE: ['Escape', 'Escape', 27],
  HOME: ['Home', 'Home', 36], PAGEDOWN: ['PageDown', 'PageDown', 34], PAGEUP: ['PageUp', 'PageUp', 33],
  SPACE: ['Space', ' ', 32], TAB: ['Tab', 'Tab', 9], ARROWDOWN: ['ArrowDown', 'ArrowDown', 40],
  ARROWLEFT: ['ArrowLeft', 'ArrowLeft', 37], ARROWRIGHT: ['ArrowRight', 'ArrowRight', 39], ARROWUP: ['ArrowUp', 'ArrowUp', 38],
}
const MODIFIERS = { ALT: 1, CONTROL: 2, CTRL: 2, CMD: 4, COMMAND: 4, META: 4, SHIFT: 8 }
function parseKeyChord(chord) {
  requireString(chord, 'key', 128)
  const trimmed = chord.trim()
  if (!trimmed) throw new ProviderError('invalid_params')
  const pieces = trimmed === '+' ? ['+'] : trimmed.split('+').map((part) => part.trim())
  if (pieces.some((part) => !part)) throw new ProviderError('invalid_params')
  let modifiers = 0
  for (const piece of pieces.slice(0, -1)) {
    const modifier = MODIFIERS[piece.toUpperCase()]
    if (!modifier) throw new ProviderError('invalid_params')
    modifiers |= modifier
  }
  const raw = pieces.at(-1) || ''
  const named = NAMED_KEYS[raw.toUpperCase()]
  if (named) return { code: named[0], key: named[1], modifiers, ...(named[1] === ' ' && !(modifiers & 7) ? { text: ' ' } : {}), windowsVirtualKeyCode: named[2] }
  const functionKey = /^F(\d{1,2})$/i.exec(raw)
  if (functionKey && Number(functionKey[1]) >= 1 && Number(functionKey[1]) <= 24) {
    const number = Number(functionKey[1]); return { code: `F${number}`, key: `F${number}`, modifiers, windowsVirtualKeyCode: 111 + number }
  }
  if ([...raw].length !== 1) throw new ProviderError('invalid_params')
  const character = [...raw][0]
  const letter = /^[a-z]$/i.test(character), digit = /^\d$/.test(character), shifted = Boolean(modifiers & 8)
  const key = letter ? (shifted ? character.toUpperCase() : character.toLowerCase()) : character
  return { code: letter ? `Key${character.toUpperCase()}` : digit ? `Digit${character}` : character === '+' ? 'Equal' : '', key, modifiers, ...(!(modifiers & 7) ? { text: key } : {}), windowsVirtualKeyCode: letter ? character.toUpperCase().charCodeAt(0) : character.charCodeAt(0) }
}

function scalar(value) { return value?.value }
function stringValue(value) { const raw = scalar(value); return typeof raw === 'string' ? raw : raw === undefined ? '' : String(raw) }
function axProperty(node, name) { return scalar(node.properties?.find((item) => item.name === name)?.value) }
function cleanText(value, max = 240) { const text = value.replace(/\s+/g, ' ').trim(); return text.length <= max ? text : `${text.slice(0, max - 1)}…` }
const INTERACTIVE = new Set(['button', 'checkbox', 'combobox', 'gridcell', 'link', 'listbox', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'option', 'radio', 'scrollbar', 'searchbox', 'slider', 'spinbutton', 'switch', 'tab', 'textbox', 'treeitem'])
const HIDDEN = new Set(['InlineTextBox', 'none', 'presentation'])
const AX_PROPERTIES = ['checked', 'pressed', 'selected', 'expanded', 'disabled', 'readonly', 'required', 'focused', 'level', 'haspopup', 'invalid']
function formatAX(nodes, options) {
  const maxNodes = Math.max(1, Math.min(options.maxNodes ?? 300, 1000)), maxDepth = Math.max(1, Math.min(options.maxDepth ?? 30, 50))
  const byId = new Map(nodes.map((node) => [node.nodeId, node])), children = new Set(nodes.flatMap((node) => node.childIds || []))
  const roots = nodes.filter((node) => !node.parentId || !byId.has(node.parentId) || (!children.has(node.nodeId) && stringValue(node.role) === 'RootWebArea'))
  if (!roots.length && nodes[0]) roots.push(nodes[0])
  const lines = [], refs = new Map(), visited = new Set(); let includedNodes = 0, stopped = false
  const quote = (value) => `"${cleanText(value).replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  function visit(node, depth) {
    if (stopped || visited.has(node.nodeId)) return
    visited.add(node.nodeId)
    const role = stringValue(node.role), interactive = INTERACTIVE.has(role.toLowerCase()) || axProperty(node, 'focusable') === true || axProperty(node, 'editable') !== undefined
    const displayed = !node.ignored && !HIDDEN.has(role) && (interactive || role === 'RootWebArea' || role === 'WebArea' || (role === 'generic' ? Boolean(stringValue(node.name) || stringValue(node.value)) : Boolean(role)))
    const show = depth <= maxDepth && displayed && (!options.interactiveOnly || interactive)
    let childDepth = depth
    if (show) {
      if (includedNodes >= maxNodes) { stopped = true; return }
      let ref
      if (interactive && typeof node.backendDOMNodeId === 'number') { ref = `e${refs.size + 1}`; refs.set(ref, { backendNodeId: node.backendDOMNodeId }) }
      const parts = [role || 'node'], name = stringValue(node.name), value = stringValue(node.value), description = stringValue(node.description)
      if (name) parts.push(quote(name)); if (ref) parts.push(`[ref=${ref}]`); if (value && value !== name) parts.push(`[value=${quote(value)}]`); if (description && description !== name) parts.push(`[description=${quote(description)}]`)
      for (const key of AX_PROPERTIES) { const current = axProperty(node, key); if (current === undefined || current === false || current === 'false') continue; parts.push(current === true ? `[${key}]` : `[${key}=${cleanText(String(current), 80)}]`) }
      lines.push(`${'  '.repeat(Math.max(0, depth))}- ${parts.join(' ')}`); includedNodes++; childDepth = depth + 1
    }
    if (depth >= maxDepth) return
    for (const id of node.childIds || []) { const child = byId.get(id); if (child) visit(child, childDepth); if (stopped) return }
  }
  for (const root of roots) { visit(root, 0); if (stopped) break }
  for (const node of nodes) { if (stopped) break; if (!visited.has(node.nodeId)) visit(node, 0) }
  let omittedNodes = Math.max(0, nodes.length - visited.size)
  if (stopped) { omittedNodes = Math.max(omittedNodes, nodes.length - includedNodes); lines.push(`… ${omittedNodes} additional accessibility nodes omitted (maxNodes=${maxNodes}).`) }
  return { text: lines.join('\n') || '(The page exposed no accessibility nodes.)', refs, includedNodes, omittedNodes }
}

async function discoverChrome() {
  const candidates = [process.env.KIWI_CODE_CHROME_BIN]
  if (process.platform === 'darwin') candidates.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/Applications/Chromium.app/Contents/MacOS/Chromium', path.join(os.homedir(), 'Applications/Google Chrome.app/Contents/MacOS/Google Chrome'))
  if (process.platform === 'linux') candidates.push('/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser')
  if (process.platform === 'win32') candidates.push(path.join(process.env.PROGRAMFILES || '', 'Google/Chrome/Application/chrome.exe'), path.join(process.env['PROGRAMFILES(X86)'] || '', 'Google/Chrome/Application/chrome.exe'))
  for (const item of candidates.filter(Boolean)) {
    try { const stat = fs.statSync(item); if (stat.isFile() && (process.platform === 'win32' || (stat.mode & 0o111))) return item } catch {}
  }
  for (const directory of (process.env.PATH || '').split(path.delimiter)) {
    for (const name of process.platform === 'win32' ? ['chrome.exe'] : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser']) {
      const item = path.join(directory, name)
      try { const stat = fs.statSync(item); if (stat.isFile() && (process.platform === 'win32' || (stat.mode & 0o111))) return item } catch {}
    }
  }
  throw new ProviderError('chrome_unavailable')
}

class Session {
  constructor(manager, key, projectId, threadId, context) {
    this.manager = manager; this.key = key; this.projectId = projectId; this.threadId = threadId; this.context = context
    this.tabs = new Map(); this.currentTargetId = null; this.refs = new Map(); this.previewGeneration = 0; this.stopped = false
  }
  async pause(ms) {
    if (this.stopped) throw new ProviderError('session_not_found')
    await new Promise((resolve) => setTimeout(resolve, ms))
    if (this.stopped) throw new ProviderError('session_not_found')
  }
  selected(required = true) { const tab = this.currentTargetId ? this.tabs.get(this.currentTargetId) : null; if (!tab && required) throw new ProviderError('page_not_found'); return tab }
  matching(id) { requireString(id, 'targetId', 128); const matches = [...this.tabs.keys()].filter((key) => key === id || key.startsWith(id)); if (matches.length !== 1) throw new ProviderError('page_not_found'); return this.tabs.get(matches[0]) }
  async configurePage(page, select = true) {
    const id = randomBytes(12).toString('hex'), cdp = await page.createCDPSession(), tab = { id, page, cdp, loading: false, captureQueue: Promise.resolve() }
    this.tabs.set(id, tab); if (select) this.currentTargetId = id; this.refs.clear()
    page.on('dialog', (dialog) => void dialog.dismiss().catch(() => {}))
    page.on('request', (request) => { const block = protectedUrl(request.url(), this.manager.protectedOrigins); void (block ? request.abort('blockedbyclient') : request.continue()).catch(() => {}) })
    await page.setBypassServiceWorker(true)
    await page.setRequestInterception(true)
    page.on('load', () => { tab.loading = false })
    page.on('domcontentloaded', () => { tab.loading = false })
    page.on('popup', (popup) => { void this.adoptPopup(popup) })
    page.on('close', () => { this.tabs.delete(id); if (this.currentTargetId === id) this.currentTargetId = this.tabs.keys().next().value || null; this.refs.clear() })
    await Promise.all([cdp.send('Page.enable'), cdp.send('Runtime.enable'), cdp.send('DOM.enable')])
    return tab
  }
  async adoptPopup(page) {
    if (this.stopped || this.tabs.size >= MAX_TABS || protectedUrl(page.url(), this.manager.protectedOrigins)) { await page.close().catch(() => {}); return }
    try { await this.configurePage(page, true) } catch { await page.close().catch(() => {}) }
  }
  async createTab(raw = 'about:blank', select = true) {
    if (this.stopped) throw new ProviderError('session_not_found')
    if (this.tabs.size >= MAX_TABS) throw new ProviderError('tab_limit_reached')
    const url = allowedUrl(raw, this.manager.protectedOrigins), page = await this.context.newPage(), tab = await this.configurePage(page, select)
    try { if (url !== 'about:blank') { tab.loading = true; await page.goto(url, { waitUntil: 'load', timeout: DEFAULT_TIMEOUT_MS }) } }
    catch (error) { if (this.stopped) throw new ProviderError('session_not_found'); if (!page.url() || page.url() === 'about:blank') { await page.close().catch(() => {}); throw error } }
    return tab
  }
  async pageInfo(tab) { return { title: truncate(await tab.page.title().catch(() => ''), 2048), url: safeUrl(tab.page.url()) } }
  tabList() { return [...this.tabs.values()].map((tab) => ({ id: tab.id, title: truncate(tab.page._frameManager?.mainFrame()?.name?.() || '', 2048), type: 'page', url: safeUrl(tab.page.url()) })) }
  async status() {
    const pages = []
    for (const tab of this.tabs.values()) { const info = await this.pageInfo(tab); pages.push({ id: tab.id, title: info.title, type: 'page', url: info.url }) }
    const tab = this.selected(false), selected = pages.find((page) => page.id === this.currentTargetId)
    let current
    if (tab && selected) {
      let canGoBack = false, canGoForward = false
      try { const history = await tab.cdp.send('Page.getNavigationHistory'); canGoBack = history.currentIndex > 0; canGoForward = history.currentIndex + 1 < history.entries.length } catch {}
      current = { ...selected, canGoBack, canGoForward, loading: tab.loading }
    }
    return { ...this.manager.statusResult(true, pages, this.currentTargetId, current), ...this.manager.recordings.snapshot(this.projectId, this.threadId) }
  }
  async stop() { if (this.stopped) return; this.stopped = true; this.refs.clear(); this.tabs.clear(); this.currentTargetId = null; await this.context.close().catch(() => {}) }
  async tabsOperation(operation, params) {
    let message = 'Listed browser tabs.'
    if (operation === 'tabs.new') { const tab = await this.createTab(params.url === undefined ? 'about:blank' : requireString(params.url, 'url', 16384)); message = `Opened and selected tab ${tab.id}.` }
    else if (operation === 'tabs.select') { const tab = this.matching(params.targetId ?? params.id); this.currentTargetId = tab.id; this.refs.clear(); message = `Selected tab ${tab.id}.` }
    else if (operation === 'tabs.close') { const tab = params.targetId || params.id ? this.matching(params.targetId ?? params.id) : this.selected(); const id = tab.id; if (this.manager.recordings.isTarget(this.projectId, this.threadId, id)) throw new ProviderError('recording_active'); await tab.page.close({ runBeforeUnload: false }); message = `Closed tab ${id}.` }
    return { message, pages: (await this.status()).pages, currentTargetId: this.currentTargetId }
  }
  async navigate(operation, params) {
    const tab = this.selected(), action = operation.slice(9), timeout = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS)
    if (action === 'goto') { const url = allowedUrl(requireString(params.url, 'url', 16384), this.manager.protectedOrigins); tab.loading = true; await tab.page.goto(url, { waitUntil: 'load', timeout }) }
    else if (action === 'reload') { tab.loading = true; await tab.page.reload({ waitUntil: 'load', timeout }) }
    else {
      const history = await tab.cdp.send('Page.getNavigationHistory'), index = action === 'back' ? history.currentIndex - 1 : history.currentIndex + 1, entry = history.entries[index]
      if (!Number.isInteger(entry?.id)) throw new ProviderError('navigation_unavailable')
      if (protectedUrl(entry.url, this.manager.protectedOrigins)) throw new ProviderError('blocked_origin')
      tab.loading = true; await tab.cdp.send('Page.navigateToHistoryEntry', { entryId: entry.id }); await tab.page.waitForFunction(() => document.readyState === 'complete', { timeout })
    }
    tab.loading = false; this.refs.clear(); const info = await this.pageInfo(tab); return { action, targetId: tab.id, ...info }
  }
  async loader(tab) { const tree = await tab.cdp.send('Page.getFrameTree'); return tree?.frameTree?.frame?.loaderId }
  async withElement(tab, params, operation) {
    const ref = params.ref, selector = params.selector
    if ((typeof ref === 'string') === (typeof selector === 'string')) throw new ProviderError('invalid_params')
    let objectId
    try {
      if (typeof ref === 'string') {
        const stored = this.refs.get(ref); if (!stored || stored.targetId !== tab.id) throw new ProviderError('stale_ref')
        const loader = await this.loader(tab); if (stored.loaderId && loader && stored.loaderId !== loader) { this.refs.clear(); throw new ProviderError('stale_ref') }
        const result = await tab.cdp.send('DOM.resolveNode', { backendNodeId: stored.backendNodeId, objectGroup: 'kiwi-code-browser' }); objectId = result?.object?.objectId
      } else {
        requireString(selector, 'selector', 10000)
        const result = await tab.cdp.send('Runtime.evaluate', { expression: `document.querySelector(${JSON.stringify(selector)})`, objectGroup: 'kiwi-code-browser', returnByValue: false })
        if (result?.exceptionDetails) throw new Error('selector evaluation failed')
        if (result?.result?.subtype === 'null') throw new ProviderError('element_not_found')
        objectId = result?.result?.objectId
      }
      if (!objectId) throw new ProviderError('element_not_found')
      return await operation(objectId)
    } finally { if (objectId) await tab.cdp.send('Runtime.releaseObject', { objectId }).catch(() => {}) }
  }
  async call(tab, objectId, declaration, args = []) {
    const response = await tab.cdp.send('Runtime.callFunctionOn', { objectId, functionDeclaration: declaration, arguments: args.map((value) => ({ value })), awaitPromise: true, returnByValue: true, userGesture: true })
    if (response.exceptionDetails) throw new Error('page function failed')
    return response.result?.value
  }
  async snapshot(params) {
    const tab = this.selected(); await tab.cdp.send('Accessibility.enable')
    const [tree, loader, info] = await Promise.all([tab.cdp.send('Accessibility.getFullAXTree'), this.loader(tab), this.pageInfo(tab)])
    const formatted = formatAX(tree.nodes || [], { interactiveOnly: params.interactiveOnly === true, maxDepth: boundedInteger(params.maxDepth, 'maxDepth', 30, 1, 50), maxNodes: boundedInteger(params.maxNodes, 'maxNodes', 300, 1, 1000) })
    this.refs = new Map([...formatted.refs].map(([ref, value]) => [ref, { ...value, loaderId: loader, targetId: tab.id }]))
    const rendered = truncateUtf8([`Page: ${info.title || '(untitled)'}`, `URL: ${info.url}`, `Target: ${tab.id}`, '', formatted.text, '', `[${formatted.includedNodes} nodes shown; ${formatted.refs.size} actionable refs${formatted.omittedNodes ? `; ${formatted.omittedNodes} nodes omitted` : ''}.]`].join('\n'), SNAPSHOT_TEXT_BYTES)
    return { includedNodes: formatted.includedNodes, omittedNodes: formatted.omittedNodes, refs: formatted.refs.size, targetId: tab.id, text: rendered.text, title: info.title, truncated: rendered.truncated, url: info.url }
  }
  async click(params) {
    const tab = this.selected(), before = new Set(this.tabs.keys())
    await this.withElement(tab, params, async (objectId) => {
      const point = await this.call(tab, objectId, `function () { const el=this&&this.nodeType===1?this:this?.parentElement;if(!el)return{error:'not element'};if(el.disabled)return{error:'disabled'};el.scrollIntoView({block:'center',inline:'center',behavior:'instant'});const r=el.getBoundingClientRect(),s=getComputedStyle(el);if(r.width<=0||r.height<=0||s.visibility==='hidden'||s.display==='none')return{error:'not visible'};const x=r.left+r.width/2,y=r.top+r.height/2,top=document.elementFromPoint(x,y);if(top&&top!==el&&!el.contains(top)&&!top.contains(el))return{error:'covered'};return{x,y};}`)
      if (point?.error || typeof point?.x !== 'number' || typeof point?.y !== 'number') throw new Error('element cannot be clicked')
      const button = params.button ?? 'left'; if (!['left', 'middle', 'right'].includes(button)) throw new ProviderError('invalid_params')
      const clickCount = boundedInteger(params.clickCount, 'clickCount', 1, 1, 3), buttons = button === 'left' ? 1 : button === 'right' ? 2 : 4
      await tab.cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y })
      await tab.cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button, buttons, clickCount })
      await tab.cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button, buttons: 0, clickCount })
    })
    await this.pause(boundedInteger(params.waitMs, 'waitMs', 500, 0, 10000)); const info = await this.pageInfo(tab)
    return { clicked: params.ref ? `ref ${params.ref}` : `selector ${JSON.stringify(params.selector)}`, newTabs: (await this.status()).pages.filter((item) => !before.has(item.id)), targetId: tab.id, ...info }
  }
  async press(tab, chord) {
    const parsed = parseKeyChord(chord), base = { code: parsed.code, key: parsed.key, modifiers: parsed.modifiers, nativeVirtualKeyCode: parsed.windowsVirtualKeyCode, windowsVirtualKeyCode: parsed.windowsVirtualKeyCode }
    await tab.cdp.send('Input.dispatchKeyEvent', { ...base, type: parsed.text === undefined ? 'rawKeyDown' : 'keyDown', ...(parsed.text === undefined ? {} : { text: parsed.text }) }); await tab.cdp.send('Input.dispatchKeyEvent', { ...base, type: 'keyUp' }); return parsed
  }
  async fill(params) {
    const tab = this.selected(), text = requireString(params.text, 'text', MAX_TEXT_CHARS)
    await this.withElement(tab, params, async (objectId) => {
      const prepared = await this.call(tab, objectId, `function(clear){const el=this&&this.nodeType===1?this:this?.parentElement;if(!(el instanceof HTMLElement))return{error:'not editable'};if(el.matches(':disabled,[readonly]'))return{error:'disabled'};if(!(el instanceof HTMLInputElement||el instanceof HTMLTextAreaElement||el.isContentEditable))return{error:'not editable'};el.scrollIntoView({block:'center',inline:'center',behavior:'instant'});el.focus();if(clear){if(typeof el.select==='function')el.select();else{const range=document.createRange();range.selectNodeContents(el);const selection=getSelection();selection.removeAllRanges();selection.addRange(range)}}else if(typeof el.setSelectionRange==='function'){const end=String(el.value??'').length;el.setSelectionRange(end,end)}return{}}`, [params.clear !== false])
      if (prepared?.error) throw new Error('element cannot be filled')
      if (text) await tab.cdp.send('Input.insertText', { text }); else if (params.clear !== false) await this.press(tab, 'Backspace')
      if (params.submit === true) await this.press(tab, 'Enter')
    })
    await this.pause(params.submit === true ? 500 : 100); const info = await this.pageInfo(tab)
    return { filled: params.ref ? `ref ${params.ref}` : `selector ${JSON.stringify(params.selector)}`, submitted: params.submit === true, targetId: tab.id, textLength: text.length, ...info }
  }
  async key(params) { const tab = this.selected(), chord = requireString(params.key ?? params.chord, 'key', 128); await this.press(tab, chord); await this.pause(100); return { chord, targetId: tab.id, ...(await this.pageInfo(tab)) } }
  async wait(params) {
    const has = params.selector !== undefined || params.text !== undefined || params.urlContains !== undefined
    if (!has && params.timeMs === undefined) throw new ProviderError('invalid_params')
    if (params.state !== undefined && (params.selector === undefined || !['hidden', 'visible'].includes(params.state))) throw new ProviderError('invalid_params')
    const started = Date.now(), timeMs = boundedNumber(params.timeMs, 'timeMs', 0, 0, MAX_WAIT_MS), timeout = has ? boundedInteger(params.timeoutMs, 'timeoutMs', 10000, 100, MAX_WAIT_MS) : 0
    if (timeMs + timeout > MAX_WAIT_MS) throw new ProviderError('invalid_params'); if (timeMs) await this.pause(timeMs)
    const tab = this.selected()
    if (has) {
      const selector = params.selector === undefined ? null : requireString(params.selector, 'selector', 10000), text = params.text === undefined ? null : requireString(params.text, 'text', MAX_TEXT_CHARS), url = params.urlContains === undefined ? null : requireString(params.urlContains, 'urlContains', 16384), deadline = Date.now() + timeout
      let done = false
      while (Date.now() < deadline) {
        done = await tab.page.evaluate(({ selector, text, url, state }) => { let selectorMatches = true; if (selector !== null) { const element = document.querySelector(selector); let visible = false; if (element) { const rect = element.getBoundingClientRect(), style = getComputedStyle(element); visible = rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden' } selectorMatches = state === 'hidden' ? !visible : visible } return selectorMatches && (text === null || (document.body?.innerText || '').includes(text)) && (url === null || location.href.includes(url)) }, { selector, text, url, state: params.state ?? 'visible' }).catch(() => false)
        if (done) break; await this.pause(200)
      }
      if (!done) throw new ProviderError('wait_timeout')
    }
    return { elapsedMs: Date.now() - started, targetId: tab.id, ...(await this.pageInfo(tab)) }
  }
  async evaluate(params) {
    const tab = this.selected(), expression = requireString(params.expression ?? params.script, 'expression', MAX_EXPRESSION_CHARS), timeout = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS)
    const response = await Promise.race([tab.cdp.send('Runtime.evaluate', { awaitPromise: true, expression, returnByValue: true, userGesture: true }), new Promise((_, reject) => setTimeout(() => reject(new ProviderError('operation_timeout')), timeout))])
    if (response.exceptionDetails) throw new Error('evaluation failed')
    let result = Object.hasOwn(response.result || {}, 'value') ? response.result.value : response.result?.unserializableValue ?? (response.result?.type === 'undefined' ? undefined : response.result?.description)
    jsonSize(result); const info = await this.pageInfo(tab); return { result: result === undefined ? null : result, targetId: tab.id, ...info }
  }
  capturePage(tab, options) {
    const capture = tab.captureQueue.then(() => tab.page.screenshot(options))
    tab.captureQueue = capture.then(() => undefined, () => undefined)
    return capture
  }
  async capture(params, preview = false) {
    const tab = this.selected(false); if (!tab) throw new ProviderError(preview ? 'frame_unavailable' : 'page_not_found')
    const format = params.format ?? (preview ? 'jpeg' : 'png'); if (!['jpeg', 'png'].includes(format)) throw new ProviderError('invalid_params')
    if (preview && (format !== 'jpeg' || (params.quality ?? 70) !== 70)) throw new ProviderError('invalid_params')
    const quality = boundedInteger(params.quality, 'quality', preview ? 70 : 80, 0, 100), fullPage = !preview && params.fullPage === true
    try {
      const metrics = await tab.cdp.send('Page.getLayoutMetrics'), content = metrics.cssContentSize ?? metrics.contentSize, viewport = tab.page.viewport() || VIEWPORT
      const width = Math.round(fullPage ? content.width : viewport.width), height = Math.round(fullPage ? content.height : viewport.height)
      if (width <= 0 || height <= 0 || width > MAX_SCREENSHOT_DIMENSION || height > MAX_SCREENSHOT_DIMENSION || width * height > MAX_SCREENSHOT_PIXELS) throw new ProviderError('output_too_large')
      const data = await this.capturePage(tab, { type: format, ...(format === 'jpeg' ? { quality } : {}), fullPage, encoding: 'base64', optimizeForSpeed: preview })
      const bytes = Buffer.from(data, 'base64').length; if (bytes <= 0 || bytes > MAX_SCREENSHOT_BYTES) throw new ProviderError('output_too_large')
      if (preview) return { data, mimeType: 'image/jpeg', width, height, generation: ++this.previewGeneration, capturedAt: new Date().toISOString() }
      return { bytes, data, mimeType: format === 'jpeg' ? 'image/jpeg' : 'image/png', width, height, targetId: tab.id, ...(await this.pageInfo(tab)) }
    } catch (error) { if (preview && (!(error instanceof ProviderError) || error.code !== 'invalid_params')) throw new ProviderError('frame_unavailable'); throw error }
  }
  async cdp(params) {
    const tab = this.selected(), method = requireString(params.method, 'method', 256)
    if (!/^[A-Za-z][A-Za-z\d]*\.[A-Za-z][A-Za-z\d]*$/.test(method) || (params.target !== undefined && params.target !== 'page') || (params.params !== undefined && !record(params.params))) throw new ProviderError('invalid_params')
    const domain = method.slice(0, method.indexOf('.')); if (!ALLOWED_CDP_DOMAINS.has(domain) || BLOCKED_CDP_METHODS.has(method)) throw new ProviderError('blocked_command')
    const commandParams = params.params ? { ...params.params } : {}
    if (['Page.navigate', 'Fetch.continueRequest', 'Network.loadNetworkResource'].includes(method) && commandParams.url !== undefined) commandParams.url = allowedUrl(requireString(commandParams.url, 'params.url', 16384), this.manager.protectedOrigins)
    const timeout = boundedInteger(params.timeoutMs, 'timeoutMs', DEFAULT_TIMEOUT_MS, 100, MAX_WAIT_MS), result = await Promise.race([tab.cdp.send(method, commandParams), new Promise((_, reject) => setTimeout(() => reject(new ProviderError('operation_timeout')), timeout))])
    jsonSize(result); if (['Page.navigate', 'Page.reload', 'Page.navigateToHistoryEntry'].includes(method)) this.refs.clear(); return { method, result, target: 'page', targetId: tab.id }
  }
  async streamInput(params) {
    const tab = this.selected(), type = requireString(params.type, 'type', 32)
    if (!['viewport', 'focus', 'blur'].includes(type) && (!Number.isInteger(params.generation) || params.generation < this.previewGeneration - 2 || params.generation > this.previewGeneration)) throw new ProviderError('invalid_params')
    if (type === 'viewport') { const width = boundedInteger(params.width, 'width', 0, 200, 4096), height = boundedInteger(params.height, 'height', 0, 150, 4096); if (width * height > 16 * 1024 * 1024) throw new ProviderError('invalid_params'); await tab.page.setViewport({ width, height, deviceScaleFactor: 1 }); return { accepted: true } }
    if (type === 'text') { const text = requireString(params.text, 'text', MAX_TEXT_CHARS); await tab.cdp.send('Input.insertText', { text }); return { accepted: true } }
    if (type === 'focus') { await tab.page.bringToFront(); return { accepted: true } }
    if (type === 'blur') return { accepted: true }
    if (type === 'pointer' || type === 'wheel') {
      const x = boundedNumber(params.x, 'x', 0, 0, 16384), y = boundedNumber(params.y, 'y', 0, 0, 16384)
      if (type === 'wheel') { await tab.cdp.send('Input.dispatchMouseEvent', { type: 'mouseWheel', x, y, deltaX: boundedNumber(params.deltaX, 'deltaX', 0, -10000, 10000), deltaY: boundedNumber(params.deltaY, 'deltaY', 0, -10000, 10000), modifiers: boundedInteger(params.modifiers, 'modifiers', 0, 0, 15) }); return { accepted: true } }
      const eventType = params.event; if (!['mouseMoved', 'mousePressed', 'mouseReleased'].includes(eventType)) throw new ProviderError('invalid_params')
      const button = params.button ?? 'none'; if (!['none', 'left', 'middle', 'right', 'back', 'forward'].includes(button)) throw new ProviderError('invalid_params')
      await tab.cdp.send('Input.dispatchMouseEvent', { type: eventType, x, y, button, buttons: boundedInteger(params.buttons, 'buttons', 0, 0, 31), clickCount: boundedInteger(params.clickCount, 'clickCount', eventType === 'mouseMoved' ? 0 : 1, 0, 3), modifiers: boundedInteger(params.modifiers, 'modifiers', 0, 0, 15) }); return { accepted: true }
    }
    if (type === 'key') {
      const eventType = params.event; if (!['keyDown', 'rawKeyDown', 'keyUp'].includes(eventType)) throw new ProviderError('invalid_params')
      await tab.cdp.send('Input.dispatchKeyEvent', { type: eventType, key: requireString(params.key, 'key', 64), code: requireString(params.code ?? '', 'code', 64), modifiers: boundedInteger(params.modifiers, 'modifiers', 0, 0, 15), ...(typeof params.text === 'string' && params.text.length <= 16 ? { text: params.text } : {}) }); return { accepted: true }
    }
    throw new ProviderError('invalid_params')
  }
  async perform(operation, params) {
    if (operation === 'session.status') return this.status()
    if (['tabs.list', 'tabs.new', 'tabs.select', 'tabs.close'].includes(operation)) return this.tabsOperation(operation, params)
    if (operation.startsWith('navigate.')) return this.navigate(operation, params)
    if (operation === 'snapshot') return this.snapshot(params); if (operation === 'click') return this.click(params); if (operation === 'fill') return this.fill(params); if (operation === 'key') return this.key(params); if (operation === 'wait') return this.wait(params); if (operation === 'evaluate') return this.evaluate(params); if (operation === 'screenshot') return this.capture(params); if (operation === 'preview') return this.capture(params, true); if (operation === 'cdp') return this.cdp(params); if (operation === 'stream.input') return this.streamInput(params)
    throw new ProviderError('unsupported_operation')
  }
}

class Manager {
  constructor() { this.browser = null; this.browserStartup = null; this.closing = false; this.rootCDP = null; this.profile = null; this.sessions = new Map(); this.queues = new Map(); this.protectedOrigins = new Set(); this.recordings = new HeadlessRecordingManager(process.env.KIWI_CODE_BROWSER_RECORDINGS_DIR || '', (code) => new ProviderError(code)); try { for (const origin of JSON.parse(process.env.KIWI_CODE_PROTECTED_ORIGINS || '[]')) this.protectedOrigins.add(new URL(origin).origin) } catch {} }
  key(projectId, threadId) { return `${projectId}\0${threadId}` }
  async ensureBrowser() {
    if (this.closing) throw new ProviderError('session_not_found')
    if (this.browser?.connected) return
    if (!this.browserStartup) this.browserStartup = this.launchBrowser()
    try { await this.browserStartup } finally { this.browserStartup = null }
  }
  async launchBrowser() {
    const executablePath = await discoverChrome(); this.profile = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'kiwi-code-chrome-')); await fs.promises.chmod(this.profile, 0o700)
    const env = {}; for (const name of ['LANG', 'LC_ALL', 'PATH', 'TMPDIR', 'TEMP', 'TMP']) if (process.env[name]) env[name] = process.env[name]
    env.HOME = this.profile; env.XDG_CONFIG_HOME = this.profile; env.XDG_CACHE_HOME = this.profile
    try {
      this.browser = await puppeteer.launch({ executablePath, headless: true, pipe: true, dumpio: Boolean(process.env.KIWI_CODE_BROWSER_DEBUG), userDataDir: this.profile, defaultViewport: VIEWPORT, env, args: ['--disable-background-networking', '--disable-breakpad', '--disable-component-update', '--disable-crash-reporter', '--disable-crashpad-for-testing', '--disable-default-apps', '--disable-features=Translate,MediaRouter', '--disable-sync', '--metrics-recording-only', '--no-default-browser-check', '--no-first-run', `--crash-dumps-dir=${this.profile}`] })
      this.rootCDP = await this.browser.target().createCDPSession()
      this.browser.on('disconnected', () => { this.browser = null; this.rootCDP = null; this.sessions.clear() })
    } catch {
      await this.browser?.close().catch(() => {}); this.browser = null; this.rootCDP = null
      if (this.profile) await fs.promises.rm(this.profile, { recursive: true, force: true }).catch(() => {})
      this.profile = null
      throw new ProviderError('chrome_unavailable')
    }
  }
  statusResult(running, pages = [], currentTargetId = null, current) {
    return { message: running ? 'Headless Chrome browser session is running.' : 'Headless Chrome browser session is not running.', status: { endpoint: running ? 'kiwi-code://headless' : '', reachable: true, product: 'Headless Chrome', protocolVersion: '1.3', pages: pages.length, currentTargetId, owned: true, presentation: 'stream', capabilities: { nativeView: false, interactiveStream: true, preview: true, recording: true } }, backend: 'headless-chrome', presentation: 'stream', capabilities: { nativeView: false, interactiveStream: true, preview: true, recording: true }, running, pages, pageList: pages, currentTargetId, ...(current ? { current, currentPage: current } : {}) }
  }
  async createSession(action, key) { const context = await this.browser.createBrowserContext(); if (context.id) await this.rootCDP?.send('Browser.setDownloadBehavior', { behavior: 'deny', browserContextId: context.id }).catch(() => {}); const session = new Session(this, key, action.projectId, action.threadId, context); this.sessions.set(key, session); return session }
  async serialized(key, operation) { const previous = this.queues.get(key) || Promise.resolve(); const current = previous.catch(() => {}).then(operation); this.queues.set(key, current); try { return await current } finally { if (this.queues.get(key) === current) this.queues.delete(key) } }
  async perform(action) {
    if (!record(action) || typeof action.projectId !== 'string' || typeof action.threadId !== 'string' || typeof action.operation !== 'string' || (action.params !== undefined && !record(action.params))) throw new ProviderError('invalid_params')
    if (Array.isArray(action.protectedOrigins)) for (const origin of action.protectedOrigins) { try { this.protectedOrigins.add(new URL(origin).origin) } catch {} }
    await this.recordings.initialize()
    const key = this.key(action.projectId, action.threadId), params = action.params || {}
    if (action.operation === 'recording.status') { await this.recordings.prune(); return this.recordings.touch(action.projectId, action.threadId) }
    if (action.operation === 'recording.stop') return this.recordings.stop(action.projectId, action.threadId, params.recordingId)
    if (action.operation === 'recording.delete') return this.recordings.delete(action.projectId, action.threadId, params.recordingId)
    if (action.operation === 'session.status' && !this.sessions.has(key)) return { ...this.statusResult(false), ...this.recordings.snapshot(action.projectId, action.threadId) }
    await this.ensureBrowser()
    if (action.operation === 'preview') { const current = this.sessions.get(key); if (!current) throw new ProviderError('frame_unavailable'); return current.capture(params, true) }
    if (action.operation === 'session.stop') {
      const active = this.recordings.activeFor(action.projectId, action.threadId)
      if (active) await this.recordings.stop(action.projectId, action.threadId, active.id)
      const current = this.sessions.get(key); if (current) await current.stop()
    }
    return this.serialized(key, async () => {
      let session = this.sessions.get(key)
      if (action.operation === 'session.status') return session ? session.status() : { ...this.statusResult(false), ...this.recordings.snapshot(action.projectId, action.threadId) }
      if (action.operation === 'session.disconnect') return session ? { ...(await session.status()), message: 'Released the browser control connection; the session remains running.' } : { ...this.statusResult(false), ...this.recordings.snapshot(action.projectId, action.threadId), message: 'No headless browser connection was active.' }
      if (action.operation === 'session.stop') { if (session) { await session.stop(); this.sessions.delete(key) } return { ...this.statusResult(false), ...this.recordings.snapshot(action.projectId, action.threadId), stopped: Boolean(session), message: session ? 'Stopped headless Chrome browser session.' : 'No headless Chrome browser session was running.' } }
      const created = !session; if (!session) session = await this.createSession(action, key)
      try {
        if (action.operation === 'session.start') { if (!session.tabs.size) await session.createTab(params.url ?? 'about:blank'); return session.status() }
        if (created && action.operation !== 'tabs.new') await session.createTab('about:blank')
        if (action.operation === 'recording.start') {
          const tab = params.targetId ? session.matching(params.targetId) : session.selected()
          return this.recordings.start({ browser: this.browser, projectId: action.projectId, threadId: action.threadId, targetId: tab.id, title: params.title, sourcePage: tab.page, capturePage: () => session.capturePage(tab, { type: 'jpeg', quality: 80, encoding: 'base64', optimizeForSpeed: true }), idleTimeoutMs: params.idleTimeoutMs })
        }
        const result = await session.perform(action.operation, params)
        this.recordings.touch(action.projectId, action.threadId)
        return result
      } catch (error) { if (created && !session.tabs.size) { await session.stop(); this.sessions.delete(key) } throw error }
    })
  }
  async close() { this.closing = true; await this.browserStartup?.catch(() => {}); await this.recordings.dispose().catch(() => {}); for (const session of this.sessions.values()) await session.stop().catch(() => {}); this.sessions.clear(); await this.rootCDP?.detach().catch(() => {}); this.rootCDP = null; await this.browser?.close().catch(() => {}); this.browser = null; if (this.profile) await fs.promises.rm(this.profile, { recursive: true, force: true }).catch(() => {}); this.profile = null }
}

const manager = new Manager()
function send(value) { process.stdout.write(`${JSON.stringify(value)}\n`) }
const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
input.on('line', (line) => {
  void (async () => {
    let request
    try { request = JSON.parse(line) } catch { return }
    if (request?.type === 'shutdown') { await manager.close(); process.exit(0); return }
    if (!Number.isSafeInteger(request?.id)) return
    try { send({ id: request.id, ok: true, result: await manager.perform(request.action) }) }
    catch (error) { if (process.env.KIWI_CODE_BROWSER_DEBUG) console.error(error?.stack || error); send({ id: request.id, ok: false, error: { code: error instanceof ProviderError ? error.code : 'operation_failed' } }) }
  })()
})
input.on('close', () => { void manager.close().finally(() => process.exit(0)) })
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => { void manager.close().finally(() => process.exit(0)) })
