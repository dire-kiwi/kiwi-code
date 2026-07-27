import { useEffect, useRef, useState, type MutableRefObject } from 'react'
import type { ConnectionStatus } from '../types'
import { NATIVE_AGENT_RESPONSE_STALE_AFTER_MS } from './nativeAgentDiagnostics'

const RECONNECT_STABLE_AFTER_MS = 5_000
const INSPECTION_INTERVAL_MS = 4_000

export type NativeAgentSocketOptions<Event extends { type?: string }> = {
  agentName: string
  fatalEventType: string
  onActivity: (event: string, summary: string) => void
  onAttempt: () => void
  onContextReset: () => void
  onError: (message: string) => void
  onEvent: (event: Event, socket: WebSocket) => void
  onProbeSent: (at: number) => void
  onStatusChange: (status: ConnectionStatus) => void
  parse?: (data: string) => Event
  probeSentAtRef: MutableRefObject<number | null>
  readyEventType: string
  url: string
}

export function useNativeAgentSocket<Event extends { type?: string }>(
  options: NativeAgentSocketOptions<Event>,
) {
  const [connectionAttempt, setConnectionAttempt] = useState(0)
  const socketRef = useRef<WebSocket | null>(null)
  const reconnectAttemptsRef = useRef(0)
  const optionsRef = useRef(options)
  optionsRef.current = options

  useEffect(() => {
    let disposed = false
    let reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
    let stableTimer: ReturnType<typeof window.setTimeout> | undefined
    let inspectionTimer: ReturnType<typeof window.setInterval> | undefined
    let agentReady = false
    let reconnectAllowed = true
    const callbacks = optionsRef.current

    callbacks.onStatusChange('connecting')
    callbacks.onContextReset()
    callbacks.probeSentAtRef.current = null
    callbacks.onAttempt()

    const socket = new WebSocket(options.url)
    socketRef.current = socket

    socket.addEventListener('open', () => {
      if (disposed) {
        socket.close(1000, `Native ${options.agentName} pane closed`)
        return
      }
      stableTimer = window.setTimeout(() => {
        if (!disposed && socket.readyState === WebSocket.OPEN) reconnectAttemptsRef.current = 0
      }, RECONNECT_STABLE_AFTER_MS)
      inspectionTimer = window.setInterval(() => {
        const now = Date.now()
        const pendingSince = optionsRef.current.probeSentAtRef.current
        if (
          disposed
          || !agentReady
          || socket.readyState !== WebSocket.OPEN
          || (
            pendingSince !== null
            && now - pendingSince <= NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
          )
        ) return
        optionsRef.current.onProbeSent(now)
        socket.send(JSON.stringify({ type: 'get_state' }))
      }, INSPECTION_INTERVAL_MS)
    })

    socket.addEventListener('message', (messageEvent) => {
      if (disposed || typeof messageEvent.data !== 'string') return
      try {
        const current = optionsRef.current
        const event = current.parse
          ? current.parse(messageEvent.data)
          : JSON.parse(messageEvent.data) as Event
        if (event.type === options.readyEventType) agentReady = true
        if (event.type === options.fatalEventType) reconnectAllowed = false
        current.onEvent(event, socket)
      } catch {
        optionsRef.current.onError(`${options.agentName} sent an unreadable conversation update.`)
      }
    })

    socket.addEventListener('error', () => {
      if (disposed) return
      optionsRef.current.onActivity(
        'connection_error',
        `The native ${options.agentName} WebSocket reported an error.`,
      )
      optionsRef.current.onContextReset()
      optionsRef.current.onStatusChange('error')
    })

    socket.addEventListener('close', (event) => {
      agentReady = false
      if (stableTimer !== undefined) window.clearTimeout(stableTimer)
      if (inspectionTimer !== undefined) window.clearInterval(inspectionTimer)
      if (disposed) return
      const closeCode = typeof event.code === 'number' ? event.code : 1006
      const suppliedReason = typeof event.reason === 'string' ? event.reason.trim() : ''
      const closeReason = suppliedReason || (closeCode === 1006
        ? 'connection ended without a close frame'
        : 'no reason supplied')
      const closeDetail = `code ${closeCode}, ${closeReason}, ${event.wasClean ? 'clean' : 'unclean'}`
      console.info(`Native ${options.agentName} WebSocket closed.`, {
        code: closeCode,
        reason: suppliedReason,
        wasClean: Boolean(event.wasClean),
        reconnecting: reconnectAllowed,
        url: options.url,
      })
      if (!reconnectAllowed) {
        optionsRef.current.onActivity(
          'connection_closed',
          `Connection closed (${closeDetail}); automatic reconnect is disabled for this startup error.`,
        )
        optionsRef.current.onContextReset()
        optionsRef.current.onStatusChange('error')
        return
      }
      optionsRef.current.onActivity(
        'connection_closed',
        `Connection lost (${closeDetail}); Kiwi Code is reconnecting.`,
      )
      optionsRef.current.onContextReset()
      optionsRef.current.onStatusChange('connecting')
      const delay = Math.min(250 * 2 ** reconnectAttemptsRef.current, 2_000)
      reconnectAttemptsRef.current += 1
      reconnectTimer = window.setTimeout(() => {
        if (!disposed) setConnectionAttempt((value) => value + 1)
      }, delay)
    })

    return () => {
      disposed = true
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      if (stableTimer !== undefined) window.clearTimeout(stableTimer)
      if (inspectionTimer !== undefined) window.clearInterval(inspectionTimer)
      if (socket.readyState < WebSocket.CLOSING) socket.close(1000, `Native ${options.agentName} pane closed`)
      if (socketRef.current === socket) socketRef.current = null
      optionsRef.current.onContextReset()
    }
  }, [connectionAttempt, options.agentName, options.fatalEventType, options.probeSentAtRef, options.readyEventType, options.url])

  function send(command: Record<string, unknown> & { type: string }): boolean {
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      optionsRef.current.onError(`${options.agentName} is still connecting.`)
      return false
    }
    socket.send(JSON.stringify(command))
    return true
  }

  return { send, socketRef }
}
