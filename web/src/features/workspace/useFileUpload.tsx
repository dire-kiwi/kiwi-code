import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent } from 'react'
import { Copy, Paperclip } from 'lucide-react'
import { uploadFile } from '@/api'
import { writeSystemClipboard } from '@/lib/clipboard'
import { filesFromClipboard, promptFilePolicy, validateImageAdditions } from '@/lib/promptImages'

export function useFileUpload(projectId: string, threadId: string) {
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [paths, setPaths] = useState('')
  const controller = useRef<AbortController | null>(null)
  useEffect(() => {
    setPaths('')
    setNotice('')
    setBusy(false)
    return () => { controller.current?.abort(); controller.current = null }
  }, [projectId, threadId])

  async function copy(value: string) {
    try {
      await writeSystemClipboard(value)
      setNotice('File paths copied')
    } catch {
      setNotice('Files uploaded. Copy paths below.')
    }
  }
  async function upload(files: File[]) {
    if (!files.length || controller.current) return
    const validation = validateImageAdditions([], files, promptFilePolicy)
    if (validation.error) { setNotice(validation.error); return }
    const request = new AbortController()
    controller.current = request
    setBusy(true)
    setNotice('Uploading files…')
    try {
      const results = await Promise.all(files.map((file) => uploadFile(projectId, file, request.signal)))
      if (request.signal.aborted) return
      // Quote paths containing spaces for use in both shell and agent prompts.
      const value = results.map(({ path }) => "'" + path.replaceAll("'", "'\\''") + "'").join(' ')
      setPaths(value)
      await copy(value)
    } catch (reason) {
      if (!request.signal.aborted) setNotice(reason instanceof Error ? reason.message : 'Upload failed')
    } finally {
      if (controller.current === request) { controller.current = null; setBusy(false) }
    }
  }
  const control = <div className="relative flex min-w-0 items-center gap-2 text-[10px]">
    <label className="flex cursor-pointer items-center gap-1 rounded px-2 py-1 hover:bg-ghost-raised" title="Upload files and copy their paths">
      <Paperclip size={12} />{busy ? 'Uploading…' : 'Upload files'}
      <input className="sr-only" aria-label="Upload files and copy paths" type="file" multiple disabled={busy}
        onChange={(event) => { void upload(Array.from(event.target.files ?? [])); event.target.value = '' }} />
    </label>
    {paths && <button type="button" title="Copy uploaded file paths" aria-label="Copy uploaded file paths" onClick={() => void copy(paths)}><Copy size={12} /></button>}
    {notice && <details className="min-w-0"><summary role="status" className="max-w-56 cursor-pointer truncate">{notice}</summary>
      <div className="absolute bottom-8 left-0 w-80 rounded border border-ghost-border bg-ghost-panel p-3">
        <p>{notice}</p>{paths && <textarea aria-label="Uploaded file paths" readOnly value={paths} className="mt-2 w-full bg-ghost-background p-2" onFocus={(event) => event.target.select()} />}
      </div>
    </details>}
  </div>
  return {
    control,
    onPasteCapture(event: ClipboardEvent) {
      const files = filesFromClipboard(event.clipboardData)
      if (!files.length) return
      event.preventDefault(); event.stopPropagation(); void upload(files)
    },
    onDragOverCapture(event: DragEvent) {
      if (Array.from(event.dataTransfer.types).includes('Files')) { event.preventDefault(); event.dataTransfer.dropEffect = 'copy' }
    },
    onDropCapture(event: DragEvent) {
      if (!event.dataTransfer.files.length) return
      event.preventDefault(); event.stopPropagation(); void upload(Array.from(event.dataTransfer.files))
    },
  }
}
