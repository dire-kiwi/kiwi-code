import { useState, type FormEvent } from 'react'
import { Check, LoaderCircle, Plus, Save, Trash2, UserRound } from 'lucide-react'
import { updateSettings } from '../../../../api'
import type { AppSettings, CodingAgentSetting } from '../../../../types'
import { GhostButton, PrimaryButton } from '../../../atoms/Button'
import { TextInput } from '../../../atoms/Input'
import { Select } from '../../../atoms/Select'
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

const maxCodingAgents = 16

function newCodingAgentId() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function ClaudeProfilesSection({ settings, onSettingsUpdated }: ClaudeProfilesSectionProps) {
  const [codingAgents, setCodingAgents] = useState<CodingAgentSetting[]>(settings.codingAgents ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const normalizedAgents = codingAgents.map((agent) => ({
    ...agent,
    name: agent.name.trim(),
    configDirectory: agent.kind === 'claude' ? (agent.configDirectory ?? '').trim() : undefined,
  }))
  const agentNames = normalizedAgents.map((agent) => agent.name.toLocaleLowerCase())
  const claudeDirectories = normalizedAgents
    .filter((agent) => agent.kind === 'claude')
    .map((agent) => agent.configDirectory)
  const agentsValid = normalizedAgents.length <= maxCodingAgents
    && normalizedAgents.every((agent) => agent.name.length > 0
      && agent.name.length <= 80
      && (agent.kind === 'claude-gpt' || Boolean(agent.configDirectory)))
    && new Set(agentNames).size === agentNames.length
    && new Set(claudeDirectories).size === claudeDirectories.length
  const agentsDirty = JSON.stringify(normalizedAgents) !== JSON.stringify(settings.codingAgents)

  function addAgent() {
    if (saving || codingAgents.length >= maxCodingAgents) return
    setCodingAgents((current) => [
      ...current,
      { id: newCodingAgentId(), name: '', kind: 'claude', configDirectory: '' },
    ])
    setError('')
    setMessage('')
  }

  function updateAgent(id: string, update: Partial<CodingAgentSetting>) {
    setCodingAgents((current) => current.map((agent) =>
      agent.id === id ? { ...agent, ...update } : agent,
    ))
    setError('')
    setMessage('')
  }

  function removeAgent(id: string) {
    if (saving) return
    setCodingAgents((current) => current.filter((agent) => agent.id !== id))
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
      const next = await updateSettings({ codingAgents: normalizedAgents })
      onSettingsUpdated(next)
      setCodingAgents(next.codingAgents)
      setMessage('Coding agents saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save coding agents.')
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
        title="Coding agents"
        description="Choose the Claude Code instances that appear in agent dropdowns."
        tone="blue"
        badge={(
          <StatusBadge tone={codingAgents.length > 0 ? 'success' : 'neutral'}>
            {codingAgents.length} configured
          </StatusBadge>
        )}
      />

      <div className="space-y-3 p-4 sm:p-5">
        {codingAgents.length === 0 ? (
          <div className="rounded-xl border border-dashed border-ghost-border/70 bg-ghost-black/20 px-4 py-5 text-center">
            <p className="text-[10px] font-medium text-ghost-muted">No Claude Code agents configured.</p>
            <p className="mt-1 text-[9px] leading-4 text-ghost-faint">
              Pi and Pi Native remain available. Add only the Claude Code instances you want in the dropdown.
            </p>
          </div>
        ) : codingAgents.map((agent, index) => (
          <div key={agent.id} className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
            <div className="flex items-center justify-between gap-3">
              <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">
                Agent {index + 1}
              </p>
              <GhostButton
                type="button"
                size="sm"
                onClick={() => removeAgent(agent.id)}
                disabled={saving}
                className="flex items-center gap-1.5 text-ghost-bright-red disabled:opacity-40"
                aria-label={`Remove ${agent.name || `agent ${index + 1}`}`}
              >
                <Trash2 size={12} />
                Remove
              </GhostButton>
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                Type
                <Select
                  value={agent.kind}
                  options={[
                    { value: 'claude', label: 'Claude Code' },
                    { value: 'claude-gpt', label: 'Claude Code with GPT' },
                  ]}
                  onChange={(kind) => updateAgent(agent.id, {
                    kind: kind as CodingAgentSetting['kind'],
                    configDirectory: kind === 'claude' ? agent.configDirectory ?? '' : undefined,
                  })}
                  disabled={saving}
                  className="mt-1.5 normal-case tracking-normal"
                />
              </label>
              <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                Dropdown label
                <TextInput
                  value={agent.name}
                  onChange={(event) => updateAgent(agent.id, { name: event.target.value })}
                  maxLength={80}
                  autoComplete="off"
                  placeholder={agent.kind === 'claude-gpt' ? 'Claude Code · GPT' : 'Claude Code · Work'}
                  className="mt-1.5"
                />
              </label>
              {agent.kind === 'claude' && (
                <div className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim sm:col-span-2">
                  Config directory
                  <DirectoryPathAutocomplete
                    value={agent.configDirectory ?? ''}
                    disabled={saving}
                    label={`${agent.name || `Agent ${index + 1}`} config directory`}
                    placeholder="~/.claude"
                    className="mt-1.5 normal-case tracking-normal"
                    onChange={(configDirectory) => updateAgent(agent.id, { configDirectory })}
                  />
                </div>
              )}
            </div>
          </div>
        ))}

        <GhostButton
          type="button"
          size="md"
          onClick={addAgent}
          disabled={saving || codingAgents.length >= maxCodingAgents}
          className="flex items-center gap-2 px-3 disabled:opacity-40"
        >
          <Plus size={13} />
          Add agent
        </GhostButton>

        <InfoCallout>
          The list starts empty and controls every Claude Code entry in the new-thread and workspace dropdowns.
          Standard instances use their selected <span className="font-mono text-ghost-blue">CLAUDE_CONFIG_DIR</span>;
          GPT instances use Kiwi Code's managed CLIProxyAPI profile. Missing standard directories are created on save.
        </InfoCallout>

        {!agentsValid && codingAgents.length > 0 && (
          <FeedbackMessage role="alert" tone="error">
            Every agent needs a unique label. Standard Claude Code agents also need a unique config directory.
          </FeedbackMessage>
        )}
        {error && <FeedbackMessage role="alert" tone="error">{error}</FeedbackMessage>}
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
          disabled={!agentsDirty || !agentsValid || saving}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save agents
        </PrimaryButton>
      </div>
    </Surface>
  )
}
