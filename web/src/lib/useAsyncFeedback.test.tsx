import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useAsyncFeedback } from './useAsyncFeedback'

afterEach(cleanup)

describe('useAsyncFeedback', () => {
  it('tracks one pending action and publishes success', async () => {
    let resolve!: (value: number) => void
    const operation = vi.fn(() => new Promise<number>((done) => {
      resolve = done
    }))
    const { result } = renderHook(() => useAsyncFeedback<'save' | 'reset'>())

    let pending!: Promise<number | undefined>
    act(() => {
      pending = result.current.run('save', operation, {
        success: 'Saved.',
        failure: 'Could not save.',
      })
    })
    expect(result.current.pendingAction).toBe('save')

    await act(async () => {
      expect(await result.current.run('reset', operation, {
        success: 'Reset.',
        failure: 'Could not reset.',
      })).toBeUndefined()
      resolve(7)
      await pending
    })

    expect(operation).toHaveBeenCalledTimes(1)
    expect(result.current.pending).toBe(false)
    expect(result.current.feedback).toEqual({ tone: 'success', message: 'Saved.' })
  })

  it('uses thrown messages, fallback errors, and supports explicit validation feedback', async () => {
    const { result } = renderHook(() => useAsyncFeedback())
    await act(() => result.current.run(
      'default',
      async () => {
        throw new Error('Server rejected it')
      },
      { success: 'Saved.', failure: 'Fallback.' },
    ))
    expect(result.current.feedback).toEqual({ tone: 'error', message: 'Server rejected it' })

    act(() => result.current.showError('Invalid value'))
    expect(result.current.feedback?.message).toBe('Invalid value')
    act(() => result.current.clearFeedback())
    expect(result.current.feedback).toBeNull()
  })
})
