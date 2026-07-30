import { Download, LoaderCircle, Play, X } from 'lucide-react'
import { browserRecordingPlaybackUrl } from '@/api'
import { formatRecordingBytes, formatRecordingDuration } from '@/lib/browserRecording'
import type { BrowserRecording } from '@/wire/domain'
import { IconButton } from '@/ui/buttons'

export type BrowserPlaybackOverlayProps = {
  projectId: string
  threadId: string
  recording: BrowserRecording
  loading: boolean
  error: string
  onLoadingChange: (loading: boolean) => void
  onError: (message: string) => void
  onDownload: (recording: BrowserRecording) => void
  onClose: () => void
}

export function BrowserPlaybackOverlay({
  projectId,
  threadId,
  recording,
  loading,
  error,
  onLoadingChange,
  onError,
  onDownload,
  onClose,
}: BrowserPlaybackOverlayProps) {
  return (
    <div className="absolute inset-0 z-30 flex flex-col bg-ghost-black" aria-label={`Playback of ${recording.title}`}>
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-ghost-border/70 bg-ghost-panel px-3">
        <Play size={11} fill="currentColor" className="shrink-0 text-ghost-green" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[10px] font-semibold text-ghost-bright-white">{recording.title}</p>
          <p className="text-[8px] text-ghost-faint">{formatRecordingDuration(recording.durationMs)}{formatRecordingBytes(recording.bytes) ? ` · ${formatRecordingBytes(recording.bytes)}` : ''}</p>
        </div>
        <IconButton type="button" size="sm" variant="subtle" onClick={() => onDownload(recording)} aria-label={`Download ${recording.title}`} title="Download recording">
          <Download size={12} />
        </IconButton>
        <IconButton type="button" size="sm" variant="subtle" onClick={onClose} aria-label="Close recording playback" title="Close playback">
          <X size={12} />
        </IconButton>
      </div>
      <div className="relative min-h-0 flex-1">
        <video
          key={recording.id}
          src={browserRecordingPlaybackUrl(projectId, threadId, recording.id)}
          controls
          autoPlay
          playsInline
          preload="metadata"
          className="h-full w-full bg-black object-contain"
          aria-label={recording.title}
          onLoadStart={() => { onLoadingChange(true); onError('') }}
          onCanPlay={() => onLoadingChange(false)}
          onPlaying={() => onLoadingChange(false)}
          onWaiting={() => onLoadingChange(true)}
          onError={() => {
            onLoadingChange(false)
            onError('This WebM could not be played inline. Download it to view externally.')
          }}
        />
        {loading && !error && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center bg-ghost-black/45" aria-label="Loading browser recording">
            <LoaderCircle size={20} className="animate-spin text-ghost-green" />
          </div>
        )}
        {error && (
          <div role="alert" className="absolute inset-x-4 bottom-4 rounded-lg border border-ghost-bright-red/30 bg-ghost-panel/95 px-3 py-2 text-center text-[10px] text-ghost-bright-red">
            {error}
          </div>
        )}
      </div>
    </div>
  )
}
