import { describe, expect, it } from 'vitest'
import {
  formatNativeActivityAge,
  nativeConnectionDescription,
  nativeResponseDescription,
  NATIVE_AGENT_RESPONSE_STALE_AFTER_MS,
} from './nativeAgentDiagnostics'

describe('native agent diagnostics', () => {
  it('describes transport and response freshness consistently', () => {
    expect(nativeConnectionDescription('open')).toBe('WebSocket connected')
    expect(nativeConnectionDescription('connecting')).toBe('WebSocket reconnecting')
    expect(nativeConnectionDescription('error')).toBe('WebSocket error')
    expect(nativeConnectionDescription('closed')).toBe('WebSocket closed')

    expect(nativeResponseDescription('Pi', null, null, 20_000)).toBe(
      'Waiting for a connection',
    )
    expect(nativeResponseDescription('Pi', null, 8_000, 20_000)).toBe(
      'Waiting for the first Pi response',
    )
    expect(nativeResponseDescription('bridge', null, 7_999, 20_000)).toBe(
      'No bridge response for 12s',
    )
    expect(nativeResponseDescription(
      'Pi',
      NATIVE_AGENT_RESPONSE_STALE_AFTER_MS,
      0,
      20_000,
    )).toBe('Responded 12s ago')
    expect(nativeResponseDescription(
      'Pi',
      NATIVE_AGENT_RESPONSE_STALE_AFTER_MS + 1_000,
      0,
      20_000,
    )).toBe('Last response 13s ago')
  })

  it('formats very recent activity without a zero-second duration', () => {
    expect(formatNativeActivityAge(-10)).toBe('just now')
    expect(formatNativeActivityAge(1_499)).toBe('just now')
    expect(formatNativeActivityAge(1_500)).toBe('2s ago')
  })
})
