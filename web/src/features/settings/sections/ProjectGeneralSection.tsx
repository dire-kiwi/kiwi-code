import { useEffect } from 'react'
import { Folder, Frame, Network, UserRound } from 'lucide-react'
import {
  updateProjectFigmaMCPEnabled,
  updateProjectProfile,
  updateProjectSubAgentNestingDepth,
} from '@/api'
import { MAX_SUB_AGENT_NESTING_DEPTH } from '@/lib/validation'
import { useAsyncFeedback } from '@/lib/useAsyncFeedback'
import type { Profile, Project } from '@/types'
import { Select } from '@/ui/inputs'
import { ActionFeedback, StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type ProjectGeneralSectionProps = {
  project: Project
  profiles: Profile[]
  onProjectUpdated: (project: Project) => void
}

export function ProjectGeneralSection({ project, profiles, onProjectUpdated }: ProjectGeneralSectionProps) {
  const profileAction = useAsyncFeedback()
  const nestingAction = useAsyncFeedback()
  const figmaAction = useAsyncFeedback()

  useEffect(() => {
    profileAction.clearFeedback()
    nestingAction.clearFeedback()
    figmaAction.clearFeedback()
  }, [
    figmaAction.clearFeedback,
    nestingAction.clearFeedback,
    profileAction.clearFeedback,
    project.id,
  ])

  const profileName = profiles.find((profile) => profile.id === project.profileId)?.name

  async function handleProfileChange(profileId: string) {
    if (profileId === project.profileId || profileAction.pending) return
    const updated = await profileAction.run(
      'default',
      () => updateProjectProfile(project.id, profileId),
      {
        success: 'Project moved to the selected profile.',
        failure: 'Could not move the project.',
      },
    )
    if (updated) onProjectUpdated(updated)
  }

  async function handleNestingChange(value: string) {
    if (nestingAction.pending) return
    const depth = value === 'inherit' ? null : Number(value)
    if (depth !== null && (!Number.isInteger(depth) || depth < 0 || depth > MAX_SUB_AGENT_NESTING_DEPTH)) return
    if (depth === (project.subAgentNestingDepthOverride ?? null)) return

    const updated = await nestingAction.run(
      'default',
      () => updateProjectSubAgentNestingDepth(project.id, depth),
      { success: 'Sub-agent nesting saved.', failure: 'Could not update sub-agent nesting.' },
    )
    if (updated) onProjectUpdated(updated)
  }

  async function handleFigmaToggle(enabled: boolean) {
    if (figmaAction.pending || enabled === project.figmaMCPEnabled) return
    const updated = await figmaAction.run(
      'default',
      () => updateProjectFigmaMCPEnabled(project.id, enabled),
      {
        success: enabled
          ? 'Figma MCP enabled. Restart the coding agent to load its tools.'
          : 'Figma MCP disabled. Restart the coding agent to drop its tools.',
        failure: 'Could not update Figma MCP.',
      },
    )
    if (updated) onProjectUpdated(updated)
  }

  return (
    <>
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
              disabled={profileAction.pending}
              aria-describedby={profileAction.feedback?.tone === 'error' ? 'project-profile-error' : undefined}
              leadingIcon={<Folder size={12} />}
            />
          </div>
          <ActionFeedback
            id="project-profile-error"
            feedback={profileAction.feedback}
            className="mt-4"
          />
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
              disabled={nestingAction.pending}
              aria-describedby={nestingAction.feedback?.tone === 'error'
                ? 'project-sub-agent-depth-error'
                : 'project-sub-agent-depth-help'}
              leadingIcon={<Network size={12} />}
            />
          </div>
          <p id="project-sub-agent-depth-help" className="mt-2 text-[9px] leading-4 text-ghost-faint">
            0 disables child agents for this project. Choose “Use global setting” to inherit the depth
            configured in the application settings.
          </p>
          <ActionFeedback
            id="project-sub-agent-depth-error"
            feedback={nestingAction.feedback}
            className="mt-4"
          />
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
              disabled={figmaAction.pending}
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

          <ActionFeedback
            id="project-figma-mcp-error"
            feedback={figmaAction.feedback}
            className="mt-4"
          />
        </div>
      </Surface>
    </>
  )
}
