import { Download, Play, Trash2 } from 'lucide-react'
import { formatRecordingBytes, formatRecordingDuration } from '@/lib/browserRecording'
import type { BrowserRecording } from '@/wire/domain'
import { Button, IconButton } from '@/ui/buttons'

export type BrowserRecordingsBarProps = {
  recordings: BrowserRecording[]
  playingRecordingId: string | undefined
  busy: boolean
  onPlay: (recording: BrowserRecording) => void
  onDownload: (recording: BrowserRecording) => void
  onDelete: (recording: BrowserRecording) => void
}

export function BrowserRecordingsBar({
  recordings,
  playingRecordingId,
  busy,
  onPlay,
  onDownload,
  onDelete,
}: BrowserRecordingsBarProps) {
  if (recordings.length === 0) return null

  return (
    <div className="flex shrink-0 items-center gap-2 overflow-x-auto border-b border-ghost-border/65 bg-ghost-panel/75 px-3 py-1.5" aria-label="Completed browser recordings">
      <span className="shrink-0 text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Recordings</span>
      {recordings.map((recording) => (
        <div
          key={recording.id}
          className={`flex max-w-[22rem] shrink-0 items-center gap-1 rounded-md border px-1.5 py-1 ${playingRecordingId === recording.id ? 'border-ghost-green/45 bg-ghost-green/10' : 'border-ghost-border/70 bg-ghost-black/25'}`}
          title={`${recording.title} · ${formatRecordingDuration(recording.durationMs)}${formatRecordingBytes(recording.bytes) ? ` · ${formatRecordingBytes(recording.bytes)}` : ''}`}
        >
          <Button
            type="button"
            variant="text"
            onClick={() => onPlay(recording)}
            className="flex min-w-0 items-center gap-1.5 px-1 text-[9px] text-ghost-white"
          >
            <Play size={9} fill="currentColor" className="shrink-0 text-ghost-green" />
            <span className="max-w-44 truncate">{recording.title}</span>
            <span className="shrink-0 font-mono text-ghost-faint">{formatRecordingDuration(recording.durationMs)}</span>
          </Button>
          <IconButton type="button" size="xs" variant="subtle" onClick={() => onDownload(recording)} aria-label={`Download ${recording.title}`} title="Download recording">
            <Download size={10} />
          </IconButton>
          <IconButton
            type="button"
            size="xs"
            variant="subtle"
            disabled={busy}
            onClick={() => onDelete(recording)}
            aria-label={`Delete ${recording.title}`}
            title="Delete recording"
            className="text-ghost-bright-red"
          >
            <Trash2 size={10} />
          </IconButton>
        </div>
      ))}
    </div>
  )
}
