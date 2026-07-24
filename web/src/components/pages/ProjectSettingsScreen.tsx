import { useEffect, useState, type FormEvent } from 'react'
import {
  Check,
  Folder,
  FolderGit2,
  Frame,
  LoaderCircle,
  Network,
  Save,
  UserRound,
} from 'lucide-react'
import {
  updateProjectFigmaMCPEnabled,
  updateProjectProfile,
  updateProjectSubAgentNestingDepth,
  updateProjectWorktreeBranchPrefix,
} from '../../api'
import { MAX_SUB_AGENT_NESTING_DEPTH } from '../../lib/validation'
import type { Profile, Project } from '../../types'
import { PrimaryButton } from '../atoms/Button'
import { TextInput } from '../atoms/Input'
import { Select } from '../atoms/Select'
import { StatusBadge } from '../atoms/StatusBadge'
import { Surface } from '../atoms/Surface'
import { FeedbackMessage } from '../molecules/FeedbackMessage'
import { InfoCallout } from '../molecules/InfoCallout'
import { PageIntro } from '../molecules/PageIntro'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { SectionHeader } from '../molecules/SectionHeader'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

type ProjectSettingsScreenProps = {
  project: Project
  profiles: Profile[]
  onOpenSidebar: () => void
  onBack: () => void
  onProjectUpdated: (project: Project) => void
}

export function ProjectSettingsScreen({
  project,
  profiles,
  onOpenSidebar,
  onBack,
  onProjectUpdated,
}: ProjectSettingsScreenProps) {
  const [profileSaving, setProfileSaving] = useState(false)
  const [profileError, setProfileError] = useState('')
  const [profileMessage, setProfileMessage] = useState('')
  const [nestingSaving, setNestingSaving] = useState(false)
  const [nestingError, setNestingError] = useState('')
  const [nestingMessage, setNestingMessage] = useState('')
  const [branchPrefix, setBranchPrefix] = useState(project.worktreeBranchPrefix)
  const [branchPrefixSaving, setBranchPrefixSaving] = useState(false)
  const [branchPrefixError, setBranchPrefixError] = useState('')
  const [branchPrefixMessage, setBranchPrefixMessage] = useState('')
  const [figmaSaving, setFigmaSaving] = useState(false)
  const [figmaError, setFigmaError] = useState('')
  const [figmaMessage, setFigmaMessage] = useState('')

  useEffect(() => {
    setBranchPrefixError('')
    setBranchPrefixMessage('')
    setProfileError('')
    setProfileMessage('')
    setNestingError('')
    setNestingMessage('')
    setFigmaError('')
    setFigmaMessage('')
  }, [project.id])

  useEffect(() => {
    setBranchPrefix(project.worktreeBranchPrefix)
  }, [project.id, project.worktreeBranchPrefix])

  const saving = profileSaving || nestingSaving || branchPrefixSaving || figmaSaving
  const profileName = profiles.find((profile) => profile.id === project.profileId)?.name
  const normalizedBranchPrefix = branchPrefix.trim()
  const branchPrefixDirty = normalizedBranchPrefix.length > 0
    && normalizedBranchPrefix !== project.worktreeBranchPrefix

  async function handleProfileChange(profileId: string) {
    if (profileId === project.profileId || profileSaving) return
    setProfileSaving(true)
    setProfileError('')
    setProfileMessage('')
    try {
      const updated = await updateProjectProfile(project.id, profileId)
      onProjectUpdated(updated)
      setProfileMessage('Project moved to the selected profile.')
    } catch (reason) {
      setProfileError(reason instanceof Error ? reason.message : 'Could not move the project.')
    } finally {
      setProfileSaving(false)
    }
  }

  async function handleNestingChange(value: string) {
    if (nestingSaving) return
    const depth = value === 'inherit' ? null : Number(value)
    if (depth !== null && (!Number.isInteger(depth) || depth < 0 || depth > MAX_SUB_AGENT_NESTING_DEPTH)) return
    if (depth === (project.subAgentNestingDepthOverride ?? null)) return

    setNestingSaving(true)
    setNestingError('')
    setNestingMessage('')
    try {
      const updated = await updateProjectSubAgentNestingDepth(project.id, depth)
      onProjectUpdated(updated)
      setNestingMessage('Sub-agent nesting saved.')
    } catch (reason) {
      setNestingError(reason instanceof Error ? reason.message : 'Could not update sub-agent nesting.')
    } finally {
      setNestingSaving(false)
    }
  }

  async function handleFigmaToggle(enabled: boolean) {
    if (figmaSaving || enabled === project.figmaMCPEnabled) return
    setFigmaSaving(true)
    setFigmaError('')
    setFigmaMessage('')
    try {
      const updated = await updateProjectFigmaMCPEnabled(project.id, enabled)
      onProjectUpdated(updated)
      setFigmaMessage(updated.figmaMCPEnabled
        ? 'Figma MCP enabled. Restart the coding agent to load its tools.'
        : 'Figma MCP disabled. Restart the coding agent to drop its tools.')
    } catch (reason) {
      setFigmaError(reason instanceof Error ? reason.message : 'Could not update Figma MCP.')
    } finally {
      setFigmaSaving(false)
    }
  }

  async function handleBranchPrefixSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!branchPrefixDirty || branchPrefixSaving) return

    setBranchPrefixSaving(true)
    setBranchPrefixError('')
    setBranchPrefixMessage('')
    try {
      const updated = await updateProjectWorktreeBranchPrefix(project.id, normalizedBranchPrefix)
      onProjectUpdated(updated)
      setBranchPrefix(updated.worktreeBranchPrefix)
      setBranchPrefixMessage('Branch prefix saved.')
    } catch (reason) {
      setBranchPrefixError(reason instanceof Error ? reason.message : 'Could not update the branch prefix.')
    } finally {
      setBranchPrefixSaving(false)
    }
  }

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title="Project settings"
          subtitle={project.name}
          backLabel="Back to workspace"
          backDisabled={saving}
          onOpenSidebar={onOpenSidebar}
          onBack={onBack}
        />
      )}
    >
      <div className="relative mx-auto w-full max-w-[44rem]">
        <PageIntro icon={<Folder size={20} />} title={project.name}>
          Configure the profile, agent behavior, and worktree branches for this project on {project.host}.
        </PageIntro>

        <div className="space-y-5">
          <Surface as="section" variant="elevated-panel" className="overflow-hidden">
            <SectionHeader
              icon={<UserRound size={16} />}
              title="Profile"
              description="Move this project and its threads to a different profile."
              tone="blue"
              badge={profileName ? <StatusBadge tone="neutral">{profileName}</StatusBadge> : undefined}
            />

            <div className="p-4 sm:p-5">
              <label
                htmlFor="project-profile-select"
                className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
              >
                Profile
              </label>
              <div className="mt-2.5 max-w-72">
                <Select
                  id="project-profile-select"
                  value={project.profileId}
                  options={profiles.map((profile) => ({ value: profile.id, label: profile.name }))}
                  onChange={(profileId) => void handleProfileChange(profileId)}
                  disabled={profileSaving}
                  aria-describedby={profileError ? 'project-profile-error' : undefined}
                  leadingIcon={<Folder size={12} />}
                />
              </div>
              {profileError && (
                <FeedbackMessage id="project-profile-error" role="alert" tone="error" className="mt-4">
                  {profileError}
                </FeedbackMessage>
              )}
              {profileMessage && (
                <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
                  <Check size={13} />
                  {profileMessage}
                </FeedbackMessage>
              )}
            </div>
          </Surface>

          <Surface as="section" variant="elevated-panel" className="overflow-hidden">
            <SectionHeader
              icon={<Network size={16} />}
              title="Sub-agent nesting"
              description="Limit child-agent generations for this project; overrides the global setting."
              tone="blue"
              badge={(
                <StatusBadge tone={project.subAgentNestingDepthOverride == null ? 'neutral' : 'success'}>
                  {project.subAgentNestingDepthOverride == null ? 'Global setting' : 'Override'}
                </StatusBadge>
              )}
            />

            <div className="p-4 sm:p-5">
              <label
                htmlFor="project-sub-agent-depth-select"
                className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
              >
                Nesting depth
              </label>
              <div className="mt-2.5 max-w-72">
                <Select
                  id="project-sub-agent-depth-select"
                  value={project.subAgentNestingDepthOverride?.toString() ?? 'inherit'}
                  options={[
                    { value: 'inherit', label: 'Use global setting' },
                    { value: '0', label: 'Disabled' },
                    ...Array.from(
                      { length: MAX_SUB_AGENT_NESTING_DEPTH },
                      (_, index) => index + 1,
                    ).map((depth) => ({
                      value: String(depth),
                      label: `${depth} ${depth === 1 ? 'child level' : 'child levels'}`,
                    })),
                  ]}
                  onChange={(depth) => void handleNestingChange(depth)}
                  disabled={nestingSaving}
                  aria-describedby={nestingError ? 'project-sub-agent-depth-error' : 'project-sub-agent-depth-help'}
                  leadingIcon={<Network size={12} />}
                />
              </div>
              <p id="project-sub-agent-depth-help" className="mt-2 text-[9px] leading-4 text-ghost-faint">
                0 disables child agents for this project. Choose “Use global setting” to inherit the depth
                configured in the application settings.
              </p>
              {nestingError && (
                <FeedbackMessage id="project-sub-agent-depth-error" role="alert" tone="error" className="mt-4">
                  {nestingError}
                </FeedbackMessage>
              )}
              {nestingMessage && (
                <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
                  <Check size={13} />
                  {nestingMessage}
                </FeedbackMessage>
              )}
            </div>
          </Surface>

          <Surface as="section" variant="elevated-panel" className="overflow-hidden">
            <SectionHeader
              icon={<Frame size={16} />}
              title="Figma MCP"
              description="Expose the Figma MCP server to Pi and Claude Code in this project."
              tone="blue"
              badge={(
                <StatusBadge tone={project.figmaMCPEnabled ? 'success' : 'neutral'}>
                  {project.figmaMCPEnabled ? 'Enabled' : 'Disabled'}
                </StatusBadge>
              )}
            />

            <div className="p-4 sm:p-5">
              <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3.5">
                <input
                  type="checkbox"
                  checked={project.figmaMCPEnabled}
                  disabled={figmaSaving}
                  onChange={(event) => void handleFigmaToggle(event.target.checked)}
                  className="mt-0.5 size-4 accent-ghost-green"
                />
                <span>
                  <span className="block text-[10px] font-semibold text-ghost-bright-white">Enable Figma MCP tools</span>
                  <span className="mt-1 block text-[9px] leading-4 text-ghost-faint">
                    Claude Code loads the server directly; Pi loads it through the bundled MCP bridge extension. The
                    endpoint is configured in the application settings and the Figma desktop app must be running.
                  </span>
                </span>
              </label>

              {figmaError && (
                <FeedbackMessage id="project-figma-mcp-error" role="alert" tone="error" className="mt-4">
                  {figmaError}
                </FeedbackMessage>
              )}
              {figmaMessage && (
                <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
                  <Check size={13} />
                  {figmaMessage}
                </FeedbackMessage>
              )}
            </div>
          </Surface>

          <Surface
            as="form"
            variant="elevated-panel"
            onSubmit={(event) => void handleBranchPrefixSubmit(event)}
            className="overflow-hidden"
          >
            <SectionHeader
              icon={<FolderGit2 size={16} />}
              title="Worktree branches"
              description="Choose the branch prefix used for new managed worktree threads."
              tone="green"
            />

            <div className="p-4 sm:p-5">
              <label
                htmlFor="project-worktree-branch-prefix"
                className="block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
              >
                Branch prefix
                <TextInput
                  id="project-worktree-branch-prefix"
                  variant="code-large"
                  value={branchPrefix}
                  onChange={(event) => {
                    setBranchPrefix(event.target.value)
                    setBranchPrefixError('')
                    setBranchPrefixMessage('')
                  }}
                  maxLength={100}
                  disabled={branchPrefixSaving}
                  required
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="kiwi-code/"
                  aria-describedby={branchPrefixError
                    ? 'project-worktree-branch-prefix-error'
                    : 'project-worktree-branch-prefix-help'}
                  className="mt-2.5"
                />
              </label>

              <InfoCallout className="mt-4">
                Used for new managed worktree branches, including their automatic rename after the first prompt.
                Include separators such as <span className="font-mono text-ghost-blue">ivan/</span>. Existing
                branches are not renamed.
              </InfoCallout>

              {branchPrefixError && (
                <FeedbackMessage id="project-worktree-branch-prefix-error" role="alert" tone="error" className="mt-4">
                  {branchPrefixError}
                </FeedbackMessage>
              )}
              {branchPrefixMessage && (
                <FeedbackMessage role="status" tone="success" size="status" className="mt-4 flex items-center gap-2">
                  <Check size={13} />
                  {branchPrefixMessage}
                </FeedbackMessage>
              )}
            </div>

            <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
              <PrimaryButton
                type="submit"
                size="md"
                disabled={!branchPrefixDirty || branchPrefixSaving}
                className="flex min-w-28 items-center justify-center gap-2"
              >
                {branchPrefixSaving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
                Save prefix
              </PrimaryButton>
            </div>
          </Surface>

          <Surface as="section" variant="elevated-panel" className="overflow-hidden">
            <SectionHeader
              icon={<Folder size={16} />}
              title="Paths"
              description="Where this project lives on disk."
              tone="yellow"
            />

            <div className="p-4 sm:p-5">
              <div className="rounded-xl border border-ghost-border/55 bg-ghost-black/25 px-3.5 py-3">
                <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Project root</p>
                <p className="mt-1.5 break-all font-mono text-[10px] leading-4 text-ghost-muted" title={project.path}>
                  {project.path}
                </p>
              </div>
            </div>
          </Surface>
        </div>
      </div>
    </FormScreenTemplate>
  )
}
