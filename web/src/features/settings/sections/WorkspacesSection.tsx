import { useEffect, useState, type FormEvent } from 'react'
import { Bot, FolderGit2, Layers, LoaderCircle, Save } from 'lucide-react'
import { updateProfile } from '@/api'
import { DEFAULT_WORKTREE_BRANCH_PREFIX } from '@/codingAgents'
import { useAsyncFeedback } from '@/lib/useAsyncFeedback'
import { useAppDispatch, useAppSelector } from '@/store/hooks'
import { selectActiveProfileId } from '@/store/slices/preferences'
import { profileUpdated, selectProfiles } from '@/store/slices/profiles'
import { selectProjects } from '@/store/slices/projects'
import type { AppSettings, Profile } from '@/types'
import { GhostButton, PrimaryButton } from '@/ui/buttons'
import { Select, TextInput } from '@/ui/inputs'
import { ActionFeedback, InfoCallout, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type WorkspacesSectionProps = {
  settings: AppSettings
}

/** Per-profile (workspace) defaults: which coding agents it offers and its branch prefix. */
export function WorkspacesSection({ settings }: WorkspacesSectionProps) {
  const profiles = useAppSelector(selectProfiles)
  const activeProfileId = useAppSelector(selectActiveProfileId)
  const [selectedId, setSelectedId] = useState(activeProfileId)
  const profile = profiles.find((item) => item.id === selectedId) ?? profiles[0]

  if (!profile) return null

  return (
    <>
      <Surface as="section" variant="elevated-panel" className="overflow-hidden">
        <SectionHeader
          icon={<Layers size={16} />}
          title="Workspace"
          description="Each profile in the sidebar rail is a workspace with its own agents and branch defaults."
          tone="blue"
        />
        <div className="p-4 sm:p-5">
          <label
            htmlFor="workspace-select"
            className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
          >
            Configure workspace
          </label>
          <div className="mt-2.5 max-w-72">
            <Select
              id="workspace-select"
              value={profile.id}
              options={profiles.map((item) => ({ value: item.id, label: item.name }))}
              onChange={setSelectedId}
            />
          </div>
        </div>
      </Surface>

      <WorkspaceAgentsForm key={`agents-${profile.id}`} profile={profile} settings={settings} />
      <WorkspaceBranchesForm key={`branches-${profile.id}`} profile={profile} />
    </>
  )
}

function WorkspaceAgentsForm({ profile, settings }: { profile: Profile; settings: AppSettings }) {
  const dispatch = useAppDispatch()
  const storedDisabled = profile.disabledCodingAgents ?? []
  const [disabled, setDisabled] = useState<string[]>(storedDisabled)
  const action = useAsyncFeedback()
  const saving = action.pending

  useEffect(() => {
    setDisabled(profile.disabledCodingAgents ?? [])
  }, [profile.disabledCodingAgents])

  // IDs of agents removed from the global list are dropped on the next save.
  const configuredIds = new Set(settings.codingAgents.map((agent) => agent.id))
  const nextDisabled = disabled.filter((id) => configuredIds.has(id))
  const enabledCount = settings.codingAgents.length - nextDisabled.length
  const storedConfigured = storedDisabled.filter((id) => configuredIds.has(id))
  const dirty = nextDisabled.length !== storedConfigured.length
    || nextDisabled.some((id) => !storedConfigured.includes(id))

  function toggle(agentId: string, enabled: boolean) {
    action.clearFeedback()
    setDisabled((current) => enabled
      ? current.filter((id) => id !== agentId)
      : settings.codingAgents.map((agent) => agent.id).filter((id) => id === agentId || current.includes(id)))
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!dirty || saving || enabledCount === 0) return
    const updated = await action.run(
      'default',
      () => updateProfile(profile.id, { disabledCodingAgents: nextDisabled }),
      { success: `${profile.name} agents saved.`, failure: 'Could not update workspace agents.' },
    )
    if (updated) dispatch(profileUpdated(updated))
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSubmit(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<Bot size={16} />}
        title="Coding agents"
        description={`Choose which configured agents projects in ${profile.name} can start.`}
        tone="magenta"
        badge={(
          <StatusBadge tone={nextDisabled.length === 0 ? 'neutral' : 'success'}>
            {enabledCount} of {settings.codingAgents.length} enabled
          </StatusBadge>
        )}
      />

      <div className="space-y-2 p-4 sm:p-5">
        {settings.codingAgents.map((agent) => {
          const enabled = !nextDisabled.includes(agent.id)
          const inputId = `workspace-agent-${profile.id}-${agent.id}`
          return (
            <label
              key={agent.id}
              htmlFor={inputId}
              className="flex cursor-pointer items-center gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-2.5"
            >
              <input
                id={inputId}
                type="checkbox"
                checked={enabled}
                disabled={saving || (enabled && enabledCount === 1)}
                onChange={(event) => toggle(agent.id, event.target.checked)}
                className="size-4 accent-ghost-green"
              />
              <span className="min-w-0 flex-1 truncate text-[11px] font-medium text-ghost-bright-white">
                {agent.name}
              </span>
              {agent.isDefault && <StatusBadge tone="neutral">Default</StatusBadge>}
              {agent.configDirectory && (
                <span className="hidden truncate font-mono text-[9px] text-ghost-faint sm:block" title={agent.configDirectory}>
                  {agent.configDirectory}
                </span>
              )}
            </label>
          )
        })}

        <InfoCallout className="!mt-4">
          Hidden agents disappear from the new-thread picker and the thread agent switcher in this workspace. Threads
          already running a hidden agent keep it. At least one agent must stay enabled.
        </InfoCallout>

        <ActionFeedback feedback={action.feedback} className="!mt-4" />
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || saving || enabledCount === 0}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save agents
        </PrimaryButton>
      </div>
    </Surface>
  )
}

function WorkspaceBranchesForm({ profile }: { profile: Profile }) {
  const dispatch = useAppDispatch()
  const projects = useAppSelector(selectProjects)
  const storedPrefix = profile.worktreeBranchPrefix ?? ''
  const [branchPrefix, setBranchPrefix] = useState(storedPrefix)
  const action = useAsyncFeedback<'save' | 'inherit'>()
  const saving = action.pendingAction

  useEffect(() => {
    setBranchPrefix(profile.worktreeBranchPrefix ?? '')
  }, [profile.worktreeBranchPrefix])

  const normalizedPrefix = branchPrefix.trim()
  const dirty = normalizedPrefix !== storedPrefix
  const effectivePrefix = storedPrefix || DEFAULT_WORKTREE_BRANCH_PREFIX
  const overridingProjects = projects.filter((item) =>
    item.profileId === profile.id && item.worktreeBranchPrefix && item.worktreeBranchPrefix !== effectivePrefix)
  const explicitProjects = projects.filter((item) =>
    item.profileId === profile.id && item.worktreeBranchPrefix)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!dirty || saving) return
    const updated = await action.run(
      'save',
      () => updateProfile(profile.id, { worktreeBranchPrefix: normalizedPrefix }),
      { success: 'Workspace branch prefix saved.', failure: 'Could not update the branch prefix.' },
    )
    if (!updated) return
    dispatch(profileUpdated(updated))
    setBranchPrefix(updated.worktreeBranchPrefix ?? '')
  }

  async function handleInheritAll() {
    if (saving || explicitProjects.length === 0) return
    await action.run(
      'inherit',
      () => updateProfile(profile.id, { inheritProjectBranchPrefixes: true }),
      {
        success: `${explicitProjects.length} ${explicitProjects.length === 1 ? 'project now uses' : 'projects now use'} the workspace prefix.`,
        failure: 'Could not update project branch prefixes.',
      },
    )
  }

  return (
    <Surface
      as="form"
      variant="elevated-panel"
      onSubmit={(event) => void handleSubmit(event)}
      className="overflow-hidden"
    >
      <SectionHeader
        icon={<FolderGit2 size={16} />}
        title="Default branch prefix"
        description={`The worktree branch prefix for projects in ${profile.name} that do not set their own.`}
        tone="green"
        badge={(
          <StatusBadge tone={storedPrefix ? 'success' : 'neutral'}>
            {storedPrefix ? 'Custom' : 'Default'}
          </StatusBadge>
        )}
      />

      <div className="p-4 sm:p-5">
        <label
          htmlFor={`workspace-branch-prefix-${profile.id}`}
          className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
        >
          Branch prefix
          <TextInput
            id={`workspace-branch-prefix-${profile.id}`}
            variant="code-large"
            value={branchPrefix}
            onChange={(event) => {
              setBranchPrefix(event.target.value)
              action.clearFeedback()
            }}
            maxLength={100}
            disabled={Boolean(saving)}
            autoComplete="off"
            spellCheck={false}
            placeholder={DEFAULT_WORKTREE_BRANCH_PREFIX}
            className="mt-2.5"
          />
        </label>

        <InfoCallout className="mt-4">
          Leave empty to use <span className="font-mono text-ghost-blue">{DEFAULT_WORKTREE_BRANCH_PREFIX}</span>.
          Include separators such as <span className="font-mono text-ghost-blue">ivan/</span>. Existing branches are
          not renamed.
        </InfoCallout>

        {explicitProjects.length > 0 && (
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
            <p className="min-w-0 flex-1 text-[10px] leading-4 text-ghost-muted">
              {explicitProjects.length} {explicitProjects.length === 1 ? 'project sets' : 'projects set'} its own prefix
              {overridingProjects.length > 0 && (
                <> ({overridingProjects.map((item) => `${item.name}: ${item.worktreeBranchPrefix}`).join(', ')})</>
              )}
              {' '}and will not follow this default.
            </p>
            <GhostButton
              type="button"
              size="md"
              disabled={Boolean(saving) || dirty}
              title={dirty ? 'Save the prefix first' : undefined}
              onClick={() => void handleInheritAll()}
              className="flex items-center gap-2"
            >
              {saving === 'inherit' && <LoaderCircle size={13} className="animate-spin" />}
              Use workspace default for all
            </GhostButton>
          </div>
        )}

        <ActionFeedback feedback={action.feedback} className="mt-4" />
      </div>

      <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
        <PrimaryButton
          type="submit"
          size="md"
          disabled={!dirty || Boolean(saving)}
          className="flex min-w-28 items-center justify-center gap-2"
        >
          {saving === 'save' ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
          Save prefix
        </PrimaryButton>
      </div>
    </Surface>
  )
}
