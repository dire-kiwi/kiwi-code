import { useState, type FormEvent } from 'react'
import { FolderGit2, LoaderCircle, RotateCcw, Save } from 'lucide-react'
import { updateSettings } from '../../../../api'
import { useAsyncFeedback } from '../../../../lib/useAsyncFeedback'
import type { AppSettings } from '../../../../types'
import { GhostButton, PrimaryButton } from '@/ui/buttons'
import { TextInput } from '@/ui/inputs'
import { ActionFeedback, InfoCallout, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type WorktreesSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

export function WorktreesSection({ settings, onSettingsUpdated }: WorktreesSectionProps) {
  const [worktreeBasePath, setWorktreeBasePath] = useState(settings.worktreeBasePath)
  const action = useAsyncFeedback<'save' | 'reset'>()
  const saving = action.pendingAction

  const normalizedInput = worktreeBasePath.trim()
  const dirty = normalizedInput !== settings.worktreeBasePath
  const canReset = !settings.usingDefault || normalizedInput !== settings.defaultWorktreeBasePath

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!normalizedInput || saving) return

    const next = await action.run(
      'save',
      () => updateSettings(normalizedInput),
      { success: 'Worktree location saved.', failure: 'Could not save settings.' },
    )
    if (!next) return
    onSettingsUpdated(next)
    setWorktreeBasePath(next.worktreeBasePath)
  }

  async function handleReset() {
    if (saving) return
    const next = await action.run(
      'reset',
      () => updateSettings(''),
      {
        success: 'Worktree location reset to the default.',
        failure: 'Could not reset settings.',
      },
    )
    if (!next) return
    onSettingsUpdated(next)
    setWorktreeBasePath(next.worktreeBasePath)
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSave(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<FolderGit2 size={16} />}
        title="Git worktrees"
        description="Choose the base directory used for newly-created worktree threads."
        tone="green"
        badge={(
          <StatusBadge tone={settings.usingDefault ? 'neutral' : 'success'}>
            {settings.usingDefault ? 'Default' : 'Custom'}
          </StatusBadge>
        )}
      />

      <div className="p-4 sm:p-5">
        <label className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
          Worktree base location
          <TextInput
            variant="code-large"
            value={worktreeBasePath}
            onChange={(event) => {
              setWorktreeBasePath(event.target.value)
              action.clearFeedback()
            }}
            required
            autoComplete="off"
            spellCheck={false}
            placeholder="/Users/me/worktrees"
            className="mt-2.5"
          />
        </label>

        <div className="mt-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
          <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Default location</p>
          <p className="mt-1.5 break-all font-mono text-[10px] leading-4 text-ghost-muted">
            {settings.defaultWorktreeBasePath}
          </p>
        </div>

        <InfoCallout className="mt-4">
          This only changes where future worktrees are created. Existing worktrees and their files are not moved.
          The selected directory is created when you save it.
        </InfoCallout>

        <ActionFeedback feedback={action.feedback} className="mt-4" />
      </div>

      <div className="flex flex-wrap items-center justify-end gap-2 border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <GhostButton
          type="button"
          size="md"
          onClick={() => void handleReset()}
          disabled={!canReset || Boolean(saving)}
          className="mr-auto flex items-center gap-2 px-3 disabled:cursor-not-allowed disabled:opacity-35"
        >
          {saving === 'reset' ? <LoaderCircle size={13} className="animate-spin" /> : <RotateCcw size={13} />}
          Reset to default
        </GhostButton>
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || !normalizedInput || Boolean(saving)}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving === 'save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save
        </PrimaryButton>
      </div>
    </Surface>
  )
}
