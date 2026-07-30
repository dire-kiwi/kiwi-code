import { browserRecordingDownloadUrl } from '@/api'

type RecordingIdentity = {
  id: string
  title: string
}

export function formatRecordingDuration(
  durationMs = 0,
  rounding: 'floor' | 'round' = 'floor',
) {
  const totalSeconds = Math.max(0, Math[rounding](durationMs / 1_000))
  return `${Math.floor(totalSeconds / 60)}:${String(totalSeconds % 60).padStart(2, '0')}`
}

export function formatRecordingBytes(bytes?: number, compactLarge = false) {
  if (!bytes || bytes < 1) return ''
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`
  const digits = compactLarge && bytes >= 10 * 1024 * 1024 ? 0 : 1
  return `${(bytes / (1024 * 1024)).toFixed(digits)} MB`
}

export function downloadBrowserRecording(
  projectId: string,
  threadId: string,
  recording: RecordingIdentity,
) {
  const link = document.createElement('a')
  link.href = browserRecordingDownloadUrl(projectId, threadId, recording.id)
  link.download = `${recording.title}.webm`
  link.rel = 'noopener'
  link.click()
}
