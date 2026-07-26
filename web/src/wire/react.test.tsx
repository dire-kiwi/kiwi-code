import { Schema } from 'effect'
import { StrictMode, Suspense } from 'react'
import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { StateSocketClient, type StateSocketLike } from './client'
import {
  StateSocketProvider,
  useLastReadySubscriptionData,
  useSubscription,
} from './react'
import type { TopicDefinition } from './topics'

const SharedTopic: TopicDefinition<
  'shared',
  { readonly key: string },
  { readonly value: number }
> = {
  tag: 'shared',
  params: Schema.Struct({ key: Schema.String }),
  snapshot: Schema.Struct({ value: Schema.Number }),
  event: Schema.Never,
  key: ({ key }) => key,
  topic: ({ key }) => ({ tag: 'shared', key }),
}

class FakeWebSocket implements StateSocketLike {
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  readonly sent: string[] = []
  closed = false

  send(data: string) {
    this.sent.push(data)
  }

  close(code = 1000, reason = '') {
    if (this.closed) return
    this.closed = true
    this.readyState = 3
    this.onclose?.(new CloseEvent('close', { code, reason }))
  }

  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  receive(value: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(value) }))
  }
}

function Viewer({ label }: { label: string }) {
  const subscription = useSubscription(SharedTopic, { key: 'same' })
  return (
    <output data-testid={label}>
      {subscription.state === 'ready' ? subscription.data.value : subscription.state}
    </output>
  )
}

function RetainedViewer() {
  const subscription = useSubscription(SharedTopic, { key: 'retained' })
  const retained = useLastReadySubscriptionData(subscription)
  return (
    <output data-testid="retained">
      {subscription.state}:{retained?.value ?? 'none'}
    </output>
  )
}

function SuspendedViewer({ waiting }: { waiting: Promise<never> }): never {
  useSubscription(SharedTopic, { key: 'abandoned' })
  throw waiting
}

function cachedEntryCount(client: StateSocketClient) {
  return (client as unknown as { entries: Map<string, unknown> }).entries.size
}

const clients: StateSocketClient[] = []

afterEach(() => {
  for (const client of clients.splice(0)) client.dispose()
  vi.unstubAllGlobals()
})

describe('state socket React integration', () => {
  it('survives Strict Mode effect replay and shares one channel between hooks', () => {
    const sockets: FakeWebSocket[] = []
    vi.stubGlobal('WebSocket', class extends FakeWebSocket {
      constructor(_url: string) {
        super()
        sockets.push(this)
      }
    })

    const view = render(
      <StrictMode>
        <StateSocketProvider>
          <Viewer label="first" />
          <Viewer label="second" />
        </StateSocketProvider>
      </StrictMode>,
    )

    expect(sockets).toHaveLength(2)
    expect(sockets[0].closed).toBe(true)
    const activeSocket = sockets[1]
    act(() => {
      activeSocket.open()
      activeSocket.receive({
        t: 'ready',
        protocol: 1,
        instanceId: 'strict-mode-instance',
        serverTime: '2026-07-26T00:00:00Z',
      })
    })

    expect(activeSocket.sent.map((message) => JSON.parse(message))).toEqual([
      { t: 'open', protocol: 1, client: 'kiwi-code-web' },
      { t: 'sub', id: 1, topic: { tag: 'shared', key: 'same' } },
    ])

    act(() => {
      activeSocket.receive({ t: 'snap', id: 1, seq: 1, data: { value: 7 } })
    })
    expect(screen.getByTestId('first').textContent).toBe('7')
    expect(screen.getByTestId('second').textContent).toBe('7')

    view.unmount()
    expect(activeSocket.closed).toBe(true)
  })

  it('does not cache a subscription from an abandoned concurrent render', () => {
    const sockets: FakeWebSocket[] = []
    const client = new StateSocketClient({
      url: 'ws://state.test/api/state',
      socketFactory: () => {
        const socket = new FakeWebSocket()
        sockets.push(socket)
        return socket
      },
    })
    clients.push(client)
    const waiting = new Promise<never>(() => {})

    const view = render(
      <StateSocketProvider client={client}>
        <Suspense fallback={<span data-testid="fallback">Waiting</span>}>
          <SuspendedViewer waiting={waiting} />
        </Suspense>
      </StateSocketProvider>,
    )

    expect(screen.getByTestId('fallback').textContent).toBe('Waiting')
    expect(cachedEntryCount(client)).toBe(0)
    expect(sockets).toHaveLength(1)

    act(() => {
      sockets[0].open()
      sockets[0].receive({
        t: 'ready',
        protocol: 1,
        instanceId: 'abandoned-render-instance',
        serverTime: '2026-07-26T00:00:00Z',
      })
    })
    expect(sockets[0].sent.map((message) => JSON.parse(message))).toEqual([
      { t: 'open', protocol: 1, client: 'kiwi-code-web' },
    ])

    view.unmount()
    expect(cachedEntryCount(client)).toBe(0)
  })

  it('retains the last ready payload after a subscription ends', () => {
    const sockets: FakeWebSocket[] = []
    const client = new StateSocketClient({
      url: 'ws://state.test/api/state',
      socketFactory: () => {
        const socket = new FakeWebSocket()
        sockets.push(socket)
        return socket
      },
    })
    clients.push(client)

    const view = render(
      <StateSocketProvider client={client}>
        <RetainedViewer />
      </StateSocketProvider>,
    )
    act(() => {
      sockets[0].open()
      sockets[0].receive({
        t: 'ready',
        protocol: 1,
        instanceId: 'retained-instance',
        serverTime: '2026-07-26T00:00:00Z',
      })
      sockets[0].receive({ t: 'snap', id: 1, seq: 1, data: { value: 7 } })
    })
    expect(screen.getByTestId('retained').textContent).toBe('ready:7')

    act(() => {
      sockets[0].receive({ t: 'subend', id: 1, reason: 'Temporary read failure.' })
    })
    expect(screen.getByTestId('retained').textContent).toBe('error:7')

    view.unmount()
  })
})
