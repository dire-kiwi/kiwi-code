import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import {
  Bot,
  Check,
  CornerUpLeft,
  Folder,
  FolderGit2,
  GitBranch,
  LoaderCircle,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  SquareTerminal,
  X,
} from 'lucide-react'
import { updateThreadTitle } from '../../api'
import { formatWhen } from '../../lib/formatWhen'
import type { Project, Thread, ThreadPlan, ThreadUsageSnapshot, WorkflowRun } from '../../types'
import { Button, GhostButton, PrimaryButton } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { TextInput } from '../atoms/Input'
import { ThreadUsageLimits } from '../molecules/ThreadUsageLimits'
import { ThreadPlansPanel } from './ThreadPlansPanel'
import { ThreadRecordingsPanel } from './ThreadRecordingsPanel'
import { WorkflowRunsPanel } from './WorkflowRunsPanel'

type SidebarTab = 'thread' | 'activity' | 'recordings'

const sidebarTabs: ReadonlyArray<{ id: SidebarTab; label: string }> = [
  { id: 'thread', label: 'Thread' },
  { id: 'activity', label: 'Activity' },
  { id: 'recordings', label: 'Recordings' },
]

const liveWorkflowStates: ReadonlySet<WorkflowRun['state']> = new Set(['queued', 'running'])
const activeWorkflowStates: ReadonlySet<WorkflowRun['state']> = new Set(['queued', 'running', 'paused'])

type ThreadProjectSidebarProps = {
  project: Project
  thread: Thread
  usage?: ThreadUsageSnapshot
  workflowRuns: WorkflowRun[]
  workflowsError: string
  plans: ThreadPlan[]
  plansError: string
  onViewPlan: (plan: ThreadPlan) => void
  onWorkflowUpdated: (run: WorkflowRun) => void
  expanded: boolean
  onExpandedChange: (expanded: boolean) => void
  onThreadUpdated: (thread: Thread) => void
  onSelectThread: (thread: Thread) => void
}

export function ThreadProjectSidebar({
  project,
  thread,
  usage,
  workflowRuns,
  workflowsError,
  plans,
  plansError,
  onViewPlan,
  onWorkflowUpdated,
  expanded,
  onExpandedChange,
  onThreadUpdated,
  onSelectThread,
}: ThreadProjectSidebarProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const tabRefs = useRef<Partial<Record<SidebarTab, HTMLButtonElement | null>>>({})
  const [tab, setTab] = useState<SidebarTab>('thread')
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(thread.title)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const threadsById = new Map(project.threads.map((candidate) => [candidate.id, candidate]))
  let mainThread = thread
  const visitedAncestors = new Set<string>()
  while (mainThread.parentThreadId && !visitedAncestors.has(mainThread.id)) {
    visitedAncestors.add(mainThread.id)
    const parent = threadsById.get(mainThread.parentThreadId)
    if (!parent) break
    mainThread = parent
  }
  const descendantIds = new Set<string>()
  let foundDescendant = true
  while (foundDescendant) {
    foundDescendant = false
    for (const candidate of project.threads) {
      if (!candidate.parentThreadId || descendantIds.has(candidate.id)) continue
      if (candidate.parentThreadId === mainThread.id || descendantIds.has(candidate.parentThreadId)) {
        descendantIds.add(candidate.id)
        foundDescendant = true
      }
    }
  }
  const completedAgentThreads = project.threads
    .filter((candidate) => descendantIds.has(candidate.id) && candidate.closedAt)
    .sort((left, right) => Date.parse(right.closedAt!) - Date.parse(left.closedAt!))

  const hasLiveWorkflow = workflowRuns.some((run) => liveWorkflowStates.has(run.state))
  const orderedRuns = [...workflowRuns].sort(
    (left, right) => Number(activeWorkflowStates.has(right.state)) - Number(activeWorkflowStates.has(left.state)),
  )
  const showWorkflows = !thread.parentThreadId
  const showPlans = !thread.parentThreadId || plans.length > 0 || Boolean(plansError)
  const showAgents = Boolean(thread.parentThreadId) || completedAgentThreads.length > 0

  useEffect(() => {
    if (!editing) setTitle(thread.title)
  }, [editing, thread.title])

  useEffect(() => {
    if (!editing || !expanded || tab !== 'thread') return
    const frame = requestAnimationFrame(() => inputRef.current?.select())
    return () => cancelAnimationFrame(frame)
  }, [editing, expanded, tab])

  function beginEditing() {
    setTitle(thread.title)
    setError('')
    setEditing(true)
  }

  function cancelEditing() {
    if (saving) return
    setTitle(thread.title)
    setError('')
    setEditing(false)
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const nextTitle = title.trim()
    if (!nextTitle || saving) return
    if (nextTitle === thread.title) {
      setEditing(false)
      setError('')
      return
    }

    setSaving(true)
    setError('')
    try {
      const updated = await updateThreadTitle(project.id, thread.id, nextTitle)
      onThreadUpdated(updated)
      setTitle(updated.title)
      setEditing(false)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not rename the thread.')
    } finally {
      setSaving(false)
    }
  }

  function handleTitleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== 'Escape') return
    event.preventDefault()
    cancelEditing()
  }

  function handleTabListKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const index = sidebarTabs.findIndex((candidate) => candidate.id === tab)
    const nextIndex = event.key === 'ArrowLeft'
      ? (index + sidebarTabs.length - 1) % sidebarTabs.length
      : event.key === 'ArrowRight'
        ? (index + 1) % sidebarTabs.length
        : event.key === 'Home' ? 0 : sidebarTabs.length - 1
    const next = sidebarTabs[nextIndex]!
    setTab(next.id)
    tabRefs.current[next.id]?.focus()
  }

  const sectionDivider = <div className="my-5 h-px bg-ghost-border/55" />

  return (
    <>
      {!expanded && (
        <div className="absolute right-0 top-0 z-20 flex h-[4.5rem] items-center px-2 md:hidden">
          <IconButton
            type="button"
            shrink
            variant="ghost"
            onClick={() => onExpandedChange(true)}
            aria-expanded={false}
            aria-controls="thread-project-details"
            aria-label="Expand thread details"
            title="Expand details"
          >
            <PanelRightOpen size={16} />
          </IconButton>
        </div>
      )}

      <aside
        className={`relative h-full shrink-0 flex-col overflow-hidden border-l border-ghost-border/70 bg-ghost-panel/95 transition-[width] duration-200 ease-out ${
          expanded ? 'flex w-[19rem]' : 'hidden w-12 md:flex'
        }`}
        aria-label="Thread and project details"
      >
      <header className={`flex h-[4.5rem] shrink-0 items-center border-b border-ghost-border/70 ${
        expanded ? 'justify-between gap-2 pl-4 pr-2' : 'justify-center px-2'
      }`}>
        {expanded && (
          <div className="min-w-0">
            <p className="truncate text-xs font-semibold text-ghost-bright-white" title={thread.title}>
              {thread.title}
            </p>
            <p className="mt-1 truncate font-mono text-[8px] uppercase tracking-[0.14em] text-ghost-faint" title={project.name}>
              {project.name} · {thread.parentThreadId ? 'agent thread' : 'main thread'}
            </p>
          </div>
        )}
        <IconButton
          type="button"
          shrink
          variant="ghost"
          onClick={() => onExpandedChange(!expanded)}
          aria-expanded={expanded}
          aria-controls="thread-project-details"
          aria-label={expanded ? 'Collapse thread details' : 'Expand thread details'}
          title={expanded ? 'Collapse details' : 'Expand details'}
        >
          {expanded ? <PanelRightClose size={16} /> : <PanelRightOpen size={16} />}
        </IconButton>
      </header>

        {expanded && (
          <div id="thread-project-details" className="flex min-h-0 w-[19rem] flex-1 flex-col">
            <div
              role="tablist"
              aria-label="Thread detail sections"
              className="mx-3 mt-3 flex shrink-0 gap-1 rounded-lg bg-ghost-black/40 p-1"
              onKeyDown={handleTabListKeyDown}
            >
              {sidebarTabs.map(({ id, label }) => {
                const selected = tab === id
                const showBadge = id === 'activity' && hasLiveWorkflow
                return (
                  <button
                    key={id}
                    ref={(node) => { tabRefs.current[id] = node }}
                    type="button"
                    role="tab"
                    id={`sidebar-tab-${id}`}
                    aria-selected={selected}
                    aria-controls={`sidebar-panel-${id}`}
                    tabIndex={selected ? 0 : -1}
                    onClick={() => setTab(id)}
                    className={`relative flex-1 rounded-md px-2 py-1.5 font-mono text-[9px] font-semibold uppercase tracking-[0.08em] transition ${
                      selected
                        ? 'bg-ghost-raised text-ghost-bright-white'
                        : 'text-ghost-dim hover:text-ghost-bright-white'
                    }`}
                  >
                    {label}
                    {showBadge && (
                      <>
                        <span
                          aria-hidden="true"
                          className="absolute right-1.5 top-1 size-1.5 rounded-full bg-ghost-green motion-safe:animate-pulse"
                        />
                        <span className="sr-only">workflow running</span>
                      </>
                    )}
                  </button>
                )
              })}
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto">
              <div
                role="tabpanel"
                id="sidebar-panel-thread"
                aria-labelledby="sidebar-tab-thread"
                hidden={tab !== 'thread'}
                className="px-4 py-4"
              >
                <section>
                  <p className="font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint">
                    Thread
                  </p>

                  {editing ? (
                    <form onSubmit={(event) => void handleSubmit(event)} className="mt-2.5">
                      <label className="sr-only" htmlFor="thread-title-input">Thread name</label>
                      <TextInput
                        ref={inputRef}
                        id="thread-title-input"
                        variant="title"
                        value={title}
                        onChange={(event) => {
                          setTitle(event.target.value)
                          setError('')
                        }}
                        onKeyDown={handleTitleKeyDown}
                        maxLength={120}
                        disabled={saving}
                        autoFocus
                        autoComplete="off"
                      />
                      <div className="mt-2 flex items-center gap-1.5">
                        <PrimaryButton
                          type="submit"
                          disabled={saving || !title.trim()}
                          className="flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-[10px]"
                        >
                          {saving ? <LoaderCircle size={12} className="animate-spin" /> : <Check size={12} />}
                          Save
                        </PrimaryButton>
                        <GhostButton
                          type="button"
                          onClick={cancelEditing}
                          disabled={saving}
                          className="flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-[10px] disabled:opacity-40"
                        >
                          <X size={12} />
                          Cancel
                        </GhostButton>
                      </div>
                      {error && (
                        <p role="alert" className="mt-2 text-[10px] leading-4 text-ghost-bright-red">
                          {error}
                        </p>
                      )}
                    </form>
                  ) : (
                    <Button
                      type="button"
                      onClick={beginEditing}
                      aria-label={`Edit thread name: ${thread.title}`}
                      className="group mt-2.5 flex w-full items-start gap-2 rounded-xl border border-transparent px-2 py-2 text-left transition hover:border-ghost-border/70 hover:bg-ghost-raised/55"
                      title="Edit thread name"
                    >
                      <SquareTerminal size={15} className="mt-0.5 shrink-0 text-ghost-green" />
                      <span className="min-w-0 flex-1 break-words text-sm font-semibold leading-5 text-ghost-bright-white">
                        {thread.title}
                      </span>
                      <Pencil size={12} className="mt-1 shrink-0 text-ghost-faint transition group-hover:text-ghost-green" />
                    </Button>
                  )}

                  <div className="mt-2.5 flex flex-wrap items-center gap-1.5 px-2">
                    {thread.worktree ? (
                      <span className="inline-flex items-center gap-1 rounded-full border border-ghost-green/35 bg-ghost-green/[0.07] px-2 py-0.5 font-mono text-[9px] text-ghost-green">
                        <FolderGit2 size={10} />
                        worktree
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 rounded-full border border-ghost-border/70 px-2 py-0.5 font-mono text-[9px] text-ghost-muted">
                        <Folder size={10} />
                        workspace
                      </span>
                    )}
                    {thread.branch && (
                      <span
                        className="inline-flex min-w-0 items-center gap-1 rounded-full border border-ghost-border/70 px-2 py-0.5 font-mono text-[9px] text-ghost-muted"
                        title={thread.branch}
                      >
                        <GitBranch size={10} className="shrink-0" />
                        <span className="truncate">{thread.branch}</span>
                      </span>
                    )}
                  </div>
                  <p className="mt-2 break-all px-2 font-mono text-[9px] leading-4 text-ghost-faint" title={thread.cwd}>
                    {thread.cwd}
                  </p>
                </section>

                {sectionDivider}

                <ThreadUsageLimits
                  projectId={project.id}
                  thread={thread}
                  usage={usage}
                  showAllThreads={project.threads.some((candidate) => candidate.parentThreadId === thread.id)}
                  onThreadUpdated={onThreadUpdated}
                />
              </div>

              <div
                role="tabpanel"
                id="sidebar-panel-activity"
                aria-labelledby="sidebar-tab-activity"
                hidden={tab !== 'activity'}
                className="px-4 py-4"
              >
                {showWorkflows && (
                  <WorkflowRunsPanel
                    projectId={project.id}
                    threadId={thread.id}
                    threads={project.threads}
                    runs={orderedRuns}
                    error={workflowsError}
                    onRunUpdated={onWorkflowUpdated}
                    onSelectThread={onSelectThread}
                  />
                )}

                {showPlans && (
                  <>
                    {showWorkflows && sectionDivider}
                    <ThreadPlansPanel
                      projectId={project.id}
                      plans={plans}
                      error={plansError}
                      onViewPlan={onViewPlan}
                    />
                  </>
                )}

                {showAgents && (
                  <>
                    {(showWorkflows || showPlans) && sectionDivider}
                    <section aria-labelledby="completed-agent-threads-heading">
                      <div className="flex items-center justify-between gap-2">
                        <p
                          id="completed-agent-threads-heading"
                          className="flex items-center gap-1.5 font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint"
                          title="Completed delegated runs stay available for review until the main thread is deleted."
                        >
                          <Bot size={10} />
                          Agent threads
                        </p>
                        {completedAgentThreads.length > 0 && (
                          <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[8px] text-ghost-faint">
                            {completedAgentThreads.length}
                          </span>
                        )}
                      </div>

                      {thread.parentThreadId && mainThread.id !== thread.id && (
                        <Button
                          type="button"
                          onClick={() => onSelectThread(mainThread)}
                          className="mt-2 flex w-full items-center gap-2 rounded-lg border border-ghost-border/55 bg-ghost-black/20 px-2.5 py-2 text-left transition hover:border-ghost-green/35 hover:bg-ghost-green/[0.06]"
                          aria-label={`Return to main thread ${mainThread.title}`}
                        >
                          <CornerUpLeft size={12} className="shrink-0 text-ghost-green" />
                          <span className="min-w-0 flex-1">
                            <span className="block font-mono text-[8px] uppercase tracking-[0.1em] text-ghost-faint">Main thread</span>
                            <span className="mt-0.5 block truncate text-[10px] font-medium text-ghost-white">{mainThread.title}</span>
                          </span>
                        </Button>
                      )}

                      {completedAgentThreads.length > 0 && (
                        <ul className="mt-1.5 space-y-0.5" aria-label="Completed agent threads">
                          {completedAgentThreads.map((agentThread) => {
                            const selected = agentThread.id === thread.id
                            const finishedAt = new Date(agentThread.closedAt!)
                            const finishedLabel = Number.isNaN(finishedAt.getTime())
                              ? 'Completed'
                              : `Completed ${finishedAt.toLocaleString()}`
                            return (
                              <li key={agentThread.id}>
                                <Button
                                  type="button"
                                  onClick={() => onSelectThread(agentThread)}
                                  aria-current={selected ? 'page' : undefined}
                                  className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition ${
                                    selected ? 'bg-ghost-green/[0.09]' : 'hover:bg-ghost-raised/55'
                                  }`}
                                  title={`${agentThread.title}\n${finishedLabel}${agentThread.branch ? `\n${agentThread.branch}` : ''}`}
                                >
                                  <Bot size={12} className={`shrink-0 ${selected ? 'text-ghost-green' : 'text-ghost-cyan'}`} />
                                  <span className="min-w-0 flex-1 truncate text-[10px] font-medium text-ghost-white">
                                    {agentThread.title}
                                  </span>
                                  <span className="shrink-0 font-mono text-[8px] text-ghost-faint">
                                    {formatWhen(agentThread.closedAt!)}
                                  </span>
                                </Button>
                              </li>
                            )
                          })}
                        </ul>
                      )}
                    </section>
                  </>
                )}
              </div>

              <div
                role="tabpanel"
                id="sidebar-panel-recordings"
                aria-labelledby="sidebar-tab-recordings"
                hidden={tab !== 'recordings'}
                className="px-4 py-4"
              >
                <ThreadRecordingsPanel
                  projectId={project.id}
                  threadId={thread.id}
                  active={tab === 'recordings'}
                />
              </div>

            </div>
          </div>
        )}
      </aside>
    </>
  )
}
