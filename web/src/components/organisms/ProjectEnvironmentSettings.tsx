import { useEffect, useState, type FormEvent } from 'react'
import {
  Braces,
  Check,
  LoaderCircle,
  Plus,
  Save,
  Settings2,
  TerminalSquare,
  Trash2,
  X,
} from 'lucide-react'
import { updateProjectEnvironment } from '../../api'
import type {
  EnvironmentAction,
  EnvironmentVariable,
  LocalEnvironment,
  PlatformScripts,
  Project,
} from '../../types'
import { GhostButton, PrimaryButton } from '../atoms/Button'
import { TextArea, TextInput } from '../atoms/Input'
import { Surface } from '../atoms/Surface'
import { FeedbackMessage } from '../molecules/FeedbackMessage'
import { InfoCallout } from '../molecules/InfoCallout'
import { SectionHeader } from '../molecules/SectionHeader'

const platforms: Array<{ id: keyof PlatformScripts; label: string }> = [
  { id: 'default', label: 'Default' },
  { id: 'macos', label: 'macOS' },
  { id: 'linux', label: 'Linux' },
  { id: 'windows', label: 'Windows' },
]

const emptyScripts = (): PlatformScripts => ({ default: '', macos: '', linux: '', windows: '' })

function cloneEnvironment(environment: LocalEnvironment): LocalEnvironment {
  return {
    ...environment,
    setupScripts: { ...environment.setupScripts },
    cleanupScripts: { ...environment.cleanupScripts },
    variables: environment.variables.map((variable) => ({ ...variable })),
    actions: environment.actions.map((action) => ({ ...action, scripts: { ...action.scripts } })),
  }
}

function actionId() {
  try {
    return crypto.randomUUID()
  } catch {
    return `action-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
  }
}

type PlatformTabsProps = {
  value: keyof PlatformScripts
  onChange: (value: keyof PlatformScripts) => void
  label: string
}

function PlatformTabs({ value, onChange, label }: PlatformTabsProps) {
  return (
    <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label={label}>
      {platforms.map((platform) => (
        <button
          key={platform.id}
          type="button"
          role="tab"
          aria-selected={value === platform.id}
          onClick={() => onChange(platform.id)}
          className={`rounded-lg px-2.5 py-1.5 text-[10px] font-medium transition ${
            value === platform.id
              ? 'bg-ghost-raised text-ghost-bright-white'
              : 'text-ghost-dim hover:bg-ghost-raised/60 hover:text-ghost-white'
          }`}
        >
          {platform.label}
        </button>
      ))}
    </div>
  )
}

type VariablesDialogProps = {
  variables: EnvironmentVariable[]
  onClose: () => void
  onApply: (variables: EnvironmentVariable[]) => void
}

function VariablesDialog({ variables, onClose, onApply }: VariablesDialogProps) {
  const [draft, setDraft] = useState(() => variables.map((variable) => ({ ...variable })))

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-[100] grid place-items-center bg-ghost-black/80 p-4 backdrop-blur-sm" onMouseDown={onClose}>
      <Surface
        as="section"
        variant="elevated-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="environment-variables-title"
        className="max-h-[80vh] w-full max-w-2xl overflow-y-auto p-4 sm:p-5"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 id="environment-variables-title" className="text-sm font-semibold text-ghost-bright-white">
              Environment variables
            </h3>
            <p className="mt-1 text-[10px] leading-4 text-ghost-dim">
              Available to setup, cleanup, and action commands. Values are stored locally in project settings.
            </p>
          </div>
          <GhostButton type="button" size="xs" aria-label="Close variables" onClick={onClose}>
            <X size={14} />
          </GhostButton>
        </div>

        <div className="mt-5 space-y-2">
          {draft.map((variable, index) => (
            <div key={index} className="grid gap-2 rounded-xl border border-ghost-border/60 bg-ghost-black/25 p-3 sm:grid-cols-[0.85fr_1.4fr_auto]">
              <TextInput
                variant="code"
                value={variable.name}
                placeholder="VARIABLE_NAME"
                aria-label={`Variable ${index + 1} name`}
                onChange={(event) => setDraft((current) => current.map((item, itemIndex) => (
                  itemIndex === index ? { ...item, name: event.target.value } : item
                )))}
              />
              <TextInput
                variant="code"
                value={variable.value}
                placeholder="Value"
                aria-label={`Variable ${index + 1} value`}
                onChange={(event) => setDraft((current) => current.map((item, itemIndex) => (
                  itemIndex === index ? { ...item, value: event.target.value } : item
                )))}
              />
              <GhostButton
                type="button"
                size="sm"
                className="flex items-center justify-center text-ghost-dim hover:text-ghost-bright-red"
                aria-label={`Remove variable ${index + 1}`}
                onClick={() => setDraft((current) => current.filter((_, itemIndex) => itemIndex !== index))}
              >
                <Trash2 size={13} />
              </GhostButton>
            </div>
          ))}
          {draft.length === 0 && (
            <div className="rounded-xl border border-dashed border-ghost-border/70 px-4 py-6 text-center text-[10px] text-ghost-faint">
              No custom variables configured.
            </div>
          )}
        </div>

        <div className="mt-4 flex items-center justify-between gap-3">
          <GhostButton
            type="button"
            size="sm"
            className="flex items-center gap-2"
            onClick={() => setDraft((current) => [...current, { name: '', value: '' }])}
          >
            <Plus size={13} />
            Add variable
          </GhostButton>
          <PrimaryButton
            type="button"
            size="sm"
            onClick={() => {
              onApply(draft)
              onClose()
            }}
          >
            Done
          </PrimaryButton>
        </div>
      </Surface>
    </div>
  )
}

type ProjectEnvironmentSettingsProps = {
  project: Project
  onProjectUpdated: (project: Project) => void
  onSavingChange?: (saving: boolean) => void
}

export function ProjectEnvironmentSettings({
  project,
  onProjectUpdated,
  onSavingChange,
}: ProjectEnvironmentSettingsProps) {
  const [environment, setEnvironment] = useState(() => cloneEnvironment(project.environment))
  const [setupPlatform, setSetupPlatform] = useState<keyof PlatformScripts>('default')
  const [cleanupPlatform, setCleanupPlatform] = useState<keyof PlatformScripts>('default')
  const [actionPlatforms, setActionPlatforms] = useState<Record<string, keyof PlatformScripts>>({})
  const [variablesOpen, setVariablesOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  useEffect(() => {
    setEnvironment(cloneEnvironment(project.environment))
    setError('')
    setMessage('')
  }, [project.environment, project.id])

  useEffect(() => {
    onSavingChange?.(saving)
    return () => onSavingChange?.(false)
  }, [onSavingChange, saving])

  function updateScripts(field: 'setupScripts' | 'cleanupScripts', platform: keyof PlatformScripts, value: string) {
    setEnvironment((current) => ({
      ...current,
      [field]: { ...current[field], [platform]: value },
    }))
    setError('')
    setMessage('')
  }

  function updateAction(id: string, update: Partial<EnvironmentAction>) {
    setEnvironment((current) => ({
      ...current,
      actions: current.actions.map((action) => action.id === id ? { ...action, ...update } : action),
    }))
    setError('')
    setMessage('')
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const updated = await updateProjectEnvironment(project.id, environment)
      onProjectUpdated(updated)
      setEnvironment(cloneEnvironment(updated.environment))
      setMessage('Local environment saved.')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save the local environment.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      className="overflow-hidden"
      onSubmit={(event) => void handleSubmit(event)}
    >
      <SectionHeader
        icon={<Settings2 size={16} />}
        title="Local environment"
        description="Prepare new worktrees and add reusable commands to the workspace header."
        tone="green"
      />

      <div className="space-y-6 p-4 sm:p-5">
        <label htmlFor="environment-name" className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
          Name
          <TextInput
            id="environment-name"
            value={environment.name}
            maxLength={80}
            required
            disabled={saving}
            className="mt-2.5"
            onChange={(event) => {
              setEnvironment((current) => ({ ...current, name: event.target.value }))
              setError('')
              setMessage('')
            }}
          />
        </label>

        <div>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 className="text-xs font-semibold text-ghost-bright-white">Setup script</h3>
              <p className="mt-1 text-[10px] leading-4 text-ghost-dim">Runs at the project root when a managed worktree is created.</p>
            </div>
            <GhostButton type="button" size="sm" className="flex items-center gap-2" onClick={() => setVariablesOpen(true)}>
              <Braces size={13} />
              Variables{environment.variables.length > 0 ? ` (${environment.variables.length})` : ''}
            </GhostButton>
          </div>
          <div className="mt-3">
            <PlatformTabs value={setupPlatform} onChange={setSetupPlatform} label="Setup script platform" />
            <TextArea
              className="mt-2 min-h-36"
              value={environment.setupScripts[setupPlatform]}
              disabled={saving}
              aria-label={`${platforms.find((item) => item.id === setupPlatform)?.label} setup script`}
              placeholder={'npm install\n./run/setup.sh'}
              onChange={(event) => updateScripts('setupScripts', setupPlatform, event.target.value)}
            />
          </div>
        </div>

        <div>
          <h3 className="text-xs font-semibold text-ghost-bright-white">Cleanup script</h3>
          <p className="mt-1 text-[10px] leading-4 text-ghost-dim">Runs at the project root before an unattached worktree is removed.</p>
          <div className="mt-3">
            <PlatformTabs value={cleanupPlatform} onChange={setCleanupPlatform} label="Cleanup script platform" />
            <TextArea
              className="mt-2 min-h-32"
              value={environment.cleanupScripts[cleanupPlatform]}
              disabled={saving}
              aria-label={`${platforms.find((item) => item.id === cleanupPlatform)?.label} cleanup script`}
              placeholder={'docker compose down --remove-orphans\nrm -rf .cache/tmp'}
              onChange={(event) => updateScripts('cleanupScripts', cleanupPlatform, event.target.value)}
            />
          </div>
        </div>

        <div>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 className="text-xs font-semibold text-ghost-bright-white">Actions</h3>
              <p className="mt-1 text-[10px] leading-4 text-ghost-dim">Commands shown in the workspace header and run in a Process shell.</p>
            </div>
            <GhostButton
              type="button"
              size="sm"
              className="flex items-center gap-2"
              disabled={saving || environment.actions.length >= 16}
              onClick={() => {
                const id = actionId()
                setEnvironment((current) => ({
                  ...current,
                  actions: [...current.actions, { id, name: '', scripts: emptyScripts() }],
                }))
                setActionPlatforms((current) => ({ ...current, [id]: 'default' }))
                setError('')
                setMessage('')
              }}
            >
              <Plus size={13} />
              Add action
            </GhostButton>
          </div>

          <div className="mt-3 space-y-3">
            {environment.actions.map((action, index) => {
              const platform = actionPlatforms[action.id] ?? 'default'
              return (
                <div key={action.id} className="rounded-xl border border-ghost-border/65 bg-ghost-black/25 p-3.5">
                  <div className="flex items-center gap-2">
                    <TerminalSquare size={14} className="shrink-0 text-ghost-green" />
                    <TextInput
                      value={action.name}
                      maxLength={40}
                      required
                      disabled={saving}
                      aria-label={`Action ${index + 1} name`}
                      placeholder="Run tests"
                      onChange={(event) => updateAction(action.id, { name: event.target.value })}
                    />
                    <GhostButton
                      type="button"
                      size="sm"
                      disabled={saving}
                      className="flex shrink-0 items-center justify-center text-ghost-dim hover:text-ghost-bright-red"
                      aria-label={`Remove ${action.name || `action ${index + 1}`}`}
                      onClick={() => setEnvironment((current) => ({
                        ...current,
                        actions: current.actions.filter((item) => item.id !== action.id),
                      }))}
                    >
                      <Trash2 size={13} />
                    </GhostButton>
                  </div>
                  <div className="mt-3">
                    <PlatformTabs
                      value={platform}
                      onChange={(value) => setActionPlatforms((current) => ({ ...current, [action.id]: value }))}
                      label={`${action.name || `Action ${index + 1}`} command platform`}
                    />
                    <TextArea
                      className="mt-2 min-h-24"
                      value={action.scripts[platform]}
                      disabled={saving}
                      required={platform === 'default' && !Object.entries(action.scripts).some(([key, value]) => key !== 'default' && value.trim())}
                      aria-label={`${action.name || `Action ${index + 1}`} command`}
                      placeholder="npm test"
                      onChange={(event) => updateAction(action.id, {
                        scripts: { ...action.scripts, [platform]: event.target.value },
                      })}
                    />
                  </div>
                </div>
              )
            })}
            {environment.actions.length === 0 && (
              <div className="rounded-xl border border-dashed border-ghost-border/70 px-4 py-5 text-center text-[10px] leading-4 text-ghost-faint">
                Add an action to run project commands from the workspace header.
              </div>
            )}
          </div>
        </div>

        <InfoCallout>
          Platform-specific scripts override Default on that operating system. Commands receive your custom variables plus
          <span className="font-mono text-ghost-blue"> CODEX_WORKTREE_PATH</span> and
          <span className="font-mono text-ghost-blue"> KIWI_CODE_WORKTREE_PATH</span>.
        </InfoCallout>

        {error && <FeedbackMessage role="alert" tone="error">{error}</FeedbackMessage>}
        {message && (
          <FeedbackMessage role="status" tone="success" size="status" className="flex items-center gap-2">
            <Check size={13} />
            {message}
          </FeedbackMessage>
        )}
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton type="submit" size="md" disabled={saving} className="flex min-w-36 items-center justify-center gap-2">
          {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save environment
        </PrimaryButton>
      </div>

      {variablesOpen && (
        <VariablesDialog
          variables={environment.variables}
          onClose={() => setVariablesOpen(false)}
          onApply={(variables) => {
            setEnvironment((current) => ({ ...current, variables }))
            setError('')
            setMessage('')
          }}
        />
      )}
    </Surface>
  )
}
