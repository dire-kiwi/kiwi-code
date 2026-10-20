import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { Folder, FolderGit2, LoaderCircle, Save } from 'lucide-react'
import { updateProjectBranchesAndPaths } from '@/api'
import { settingsPath } from '@/app/routes'
import { workspaceWorktreeBranchPrefix } from '@/codingAgents'
import { useAsyncFeedback } from '@/lib/useAsyncFeedback'
import { useAppSelector } from '@/store/hooks'
import { selectProfiles } from '@/store/slices/profiles'
import type { Project } from '@/types'
import { PrimaryButton } from '@/ui/buttons'
import { TextArea, TextInput } from '@/ui/inputs'
import { SectionHeader, Surface } from '@/ui/layout'
import { ActionFeedback, InfoCallout, StatusBadge } from '@/ui/feedback'

type ProjectBranchesSectionProps = {
  project: Project
  onProjectUpdated: (project: Project) => void
}

export function ProjectBranchesSection({ project, onProjectUpdated }: ProjectBranchesSectionProps) {
  const storedRelatedProjects = project.relatedProjects.join('\n')
  const [branchPrefix, setBranchPrefix] = useState(project.worktreeBranchPrefix)
  const [relatedProjects, setRelatedProjects] = useState(storedRelatedProjects)
  const action = useAsyncFeedback()
  const saving = action.pending
  const profile = useAppSelector(selectProfiles).find((item) => item.id === project.profileId)
  const workspacePrefix = workspaceWorktreeBranchPrefix(profile)

  useEffect(() => {
    setBranchPrefix(project.worktreeBranchPrefix)
    setRelatedProjects(storedRelatedProjects)
    action.clearFeedback()
  }, [action.clearFeedback, project.id, project.worktreeBranchPrefix, storedRelatedProjects])

  const normalizedBranchPrefix = branchPrefix.trim()
  const normalizedRelatedProjects = relatedProjects
    .split(/\r?\n/)
    .map((path) => path.trim())
    .filter(Boolean)
  const dirty = normalizedBranchPrefix !== project.worktreeBranchPrefix
    || normalizedRelatedProjects.join('\n') !== storedRelatedProjects

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!dirty || saving) return

    const updated = await action.run(
      'default',
      () => updateProjectBranchesAndPaths(project.id, normalizedBranchPrefix, normalizedRelatedProjects),
      { success: 'Branch and path settings saved.', failure: 'Could not update branch and path settings.' },
    )
    if (!updated) return
    onProjectUpdated(updated)
    setBranchPrefix(updated.worktreeBranchPrefix)
    setRelatedProjects(updated.relatedProjects.join('\n'))
  }

  return (
    <>
      <Surface
        as="form"
        variant="elevated-panel"
        onSubmit={(event) => void handleSubmit(event)}
        className="overflow-hidden"
      >
        <SectionHeader
          icon={<FolderGit2 size={16} />}
          title="Worktree branches"
          description="Choose the branch prefix used for new managed worktree threads."
          badge={(
            <StatusBadge tone={project.worktreeBranchPrefix ? 'success' : 'neutral'}>
              {project.worktreeBranchPrefix ? 'Project override' : 'Workspace default'}
            </StatusBadge>
          )}
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
                action.clearFeedback()
              }}
              maxLength={100}
              disabled={saving}
              autoComplete="off"
              spellCheck={false}
              placeholder={workspacePrefix}
              aria-describedby={action.feedback?.tone === 'error'
                ? 'project-worktree-branch-prefix-error'
                : 'project-worktree-branch-prefix-help'}
              className="mt-2.5"
            />
          </label>

          <InfoCallout className="mt-4">
            Used for new managed worktree branches, including their automatic rename after the first prompt.
            Leave empty to use the {profile?.name ?? 'workspace'} default,{' '}
            <span className="font-mono text-ghost-blue">{workspacePrefix}</span>, set in{' '}
            <Link to={settingsPath('workspaces')} className="text-ghost-blue underline-offset-2 hover:underline">
              Workspaces
            </Link>
            . Include separators such as <span className="font-mono text-ghost-blue">ivan/</span>. Existing
            branches are not renamed.
          </InfoCallout>

          <label
            htmlFor="project-related-projects"
            className="mt-6 block text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim"
          >
            Related project paths
            <TextArea
              id="project-related-projects"
              value={relatedProjects}
              onChange={(event) => {
                setRelatedProjects(event.target.value)
                action.clearFeedback()
              }}
              disabled={saving}
              spellCheck={false}
              placeholder={'../shared-library\n$HOME/personal/other-repo'}
              className="mt-2.5"
            />
          </label>
          <InfoCallout className="mt-4">
            Add one path per line. Relative paths resolve against the active thread directory, and paths may use
            <span className="font-mono text-ghost-blue"> $CWD</span>,
            <span className="font-mono text-ghost-blue"> $HOME</span>,
            <span className="font-mono text-ghost-blue"> $TMPDIR</span>, or
            <span className="font-mono text-ghost-blue"> ~</span>. Kiwi Code passes these directories to Claude
            Code and Codex CLI with <span className="font-mono text-ghost-blue">--add-dir</span>.
          </InfoCallout>

          <ActionFeedback
            id="project-worktree-branch-prefix-error"
            feedback={action.feedback}
            className="mt-4"
          />
        </div>

        <div className="flex items-center justify-end border-t border-ghost-border/60 bg-ghost-black/15 px-4 py-3 sm:px-5">
          <PrimaryButton
            type="submit"
            size="md"
            disabled={!dirty || saving}
            className="flex min-w-28 items-center justify-center gap-2"
          >
            {saving ? <LoaderCircle size={14} className="animate-spin" /> : <Save size={14} />}
            Save settings
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
    </>
  )
}
