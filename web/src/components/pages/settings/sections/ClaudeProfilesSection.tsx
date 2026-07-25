import { useState, type FormEvent } from 'react'
import { Check, LoaderCircle, Plus, Save, Trash2, UserRound } from 'lucide-react'
import { updateSettings } from '../../../../api'
import type { AppSettings, ClaudeCodeProfile } from '../../../../types'
import { GhostButton, PrimaryButton } from '../../../atoms/Button'
import { TextInput } from '../../../atoms/Input'
import { StatusBadge } from '../../../atoms/StatusBadge'
import { Surface } from '../../../atoms/Surface'
import { FeedbackMessage } from '../../../molecules/FeedbackMessage'
import { InfoCallout } from '../../../molecules/InfoCallout'
import { DirectoryPathAutocomplete } from '../../../molecules/ProjectPathAutocomplete'
import { SectionHeader } from '../../../molecules/SectionHeader'

type ClaudeProfilesSectionProps = {
  settings: AppSettings
  onSettingsUpdated: (settings: AppSettings) => void
}

const maxClaudeCodeProfiles = 16

function newClaudeCodeProfileId() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function ClaudeProfilesSection({ settings, onSettingsUpdated }: ClaudeProfilesSectionProps) {
  const [claudeCodeProfiles, setClaudeCodeProfiles] = useState<ClaudeCodeProfile[]>(
    settings.claudeCodeProfiles ?? [],
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const normalizedProfiles = claudeCodeProfiles.map((profile) => ({
    ...profile,
    name: profile.name.trim(),
    configDirectory: profile.configDirectory.trim(),
  }))
  const profileNames = normalizedProfiles.map((profile) => profile.name.toLocaleLowerCase())
  const profileDirectories = normalizedProfiles.map((profile) => profile.configDirectory)
  const profilesValid = normalizedProfiles.length <= maxClaudeCodeProfiles
    && normalizedProfiles.every((profile) => profile.name.length > 0
      && profile.name.length <= 80
      && profile.configDirectory.length > 0)
    && new Set(profileNames).size === profileNames.length
    && new Set(profileDirectories).size === profileDirectories.length
  const profilesDirty = JSON.stringify(normalizedProfiles) !== JSON.stringify(settings.claudeCodeProfiles)

  function addProfile() {
    if (saving || claudeCodeProfiles.length >= maxClaudeCodeProfiles) return
    setClaudeCodeProfiles((current) => [
      ...current,
      { id: newClaudeCodeProfileId(), name: '', configDirectory: '' },
    ])
    setError('')
    setMessage('')
  }

  function updateProfile(id: string, update: Partial<ClaudeCodeProfile>) {
    setClaudeCodeProfiles((current) => current.map((profile) =>
      profile.id === id ? { ...profile, ...update } : profile,
    ))
    setError('')
    setMessage('')
  }

  function removeProfile(id: string) {
    if (saving) return
    setClaudeCodeProfiles((current) => current.filter((profile) => profile.id !== id))
    setError('')
    setMessage('')
  }

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const next = await updateSettings({ claudeCodeProfiles: normalizedProfiles })
      onSettingsUpdated(next)
      setClaudeCodeProfiles(next.claudeCodeProfiles)
      setMessage('Claude Code profiles saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save Claude Code profiles.')
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
        icon={<UserRound size={16} />}
        title="Claude Code profiles"
        description="Add named Claude Code sessions backed by separate configuration directories."
        tone="blue"
        badge={(
          <StatusBadge tone={claudeCodeProfiles.length > 0 ? 'success' : 'neutral'}>
            {claudeCodeProfiles.length} configured
          </StatusBadge>
        )}
      />

      <div className="space-y-3 p-4 sm:p-5">
        {claudeCodeProfiles.length === 0 ? (
          <div className="rounded-xl border border-dashed border-ghost-border/70 bg-ghost-black/20 px-4 py-5 text-center">
            <p className="text-[10px] font-medium text-ghost-muted">No additional Claude Code profiles.</p>
            <p className="mt-1 text-[9px] leading-4 text-ghost-faint">
              The built-in Claude Code entry continues to use your default configuration directory.
            </p>
          </div>
        ) : claudeCodeProfiles.map((profile, index) => (
          <div key={profile.id} className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
            <div className="flex items-center justify-between gap-3">
              <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">
                Profile {index + 1}
              </p>
              <GhostButton
                type="button"
                size="sm"
                onClick={() => removeProfile(profile.id)}
                disabled={saving}
                className="flex items-center gap-1.5 text-ghost-bright-red disabled:opacity-40"
                aria-label={`Remove ${profile.name || `profile ${index + 1}`}`}
              >
                <Trash2 size={12} />
                Remove
              </GhostButton>
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-[minmax(0,0.7fr)_minmax(0,1.3fr)]">
              <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                Title
                <TextInput
                  value={profile.name}
                  onChange={(event) => updateProfile(profile.id, { name: event.target.value })}
                  maxLength={80}
                  autoComplete="off"
                  placeholder="Work"
                  className="mt-1.5"
                />
              </label>
              <div className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                Config directory
                <DirectoryPathAutocomplete
                  value={profile.configDirectory}
                  disabled={saving}
                  label={`${profile.name || `Profile ${index + 1}`} config directory`}
                  placeholder="~/.claude-work"
                  className="mt-1.5 normal-case tracking-normal"
                  onChange={(configDirectory) => updateProfile(profile.id, { configDirectory })}
                />
              </div>
            </div>
          </div>
        ))}

        <GhostButton
          type="button"
          size="md"
          onClick={addProfile}
          disabled={saving || claudeCodeProfiles.length >= maxClaudeCodeProfiles}
          className="flex items-center gap-2 px-3 disabled:opacity-40"
        >
          <Plus size={13} />
          Add Claude Code
        </GhostButton>

        <InfoCallout>
          Each entry appears as “Claude Code · Title” in the new-thread and workspace agent lists. Kiwi Code
          launches it with <span className="font-mono text-ghost-blue">CLAUDE_CONFIG_DIR</span> set to the selected
          directory. Login and session state stay separate; before each launch, Kiwi Code mirrors the default Claude
          settings and uses the same installed plugins. Missing directories are created when you save.
        </InfoCallout>

        {!profilesValid && claudeCodeProfiles.length > 0 && (
          <FeedbackMessage role="alert" tone="error">
            Every profile needs a unique title and config directory.
          </FeedbackMessage>
        )}
        {error && (
          <FeedbackMessage role="alert" tone="error">{error}</FeedbackMessage>
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
          disabled={!profilesDirty || !profilesValid || saving}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save profiles
        </PrimaryButton>
      </div>
    </Surface>
  )
}
