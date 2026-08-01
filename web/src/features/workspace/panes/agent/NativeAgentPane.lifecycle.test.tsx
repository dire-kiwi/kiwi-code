import {
  act,
  cleanup,
  fireEvent,
  screen,
} from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTestStore, renderWithStore } from '@/store/testing'
import { ClaudeNativePane } from './ClaudeNativePane'
import { PiNativePane } from './PiNativePane'

const mocks = vi.hoisted(() => ({
  uploadPiImage: vi.fn(),
}))

vi.mock('@/api', () => ({
  uploadPiImage: mocks.uploadPiImage,
}))

type NativePaneKind = 'Pi' | 'Claude'
type SentMessage = Record<string, unknown> & { type?: string }

const sockets: FakeWebSocket[] = []

class FakeWebSocket extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  readonly url: string
  readyState = FakeWebSocket.CONNECTING
  readonly sent: string[] = []
  readonly closeCalls: Array<{ code?: number; reason?: string }> = []

  constructor(url: string | URL) {
    super()
    this.url = String(url)
    sockets.push(this)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close(code?: number, reason?: string) {
    if (this.readyState === FakeWebSocket.CLOSED) return
    this.closeCalls.push({ code, reason })
    this.readyState = FakeWebSocket.CLOSED
    this.dispatchEvent(new Event('close'))
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.dispatchEvent(new Event('open'))
  }

  receive(message: unknown) {
    this.receiveRaw(JSON.stringify(message))
  }

  receiveRaw(data: unknown) {
    this.dispatchEvent(new MessageEvent('message', {
      data,
    }))
  }

  error() {
    this.dispatchEvent(new Event('error'))
  }

  serverClose() {
    this.readyState = FakeWebSocket.CLOSED
    this.dispatchEvent(new Event('close'))
  }
}

function messages(socket: FakeWebSocket): SentMessage[] {
  return socket.sent.map((message) => JSON.parse(message) as SentMessage)
}

function messagesOfType(socket: FakeWebSocket, type: string): SentMessage[] {
  return messages(socket).filter((message) => message.type === type)
}

function readyEvent(kind: NativePaneKind) {
  return { type: kind === 'Pi' ? 'pi_native_ready' : 'claude_native_ready' }
}

function fatalEvent(kind: NativePaneKind) {
  return {
    type: kind === 'Pi' ? 'pi_native_fatal' : 'claude_native_fatal',
    message: `${kind} could not start`,
  }
}

function nativeErrorEvent(kind: NativePaneKind) {
  return {
    type: kind === 'Pi' ? 'pi_native_error' : 'claude_native_error',
    message: `${kind} recoverable bridge error`,
  }
}

function stateEvent(kind: NativePaneKind) {
  return kind === 'Pi'
    ? {
        type: 'response',
        command: 'get_state',
        success: true,
        data: { isStreaming: false },
      }
    : {
        type: 'claude_native_state',
        isStreaming: false,
      }
}

function renderPane(kind: NativePaneKind) {
  const onStatusChange = vi.fn()
  const onContextStatusChange = vi.fn()
  const commonProps = {
    projectId: 'project/a',
    threadId: 'thread/b',
    threadTitle: `${kind} lifecycle test`,
    active: true,
    onStatusChange,
    onContextStatusChange,
  }
  const store = createTestStore()
  const view = kind === 'Pi'
    ? renderWithStore(<PiNativePane {...commonProps} />, { store })
    : renderWithStore(<ClaudeNativePane {...commonProps} />, { store })

  return { ...view, onStatusChange, onContextStatusChange }
}

function attachImage(name = 'screen.png') {
  const image = new File(['pixels'], name, { type: 'image/png' })
  const input = screen.getByLabelText('Attach images', {
    selector: 'input',
  }) as HTMLInputElement
  fireEvent.change(input, { target: { files: [image] } })
  return { image, input }
}

function writePrompt(message: string) {
  fireEvent.change(screen.getByTestId('pi-native-composer'), {
    target: { value: message },
  })
}

async function submitPrompt() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
    await Promise.resolve()
    await Promise.resolve()
  })
}

const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, 'createObjectURL')
const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL')

beforeEach(() => {
  vi.useFakeTimers()
  sockets.length = 0
  mocks.uploadPiImage.mockReset()
  mocks.uploadPiImage.mockResolvedValue({ path: '/uploaded/screenshot.png' })
  vi.stubGlobal('WebSocket', FakeWebSocket)
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: vi.fn(() => 'blob:characterization-test'),
  })
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: vi.fn(),
  })
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', originalCreateObjectURL)
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL')
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', originalRevokeObjectURL)
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL')
  }
})

describe.each<NativePaneKind>(['Pi', 'Claude'])('%sNativePane WebSocket lifecycle', (kind) => {
  it('backs off reconnects to the two-second cap and resets after a stable connection', () => {
    renderPane(kind)
    const reconnectDelays = [250, 500, 1_000, 2_000, 2_000]
    reconnectDelays.forEach((delay, index) => {
      const socket = sockets[index]
      expect(socket).toBeDefined()
      act(() => socket.serverClose())
      act(() => vi.advanceTimersByTime(delay - 1))
      expect(sockets).toHaveLength(index + 1)
      act(() => vi.advanceTimersByTime(1))
      expect(sockets).toHaveLength(index + 2)
    })

    const stable = sockets[reconnectDelays.length]
    act(() => {
      stable.open()
      stable.receive(readyEvent(kind))
      vi.advanceTimersByTime(5_000)
      stable.serverClose()
    })
    act(() => vi.advanceTimersByTime(249))
    expect(sockets).toHaveLength(reconnectDelays.length + 1)
    act(() => vi.advanceTimersByTime(1))
    expect(sockets).toHaveLength(reconnectDelays.length + 2)
  })

  it('does not reconnect after a fatal startup event', () => {
    const { onStatusChange } = renderPane(kind)
    const socket = sockets[0]

    act(() => {
      socket.open()
      socket.receive(fatalEvent(kind))
      socket.serverClose()
      vi.advanceTimersByTime(30_000)
    })

    expect(sockets).toHaveLength(1)
    expect(onStatusChange).toHaveBeenLastCalledWith('error')
    expect(screen.getByRole('alert').textContent).toContain(`${kind} could not start`)
  })

  it('probes on cadence and retries only after a pending probe becomes stale', () => {
    renderPane(kind)
    const socket = sockets[0]

    act(() => {
      socket.open()
      socket.receive(readyEvent(kind))
    })
    const readyProbeCount = kind === 'Claude' ? 1 : 0
    expect(messagesOfType(socket, 'get_state')).toHaveLength(readyProbeCount)

    act(() => socket.receive(stateEvent(kind)))
    act(() => vi.advanceTimersByTime(3_999))
    expect(messagesOfType(socket, 'get_state')).toHaveLength(readyProbeCount)
    act(() => vi.advanceTimersByTime(1))
    expect(messagesOfType(socket, 'get_state')).toHaveLength(readyProbeCount + 1)

    act(() => vi.advanceTimersByTime(12_000))
    expect(messagesOfType(socket, 'get_state')).toHaveLength(readyProbeCount + 1)
    act(() => vi.advanceTimersByTime(4_000))
    expect(messagesOfType(socket, 'get_state')).toHaveLength(readyProbeCount + 2)
  })

  it('surfaces malformed and native error frames and records socket errors', () => {
    const { onStatusChange } = renderPane(kind)
    const socket = sockets[0]
    act(() => {
      socket.open()
      socket.receive(readyEvent(kind))
      socket.receiveRaw('{not valid json')
    })

    expect(screen.getByRole('alert').textContent).toContain(
      `${kind} sent an unreadable conversation update.`,
    )
    expect(onStatusChange).toHaveBeenLastCalledWith('open')

    act(() => socket.receive(nativeErrorEvent(kind)))
    expect(screen.getByRole('alert').textContent).toContain(
      `${kind} recoverable bridge error`,
    )
    expect(onStatusChange).toHaveBeenLastCalledWith('open')

    act(() => socket.error())
    expect(onStatusChange).toHaveBeenLastCalledWith('error')
  })

  it('closes and ignores stale socket events after unmount', () => {
    const { unmount, onStatusChange } = renderPane(kind)
    const socket = sockets[0]
    act(() => socket.open())
    const statusCallsBeforeUnmount = onStatusChange.mock.calls.length

    unmount()
    const sentBeforeStaleEvents = socket.sent.length
    act(() => {
      socket.receive(readyEvent(kind))
      socket.receive(fatalEvent(kind))
      socket.serverClose()
      vi.advanceTimersByTime(30_000)
    })

    expect(socket.closeCalls).toContainEqual({
      code: 1000,
      reason: `Native ${kind} pane closed`,
    })
    expect(socket.sent).toHaveLength(sentBeforeStaleEvents)
    expect(sockets).toHaveLength(1)
    expect(onStatusChange).toHaveBeenCalledTimes(statusCallsBeforeUnmount)
  })
})

describe('PiNativePane prompt lifecycle', () => {
  it('uploads an image, sends the prompt payload, and aborts the active run', async () => {
    renderPane('Pi')
    const socket = sockets[0]
    act(() => {
      socket.open()
      socket.receive(readyEvent('Pi'))
    })

    const { image } = attachImage()
    writePrompt('Inspect this screenshot')
    await submitPrompt()

    expect(mocks.uploadPiImage).toHaveBeenCalledTimes(1)
    expect(mocks.uploadPiImage).toHaveBeenCalledWith(
      'project/a',
      image,
      expect.any(AbortSignal),
    )
    expect(messagesOfType(socket, 'prompt')).toContainEqual({
      type: 'prompt',
      message: 'Inspect this screenshot',
      images: [{ path: '/uploaded/screenshot.png' }],
    })

    act(() => socket.receive({ type: 'agent_start' }))
    fireEvent.click(screen.getByRole('button', { name: 'Stop Pi' }))
    expect(messagesOfType(socket, 'abort')).toEqual([{ type: 'abort' }])
  })

  it('restores composer controls after an image upload failure and allows retry', async () => {
    mocks.uploadPiImage
      .mockRejectedValueOnce(new Error('Image upload failed'))
      .mockResolvedValueOnce({ path: '/uploaded/retry.png' })
    renderPane('Pi')
    const socket = sockets[0]
    act(() => {
      socket.open()
      socket.receive(readyEvent('Pi'))
    })

    const { input } = attachImage('retry.png')
    writePrompt('Retry this image')
    await submitPrompt()

    expect(messagesOfType(socket, 'prompt')).toHaveLength(0)
    expect(screen.getByRole('alert').textContent).toContain('Image upload failed')
    expect(input.disabled).toBe(false)
    expect((screen.getByRole('button', { name: 'Send message' }) as HTMLButtonElement).disabled).toBe(false)

    await submitPrompt()
    expect(mocks.uploadPiImage).toHaveBeenCalledTimes(2)
    expect(messagesOfType(socket, 'prompt')).toEqual([{
      type: 'prompt',
      message: 'Retry this image',
      images: [{ path: '/uploaded/retry.png' }],
    }])
  })

  it('aborts an in-flight image upload and releases its preview on unmount', async () => {
    let uploadSignal: AbortSignal | undefined
    mocks.uploadPiImage.mockImplementation((
      _projectId: string,
      _image: Blob,
      signal?: AbortSignal,
    ) => new Promise<{ path: string }>((_resolve, reject) => {
      uploadSignal = signal
      signal?.addEventListener('abort', () => {
        reject(new DOMException('Upload aborted', 'AbortError'))
      }, { once: true })
    }))
    const { unmount } = renderPane('Pi')
    const socket = sockets[0]
    act(() => {
      socket.open()
      socket.receive(readyEvent('Pi'))
    })

    attachImage('pending.png')
    writePrompt('This upload will be cancelled')
    await submitPrompt()
    expect(uploadSignal?.aborted).toBe(false)
    expect(screen.getByRole('button', { name: 'Uploading images' })).toBeDefined()

    await act(async () => {
      unmount()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(uploadSignal?.aborted).toBe(true)
    expect(messagesOfType(socket, 'prompt')).toHaveLength(0)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:characterization-test')
  })
})

describe('ClaudeNativePane prompt lifecycle', () => {
  it('uploads an image, sends the Claude prompt payload, and aborts the active run', async () => {
    renderPane('Claude')
    const socket = sockets[0]
    act(() => {
      socket.open()
      socket.receive(readyEvent('Claude'))
    })

    const { image } = attachImage('claude.png')
    writePrompt('Claude, inspect this screenshot')
    await submitPrompt()

    expect(mocks.uploadPiImage).toHaveBeenCalledWith(
      'project/a',
      image,
      expect.any(AbortSignal),
    )
    expect(messagesOfType(socket, 'prompt')).toEqual([{
      type: 'prompt',
      message: 'Claude, inspect this screenshot',
      images: [{ path: '/uploaded/screenshot.png' }],
    }])

    act(() => socket.receive({
      type: 'stream_event',
      event: { type: 'message_start' },
    }))
    fireEvent.click(screen.getByRole('button', { name: 'Stop Claude' }))
    expect(messagesOfType(socket, 'abort')).toEqual([{ type: 'abort' }])
  })
})
