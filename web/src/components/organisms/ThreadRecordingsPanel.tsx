import { useEffect, useState } from 'react'
import { Circle, Download, LoaderCircle, Play, RefreshCw, Trash2, Video, X } from 'lucide-react'
import {
  browserRecordingDownloadUrl,
  browserRecordingPlaybackUrl,
  getBrowserStatus,
  performBrowserAction,
} from '../../api'
import type { BrowserActionResponse, BrowserRecording, BrowserStatusResult } from '../../types'
import { Button } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'

export type ThreadRecordingsPanelProps = {
  projectId: string
  threadId: string
  active: boolean
}

function statusPayload(value: BrowserStatusResult | BrowserActionResponse<BrowserStatusResult>) {
  return 'result' in value ? value.result : value
}

function recordingIsValid(value: unknown): value is BrowserRecording {
  if (!value || typeof value !== 'object') return false
  const recording = value as Partial<BrowserRecording>
  return typeof recording.id === 'string'
    && typeof recording.targetId === 'string'
    && typeof recording.title === 'string'
    && typeof recording.startedAt === 'string'
    && ['starting', 'recording', 'finalizing', 'completed'].includes(recording.state ?? '')
}

function formatDuration(durationMs?: number) {
  const totalSeconds = Math.max(0, Math.round((durationMs ?? 0) / 1_000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

function formatBytes(bytes?: number) {
  if (!bytes || bytes < 1) return ''
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`
  return `${(bytes / (1024 * 1024)).toFixed(bytes < 10 * 1024 * 1024 ? 1 : 0)} MB`
}

export function ThreadRecordingsPanel({ projectId, threadId, active }: ThreadRecordingsPanelProps) {
  const [recording, setRecording] = useState<BrowserRecording | null>(null)
  const [recordings, setRecordings] = useState<BrowserRecording[]>([])
  const [playback, setPlayback] = useState<BrowserRecording | null>(null)
  const [loading, setLoading] = useState(false)
  const [deletingId, setDeletingId] = useState('')
  const [error, setError] = useState('')

  async function load(signal?: AbortSignal, showLoading = false) {
    if (showLoading) setLoading(true)
    try {
      const result = statusPayload(await getBrowserStatus(projectId, threadId, signal))
      const nextActive = recordingIsValid(result.recording) && result.recording.state !== 'completed'
        ? result.recording
        : null
      const nextRecordings = Array.isArray(result.recordings)
        ? result.recordings
          .filter(recordingIsValid)
          .filter((item) => item.state === 'completed')
          .sort((left, right) => Date.parse(right.finishedAt ?? right.startedAt) - Date.parse(left.finishedAt ?? left.startedAt))
        : []
      setRecording(nextActive)
      setRecordings(nextRecordings)
      setPlayback((current) => current && nextRecordings.some((item) => item.id === current.id) ? current : null)
      setError('')
    } catch (reason) {
      if (signal?.aborted) return
      setError(reason instanceof Error ? reason.message : 'Could not load browser recordings.')
    } finally {
      if (!signal?.aborted && showLoading) setLoading(false)
    }
  }

  useEffect(() => {
    setRecording(null)
    setRecordings([])
    setPlayback(null)
    setError('')
    if (!active) return

    const controller = new AbortController()
    void load(controller.signal, true)
    const interval = window.setInterval(() => void load(controller.signal), 5_000)
    return () => {
      controller.abort()
      window.clearInterval(interval)
    }
  }, [active, projectId, threadId])

  function download(item: BrowserRecording) {
    const link = document.createElement('a')
    link.href = browserRecordingDownloadUrl(projectId, threadId, item.id)
    link.download = `${item.title}.webm`
    link.rel = 'noopener'
    link.click()
  }

  async function remove(item: BrowserRecording) {
    if (!window.confirm(`Delete “${item.title}”?`)) return
    setDeletingId(item.id)
    setError('')
    try {
      await performBrowserAction(projectId, threadId, {
        operation: 'recording.delete',
        params: { recordingId: item.id },
      })
      if (playback?.id === item.id) setPlayback(null)
      await load()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not delete the recording.')
    } finally {
      setDeletingId('')
    }
  }

  return (
    <section aria-labelledby="thread-recordings-heading">
      <div className="flex items-center justify-between gap-2">
        <p id="thread-recordings-heading" className="flex items-center gap-1.5 font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint">
          <Video size={10} />
          Recordings
        </p>
        <div className="flex items-center gap-1">
          {recordings.length > 0 && (
            <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[8px] text-ghost-faint">
              {recordings.length}
            </span>
          )}
          <IconButton
            type="button"
            size="xs"
            variant="subtle"
            onClick={() => void load(undefined, true)}
            disabled={loading}
            aria-label="Refresh browser recordings"
            title="Refresh recordings"
          >
            <RefreshCw size={10} className={loading ? 'animate-spin' : ''} />
          </IconButton>
        </div>
      </div>

      {recording && (
        <div className="mt-2 flex items-center gap-2 rounded-lg border border-ghost-bright-red/25 bg-ghost-bright-red/[0.06] px-2.5 py-2" title={recording.title}>
          {recording.state === 'finalizing'
            ? <LoaderCircle size={11} className="shrink-0 animate-spin text-ghost-bright-red" />
            : <Circle size={9} fill="currentColor" className="shrink-0 animate-pulse text-ghost-bright-red" />}
          <span className="min-w-0 flex-1 truncate text-[10px] font-medium text-ghost-white">{recording.title}</span>
          <span className="shrink-0 font-mono text-[8px] uppercase text-ghost-bright-red">
            {recording.state === 'finalizing' ? 'Saving' : 'Recording'}
          </span>
        </div>
      )}

      {playback && (
        <div className="mt-2 overflow-hidden rounded-lg border border-ghost-green/30 bg-ghost-black/55">
          <div className="flex items-center gap-1.5 border-b border-ghost-border/60 px-2 py-1.5">
            <Play size={9} fill="currentColor" className="shrink-0 text-ghost-green" />
            <span className="min-w-0 flex-1 truncate text-[9px] font-medium text-ghost-white">{playback.title}</span>
            <IconButton type="button" size="xs" variant="subtle" onClick={() => setPlayback(null)} aria-label="Close recording playback" title="Close playback">
              <X size={10} />
            </IconButton>
          </div>
          <video
            key={playback.id}
            src={browserRecordingPlaybackUrl(projectId, threadId, playback.id)}
            controls
            playsInline
            preload="metadata"
            className="aspect-video w-full bg-black object-contain"
            aria-label={`Playback of ${playback.title}`}
          />
        </div>
      )}

      {error && <p role="alert" className="mt-2 text-[9px] leading-4 text-ghost-bright-red">{error}</p>}

      {!loading && recordings.length === 0 && !recording && !error && (
        <p className="mt-2 rounded-lg border border-dashed border-ghost-border/60 px-2.5 py-3 text-[9px] leading-4 text-ghost-faint">
          Completed browser recordings for this thread will appear here.
        </p>
      )}

      {recordings.length > 0 && (
        <ul className="mt-2 space-y-1" aria-label="Browser recordings for this thread">
          {recordings.map((item) => (
            <li key={item.id} className={`group rounded-lg border px-2 py-1.5 ${playback?.id === item.id ? 'border-ghost-green/35 bg-ghost-green/[0.06]' : 'border-ghost-border/55 bg-ghost-black/20'}`}>
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  variant="text"
                  onClick={() => setPlayback(item)}
                  className="flex min-w-0 flex-1 items-center gap-2 px-0 text-left"
                  aria-label={`Play ${item.title}`}
                >
                  <Play size={10} fill="currentColor" className="shrink-0 text-ghost-green" />
                  <span className="min-w-0 flex-1 truncate text-[10px] font-medium text-ghost-white">{item.title}</span>
                </Button>
                <IconButton type="button" size="xs" variant="subtle" onClick={() => download(item)} aria-label={`Download ${item.title}`} title="Download recording">
                  <Download size={10} />
                </IconButton>
                <IconButton type="button" size="xs" variant="danger" disabled={Boolean(deletingId)} onClick={() => void remove(item)} aria-label={`Delete ${item.title}`} title="Delete recording">
                  {deletingId === item.id ? <LoaderCircle size={10} className="animate-spin" /> : <Trash2 size={10} />}
                </IconButton>
              </div>
              <div className="mt-1 flex items-center gap-1 pl-[18px] font-mono text-[8px] text-ghost-faint">
                <span>{formatDuration(item.durationMs)}</span>
                {formatBytes(item.bytes) && <><span aria-hidden="true">·</span><span>{formatBytes(item.bytes)}</span></>}
                {item.finishedAt && <><span aria-hidden="true">·</span><span>{new Date(item.finishedAt).toLocaleString()}</span></>}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
