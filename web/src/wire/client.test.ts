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
    this.finishClose(code, reason)
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

function ready(socket: FakeSocket, instanceId = 'instance-a') {
  socket.open()
  socket.receive({
    t: 'ready',
    protocol: 1,
    instanceId,
    serverTime: '2026-07-26T00:00:00Z',
  })
}

function createHarness() {
  const sockets: FakeSocket[] = []
  const client = new StateSocketClient({
    url: 'ws://state.test/api/state',
    socketFactory: () => {
      const socket = new FakeSocket()
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
