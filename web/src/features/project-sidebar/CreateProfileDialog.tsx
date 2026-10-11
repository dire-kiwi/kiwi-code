import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { LoaderCircle, X } from 'lucide-react'
import { createProfile } from '@/api'
import type { Profile } from '@/types'
import { Button } from '@/ui/buttons'
import { FeedbackMessage } from '@/ui/feedback'
import { TextInput } from '@/ui/inputs'
import { profileInitials } from './profileInitials'

// The server enforces the same limit; mirroring it here stops the input before
// the request rather than after.
const maxProfileNameLength = 80

/**
 * Names a new profile. This replaces a window.prompt, which Electron does not
 * implement, so the desktop app had no way to create a profile at all.
 */
export function CreateProfileDialog({ onCreated, onClose }: {
  onCreated: (profile: Profile) => void
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const id = useId()
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const trimmedName = name.trim()

  useEffect(() => {
    const dialog = dialogRef.current
    dialog?.showModal()
    inputRef.current?.focus()
    return () => dialog?.close()
  }, [])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!trimmedName || saving) return
    setSaving(true)
    setError('')
    try {
      onCreated(await createProfile(trimmedName))
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not create that profile.')
      setSaving(false)
    }
  }

  return (
    <dialog ref={dialogRef} aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`}
      onCancel={(event) => {
        event.preventDefault()
        if (!saving) onClose()
      }}
      onClick={(event) => { if (event.target === event.currentTarget && !saving) onClose() }}
      className="fixed inset-0 m-auto w-[calc(100%-2rem)] max-w-sm overflow-hidden rounded-2xl border border-ghost-border bg-ghost-panel p-0 text-ghost-white shadow-2xl backdrop:bg-ghost-black/70 backdrop:backdrop-blur-sm">
      <form onSubmit={(event) => void handleSubmit(event)}>
        <div className="flex items-center justify-between px-4 pt-4">
          <h2 id={`${id}-title`} className="text-sm font-medium">Create a profile</h2>
          <button type="button" onClick={onClose} disabled={saving} aria-label="Close" className="rounded p-1 text-ghost-dim hover:bg-ghost-raised disabled:opacity-40"><X size={15} /></button>
        </div>
        <div className="flex flex-col items-center gap-3 px-4 pb-1 pt-3">
          <span aria-hidden="true" className="grid size-14 place-items-center rounded-2xl bg-ghost-green text-lg font-semibold text-ghost-black">
            {trimmedName ? profileInitials(trimmedName) : '?'}
          </span>
          <p id={`${id}-description`} className="text-center text-[11px] leading-4 text-ghost-dim">
            Profiles keep separate project lists, such as personal, work, or client projects.
          </p>
        </div>
        <label className="block px-4 pt-3">
          <span className="mb-1.5 block text-[10px] font-semibold uppercase tracking-wide text-ghost-dim">Profile name</span>
          <TextInput ref={inputRef} value={name} maxLength={maxProfileNameLength} placeholder="Client work"
            onChange={(event) => setName(event.target.value)} disabled={saving} />
        </label>
        {error && <FeedbackMessage tone="error" role="alert" className="mx-4 mt-3">{error}</FeedbackMessage>}
        <div className="mt-4 flex justify-end gap-2 border-t border-ghost-border/50 px-4 py-3">
          <Button type="button" variant="text" onClick={onClose} disabled={saving} className="h-8 rounded-lg px-3 text-xs disabled:opacity-40">Cancel</Button>
          <Button type="submit" variant="primary" disabled={!trimmedName || saving} className="flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs">
            {saving && <LoaderCircle size={12} className="animate-spin" />}
            Create profile
          </Button>
        </div>
      </form>
    </dialog>
  )
}
