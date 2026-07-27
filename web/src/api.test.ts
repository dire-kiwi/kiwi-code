import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createProfile, decodeApiError, getApplicationHealth } from './api'

const fetchMock = vi.fn()

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function errorResponse(body: string, status = 500) {
  return {
    ok: false,
    status,
    text: vi.fn().mockResolvedValue(body),
  } as unknown as Response
}

describe('API response helpers', () => {
  it('decodes structured, plain-text, and empty errors', async () => {
    await expect(decodeApiError(errorResponse('{"error":"structured"}'), 'fallback'))
      .resolves.toBe('structured')
    await expect(decodeApiError(errorResponse('upstream unavailable'), 'fallback'))
      .resolves.toBe('upstream unavailable')
    await expect(decodeApiError(errorResponse(''), 'fallback')).resolves.toBe('fallback')
  })

  it('uses plain-text API errors in normal requests', async () => {
    fetchMock.mockResolvedValue(errorResponse('gateway offline', 502))
    await expect(getApplicationHealth()).rejects.toThrow('gateway offline')
  })

  it('serializes simple JSON mutations through the shared wrapper', async () => {
    const profile = { id: 'work', name: 'Work' }
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: vi.fn().mockResolvedValue(profile),
    })

    await expect(createProfile('Work')).resolves.toEqual(profile)
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/profiles'),
      expect.objectContaining({
        method: 'POST',
        body: '{"name":"Work"}',
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
      }),
    )
  })
})
