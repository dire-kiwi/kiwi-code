import { Schema } from 'effect'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  StateSocketClient,
  type StateSocketLike,
} from './client'
import type { TopicDefinition } from './topics'

const SampleTopic: TopicDefinition<
  'sample',
  { readonly name: string },
  { readonly value: number }
> = {
  tag: 'sample',
  params: Schema.Struct({ name: Schema.String }),
  snapshot: Schema.Struct({ value: Schema.Number }),
  event: Schema.Never,
  key: ({ name }) => name,
  topic: ({ name }) => ({ tag: 'sample', name }),
}

class FakeSocket implements StateSocketLike {
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  readonly sent: string[] = []
  closeCode: number | undefined
  closeReason: string | undefined

  constructor(private readonly finishClientClose = true) {}

  send(data: string) {
    this.sent.push(data)
  }

  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  receive(value: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(value) }))
  }

  serverClose(code = 1006, reason = '') {
    this.finishClose(code, reason)
  }

  close(code = 1000, reason = '') {
    this.closeCode = code
    this.closeReason = reason
    if (this.finishClientClose) {
      this.finishClose(code, reason)
    } else {
      this.readyState = 2
    }
  }

  private finishClose(code: number, reason: string) {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.(new CloseEvent('close', { code, reason }))
  }
}

function sentMessages(socket: FakeSocket) {
  return socket.sent.map((message) => JSON.parse(message) as Record<string, unknown>)
}

function lastPing(socket: FakeSocket) {
  const message = sentMessages(socket).filter(({ t }) => t === 'ping').at(-1)
  if (!message || typeof message.ts !== 'number') throw new Error('Expected a ping message.')
  return message as { t: 'ping'; ts: number }
}

function ready(socket: FakeSocket, instanceId = 'instance-a') {
  socket.open()
  socket.receive({
    t: 'ready',
    protocol: 1,
    instanceId,
    serverTime: '2026-07-26T00:00:00Z',
  })
}

function createHarness(finishClientClose = true) {
  const sockets: FakeSocket[] = []
  const client = new StateSocketClient({
    url: 'ws://state.test/api/state',
    socketFactory: () => {
      const socket = new FakeSocket(finishClientClose)
      sockets.push(socket)
      return socket
    },
    random: () => 0.5,
  })
  return { client, sockets }
}

function cachedEntryCount(client: StateSocketClient) {
  return (client as unknown as { entries: Map<string, unknown> }).entries.size
}

const clients: StateSocketClient[] = []

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  for (const client of clients.splice(0)) client.dispose()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('StateSocketClient', () => {
  it('shares a channel by semantic topic key and unsubscribes after the last listener', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const first = client.observe(SampleTopic, { name: 'shared' })
    const second = client.observe(SampleTopic, { name: 'shared' })
    const unsubscribeFirst = first.subscribe(vi.fn())
    const unsubscribeSecond = second.subscribe(vi.fn())

    client.start()
    ready(sockets[0])

    expect(sentMessages(sockets[0])).toEqual([
      { t: 'open', protocol: 1, client: 'kiwi-code-web' },
      { t: 'sub', id: 1, topic: { tag: 'sample', name: 'shared' } },
    ])

    unsubscribeFirst()
    expect(sentMessages(sockets[0])).toHaveLength(2)
    unsubscribeSecond()
    expect(sentMessages(sockets[0]).at(-1)).toEqual({ t: 'unsub', id: 1 })
  })

  it('does not cache observers that are never enabled or subscribed', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'disabled' })
    expect(observer.getSnapshot()).toEqual({ state: 'loading' })
    observer.retry()
    expect(cachedEntryCount(client)).toBe(0)

    client.start()
    ready(sockets[0])
    expect(sentMessages(sockets[0])).toEqual([
      { t: 'open', protocol: 1, client: 'kiwi-code-web' },
    ])
    expect(cachedEntryCount(client)).toBe(0)
  })

  it('treats every later snapshot as an authoritative replacement', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'replace' })
    const listener = vi.fn()
    observer.subscribe(listener)
    client.start()
    ready(sockets[0])

    sockets[0].receive({ t: 'snap', id: 1, seq: 1, data: { value: 1 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 1 } })

    sockets[0].receive({ t: 'snap', id: 1, seq: 2, data: { value: 2 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 2 } })

    sockets[0].receive({ t: 'snap', id: 1, seq: 1, data: { value: 0 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 2 } })
    expect(listener).toHaveBeenCalledTimes(2)
  })

  it('surfaces a snapshot decode error and requests one automatic resnapshot', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'decode' })
    observer.subscribe(vi.fn())
    client.start()
    ready(sockets[0])

    sockets[0].receive({ t: 'snap', id: 1, seq: 1, data: { value: 'wrong' } })
    expect(observer.getSnapshot().state).toBe('error')
    expect(sentMessages(sockets[0]).filter(({ t }) => t === 'resnap')).toEqual([
      { t: 'resnap', id: 1 },
    ])

    sockets[0].receive({ t: 'snap', id: 1, seq: 2, data: { value: 'still wrong' } })
    expect(sentMessages(sockets[0]).filter(({ t }) => t === 'resnap')).toHaveLength(1)

    sockets[0].receive({ t: 'snap', id: 1, seq: 3, data: { value: 3 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 3 } })
  })

  it('rejects reserved event envelopes for snapshot-only v1 topics', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'event' })
    observer.subscribe(vi.fn())
    client.start()
    ready(sockets[0])

    sockets[0].receive({ t: 'event', id: 1, seq: 1, data: { value: 1 } })
    expect(observer.getSnapshot().state).toBe('error')
    expect(sentMessages(sockets[0]).at(-1)).toEqual({ t: 'resnap', id: 1 })
  })

  it.each([
    ['suberr', 'subscription rejected'],
    ['subend', 'resource disappeared'],
  ] as const)('surfaces %s and retries under a new channel id', (messageType, message) => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: messageType })
    observer.subscribe(vi.fn())
    client.start()
    ready(sockets[0])

    sockets[0].receive({
      t: messageType,
      id: 1,
      ...(messageType === 'suberr' ? { error: message } : { reason: message }),
    })
    const failed = observer.getSnapshot()
    expect(failed.state).toBe('error')
    if (failed.state === 'error') expect(failed.error.message).toBe(message)

    observer.retry()
    expect(sentMessages(sockets[0]).at(-1)).toEqual({
      t: 'sub',
      id: 2,
      topic: { tag: 'sample', name: messageType },
    })
    sockets[0].receive({ t: 'snap', id: 2, seq: 1, data: { value: 2 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 2 } })
  })

  it('reconnects with backoff and re-subscribes every live observer', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'reconnect' })
    observer.subscribe(vi.fn())
    const instances: Array<[string, string | undefined]> = []
    client.subscribeInstance((current, previous) => instances.push([current, previous]))
    client.start()
    ready(sockets[0], 'instance-a')
    sockets[0].receive({ t: 'snap', id: 1, seq: 1, data: { value: 1 } })

    sockets[0].serverClose()
    expect(client.getConnectionSnapshot().state).toBe('reconnecting')
    vi.advanceTimersByTime(249)
    expect(sockets).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(sockets).toHaveLength(2)

    ready(sockets[1], 'instance-b')
    expect(sentMessages(sockets[1])).toEqual([
      { t: 'open', protocol: 1, client: 'kiwi-code-web' },
      { t: 'sub', id: 1, topic: { tag: 'sample', name: 'reconnect' } },
    ])
    sockets[1].receive({ t: 'snap', id: 1, seq: 1, data: { value: 4 } })
    expect(observer.getSnapshot()).toEqual({ state: 'ready', data: { value: 4 } })
    expect(instances).toEqual([
      ['instance-a', undefined],
      ['instance-b', 'instance-a'],
    ])
  })

  it('keeps the connection alive when the server returns the matching pong', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    client.start()
    ready(sockets[0])

    vi.advanceTimersByTime(15_000)
    const ping = lastPing(sockets[0])
    vi.advanceTimersByTime(9_000)
    sockets[0].receive({ t: 'pong', ts: ping.ts })
    vi.advanceTimersByTime(1_000)

    expect(sockets[0].closeCode).toBeUndefined()
    expect(client.getConnectionSnapshot().state).toBe('open')
    expect(vi.getTimerCount()).toBe(1)
  })

  it('closes and reconnects when the matching pong misses its deadline', () => {
    const { client, sockets } = createHarness(false)
    clients.push(client)
    client.start()
    ready(sockets[0])

    vi.advanceTimersByTime(15_000)
    const ping = lastPing(sockets[0])
    sockets[0].receive({ t: 'pong', ts: ping.ts + 1 })
    vi.advanceTimersByTime(9_999)
    expect(sockets[0].closeCode).toBeUndefined()

    vi.advanceTimersByTime(1)
    expect(sockets[0].closeCode).toBe(4000)
    expect(sockets[0].closeReason).toBe('State connection ping timed out')
    expect(client.getConnectionSnapshot().state).toBe('reconnecting')

    vi.advanceTimersByTime(249)
    expect(sockets).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(sockets).toHaveLength(2)
  })

  it('does not let a stale pong deadline close or clear liveness for a newer socket', () => {
    const capturedPongDeadlines: Array<() => void> = []
    const setTimer = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      const callback = () => {
        if (typeof handler === 'function') handler(...args)
      }
      if (delay === 10_000) capturedPongDeadlines.push(callback)
      return window.setTimeout(callback, delay)
    }) as typeof window.setTimeout
    const sockets: FakeSocket[] = []
    const client = new StateSocketClient({
      url: 'ws://state.test/api/state',
      socketFactory: () => {
        const socket = new FakeSocket()
        sockets.push(socket)
        return socket
      },
      random: () => 0.5,
      setTimer,
    })
    clients.push(client)
    client.start()
    ready(sockets[0])

    vi.advanceTimersByTime(15_000)
    expect(capturedPongDeadlines).toHaveLength(1)
    sockets[0].serverClose()
    vi.advanceTimersByTime(250)
    ready(sockets[1])
    vi.advanceTimersByTime(15_000)
    expect(capturedPongDeadlines).toHaveLength(2)
    const currentPing = lastPing(sockets[1])

    capturedPongDeadlines[0]()
    expect(sockets[1].closeCode).toBeUndefined()
    sockets[1].receive({ t: 'pong', ts: currentPing.ts })
    vi.advanceTimersByTime(10_000)

    expect(sockets[1].closeCode).toBeUndefined()
    expect(client.getConnectionSnapshot().state).toBe('open')
  })

  it('clears ping intervals and pong deadlines when disposed', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    client.start()
    ready(sockets[0])
    vi.advanceTimersByTime(15_000)
    lastPing(sockets[0])
    expect(vi.getTimerCount()).toBe(2)

    client.dispose()
    expect(sockets[0].closeCode).toBe(1000)
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(60_000)
    expect(sockets).toHaveLength(1)
  })

  it('can stop and start again with live subscriptions, as React Strict Mode requires', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    const observer = client.observe(SampleTopic, { name: 'strict-mode' })
    observer.subscribe(vi.fn())

    client.start()
    ready(sockets[0])
    client.stop()
    expect(sockets[0].closeCode).toBe(1000)

    client.start()
    ready(sockets[1])
    expect(sentMessages(sockets[1]).at(-1)).toEqual({
      t: 'sub',
      id: 1,
      topic: { tag: 'sample', name: 'strict-mode' },
    })
  })

  it('does not reconnect after a protocol mismatch in ready', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    client.start()
    sockets[0].open()
    sockets[0].receive({
      t: 'ready',
      protocol: 2,
      instanceId: 'instance-a',
      serverTime: '2026-07-26T00:00:00Z',
    })

    expect(client.getConnectionSnapshot().state).toBe('incompatible')
    expect(sockets[0].closeCode).toBe(1002)
    vi.advanceTimersByTime(60_000)
    expect(sockets).toHaveLength(1)
  })

  it('recognizes the server close used for a pre-ready protocol mismatch', () => {
    const { client, sockets } = createHarness()
    clients.push(client)
    client.start()
    sockets[0].open()
    sockets[0].serverClose(1002, 'unsupported state protocol')

    expect(client.getConnectionSnapshot().state).toBe('incompatible')
    vi.advanceTimersByTime(60_000)
    expect(sockets).toHaveLength(1)
  })

  it('strictly rejects excess subscription parameters before opening a socket', () => {
    const { client } = createHarness()
    clients.push(client)
    expect(() => client.observe(
      SampleTopic,
      { name: 'extra', unexpected: true } as { name: string },
    )).toThrow(/unexpected/i)
  })
})
