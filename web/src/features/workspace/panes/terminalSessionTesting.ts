import { vi } from 'vitest'

// Test doubles for TerminalSession. xterm needs canvas and layout support that
// jsdom does not have, so tests swap in these recorders through vi.mock and
// drive the terminal socket by hand.

type Disposable = { dispose: () => void }

const noopDisposable: Disposable = { dispose() {} }

export class FakeTerminal {
  static instances: FakeTerminal[] = []

  readonly options: Record<string, unknown>
  readonly addons: unknown[] = []
  readonly parser = { registerOscHandler: () => noopDisposable }
  cols = 80
  rows = 24
  textarea: HTMLTextAreaElement | undefined
  customKeyEventHandler: ((event: KeyboardEvent) => boolean) | undefined
  disposed = false

  constructor(options: Record<string, unknown> = {}) {
    this.options = { ...options }
    FakeTerminal.instances.push(this)
  }

  loadAddon(addon: unknown) {
    this.addons.push(addon)
  }

  open(host: HTMLElement) {
    const element = document.createElement('div')
    element.className = 'xterm'
    this.textarea = document.createElement('textarea')
    this.textarea.className = 'xterm-helper-textarea'
    element.append(this.textarea)
    host.append(element)
  }

  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean) {
    this.customKeyEventHandler = handler
  }

  onData() {
    return noopDisposable
  }

  onResize() {
    return noopDisposable
  }

  focus() {
    this.textarea?.focus()
  }

  hasSelection() {
    return false
  }

  getSelection() {
    return ''
  }

  clearSelection() {}

  write() {}

  paste() {}

  refresh() {}

  dispose() {
    this.disposed = true
    this.textarea?.closest('.xterm')?.remove()
  }
}

export class FakeFitAddon {
  fit() {}
}

export class FakeCanvasAddon {}

export class FakeWebLinksAddon {
  static instances: FakeWebLinksAddon[] = []

  constructor(readonly handler?: (event: MouseEvent, uri: string) => void) {
    FakeWebLinksAddon.instances.push(this)
  }
}

export class FakeTerminalSocket extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: FakeTerminalSocket[] = []

  readonly url: URL
  readyState = FakeTerminalSocket.CONNECTING
  binaryType = 'blob'
  readonly sent: unknown[] = []

  constructor(url: string | URL) {
    super()
    this.url = new URL(String(url))
    FakeTerminalSocket.instances.push(this)
  }

  send(data: unknown) {
    this.sent.push(data)
  }

  close() {
    this.readyState = FakeTerminalSocket.CLOSED
  }

  open() {
    this.readyState = FakeTerminalSocket.OPEN
    this.dispatchEvent(new Event('open'))
  }

  receive(data: unknown) {
    this.dispatchEvent(new MessageEvent('message', { data }))
  }

  ready() {
    this.receive(JSON.stringify({ type: 'terminal_ready', protocol: 2 }))
  }

  serverClose(code: number, reason = '') {
    this.readyState = FakeTerminalSocket.CLOSED
    this.dispatchEvent(Object.assign(new Event('close'), { code, reason, wasClean: true }))
  }
}

// Call from beforeEach; undo with vi.unstubAllGlobals() and
// restoreTerminalSessionDoubles() in afterEach.
export function installTerminalSessionDoubles() {
  FakeTerminal.instances.length = 0
  FakeWebLinksAddon.instances.length = 0
  FakeTerminalSocket.instances.length = 0
  vi.stubGlobal('WebSocket', FakeTerminalSocket)
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  })
  Object.defineProperty(document, 'fonts', {
    configurable: true,
    value: { ready: new Promise(() => {}) },
  })
}

export function restoreTerminalSessionDoubles() {
  Reflect.deleteProperty(document, 'fonts')
}
