import { describe, expect, it } from 'vitest'
import { stateConnectionBanner } from './connectionBanner'

describe('stateConnectionBanner', () => {
  it('prioritizes an incompatible protocol over a surviving topic error', () => {
    expect(stateConnectionBanner({
      state: 'incompatible',
      instanceId: 'old-instance',
      error: new Error('protocol mismatch'),
    }, 'stale topic error')).toEqual({
      message: 'UI update required — reload Kiwi Code',
      canRetryTopics: false,
    })
  })

  it('offers topic retry when the connection is compatible', () => {
    expect(stateConnectionBanner({
      state: 'open',
      instanceId: 'current-instance',
    }, 'snapshot failed')).toEqual({
      message: 'UI state error: snapshot failed',
      canRetryTopics: true,
    })
  })
})
