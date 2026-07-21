import { GitBranch, LoaderCircle, RefreshCw } from 'lucide-react'
import type { GitBranchState } from '../../types'
import { Button } from '../atoms/Button'
import { Select } from '../atoms/Select'

type WorktreeBaseBranchFieldProps = {
  branchState: GitBranchState | null
  value: string
  loading: boolean
  error: string
  disabled: boolean
  onChange: (branch: string) => void
  onRetry: () => void
}

export function WorktreeBaseBranchField({
  branchState,
  value,
  loading,
  error,
  disabled,
  onChange,
  onRetry,
}: WorktreeBaseBranchFieldProps) {
  const noBranches = !loading && branchState?.branches.length === 0

  return (
    <div className="mx-3.5 border-t border-ghost-green/15 pb-3.5 pt-3">
      <div className="flex items-center justify-between gap-3">
        <label
          htmlFor="worktree-base-branch"
          className="text-[9px] font-semibold uppercase tracking-[0.13em] text-ghost-dim"
        >
          Base branch
        </label>
        {loading && (
          <span className="flex items-center gap-1.5 text-[9px] text-ghost-faint">
            <LoaderCircle size={10} className="animate-spin" />
            Loading branches
          </span>
        )}
      </div>
      <div className="mt-2">
        <Select
          id="worktree-base-branch"
          value={value}
          options={[
            ...(!value ? [{
              value: '',
              label: loading ? 'Loading branches…' : 'No local branches available',
            }] : []),
            ...(branchState?.branches.map((branch) => ({
              value: branch.name,
              label: `${branch.name}${branch.current ? ' (current)' : ''}`,
            })) ?? []),
          ]}
          onChange={onChange}
          disabled={disabled || loading || Boolean(error) || !branchState?.branches.length}
          aria-describedby="worktree-base-branch-help"
          leadingIcon={<GitBranch size={12} />}
        />
      </div>

      {error || noBranches ? (
        <div
          id="worktree-base-branch-help"
          role="alert"
          className="mt-2 flex items-start justify-between gap-3 rounded-md border border-ghost-red/20 bg-ghost-red/[0.06] px-2.5 py-2 text-[9px] leading-4 text-ghost-bright-red"
        >
          <span>{error || 'No local branches are available. Commit the repository before creating a worktree.'}</span>
          <Button
            type="button"
            variant="text"
            onClick={onRetry}
            disabled={loading || disabled}
            className="flex shrink-0 items-center gap-1 disabled:opacity-40"
          >
            <RefreshCw size={10} />
            Retry
          </Button>
        </div>
      ) : (
        <p id="worktree-base-branch-help" className="mt-2 text-[9px] leading-4 text-ghost-faint">
          <span className="font-mono text-ghost-muted">kiwi-code/thread-…</span> starts from this branch and is renamed after the first coding-agent prompt.
        </p>
      )}
    </div>
  )
}
