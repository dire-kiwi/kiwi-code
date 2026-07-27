import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  downloadBrowserRecording,
  formatRecordingBytes,
  formatRecordingDuration,
} from './browserRecording'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('browser recording presentation', () => {
  it('preserves elapsed and completed duration rounding', () => {
    expect(formatRecordingDuration(59_999)).toBe('0:59')
    expect(formatRecordingDuration(59_999, 'round')).toBe('1:00')
  })

  it('formats compact recording byte counts', () => {
    expect(formatRecordingBytes(1)).toBe('1 KB')
    expect(formatRecordingBytes(5.25 * 1024 * 1024)).toBe('5.3 MB')
    expect(formatRecordingBytes(15.25 * 1024 * 1024, true)).toBe('15 MB')
  })

  it('downloads through the recording endpoint with a useful filename', () => {
    const link = document.createElement('a')
    const click = vi.spyOn(link, 'click').mockImplementation(() => {})
    vi.spyOn(document, 'createElement').mockReturnValue(link)

    downloadBrowserRecording('project one', 'thread/two', {
      id: 'recording three',
      title: 'Demo',
    })

    expect(link.href).toContain(
      '/api/projects/project%20one/threads/thread%2Ftwo/browser/recordings/recording%20three',
    )
    expect(link.download).toBe('Demo.webm')
    expect(link.rel).toBe('noopener')
    expect(click).toHaveBeenCalledOnce()
  })
})
