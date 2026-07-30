import { useState, type FormEvent } from 'react'
import {
  ArrowDown,
  ArrowUp,
  Crown,
  LoaderCircle,
  Plus,
  Save,
  Trash2,
  UserRound,
} from 'lucide-react'
import { updateSettings } from '@/api'
import { useAsyncFeedback } from '@/lib/useAsyncFeedback'
import { useAppDispatch } from '@/store/hooks'
import { settingsReceived } from '@/store/slices/settings'
import type { AppSettings, CodingAgentSetting } from '@/types'
import { GhostButton, PrimaryButton } from '@/ui/buttons'
import { DirectoryPathAutocomplete, Select, TextInput } from '@/ui/inputs'
import { ActionFeedback, FeedbackMessage, InfoCallout, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type ClaudeProfilesSectionProps = {
  settings: AppSettings
}

const maxCustomCodingAgents = 16

function newCodingAgentId() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function isBuiltInAgent(agent: CodingAgentSetting) {
  return agent.kind === 'pi' || agent.kind === 'pi-native' || agent.kind === 'codex'
}

function agentTypeLabel(agent: CodingAgentSetting) {
  switch (agent.kind) {
    case 'pi': return 'Pi (terminal)'
    case 'pi-native': return 'Pi Native'
    case 'codex': return 'Codex CLI (terminal)'
    case 'claude-gpt': return 'Claude Code with GPT'
    default: return 'Claude Code'
  }
}

export function ClaudeProfilesSection({ settings }: ClaudeProfilesSectionProps) {
  const dispatch = useAppDispatch()
  const [codingAgents, setCodingAgents] = useState<CodingAgentSetting[]>(settings.codingAgents ?? [])
  const action = useAsyncFeedback()
  const saving = action.pending

  const normalizedAgents = codingAgents.map((agent) => ({
    ...agent,
    name: isBuiltInAgent(agent)
      ? agent.kind === 'pi' ? 'Pi' : agent.kind === 'pi-native' ? 'Pi Native' : 'Codex CLI'
      : agent.name.trim(),
    configDirectory: agent.kind === 'claude' ? (agent.configDirectory ?? '').trim() : undefined,
  }))
  const customAgents = normalizedAgents.filter((agent) => !isBuiltInAgent(agent))
  const agentNames = customAgents.map((agent) => agent.name.toLocaleLowerCase())
  const claudeDirectories = customAgents
    .filter((agent) => agent.kind === 'claude')
    .map((agent) => agent.configDirectory)
  const agentsValid = customAgents.length <= maxCustomCodingAgents
    && customAgents.every((agent) => agent.name.length > 0
      && agent.name.length <= 80
      && (agent.kind === 'claude-gpt' || Boolean(agent.configDirectory)))
    && new Set(agentNames).size === agentNames.length
    && new Set(claudeDirectories).size === claudeDirectories.length
    && normalizedAgents.filter((agent) => agent.kind === 'pi').length === 1
    && normalizedAgents.filter((agent) => agent.kind === 'pi-native').length === 1
    && normalizedAgents.filter((agent) => agent.kind === 'codex').length === 1
    && normalizedAgents.filter((agent) => agent.isDefault).length === 1
  const agentsDirty = JSON.stringify(normalizedAgents) !== JSON.stringify(settings.codingAgents)

  function clearFeedback() {
    action.clearFeedback()
  }

  function addAgent() {
    if (saving || customAgents.length >= maxCustomCodingAgents) return
    setCodingAgents((current) => [
      ...current,
      { id: newCodingAgentId(), name: '', kind: 'claude', configDirectory: '', isDefault: false },
    ])
    clearFeedback()
  }

  function updateAgent(index: number, update: Partial<CodingAgentSetting>) {
    setCodingAgents((current) => current.map((agent, agentIndex) =>
      agentIndex === index ? { ...agent, ...update } : agent,
    ))
    clearFeedback()
  }

  function removeAgent(index: number) {
    if (saving || isBuiltInAgent(codingAgents[index])) return
    setCodingAgents((current) => {
      const removed = current[index]
      const next = current.filter((_, agentIndex) => agentIndex !== index)
      if (!removed.isDefault) return next
      return next.map((agent) => ({ ...agent, isDefault: agent.kind === 'pi-native' }))
    })
    clearFeedback()
  }

  function moveAgent(index: number, direction: -1 | 1) {
    const target = index + direction
    if (saving || target < 0 || target >= codingAgents.length) return
    setCodingAgents((current) => {
      const next = [...current]
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })
    clearFeedback()
  }

  function makeDefault(index: number) {
    if (saving) return
    setCodingAgents((current) => current.map((agent, agentIndex) => ({
      ...agent,
      isDefault: agentIndex === index,
    })))
    clearFeedback()
  }

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    const next = await action.run(
      'default',
      () => updateSettings({ codingAgents: normalizedAgents }),
      { success: 'Coding agents saved.', failure: 'Could not save coding agents.' },
    )
    if (!next) return
    dispatch(settingsReceived(next))
    setCodingAgents(next.codingAgents)
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
        description="Choose the agents in dropdowns, their order, and the default for new threads."
        tone="blue"
        badge={(
          <StatusBadge tone="success">
            {customAgents.length} custom
          </StatusBadge>
        )}
      />

      <div className="space-y-3 p-4 sm:p-5">
        {codingAgents.map((agent, index) => {
          const builtIn = isBuiltInAgent(agent)
          return (
            <div key={`${agent.kind}:${agent.id}`} className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                  <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">
                    Agent {index + 1}
                  </p>
                  {agent.isDefault && <StatusBadge tone="success">Default</StatusBadge>}
                  {builtIn && <StatusBadge tone="neutral">Built in</StatusBadge>}
                </div>
                <div className="flex items-center gap-1">
                  <GhostButton
                    type="button"
                    size="sm"
                    onClick={() => moveAgent(index, -1)}
                    disabled={saving || index === 0}
                    className="px-2 disabled:opacity-30"
                    aria-label={`Move ${agent.name || `agent ${index + 1}`} up`}
                  >
                    <ArrowUp size={12} />
                  </GhostButton>
                  <GhostButton
                    type="button"
                    size="sm"
                    onClick={() => moveAgent(index, 1)}
                    disabled={saving || index === codingAgents.length - 1}
                    className="px-2 disabled:opacity-30"
                    aria-label={`Move ${agent.name || `agent ${index + 1}`} down`}
                  >
                    <ArrowDown size={12} />
                  </GhostButton>
                  {!agent.isDefault && (
                    <GhostButton
                      type="button"
                      size="sm"
                      onClick={() => makeDefault(index)}
                      disabled={saving}
                      className="flex items-center gap-1.5"
                    >
                      <Crown size={12} />
                      Make default
                    </GhostButton>
                  )}
                  {!builtIn && (
                    <GhostButton
                      type="button"
                      size="sm"
                      onClick={() => removeAgent(index)}
                      disabled={saving}
                      className="flex items-center gap-1.5 text-ghost-bright-red disabled:opacity-40"
                      aria-label={`Remove ${agent.name || `agent ${index + 1}`}`}
                    >
                      <Trash2 size={12} />
                      Remove
                    </GhostButton>
                  )}
                </div>
              </div>

              {builtIn ? (
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                    Type
                    <TextInput value={agentTypeLabel(agent)} disabled className="mt-1.5" />
                  </label>
                  <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                    Dropdown label
                    <TextInput value={agent.name} disabled className="mt-1.5" />
                  </label>
                </div>
              ) : (
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <label className="block text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-dim">
                    Type
                    <Select
                      value={agent.kind}
                      options={[
                        { value: 'claude', label: 'Claude Code' },
                        { value: 'claude-gpt', label: 'Claude Code with GPT' },
                      ]}
                      onChange={(kind) => updateAgent(index, {
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
                      onChange={(event) => updateAgent(index, { name: event.target.value })}
                      disabled={saving}
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
                        onChange={(configDirectory) => updateAgent(index, { configDirectory })}
                      />
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}

        <GhostButton
          type="button"
          size="md"
          onClick={addAgent}
          disabled={saving || customAgents.length >= maxCustomCodingAgents}
          className="flex items-center gap-2 px-3 disabled:opacity-40"
        >
          <Plus size={13} />
          Add agent
        </GhostButton>

        <InfoCallout>
          Pi, Pi Native, and Codex CLI are always available and can be reordered or selected as the default.
          Standard Claude Code instances use their selected <span className="font-mono text-ghost-blue">CLAUDE_CONFIG_DIR</span>;
          GPT instances use Kiwi Code&apos;s managed CLIProxyAPI profile. Missing standard directories are created on save.
        </InfoCallout>

        {!agentsValid && (
          <FeedbackMessage role="alert" tone="error">
            Select one default and give every custom agent a unique label. Standard Claude Code agents also need a unique config directory.
          </FeedbackMessage>
        )}
        <ActionFeedback feedback={action.feedback} />
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
