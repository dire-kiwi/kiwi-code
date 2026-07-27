import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { BrowserRecording, BrowserStatusResult } from '../../wire/domain'
import type { SubscriptionResult } from '../../wire/react'
import { BrowserStatusTopic } from '../../wire/topics'
import { ThreadRecordingsPanel } from './ThreadRecordingsPanel'

const mocks = vi.hoisted(() => ({
  retry: vi.fn(),
  useSubscription: vi.fn(),
  subscription: {
    current: { state: 'loading', retry: vi.fn() } as SubscriptionResult<BrowserStatusResult>,
  },
}))

vi.mock('../../wire/react', () => ({
  useSubscription: (...args: unknown[]) => {
    mocks.useSubscription(...args)
    return mocks.subscription.current
  },
}))

function recording(overrides: Partial<BrowserRecording>): BrowserRecording {
  return {
    id: 'recording',
    state: 'completed',
    targetId: 'page',
    title: 'Recording',
    startedAt: '2026-07-26T00:00:00Z',
    ...overrides,
  }
}

function status(overrides: Partial<BrowserStatusResult>): BrowserStatusResult {
  return {
    backend: 'headless-chrome',
    presentation: 'stream',
    capabilities: {},
    pages: [],
    currentTargetId: null,
    recording: null,
    recordings: [],
    ...overrides,
  }
}

beforeEach(() => {
  mocks.retry.mockReset()
  mocks.useSubscription.mockReset()
})

describe('ThreadRecordingsPanel', () => {
  it('uses browser.status and deduplicates its active and historical recordings', () => {
    const active = recording({
      id: 'active',
      state: 'recording',
      title: 'Active recording',
    })
    const completed = recording({
      id: 'completed',
      title: 'Completed recording',
      finishedAt: '2026-07-26T00:01:00Z',
    })
    mocks.subscription.current = {
      state: 'ready',
      retry: mocks.retry,
      data: status({
        recording: active,
        recordings: [
          {
            ...active,
            state: 'completed',
            title: 'Duplicate active recording',
            finishedAt: '2026-07-26T00:01:00Z',
          },
          completed,
          { ...completed, title: 'Duplicate completed recording' },
        ],
      }),
    }

    render(<ThreadRecordingsPanel projectId="project" threadId="thread" active />)

    expect(mocks.useSubscription).toHaveBeenCalledWith(
      BrowserStatusTopic,
      { projectId: 'project', threadId: 'thread' },
      { enabled: true },
    )
    expect(screen.getByText('Active recording')).toBeTruthy()
    expect(screen.queryByText('Duplicate active recording')).toBeNull()
    expect(screen.getAllByRole('button', { name: 'Play Completed recording' })).toHaveLength(1)
    expect(screen.queryByText('Duplicate completed recording')).toBeNull()
  })
})
