'use strict'

const MAX_SNAPSHOT_NODES = 1_000
const MAX_SNAPSHOT_DEPTH = 50

class BrowserProviderError extends Error {
  constructor(code, message, status = 400, exposeMessage = true) {
    super(message)
    this.name = 'BrowserProviderError'
    this.code = code
    this.status = status
    this.exposeMessage = exposeMessage
  }
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function normalizeNavigationUrl(raw) {
  if (typeof raw !== 'string') throw new BrowserProviderError('invalid_params', 'url must be a string.')
  const input = raw.trim()
  if (!input) throw new BrowserProviderError('invalid_params', 'Navigation URL cannot be empty.')
  if (/^(javascript|vbscript):/i.test(input)) {
    throw new BrowserProviderError('invalid_url', 'javascript: and vbscript: URLs are blocked.')
  }
  if (input.startsWith('//')) return `https:${input}`
  if (/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?(?:\/|$)/i.test(input)) {
    return `http://${input}`
  }
  if (/^(?:[a-z\d.-]+|\[[a-f\d:]+\]):\d+(?:\/|$)/i.test(input)) {
    return `https://${input}`
  }
  if (!/^[a-z][a-z\d+.-]*:/i.test(input)) return `https://${input}`
  try {
    return new URL(input).toString()
  } catch {
    if (/^about:blank$/i.test(input)) return 'about:blank'
    throw new BrowserProviderError('invalid_url', 'Invalid navigation URL.')
  }
}

function navigationOrigin(raw) {
  try {
    const url = new URL(raw)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
    return url.origin
  } catch {
    return null
  }
}

function isLoopbackHostname(hostname) {
  const normalized = hostname.toLowerCase().replace(/^\[|\]$/g, '')
  return normalized === 'localhost' || normalized === '::1' || normalized.startsWith('127.')
}

function requireLoopbackHttpUrl(raw, name = 'URL') {
  let url
  try {
    url = new URL(raw)
  } catch {
    throw new BrowserProviderError('invalid_url', `${name} must be a valid URL.`)
  }
  if (
    (url.protocol !== 'http:' && url.protocol !== 'https:') ||
    !isLoopbackHostname(url.hostname) ||
    url.username ||
    url.password
  ) {
    throw new BrowserProviderError('invalid_url', `${name} must use HTTP or HTTPS on a loopback host.`)
  }
  return url
}

function isProtectedUrl(url, protectedOrigins) {
  const comparableProtocol = url.protocol === 'ws:' ? 'http:' : url.protocol === 'wss:' ? 'https:' : url.protocol
  const comparableOrigin = `${comparableProtocol}//${url.host}`
  if (protectedOrigins.has(url.origin) || protectedOrigins.has(comparableOrigin)) return true
  for (const protectedOrigin of protectedOrigins) {
    try {
      const protectedUrl = new URL(protectedOrigin)
      if (
        isLoopbackHostname(protectedUrl.hostname) &&
        protectedUrl.protocol === comparableProtocol &&
        protectedUrl.port === url.port &&
        (protectedUrl.port !== '' || isLoopbackHostname(url.hostname))
      ) return true
    } catch {
      // Ignore invalid entries; origins are validated before insertion.
    }
  }
  return false
}

function assertAllowedGuestUrl(raw, protectedOrigins) {
  const normalized = normalizeNavigationUrl(raw)
  if (normalized === 'about:blank') return normalized
  let url
  try {
    url = new URL(normalized)
  } catch {
    throw new BrowserProviderError('invalid_url', 'Invalid navigation URL.')
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new BrowserProviderError('invalid_url', 'Only HTTP and HTTPS navigation is allowed.')
  }
  if (isProtectedUrl(url, protectedOrigins)) {
    throw new BrowserProviderError('blocked_origin', 'Navigation to a protected Dire Mux origin is blocked.')
  }
  return url.toString()
}

const NAMED_KEYS = {
  BACKSPACE: { code: 'Backspace', key: 'Backspace', virtualKey: 8 },
  DELETE: { code: 'Delete', key: 'Delete', virtualKey: 46 },
  END: { code: 'End', key: 'End', virtualKey: 35 },
  ENTER: { code: 'Enter', key: 'Enter', virtualKey: 13 },
  ESC: { code: 'Escape', key: 'Escape', virtualKey: 27 },
  ESCAPE: { code: 'Escape', key: 'Escape', virtualKey: 27 },
  HOME: { code: 'Home', key: 'Home', virtualKey: 36 },
  PAGEDOWN: { code: 'PageDown', key: 'PageDown', virtualKey: 34 },
  PAGEUP: { code: 'PageUp', key: 'PageUp', virtualKey: 33 },
  SPACE: { code: 'Space', key: ' ', virtualKey: 32 },
  TAB: { code: 'Tab', key: 'Tab', virtualKey: 9 },
  ARROWDOWN: { code: 'ArrowDown', key: 'ArrowDown', virtualKey: 40 },
  ARROWLEFT: { code: 'ArrowLeft', key: 'ArrowLeft', virtualKey: 37 },
  ARROWRIGHT: { code: 'ArrowRight', key: 'ArrowRight', virtualKey: 39 },
  ARROWUP: { code: 'ArrowUp', key: 'ArrowUp', virtualKey: 38 },
}

const MODIFIERS = { ALT: 1, CONTROL: 2, CTRL: 2, CMD: 4, COMMAND: 4, META: 4, SHIFT: 8 }

function parseKeyChord(chord) {
  if (typeof chord !== 'string') throw new BrowserProviderError('invalid_params', 'key must be a string.')
  const trimmed = chord.trim()
  if (!trimmed) throw new BrowserProviderError('invalid_params', 'Key chord cannot be empty.')
  const pieces = trimmed === '+' ? ['+'] : trimmed.split('+').map((part) => part.trim())
  if (pieces.some((part) => !part)) {
    throw new BrowserProviderError('invalid_params', `Invalid key chord ${JSON.stringify(chord)}.`)
  }
  let modifiers = 0
  for (const piece of pieces.slice(0, -1)) {
    const modifier = MODIFIERS[piece.toUpperCase()]
    if (!modifier) throw new BrowserProviderError('invalid_params', `Unknown key modifier ${piece}.`)
    modifiers |= modifier
  }
  const rawKey = pieces.at(-1) || ''
  const named = NAMED_KEYS[rawKey.toUpperCase()]
  if (named) {
    return {
      code: named.code,
      key: named.key,
      modifiers,
      ...(named.key === ' ' && (modifiers & 7) === 0 ? { text: ' ' } : {}),
      windowsVirtualKeyCode: named.virtualKey,
    }
  }
  const functionKey = /^F(\d{1,2})$/i.exec(rawKey)
  if (functionKey) {
    const number = Number(functionKey[1])
    if (number >= 1 && number <= 24) {
      return { code: `F${number}`, key: `F${number}`, modifiers, windowsVirtualKeyCode: 111 + number }
    }
  }
  if ([...rawKey].length !== 1) {
    throw new BrowserProviderError('invalid_params', `Unknown key ${rawKey}.`)
  }
  const character = [...rawKey][0] || ''
  const letter = /^[a-z]$/i.test(character)
  const digit = /^\d$/.test(character)
  const shifted = (modifiers & 8) !== 0
  const key = letter ? (shifted ? character.toUpperCase() : character.toLowerCase()) : character
  const code = letter ? `Key${character.toUpperCase()}` : digit ? `Digit${character}` : character === '+' ? 'Equal' : ''
  return {
    code,
    key,
    modifiers,
    ...((modifiers & 7) === 0 ? { text: key } : {}),
    windowsVirtualKeyCode: letter ? character.toUpperCase().charCodeAt(0) : character.charCodeAt(0),
  }
}

function validateBounds(value) {
  if (!isRecord(value)) throw new BrowserProviderError('invalid_bounds', 'bounds must be an object.')
  const result = {}
  for (const key of ['x', 'y', 'width', 'height']) {
    const number = value[key]
    if (typeof number !== 'number' || !Number.isFinite(number) || number < 0) {
      throw new BrowserProviderError('invalid_bounds', `bounds.${key} must be a finite nonnegative number.`)
    }
    result[key] = Math.round(number)
  }
  return result
}

function clampBounds(bounds, contentSize) {
  const width = Math.max(0, Math.floor(contentSize.width || 0))
  const height = Math.max(0, Math.floor(contentSize.height || 0))
  const x = Math.min(bounds.x, width)
  const y = Math.min(bounds.y, height)
  return {
    x,
    y,
    width: Math.min(bounds.width, width - x),
    height: Math.min(bounds.height, height - y),
  }
}

function validateActionBody(value) {
  if (!isRecord(value)) throw new BrowserProviderError('invalid_request', 'Request body must be a JSON object.')
  const allowed = new Set(['projectId', 'threadId', 'operation', 'params'])
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new BrowserProviderError('invalid_request', `Unknown request field ${key}.`)
  }
  for (const key of ['projectId', 'threadId', 'operation']) {
    if (typeof value[key] !== 'string' || value[key].length < 1 || value[key].length > 256) {
      throw new BrowserProviderError('invalid_request', `${key} must be a nonempty string of at most 256 characters.`)
    }
  }
  if (value.params !== undefined && !isRecord(value.params)) {
    throw new BrowserProviderError('invalid_request', 'params must be a JSON object.')
  }
  return {
    projectId: value.projectId,
    threadId: value.threadId,
    operation: value.operation,
    params: value.params || {},
  }
}

function scalar(value) {
  return value?.value
}

function stringValue(value) {
  const raw = scalar(value)
  return typeof raw === 'string' ? raw : raw === undefined ? '' : String(raw)
}

function axProperty(node, name) {
  return scalar(node.properties?.find((item) => item.name === name)?.value)
}

function cleanText(value, maxLength = 240) {
  const compact = value.replace(/\s+/g, ' ').trim()
  return compact.length <= maxLength ? compact : `${compact.slice(0, maxLength - 1)}…`
}

const INTERACTIVE_ROLES = new Set([
  'button', 'checkbox', 'combobox', 'gridcell', 'link', 'listbox', 'menuitem',
  'menuitemcheckbox', 'menuitemradio', 'option', 'radio', 'scrollbar', 'searchbox',
  'slider', 'spinbutton', 'switch', 'tab', 'textbox', 'treeitem',
])
const HIDDEN_ROLES = new Set(['InlineTextBox', 'none', 'presentation'])
const DISPLAYED_PROPERTIES = [
  'checked', 'pressed', 'selected', 'expanded', 'disabled', 'readonly', 'required',
  'focused', 'level', 'haspopup', 'invalid',
]

function formatAccessibilityTree(nodes, options = {}) {
  const maxNodes = Math.max(1, Math.min(options.maxNodes ?? 300, MAX_SNAPSHOT_NODES))
  const maxDepth = Math.max(1, Math.min(options.maxDepth ?? 30, MAX_SNAPSHOT_DEPTH))
  const byId = new Map(nodes.map((node) => [node.nodeId, node]))
  const referencedChildren = new Set(nodes.flatMap((node) => node.childIds || []))
  const roots = nodes.filter((node) => !node.parentId || !byId.has(node.parentId) || (!referencedChildren.has(node.nodeId) && stringValue(node.role) === 'RootWebArea'))
  if (roots.length === 0 && nodes[0]) roots.push(nodes[0])
  const lines = []
  const refs = new Map()
  const visited = new Set()
  let includedNodes = 0
  let stopped = false

  function quote(value) {
    return `"${cleanText(value).replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  }
  function visit(node, depth) {
    if (stopped || visited.has(node.nodeId)) return
    visited.add(node.nodeId)
    const role = stringValue(node.role)
    const interactive = INTERACTIVE_ROLES.has(role.toLowerCase()) || axProperty(node, 'focusable') === true || axProperty(node, 'editable') !== undefined
    const displayed = !node.ignored && !HIDDEN_ROLES.has(role) && (
      interactive || role === 'RootWebArea' || role === 'WebArea' ||
      (role === 'generic' ? stringValue(node.name).length > 0 || stringValue(node.value).length > 0 : role.length > 0)
    )
    const show = depth <= maxDepth && displayed && (!options.interactiveOnly || interactive)
    let childDepth = depth
    if (show) {
      if (includedNodes >= maxNodes) { stopped = true; return }
      let ref
      if (interactive && typeof node.backendDOMNodeId === 'number') {
        ref = `e${refs.size + 1}`
        refs.set(ref, { backendNodeId: node.backendDOMNodeId, name: cleanText(stringValue(node.name)), role })
      }
      const parts = [role || 'node']
      const name = stringValue(node.name)
      const value = stringValue(node.value)
      const description = stringValue(node.description)
      if (name) parts.push(quote(name))
      if (ref) parts.push(`[ref=${ref}]`)
      if (value && value !== name) parts.push(`[value=${quote(value)}]`)
      if (description && description !== name) parts.push(`[description=${quote(description)}]`)
      for (const key of DISPLAYED_PROPERTIES) {
        const current = axProperty(node, key)
        if (current === undefined || current === false || current === 'false') continue
        parts.push(current === true ? `[${key}]` : `[${key}=${cleanText(String(current), 80)}]`)
      }
      lines.push(`${'  '.repeat(Math.max(0, depth))}- ${parts.join(' ')}`)
      includedNodes += 1
      childDepth = depth + 1
    }
    if (depth >= maxDepth) return
    for (const childId of node.childIds || []) {
      const child = byId.get(childId)
      if (child) visit(child, childDepth)
      if (stopped) return
    }
  }
  for (const root of roots) { visit(root, 0); if (stopped) break }
  for (const node of nodes) { if (stopped) break; if (!visited.has(node.nodeId)) visit(node, 0) }
  let omittedNodes = Math.max(0, nodes.length - visited.size)
  if (stopped) {
    omittedNodes = Math.max(omittedNodes, nodes.length - includedNodes)
    lines.push(`… ${omittedNodes} additional accessibility nodes omitted (maxNodes=${maxNodes}).`)
  }
  return {
    includedNodes,
    omittedNodes,
    refs,
    text: lines.join('\n') || '(The page exposed no accessibility nodes.)',
  }
}

module.exports = {
  BrowserProviderError,
  assertAllowedGuestUrl,
  clampBounds,
  formatAccessibilityTree,
  isProtectedUrl,
  isRecord,
  navigationOrigin,
  normalizeNavigationUrl,
  parseKeyChord,
  requireLoopbackHttpUrl,
  validateActionBody,
  validateBounds,
}
