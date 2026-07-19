import { useEffect, useState } from 'react'
import {
  Archive,
  Clock3,
  FolderGit2,
  GitBranch,
  RefreshCw,
  ShieldCheck,
  TriangleAlert,
} from 'lucide-react'
import { getCleanupOverview } from '../../api'
import { classNames } from '../../lib/classNames'
import type {
  CleanupOverview,
  ThreadCleanupOverview,
  WorktreeCleanupOverview,
} from '../../types'
import { GhostButton } from '../atoms/Button'
import { StatusBadge } from '../atoms/StatusBadge'
import { Surface } from '../atoms/Surface'
import { LoadErrorPanel, LoadingPanel } from '../molecules/AsyncStatePanel'
import { InfoCallout } from '../molecules/InfoCallout'
import { PageIntro } from '../molecules/PageIntro'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { SectionHeader } from '../molecules/SectionHeader'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

type CleanupScreenProps = {
  onOpenSidebar: () => void
  onBack: () => void
}

type ScheduleState = 'disabled' | 'scheduled' | 'due'

const absoluteDateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
})

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? 'Unknown time' : absoluteDateFormatter.format(date)
}

function scheduleState(scheduledAt: string | null, generatedAt: string): ScheduleState {
  if (!scheduledAt) return 'disabled'
  const scheduledTime = Date.parse(scheduledAt)
  const generatedTime = Date.parse(generatedAt)
  if (Number.isNaN(scheduledTime) || Number.isNaN(generatedTime)) return 'scheduled'
  return scheduledTime <= generatedTime ? 'due' : 'scheduled'
}

function retentionLabel(days: number) {
  if (days === 0) return 'Automatic deletion disabled'
  return `${days} day${days === 1 ? '' : 's'} retention`
}

function ScheduleBadge({ state }: { state: ScheduleState }) {
  return (
    <StatusBadge tone={state === 'due' ? 'error' : state === 'scheduled' ? 'warning' : 'neutral'}>
      {state === 'due' ? 'Due now' : state === 'scheduled' ? 'Scheduled' : 'Kept'}
    </StatusBadge>
  )
}

function CleanupScheduleDetails({
  state,
  scheduledAt,
  className,
}: {
  state: ScheduleState
  scheduledAt: string | null
  className?: string
}) {
  return (
    <div className={classNames('shrink-0 sm:max-w-56 sm:text-right', className)}>
      <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">
        {state === 'disabled' ? 'Retention' : state === 'due' ? 'Eligible since' : 'Eligible for deletion'}
      </p>
      <p className={classNames(
        'mt-1 text-[10px] leading-4',
        state === 'due' ? 'text-ghost-bright-red' : 'text-ghost-muted',
      )}>
        {scheduledAt ? formatDate(scheduledAt) : 'Kept until cleanup is enabled'}
      </p>
    </div>
  )
}

function ThreadRow({ item, generatedAt }: { item: ThreadCleanupOverview; generatedAt: string }) {
  const state = scheduleState(item.scheduledDeletionAt, generatedAt)
  return (
    <li className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:px-5">
      <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-ghost-raised text-ghost-yellow">
        <Archive size={15} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="truncate text-xs font-semibold text-ghost-bright-white">{item.threadTitle}</h3>
          <ScheduleBadge state={state} />
        </div>
        <p className="mt-1 truncate text-[10px] text-ghost-muted">{item.projectName}</p>
        <p className="mt-1 font-mono text-[8px] text-ghost-faint">
          Archived {formatDate(item.archivedAt)}
        </p>
      </div>
      <CleanupScheduleDetails state={state} scheduledAt={item.scheduledDeletionAt} />
    </li>
  )
}

function worktreeTitle(item: WorktreeCleanupOverview) {
  if (item.threadTitle) return item.threadTitle
  if (item.branch) return item.branch
  return item.worktreePath.split('/').filter(Boolean).at(-1) || 'Unattached worktree'
}

function WorktreeRow({ item, generatedAt }: { item: WorktreeCleanupOverview; generatedAt: string }) {
  const state = scheduleState(item.scheduledDeletionAt, generatedAt)
  const blocked = state === 'due' && item.hasUncommittedChanges
  const statusUnknown = Boolean(item.inspectionError)
  const badge = statusUnknown
    ? (
        <StatusBadge tone="warning">Status unknown</StatusBadge>
      )
    : item.hasUncommittedChanges
      ? (
          <StatusBadge tone="info">{blocked ? 'Blocked by changes' : 'Changes present'}</StatusBadge>
        )
      : <ScheduleBadge state={state} />

  return (
    <li className="px-4 py-4 sm:px-5">
      <div className="flex items-start gap-3">
        <span className={`grid size-9 shrink-0 place-items-center rounded-lg bg-ghost-raised ${
          blocked || statusUnknown ? 'text-ghost-yellow' : 'text-ghost-green'
        }`}>
          {blocked || statusUnknown ? <TriangleAlert size={15} /> : <GitBranch size={15} />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="min-w-0 truncate text-xs font-semibold text-ghost-bright-white">{worktreeTitle(item)}</h3>
            {badge}
          </div>
          <p className="mt-1 truncate text-[10px] text-ghost-muted">
            {item.projectName || `Project ${item.projectId.slice(0, 8)}`}
            {item.branch ? ` · ${item.branch}` : ''}
          </p>
          <p className="mt-2 break-all font-mono text-[9px] leading-4 text-ghost-faint" title={item.worktreePath}>
            {item.worktreePath}
          </p>
        </div>
        <CleanupScheduleDetails
          state={state}
          scheduledAt={item.scheduledDeletionAt}
          className={classNames('hidden sm:block', blocked && '[&_p:last-child]:text-ghost-yellow')}
        />
      </div>

      <div className={`mt-3 rounded-lg border px-3 py-2.5 sm:ml-12 ${
        statusUnknown
          ? 'border-ghost-yellow/20 bg-ghost-yellow/[0.04]'
          : item.hasUncommittedChanges
            ? 'border-ghost-blue/20 bg-ghost-blue/[0.04]'
            : 'border-ghost-border/50 bg-ghost-black/20'
      }`}>
        <div className="flex items-start gap-2">
          {statusUnknown
            ? <TriangleAlert size={12} className="mt-0.5 shrink-0 text-ghost-yellow" />
            : item.hasUncommittedChanges
              ? <ShieldCheck size={12} className="mt-0.5 shrink-0 text-ghost-blue" />
              : <ShieldCheck size={12} className="mt-0.5 shrink-0 text-ghost-green" />}
          <div className="min-w-0 text-[9px] leading-4 text-ghost-muted">
            {statusUnknown ? (
              <>
                <p>Cleanup will keep this worktree until its Git status can be checked.</p>
                <p className="mt-1 break-words text-ghost-faint">{item.inspectionError}</p>
              </>
            ) : item.hasUncommittedChanges ? (
              <p>
                Uncommitted changes detected. Dire Mux will keep this worktree and check it again during later cleanup cycles.
              </p>
            ) : (
              <p>No staged, unstaged, or untracked changes were detected when this page was refreshed.</p>
            )}
          </div>
        </div>
      </div>

      <CleanupScheduleDetails
        state={state}
        scheduledAt={item.scheduledDeletionAt}
        className={classNames('mt-3 sm:hidden', blocked && '[&_p:last-child]:text-ghost-yellow')}
      />
    </li>
  )
}

export function CleanupScreen({ onOpenSidebar, onBack }: CleanupScreenProps) {
  const [overview, setOverview] = useState<CleanupOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [loadKey, setLoadKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    getCleanupOverview(controller.signal)
      .then(setOverview)
      .catch((reason) => {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : 'Could not load the cleanup queue.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [loadKey])

  const blockedWorktrees = overview?.worktrees.filter((item) =>
    item.hasUncommittedChanges
      && scheduleState(item.scheduledDeletionAt, overview.generatedAt) === 'due',
  ).length ?? 0

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title="Cleanup"
          subtitle="automatic deletion queue"
          backLabel="Back to workspace"
          onOpenSidebar={onOpenSidebar}
          onBack={onBack}
        />
      )}
    >
      <div className="relative mx-auto w-full max-w-[52rem]">
        <div className="absolute right-0 top-0 z-10">
          <GhostButton
            type="button"
            size="md"
            onClick={() => setLoadKey((current) => current + 1)}
            disabled={loading}
            className="flex items-center gap-2 px-3 disabled:opacity-45"
          >
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
            Refresh
          </GhostButton>
        </div>
        <PageIntro icon={<Clock3 size={20} />} title="Scheduled deletion">
          Review archived threads and unattached Git worktrees before automatic cleanup removes them.
        </PageIntro>

        {loading && !overview ? (
          <LoadingPanel label="Loading cleanup queue" />
        ) : error && !overview ? (
          <LoadErrorPanel
            message={error}
            onRetry={() => setLoadKey((current) => current + 1)}
          />
        ) : overview ? (
          <div className="space-y-5">
            {error && (
              <Surface className="border-ghost-bright-red/25 bg-ghost-bright-red/[0.04] px-4 py-3 text-[10px] text-ghost-bright-red">
                {error} The last loaded queue is still shown.
              </Surface>
            )}

            <div className="grid gap-3 sm:grid-cols-3">
              <Surface className="px-4 py-3.5">
                <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Archived threads</p>
                <p className="mt-2 text-xl font-semibold text-ghost-bright-white">{overview.threads.length}</p>
                <p className="mt-1 text-[9px] text-ghost-muted">{retentionLabel(overview.archivedThreadRetentionDays)}</p>
              </Surface>
              <Surface className="px-4 py-3.5">
                <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Unattached worktrees</p>
                <p className="mt-2 text-xl font-semibold text-ghost-bright-white">{overview.worktrees.length}</p>
                <p className="mt-1 text-[9px] text-ghost-muted">{retentionLabel(overview.orphanedWorktreeRetentionDays)}</p>
              </Surface>
              <Surface className={`px-4 py-3.5 ${blockedWorktrees ? 'border-ghost-blue/30 bg-ghost-blue/[0.04]' : ''}`}>
                <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Blocked by changes</p>
                <p className={`mt-2 text-xl font-semibold ${blockedWorktrees ? 'text-ghost-blue' : 'text-ghost-bright-white'}`}>
                  {blockedWorktrees}
                </p>
                <p className="mt-1 text-[9px] text-ghost-muted">Due worktrees kept safe</p>
              </Surface>
            </div>

            <InfoCallout>
              Times show when an item becomes eligible. Cleanup runs at startup and once per hour, so deletion happens in the first successful cycle after that time. Dirty worktrees and worktrees whose Git status cannot be checked are kept; Git branches are never deleted.
            </InfoCallout>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<Archive size={16} />}
                title="Archived threads"
                description="Deleting a thread stops its tmux sessions and detaches its managed worktree."
                tone="yellow"
                badge={<StatusBadge monospace>{overview.threads.length}</StatusBadge>}
              />
              {overview.threads.length ? (
                <ul className="divide-y divide-ghost-border/50">
                  {overview.threads.map((item) => (
                    <ThreadRow key={`${item.projectId}:${item.threadId}`} item={item} generatedAt={overview.generatedAt} />
                  ))}
                </ul>
              ) : (
                <div className="px-5 py-9 text-center">
                  <Archive size={18} className="mx-auto text-ghost-faint" />
                  <p className="mt-3 text-xs font-medium text-ghost-muted">No archived threads</p>
                  <p className="mt-1 text-[9px] text-ghost-faint">Nothing is queued for thread deletion.</p>
                </div>
              )}
            </Surface>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<FolderGit2 size={16} />}
                title="Unattached worktrees"
                description="Worktrees remain here after their thread or project is deleted, until they are clean and their retention time has elapsed."
                tone="green"
                badge={<StatusBadge monospace>{overview.worktrees.length}</StatusBadge>}
              />
              {overview.worktrees.length ? (
                <ul className="divide-y divide-ghost-border/50">
                  {overview.worktrees.map((item) => (
                    <WorktreeRow key={item.worktreePath} item={item} generatedAt={overview.generatedAt} />
                  ))}
                </ul>
              ) : (
                <div className="px-5 py-9 text-center">
                  <FolderGit2 size={18} className="mx-auto text-ghost-faint" />
                  <p className="mt-3 text-xs font-medium text-ghost-muted">No unattached worktrees</p>
                  <p className="mt-1 text-[9px] text-ghost-faint">Nothing is queued for worktree deletion.</p>
                </div>
              )}
            </Surface>

            <p className="text-center font-mono text-[8px] text-ghost-faint">
              Status checked {formatDate(overview.generatedAt)}
            </p>
          </div>
        ) : null}
      </div>
    </FormScreenTemplate>
  )
}
