import { Clock3, History, RefreshCw, SquareTerminal } from 'lucide-react'
import type { SessionClosureEvent, SessionClosureOverview } from '../../types'
import { useLastReadySubscriptionData, useSubscription } from '../../wire/react'
import { SessionClosuresTopic } from '../../wire/topics'
import { GhostButton } from '../atoms/Button'
import { StatusBadge } from '../atoms/StatusBadge'
import { Surface } from '../atoms/Surface'
import { LoadErrorPanel, LoadingPanel } from '../molecules/AsyncStatePanel'
import { InfoCallout } from '../molecules/InfoCallout'
import { PageIntro } from '../molecules/PageIntro'
import { ScreenHeader } from '../molecules/ScreenHeader'
import { SectionHeader } from '../molecules/SectionHeader'
import { FormScreenTemplate } from '../templates/FormScreenTemplate'

type SessionLogScreenProps = {
  onOpenSidebar: () => void
  onBack: () => void
}

const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
})

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? 'Unknown time' : dateFormatter.format(date)
}

function sessionKind(name: string) {
  if (name.endsWith('-terminal')) return 'Shell'
  if (name.endsWith('-tools')) return 'Tools'
  return name
}

function ClosureRow({ event }: { event: SessionClosureEvent }) {
  return (
    <li className="px-4 py-4 sm:px-5">
      <div className="flex items-start gap-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-ghost-raised text-ghost-green">
          <SquareTerminal size={15} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="truncate text-xs font-semibold text-ghost-bright-white">{event.threadTitle}</h3>
            <StatusBadge tone="neutral">Inactive</StatusBadge>
          </div>
          <p className="mt-1 truncate text-[10px] text-ghost-muted">{event.projectName}</p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {event.sessionNames.map((name) => (
              <span
                key={name}
                title={name}
                className="rounded-md border border-ghost-border/65 bg-ghost-black/25 px-2 py-1 font-mono text-[8px] text-ghost-dim"
              >
                {sessionKind(name)}
              </span>
            ))}
          </div>
        </div>
        <div className="hidden shrink-0 text-right sm:block">
          <p className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Closed</p>
          <p className="mt-1 text-[10px] text-ghost-muted">{formatDate(event.closedAt)}</p>
        </div>
      </div>
      <div className="mt-3 rounded-lg border border-ghost-border/50 bg-ghost-black/20 px-3 py-2.5 sm:ml-12">
        <div className="flex items-center gap-2 text-[9px] leading-4 text-ghost-muted">
          <Clock3 size={12} className="shrink-0 text-ghost-dim" />
          <span>Last activity {formatDate(event.lastActivityAt)}</span>
          <span className="text-ghost-faint sm:hidden">· Closed {formatDate(event.closedAt)}</span>
        </div>
      </div>
    </li>
  )
}

export function SessionLogScreen({ onOpenSidebar, onBack }: SessionLogScreenProps) {
  const subscription = useSubscription(SessionClosuresTopic, undefined)
  const overview = useLastReadySubscriptionData(subscription) as SessionClosureOverview | null
  const loading = subscription.state === 'loading'
  const error = subscription.state === 'error' ? subscription.error.message : ''

  return (
    <FormScreenTemplate
      header={(
        <ScreenHeader
          title="Session log"
          subtitle="automatic tmux closures"
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
            onClick={subscription.retry}
            disabled={loading}
            className="flex items-center gap-2 px-3 disabled:opacity-45"
          >
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
            Refresh
          </GhostButton>
        </div>

        <PageIntro icon={<History size={20} />} title="Closed tmux sessions">
          See when Kiwi Code stopped a thread’s Shell and Tools sessions after inactivity.
        </PageIntro>

        {loading && !overview ? (
          <LoadingPanel label="Loading session log" />
        ) : error && !overview ? (
          <LoadErrorPanel message={error} onRetry={subscription.retry} />
        ) : overview ? (
          <div className="space-y-5">
            {error && (
              <Surface className="border-ghost-bright-red/25 bg-ghost-bright-red/[0.04] px-4 py-3 text-[10px] text-ghost-bright-red">
                {error} The last loaded log is still shown.
              </Surface>
            )}

            <InfoCallout>
              Kiwi Code checks at startup and once per hour. It closes a thread’s tmux sessions after {overview.inactivityHours} hours without workspace use, tmux activity, attachment, or a new prompt. Attached sessions and working coding agents are kept. Opening the thread again creates fresh sessions.
            </InfoCallout>

            <Surface as="section" variant="elevated-panel" className="overflow-hidden">
              <SectionHeader
                icon={<History size={16} />}
                title="Closure history"
                description={`The latest ${overview.events.length} automatic closure${overview.events.length === 1 ? '' : 's'}.`}
                tone="green"
                badge={<StatusBadge monospace>{overview.events.length}</StatusBadge>}
              />
              {overview.events.length ? (
                <ul className="divide-y divide-ghost-border/50">
                  {overview.events.map((event) => <ClosureRow key={event.id} event={event} />)}
                </ul>
              ) : (
                <div className="px-5 py-10 text-center">
                  <SquareTerminal size={19} className="mx-auto text-ghost-faint" />
                  <p className="mt-3 text-xs font-medium text-ghost-muted">No automatic closures</p>
                  <p className="mt-1 text-[9px] text-ghost-faint">Inactive tmux sessions will appear here after they are closed.</p>
                </div>
              )}
            </Surface>

            <p className="text-center font-mono text-[8px] text-ghost-faint">
              Log checked {formatDate(overview.generatedAt)}
            </p>
          </div>
        ) : null}
      </div>
    </FormScreenTemplate>
  )
}
