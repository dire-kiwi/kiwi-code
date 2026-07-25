import { useState, type FormEvent } from 'react'
import { Archive, Check, Clock3, FolderGit2, LoaderCircle, Save } from 'lucide-react'
import { updateSettings } from '../../../../api'
import { MAX_CLEANUP_RETENTION_DAYS } from '../../../../lib/validation'
import type { AppSettings } from '../../../../types'
import { PrimaryButton } from '../../../atoms/Button'
import { TextInput } from '../../../atoms/Input'
import { Surface } from '../../../atoms/Surface'
import { FeedbackMessage } from '../../../molecules/FeedbackMessage'
import { InfoCallout } from '../../../molecules/InfoCallout'
import { SectionHeader } from '../../../molecules/SectionHeader'

type CleanupSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

export function CleanupSection({ settings, onSettingsUpdated }: CleanupSectionProps) {
  const [archivedThreadRetentionDays, setArchivedThreadRetentionDays] = useState(
    String(settings.archivedThreadRetentionDays),
  )
  const [orphanedWorktreeRetentionDays, setOrphanedWorktreeRetentionDays] = useState(
    String(settings.orphanedWorktreeRetentionDays),
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const parsedArchivedDays = Number(archivedThreadRetentionDays)
  const parsedOrphanedDays = Number(orphanedWorktreeRetentionDays)
  const valuesValid = archivedThreadRetentionDays.trim() !== ''
    && orphanedWorktreeRetentionDays.trim() !== ''
    && Number.isInteger(parsedArchivedDays)
    && Number.isInteger(parsedOrphanedDays)
    && parsedArchivedDays >= 0
    && parsedOrphanedDays >= 0
    && parsedArchivedDays <= MAX_CLEANUP_RETENTION_DAYS
    && parsedOrphanedDays <= MAX_CLEANUP_RETENTION_DAYS
  const dirty = valuesValid && (
    parsedArchivedDays !== settings.archivedThreadRetentionDays
    || parsedOrphanedDays !== settings.orphanedWorktreeRetentionDays
  )

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    if (!valuesValid) {
      setError(`Retention must be a whole number from 0 to ${MAX_CLEANUP_RETENTION_DAYS} days.`)
      return
    }

    setSaving(true)
    setError('')
    setMessage('')
    try {
      const next = await updateSettings({
        archivedThreadRetentionDays: parsedArchivedDays,
        orphanedWorktreeRetentionDays: parsedOrphanedDays,
      })
      onSettingsUpdated(next)
      setArchivedThreadRetentionDays(String(next.archivedThreadRetentionDays))
      setOrphanedWorktreeRetentionDays(String(next.orphanedWorktreeRetentionDays))
      setMessage('Automatic cleanup settings saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save cleanup settings.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSave(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<Clock3 size={16} />}
        title="Automatic cleanup"
        description="Choose how long archived threads and unattached worktrees are retained."
        tone="yellow"
      />

      <div className="space-y-4 p-4 sm:p-5">
        <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
          <span className="flex items-center gap-2 text-[10px] font-semibold text-ghost-bright-white">
            <Archive size={14} className="text-ghost-yellow" />
            Delete archived threads after
          </span>
          <span className="mt-3 flex items-center gap-2">
            <TextInput
              type="number"
              min={0}
              max={MAX_CLEANUP_RETENTION_DAYS}
              step={1}
              value={archivedThreadRetentionDays}
              onChange={(event) => {
                setArchivedThreadRetentionDays(event.target.value)
                setError('')
                setMessage('')
              }}
              required
              inputMode="numeric"
              className="max-w-28 font-mono"
              aria-describedby="archived-thread-retention-help"
            />
            <span className="text-[10px] text-ghost-muted">days</span>
          </span>
          <span id="archived-thread-retention-help" className="mt-2 block text-[9px] leading-4 text-ghost-faint">
            Deletion stops the thread’s tmux sessions. Enter 0 to keep archived threads forever.
          </span>
        </label>

        <label className="block rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
          <span className="flex items-center gap-2 text-[10px] font-semibold text-ghost-bright-white">
            <FolderGit2 size={14} className="text-ghost-green" />
            Delete unattached worktrees after
          </span>
          <span className="mt-3 flex items-center gap-2">
            <TextInput
              type="number"
              min={0}
              max={MAX_CLEANUP_RETENTION_DAYS}
              step={1}
              value={orphanedWorktreeRetentionDays}
              onChange={(event) => {
                setOrphanedWorktreeRetentionDays(event.target.value)
                setError('')
                setMessage('')
              }}
              required
              inputMode="numeric"
              className="max-w-28 font-mono"
              aria-describedby="orphaned-worktree-retention-help"
            />
            <span className="text-[10px] text-ghost-muted">days</span>
          </span>
          <span id="orphaned-worktree-retention-help" className="mt-2 block text-[9px] leading-4 text-ghost-faint">
            Only worktrees with no staged, unstaged, or untracked changes are removed. Git branches are kept. Enter 0 to disable cleanup.
          </span>
        </label>

        <InfoCallout>
          Cleanup runs when Kiwi Code starts and then once per hour. A worktree becomes unattached when its thread or project is deleted.
        </InfoCallout>

        {error && (
          <FeedbackMessage role="alert" tone="error">
            {error}
          </FeedbackMessage>
        )}
        {message && (
          <FeedbackMessage role="status" tone="success" size="status" className="flex items-center gap-2">
            <Check size={13} />
            {message}
          </FeedbackMessage>
        )}
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || !valuesValid || saving}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save cleanup
        </PrimaryButton>
      </div>
    </Surface>
  )
}
