import { Either, ParseResult, Schema } from 'effect'
import { apiWebSocketUrl } from '@/apiUrl'
import {
  protocolVersion,
  ServerMessageSchema,
  wireClientName,
  type ServerMessage,
} from './protocol'
import type { TopicDefinition } from './topics'

const strictParseOptions = { onExcessProperty: 'error' } as const
const initialReconnectDelayMs = 250
const maximumReconnectDelayMs = 10_000
const pingIntervalMs = 15_000
const pongTimeoutMs = 10_000
const pongTimeoutCloseCode = 4000
const pongTimeoutCloseReason = 'State connection ping timed out'
const websocketOpenState = 1

export type SubscriptionState<Snapshot> =
  | { readonly state: 'loading' }
  | { readonly state: 'ready'; readonly data: Snapshot }
  | { readonly state: 'error'; readonly error: Error }

const loadingSubscriptionState = { state: 'loading' } as const

export type StateConnectionSnapshot =
  | { readonly state: 'connecting'; readonly instanceId?: string }
  | { readonly state: 'open'; readonly instanceId: string }
  | { readonly state: 'reconnecting'; readonly instanceId?: string }
  | { readonly state: 'error'; readonly instanceId?: string; readonly error: Error }
  | { readonly state: 'incompatible'; readonly instanceId?: string; readonly error: Error }

type Listener = () => void

export interface StateSocketLike {
  readonly readyState: number
  onopen: ((event: Event) => void) | null
  onmessage: ((event: MessageEvent) => void) | null
  onerror: ((event: Event) => void) | null
  onclose: ((event: CloseEvent) => void) | null
  send(data: string): void
  close(code?: number, reason?: string): void
}

export type StateSocketFactory = (url: string) => StateSocketLike

type InternalTopic = TopicDefinition<string, any, any>

type ChannelEntry = {
  readonly cacheKey: string
  readonly topic: InternalTopic
  readonly params: unknown
  readonly listeners: Set<Listener>
  state: SubscriptionState<unknown>
  channelId?: number
  sequence: number
  automaticResnapUsed: boolean
}

type PendingPing = {
  readonly generation: number
  readonly socket: StateSocketLike
  readonly timestamp: number
}

export type StateClientOptions = {
  readonly url?: string
  readonly socketFactory?: StateSocketFactory
  readonly random?: () => number
  readonly setTimer?: typeof window.setTimeout
  readonly clearTimer?: typeof window.clearTimeout
  readonly setInterval?: typeof window.setInterval
  readonly clearInterval?: typeof window.clearInterval
}

export interface TopicObserver<Snapshot> {
  readonly key: string
  getSnapshot(): SubscriptionState<Snapshot>
  subscribe(listener: Listener): () => void
  retry(): void
}

function parseError(error: ParseResult.ParseError, prefix: string) {
  const details = ParseResult.ArrayFormatter.formatErrorSync(error)
  const message = details.length > 0
    ? details.map((item) => `${item.path.length ? item.path.join('.') : '<root>'}: ${item.message}`).join('; ')
    : error.message
  return new Error(`${prefix}: ${message}`)
}

export function decodeStrict<A>(schema: Schema.Schema<A>, value: unknown, label: string): A {
  const result = Schema.decodeUnknownEither(schema, strictParseOptions)(value)
  if (Either.isRight(result)) return result.right
  throw parseError(result.left, label)
}

function websocketURL() {
  return apiWebSocketUrl('/api/state').toString()
}

function defaultSocketFactory(url: string) {
  return new WebSocket(url)
}

function messageText(data: unknown) {
  if (typeof data === 'string') return data
  return null
}

export class StateSocketClient {
  private readonly url: string
  private readonly socketFactory: StateSocketFactory
  private readonly random: () => number
  private readonly setTimer: typeof window.setTimeout
  private readonly clearTimer: typeof window.clearTimeout
  private readonly setIntervalTimer: typeof window.setInterval
  private readonly clearIntervalTimer: typeof window.clearInterval
  private readonly entries = new Map<string, ChannelEntry>()
  private readonly channels = new Map<number, ChannelEntry>()
  private readonly connectionListeners = new Set<Listener>()
  private readonly instanceListeners = new Set<(current: string, previous?: string) => void>()
  private socket: StateSocketLike | null = null
  private connection: StateConnectionSnapshot = { state: 'connecting' }
  private started = false
  private disposed = false
  private reconnectAllowed = true
  private ready = false
  private nextChannelId = 1
  private reconnectAttempt = 0
  private reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
  private pingTimer: ReturnType<typeof window.setInterval> | undefined
  private pongTimer: ReturnType<typeof window.setTimeout> | undefined
  private pendingPing: PendingPing | undefined
  private generation = 0

  constructor(options: StateClientOptions = {}) {
    this.url = options.url ?? websocketURL()
    this.socketFactory = options.socketFactory ?? defaultSocketFactory
    this.random = options.random ?? Math.random
    this.setTimer = options.setTimer ?? window.setTimeout.bind(window)
    this.clearTimer = options.clearTimer ?? window.clearTimeout.bind(window)
    this.setIntervalTimer = options.setInterval ?? window.setInterval.bind(window)
    this.clearIntervalTimer = options.clearInterval ?? window.clearInterval.bind(window)
  }

  start() {
    if (this.started || this.disposed) return
    this.started = true
    this.reconnectAllowed = true
    this.connection = {
      state: this.connection.instanceId ? 'reconnecting' : 'connecting',
      instanceId: this.connection.instanceId,
    }
    this.notifyConnection()
    this.connect()
  }

  stop() {
    if (!this.started) return
    this.started = false
    this.ready = false
    this.reconnectAllowed = false
    this.generation += 1
    if (this.reconnectTimer !== undefined) this.clearTimer(this.reconnectTimer)
    this.reconnectTimer = undefined
    this.clearLiveness()
    const socket = this.socket
    this.socket = null
    if (socket) {
      socket.onopen = null
      socket.onmessage = null
      socket.onerror = null
      socket.onclose = null
      socket.close(1000, 'State client disposed')
    }
    this.channels.clear()
    for (const entry of this.entries.values()) entry.channelId = undefined
  }

  dispose() {
    if (this.disposed) return
    this.stop()
    this.disposed = true
    this.entries.clear()
    this.connectionListeners.clear()
    this.instanceListeners.clear()
  }

  observe<Tag extends string, Params, Snapshot>(
    topic: TopicDefinition<Tag, Params, Snapshot>,
    params: Params,
  ): TopicObserver<Snapshot> {
    const validatedParams = decodeStrict(topic.params, params, `${topic.tag} subscription params`)
    const cacheKey = `${topic.tag}\u0000${topic.key(validatedParams)}`
    const ensureEntry = () => {
      let entry = this.entries.get(cacheKey)
      if (entry) return entry
      entry = {
        cacheKey,
        topic: topic as InternalTopic,
        params: validatedParams,
        listeners: new Set(),
        state: loadingSubscriptionState,
        sequence: 0,
        automaticResnapUsed: false,
      }
      this.entries.set(cacheKey, entry)
      return entry
    }
    return {
      key: cacheKey,
      getSnapshot: () => (
        this.entries.get(cacheKey)?.state ?? loadingSubscriptionState
      ) as SubscriptionState<Snapshot>,
      subscribe: (listener) => this.acquire(ensureEntry(), listener),
      retry: () => {
        const entry = this.entries.get(cacheKey)
        if (entry) this.retry(entry)
      },
    }
  }

  getConnectionSnapshot = () => this.connection

  subscribeConnection = (listener: Listener) => {
    this.connectionListeners.add(listener)
    return () => {
      this.connectionListeners.delete(listener)
    }
  }

  subscribeInstance(listener: (current: string, previous?: string) => void) {
    this.instanceListeners.add(listener)
    return () => {
      this.instanceListeners.delete(listener)
    }
  }

  waitForInstanceChange(instanceId: string, timeoutMs = 30_000) {
    const current = this.connection.instanceId
    if (current && current !== instanceId) return Promise.resolve(current)
    return new Promise<string>((resolve, reject) => {
      const timeout = this.setTimer(() => {
        unsubscribe()
        reject(new Error('Timed out waiting for Kiwi Code to restart.'))
      }, timeoutMs)
      const unsubscribe = this.subscribeInstance((next) => {
        if (next === instanceId) return
        this.clearTimer(timeout)
        unsubscribe()
        resolve(next)
      })
    })
  }

  private acquire(entry: ChannelEntry, listener: Listener) {
    const first = entry.listeners.size === 0
    entry.listeners.add(listener)
    if (first && this.ready) this.openChannel(entry)
    return () => {
      entry.listeners.delete(listener)
      if (entry.listeners.size !== 0) return
      if (entry.channelId !== undefined) {
        this.send({ t: 'unsub', id: entry.channelId })
        this.channels.delete(entry.channelId)
        entry.channelId = undefined
      }
      if (this.entries.get(entry.cacheKey) === entry) this.entries.delete(entry.cacheKey)
    }
  }

  private retry(entry: ChannelEntry) {
    entry.automaticResnapUsed = false
    if (!this.ready) {
      entry.state = { state: 'loading' }
      this.notifyEntry(entry)
      return
    }
    if (entry.channelId !== undefined) {
      this.send({ t: 'resnap', id: entry.channelId })
      return
    }
    entry.state = { state: 'loading' }
    this.notifyEntry(entry)
    this.openChannel(entry)
  }

  private connect() {
    if (this.disposed || !this.started) return
    const generation = ++this.generation
    this.ready = false
    this.channels.clear()
    this.nextChannelId = 1
    for (const entry of this.entries.values()) {
      entry.channelId = undefined
      entry.sequence = 0
      entry.automaticResnapUsed = false
    }

    let socket: StateSocketLike
    try {
      socket = this.socketFactory(this.url)
    } catch (reason) {
      this.connection = {
        state: 'error',
        instanceId: this.connection.instanceId,
        error: reason instanceof Error ? reason : new Error('Could not open the state connection.'),
      }
      this.notifyConnection()
      this.scheduleReconnect()
      return
    }
    this.socket = socket
    socket.onopen = () => {
      if (!this.current(generation, socket)) return
      socket.send(JSON.stringify({ t: 'open', protocol: protocolVersion, client: wireClientName }))
    }
    socket.onmessage = (event) => {
      if (!this.current(generation, socket)) return
      this.handleMessage(event.data)
    }
    socket.onerror = () => {
      if (!this.current(generation, socket)) return
      this.connection = {
        state: 'error',
        instanceId: this.connection.instanceId,
        error: new Error('The UI state connection encountered an error.'),
      }
      this.notifyConnection()
    }
    socket.onclose = (event) => {
      if (!this.current(generation, socket)) return
      this.socket = null
      this.ready = false
      this.clearLiveness()
      if (event.code === 1002 && event.reason === 'unsupported state protocol') {
        this.reconnectAllowed = false
        this.connection = {
          state: 'incompatible',
          instanceId: this.connection.instanceId,
          error: new Error('This Kiwi Code backend uses an incompatible UI-state protocol. Reload required.'),
        }
        this.notifyConnection()
        return
      }
      if (!this.reconnectAllowed && this.connection.state === 'incompatible') return
      this.connection = {
        state: 'reconnecting',
        instanceId: this.connection.instanceId,
      }
      this.notifyConnection()
      this.scheduleReconnect()
    }
  }

  private current(generation: number, socket: StateSocketLike) {
    return !this.disposed && generation === this.generation && this.socket === socket
  }

  private scheduleReconnect() {
    if (this.disposed || !this.started || !this.reconnectAllowed || this.reconnectTimer !== undefined) return
    const base = Math.min(initialReconnectDelayMs * 2 ** this.reconnectAttempt, maximumReconnectDelayMs)
    const jitter = 0.75 + this.random() * 0.5
    const delay = Math.max(1, Math.round(base * jitter))
    this.reconnectAttempt += 1
    this.reconnectTimer = this.setTimer(() => {
      this.reconnectTimer = undefined
      this.connect()
    }, delay)
  }

  private handleMessage(raw: unknown) {
    const text = messageText(raw)
    if (text === null) {
      this.failProtocol(new Error('The UI state socket received a non-text frame.'))
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      this.failProtocol(new Error('The UI state socket received malformed JSON.'))
      return
    }
    let message: ServerMessage
    try {
      message = decodeStrict(ServerMessageSchema, parsed, 'Invalid state message')
    } catch (reason) {
      this.failProtocol(reason instanceof Error ? reason : new Error('Invalid state message.'))
      return
    }

    switch (message.t) {
      case 'ready':
        this.handleReady(message)
        break
      case 'snap':
        this.handleSnapshot(message.id, message.seq, message.data)
        break
      case 'event':
        this.handleEvent(message.id, message.seq, message.data)
        break
      case 'suberr':
        this.endChannel(message.id, message.error)
        break
      case 'subend':
        this.endChannel(message.id, message.reason)
        break
      case 'pong':
        this.handlePong(message.ts)
        break
    }
  }

  private handleReady(message: Extract<ServerMessage, { t: 'ready' }>) {
    if (message.protocol !== protocolVersion) {
      const error = new Error(
        `Kiwi Code state protocol ${message.protocol} is incompatible with this UI (expected ${protocolVersion}). Reload required.`,
      )
      this.connection = {
        state: 'incompatible',
        instanceId: this.connection.instanceId,
        error,
      }
      this.notifyConnection()
      this.reconnectAllowed = false
      this.socket?.close(1002, 'Protocol mismatch')
      return
    }
    const previous = this.connection.instanceId
    this.ready = true
    this.reconnectAttempt = 0
    this.connection = { state: 'open', instanceId: message.instanceId }
    this.notifyConnection()
    if (previous !== message.instanceId) {
      for (const listener of this.instanceListeners) listener(message.instanceId, previous)
    }
    for (const entry of this.entries.values()) {
      if (entry.listeners.size > 0) this.openChannel(entry)
    }
    this.clearLiveness()
    this.pingTimer = this.setIntervalTimer(() => {
      this.ping()
    }, pingIntervalMs)
  }

  private ping() {
    const socket = this.socket
    if (!socket || !this.ready || this.pendingPing) return
    const pending: PendingPing = {
      generation: this.generation,
      socket,
      timestamp: Date.now(),
    }
    this.pendingPing = pending
    this.pongTimer = this.setTimer(() => {
      if (this.pendingPing !== pending) return
      this.pendingPing = undefined
      this.pongTimer = undefined
      if (!this.ready || !this.current(pending.generation, pending.socket)) return
      this.reconnectAfterPongTimeout(pending)
    }, pongTimeoutMs)
    this.send({ t: 'ping', ts: pending.timestamp })
  }

  private reconnectAfterPongTimeout(pending: PendingPing) {
    const socket = pending.socket
    this.socket = null
    this.ready = false
    this.generation += 1
    this.clearLiveness()
    socket.onopen = null
    socket.onmessage = null
    socket.onerror = null
    socket.onclose = null
    socket.close(pongTimeoutCloseCode, pongTimeoutCloseReason)
    this.connection = {
      state: 'reconnecting',
      instanceId: this.connection.instanceId,
    }
    this.notifyConnection()
    this.scheduleReconnect()
  }

  private handlePong(timestamp: number) {
    const pending = this.pendingPing
    if (
      !pending
      || pending.timestamp !== timestamp
      || !this.current(pending.generation, pending.socket)
    ) return
    this.clearPongDeadline()
  }

  private clearPongDeadline() {
    if (this.pongTimer !== undefined) this.clearTimer(this.pongTimer)
    this.pongTimer = undefined
    this.pendingPing = undefined
  }

  private clearLiveness() {
    if (this.pingTimer !== undefined) this.clearIntervalTimer(this.pingTimer)
    this.pingTimer = undefined
    this.clearPongDeadline()
  }

  private openChannel(entry: ChannelEntry) {
    if (!this.ready || entry.listeners.size === 0 || entry.channelId !== undefined) return
    if (this.nextChannelId > 0xffff_ffff) {
      this.failProtocol(new Error('The UI state channel id space was exhausted.'))
      return
    }
    const id = this.nextChannelId++
    entry.channelId = id
    entry.sequence = 0
    entry.automaticResnapUsed = false
    this.channels.set(id, entry)
    this.send({ t: 'sub', id, topic: entry.topic.topic(entry.params) })
  }

  private handleSnapshot(id: number, sequence: number, data: unknown) {
    const entry = this.channels.get(id)
    if (!entry || sequence <= entry.sequence) return
    entry.sequence = sequence
    try {
      const decoded = decodeStrict(entry.topic.snapshot, data, `${entry.topic.tag} snapshot`)
      entry.automaticResnapUsed = false
      entry.state = { state: 'ready', data: decoded }
    } catch (reason) {
      const error = reason instanceof Error ? reason : new Error(`Invalid ${entry.topic.tag} snapshot.`)
      entry.state = { state: 'error', error }
      if (import.meta.env.DEV) console.error(error)
      if (!entry.automaticResnapUsed) {
        entry.automaticResnapUsed = true
        this.send({ t: 'resnap', id })
      }
    }
    this.notifyEntry(entry)
  }

  private handleEvent(id: number, sequence: number, data: unknown) {
    const entry = this.channels.get(id)
    if (!entry || sequence <= entry.sequence) return
    entry.sequence = sequence
    let error: Error
    try {
      decodeStrict(entry.topic.event, data, `${entry.topic.tag} event`)
      error = new Error(`${entry.topic.tag} unexpectedly accepted an event in snapshot-only protocol v1.`)
    } catch (reason) {
      error = reason instanceof Error ? reason : new Error(`Invalid ${entry.topic.tag} event.`)
    }
    entry.state = { state: 'error', error }
    if (import.meta.env.DEV) console.error(error)
    if (!entry.automaticResnapUsed) {
      entry.automaticResnapUsed = true
      this.send({ t: 'resnap', id })
    }
    this.notifyEntry(entry)
  }

  private endChannel(id: number, message: string) {
    const entry = this.channels.get(id)
    if (!entry) return
    this.channels.delete(id)
    entry.channelId = undefined
    entry.state = { state: 'error', error: new Error(message) }
    this.notifyEntry(entry)
  }

  private failProtocol(error: Error) {
    this.connection = {
      state: 'error',
      instanceId: this.connection.instanceId,
      error,
    }
    this.notifyConnection()
    this.socket?.close(1002, 'Invalid state protocol')
  }

  private send(message: Record<string, unknown>) {
    const socket = this.socket
    if (!socket || socket.readyState !== websocketOpenState) return
    socket.send(JSON.stringify(message))
  }

  private notifyEntry(entry: ChannelEntry) {
    for (const listener of entry.listeners) listener()
  }

  private notifyConnection() {
    for (const listener of this.connectionListeners) listener()
  }
}
