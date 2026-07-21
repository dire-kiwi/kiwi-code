import { Check, Folder, GitBranch } from 'lucide-react'
import type { GitBranchState, Project } from '../../types'
import { RadioInput } from '../atoms/Input'
import { SelectableCard } from '../atoms/SelectableCard'
import { ThreadLocationOption } from '../molecules/ThreadLocationOption'
import { WorktreeBaseBranchField } from '../molecules/WorktreeBaseBranchField'

export type ThreadLocation = 'project' | 'worktree'

type ThreadLocationPickerProps = {
  project: Project
  value: ThreadLocation
  disabled: boolean
  baseBranch: string
  branchState: GitBranchState | null
  branchesLoading: boolean
  branchLoadError: string
  onChange: (location: ThreadLocation) => void
  onBaseBranchChange: (branch: string) => void
  onReloadBranches: () => void
}

export function ThreadLocationPicker({
  project,
  value,
  disabled,
  baseBranch,
  branchState,
  branchesLoading,
  branchLoadError,
  onChange,
  onBaseBranchChange,
  onReloadBranches,
}: ThreadLocationPickerProps) {
  return (
    <fieldset className="mt-6 min-w-0">
      <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-ghost-dim">
        Working directory
      </legend>
      <div className="mt-2.5 space-y-2.5">
        <ThreadLocationOption
          value="project"
          selected={value === 'project'}
          icon={<Folder size={15} />}
          iconClassName="text-ghost-muted"
          title="Project folder"
          disabled={disabled}
          onSelect={() => onChange('project')}
        >
          <span className="mt-1 block truncate font-mono text-[10px] text-ghost-dim" title={project.path}>
            {project.path}
          </span>
        </ThreadLocationOption>

        {project.isGitRepo ? (
          <SelectableCard
            as="div"
            layout="container"
            selected={value === 'worktree'}
          >
            <label className="flex cursor-pointer items-start gap-3 p-3.5">
              <RadioInput
                name="thread-location"
                value="worktree"
                checked={value === 'worktree'}
                onChange={() => onChange('worktree')}
                disabled={disabled}
              />
              <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-ghost-raised text-ghost-green">
                <GitBranch size={15} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-xs font-semibold text-ghost-bright-white">New Git worktree</span>
                <span className="mt-1 block text-[10px] leading-4 text-ghost-dim">
                  Create an isolated checkout and branch without changing the project folder.
                </span>
              </span>
              {value === 'worktree' && (
                <span className="grid size-5 shrink-0 place-items-center rounded-full bg-ghost-green text-ghost-black">
                  <Check size={12} strokeWidth={2.5} />
                </span>
              )}
            </label>

            {value === 'worktree' && (
              <WorktreeBaseBranchField
                branchState={branchState}
                value={baseBranch}
                branchPrefix={project.worktreeBranchPrefix}
                loading={branchesLoading}
                error={branchLoadError}
                disabled={disabled}
                onChange={onBaseBranchChange}
                onRetry={onReloadBranches}
              />
            )}
          </SelectableCard>
        ) : (
          <div className="flex items-center gap-3 rounded-xl border border-dashed border-ghost-border/55 px-3.5 py-3 text-ghost-faint">
            <GitBranch size={15} className="shrink-0" />
            <p className="text-[10px] leading-4">
              Git worktrees are available when this project is a Git repository.
            </p>
          </div>
        )}
      </div>
    </fieldset>
  )
}
