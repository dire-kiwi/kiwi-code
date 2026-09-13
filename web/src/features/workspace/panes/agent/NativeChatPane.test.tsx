import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NativeChatPane } from './NativeChatPane'
import { emptyChat, reduceChat } from './nativeChat'
vi.mock('@/wire/react', () => ({ useSubscription: () => ({ state: 'loading' }) }))
vi.mock('@/ui/markdown', () => ({
  AgentMarkdown: ({ text }: { text: string }) => <div>{text}</div>,
}))
class Socket {
  static OPEN = 1
  static instances: Socket[] = []
  readyState = 1
  sent: object[] = []
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(readonly url: string) {
    Socket.instances.push(this)
  }
  send(raw: string) {
    this.sent.push(JSON.parse(raw))
  }
  close() {
    this.readyState = 3
    this.onclose?.()
  }
  receive(value: object) {
    act(() => this.onmessage?.({ data: JSON.stringify(value) }))
  }
}
const props = {
  provider: 'codex' as const,
  projectId: 'project',
  threadId: 'thread',
  threadTitle: 'Test',
  active: true,
  onStatusChange: vi.fn(),
}
const snapshot = {
  type: 'chat_snapshot',
  sequence: 0,
  value: { items: [], requests: [], working: false },
}
beforeEach(() => {
  Socket.instances = []
  vi.stubGlobal('WebSocket', Socket)
  localStorage.clear()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
describe('native chat', () => {
  it('preserves drafts on provider errors and clears them only after acknowledgement', async () => {
    render(<NativeChatPane {...props} />)
    const socket = Socket.instances[0]
    socket.receive(snapshot)
    fireEvent.change(screen.getByLabelText('Message Codex'), {
      target: { value: 'keep my prompt' },
    })
    fireEvent.click(screen.getByLabelText('Send message'))
    await waitFor(() => expect(socket.sent).toHaveLength(1))
    expect((screen.getByLabelText('Message Codex') as HTMLTextAreaElement).value).toBe(
      'keep my prompt',
    )
    socket.receive({ type: 'chat_error', message: 'Model unavailable' })
    expect(screen.getByText('Model unavailable')).toBeTruthy()
    expect((screen.getByLabelText('Message Codex') as HTMLTextAreaElement).value).toBe(
      'keep my prompt',
    )
    fireEvent.click(screen.getByLabelText('Send message'))
    await waitFor(() => expect(socket.sent).toHaveLength(2))
    socket.receive({ type: 'chat_sent' })
    expect((screen.getByLabelText('Message Codex') as HTMLTextAreaElement).value).toBe('')
  })
  it('reconnects with history without sending the initial prompt twice', () => {
    render(<NativeChatPane {...props} initialPrompt="hello" />)
    const first = Socket.instances[0]
    first.receive(snapshot)
    expect(first.sent).toHaveLength(1)
    act(() => first.close())
    fireEvent.click(screen.getByText('Reconnect'))
    const second = Socket.instances[1]
    second.receive({
      ...snapshot,
      value: { ...snapshot.value, items: [{ id: '1', kind: 'user', text: 'hello' }] },
    })
    expect(second.sent).toHaveLength(0)
    expect(screen.getByText('hello')).toBeTruthy()
  })
  it('renders approval and stop controls for the current request', () => {
    render(<NativeChatPane {...props} />)
    const socket = Socket.instances[0]
    socket.receive({
      ...snapshot,
      value: {
        ...snapshot.value,
        working: true,
        requests: [{ id: 5, kind: 'approval', title: 'Run command?', details: 'npm test' }],
      },
    })
    fireEvent.click(screen.getByText('Allow once'))
    expect(socket.sent[0]).toEqual({ type: 'respond', requestId: 5, decision: 'accept' })
    fireEvent.click(screen.getByLabelText('Stop Codex'))
    expect(socket.sent[1]).toEqual({ type: 'abort' })
  })
  it('ignores pre-snapshot events and replaces streamed items by identity', () => {
    const state = reduceChat(emptyChat, { ...snapshot, type: 'chat_snapshot', sequence: 3 })
    expect(
      reduceChat(state, {
        type: 'chat_item',
        sequence: 2,
        value: { id: 'a', kind: 'assistant', text: 'stale' },
      }),
    ).toBe(state)
    const first = reduceChat(state, {
      type: 'chat_item',
      sequence: 4,
      value: { id: 'a', kind: 'assistant', text: 'Hello' },
    })
    const next = reduceChat(first, {
      type: 'chat_item',
      sequence: 5,
      value: { id: 'a', kind: 'assistant', text: 'Hello world' },
    })
    expect(next.items).toEqual([{ id: 'a', kind: 'assistant', text: 'Hello world' }])
  })
})
