import { act, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { StateSocketClient, type StateSocketLike } from '../../wire/client'
import { StateSocketProvider } from '../../wire/react'
import { CleanupScreen } from './CleanupScreen'
import { SessionLogScreen } from './SessionLogScreen'

class FakeSocket implements StateSocketLike {
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  send() {}

  close(code = 1000, reason = '') {
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

const clients: StateSocketClient[] = []

afterEach(() => {
  for (const client of clients.splice(0)) client.dispose()
})

function renderWithSocket(component: ReactNode) {
  const socket = new FakeSocket()
  const client = new StateSocketClient({
    url: 'ws://state.test/api/state',
    socketFactory: () => socket,
  })
  clients.push(client)
  const view = render(
    <StateSocketProvider client={client}>
      {component}
    </StateSocketProvider>,
  )
  act(() => {
    socket.open()
    socket.receive({
      t: 'ready',
      protocol: 1,
      instanceId: 'retained-overview-instance',
      serverTime: '2026-07-26T00:00:00Z',
    })
  })
  return { socket, view }
}

describe('overview screens', () => {
  it('keeps the cleanup queue visible after its live subscription ends', () => {
    const { socket, view } = renderWithSocket(
      <CleanupScreen onOpenSidebar={() => {}} onBack={() => {}} />,
    )
    act(() => {
      socket.receive({
        t: 'snap',
        id: 1,
        seq: 1,
        data: {
          generatedAt: '2026-07-26T00:00:00Z',
          archivedThreadRetentionDays: 7,
          orphanedWorktreeRetentionDays: 7,
          threads: [],
          worktrees: [],
        },
      })
    })
    expect(screen.getAllByText('Archived threads')).toHaveLength(2)

    act(() => {
      socket.receive({ t: 'subend', id: 1, reason: 'Cleanup updates stopped.' })
    })
    expect(screen.getByText(/cleanup updates stopped.*last loaded queue is still shown/i)).toBeTruthy()
    expect(screen.getAllByText('Archived threads')).toHaveLength(2)
    view.unmount()
  })

  it('keeps the session log visible after its live subscription ends', () => {
    const { socket, view } = renderWithSocket(
      <SessionLogScreen onOpenSidebar={() => {}} onBack={() => {}} />,
    )
    act(() => {
      socket.receive({
        t: 'snap',
        id: 1,
        seq: 1,
        data: {
          generatedAt: '2026-07-26T00:00:00Z',
          inactivityHours: 24,
          events: [],
        },
      })
    })
    expect(screen.getByText('No automatic closures')).toBeTruthy()

    act(() => {
      socket.receive({ t: 'subend', id: 1, reason: 'Session updates stopped.' })
    })
    expect(screen.getByText(/session updates stopped.*last loaded log is still shown/i)).toBeTruthy()
    expect(screen.getByText('No automatic closures')).toBeTruthy()
    view.unmount()
  })
})
