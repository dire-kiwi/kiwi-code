import { useMemo, useState, type FormEvent } from 'react'
import {
  Bot,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  LoaderCircle,
  Pause,
  Play,
  Save,
  Square,
  Workflow,
} from 'lucide-react'
import { pauseWorkflow, resumeWorkflow, saveWorkflow, stopWorkflow } from '../../api'
import { formatDuration } from '../../lib/formatDuration'
import type { SavedWorkflow, Thread, WorkflowRun } from '../../types'
import { Button, GhostButton, PrimaryButton } from '../atoms/Button'
import { TextInput } from '../atoms/Input'
import { Select } from '../atoms/Select'

const workflowNamePattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$/

type WorkflowRunsPanelProps = {
  projectId: string
  threadId: string
  threads: Thread[]
  runs: WorkflowRun[]
  error: string
  onRunUpdated: (run: WorkflowRun) => void
  onSelectThread: (thread: Thread) => void
}

type RunAction = 'pause' | 'resume' | 'stop'

type SaveDraft = {
  runId: string
  name: string
  scope: 'project' | 'personal'
  overwrite: boolean
}

function workflowStateTone(state: WorkflowRun['state']) {
  switch (state) {
    case 'running': return 'border-ghost-green/40 bg-ghost-green/10 text-ghost-green'
    case 'queued': return 'border-ghost-yellow/40 bg-ghost-yellow/10 text-ghost-yellow'
    case 'paused': return 'border-ghost-blue/40 bg-ghost-blue/10 text-ghost-blue'
    case 'finished': return 'border-ghost-cyan/35 bg-ghost-cyan/10 text-ghost-cyan'
    case 'failed': return 'border-ghost-bright-red/35 bg-ghost-bright-red/10 text-ghost-bright-red'
    case 'stopped': return 'border-ghost-border/70 bg-ghost-raised/55 text-ghost-dim'
  }
}

function runElapsed(run: WorkflowRun) {
  const start = Date.parse(run.startedAt ?? run.createdAt)
  const finish = run.finishedAt ? Date.parse(run.finishedAt) : Date.now()
  if (!Number.isFinite(start) || !Number.isFinite(finish)) return ''
  return formatDuration(Math.max(0, finish - start))
}

function defaultSaveName(run: WorkflowRun) {
  return run.name
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80) || 'workflow'
}

export function WorkflowRunsPanel({
  projectId,
  threadId,
  threads,
  runs,
  error,
  onRunUpdated,
  onSelectThread,
}: WorkflowRunsPanelProps) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [pending, setPending] = useState<Record<string, RunAction | 'save' | undefined>>({})
  const [actionErrors, setActionErrors] = useState<Record<string, string | undefined>>({})
  const [saveDraft, setSaveDraft] = useState<SaveDraft | null>(null)
  const [saved, setSaved] = useState<{ runId: string; workflow: SavedWorkflow } | null>(null)
  const threadsById = useMemo(() => new Map(threads.map((thread) => [thread.id, thread])), [threads])

  function toggle(runId: string) {
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(runId)) next.delete(runId)
      else next.add(runId)
      return next
    })
  }

  async function runAction(run: WorkflowRun, action: RunAction) {
    if (pending[run.id]) return
    setPending((current) => ({ ...current, [run.id]: action }))
    setActionErrors((current) => ({ ...current, [run.id]: undefined }))
    try {
      const updated = action === 'pause'
        ? await pauseWorkflow(projectId, threadId, run.id)
        : action === 'resume'
          ? await resumeWorkflow(projectId, threadId, run.id)
          : await stopWorkflow(projectId, threadId, run.id)
      onRunUpdated(updated)
    } catch (reason) {
      setActionErrors((current) => ({
        ...current,
        [run.id]: reason instanceof Error ? reason.message : `Could not ${action} the workflow.`,
      }))
    } finally {
      setPending((current) => ({ ...current, [run.id]: undefined }))
    }
  }

  async function submitSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!saveDraft || !workflowNamePattern.test(saveDraft.name) || pending[saveDraft.runId]) return
    setPending((current) => ({ ...current, [saveDraft.runId]: 'save' }))
    setActionErrors((current) => ({ ...current, [saveDraft.runId]: undefined }))
    setSaved(null)
    try {
      const result = await saveWorkflow(projectId, threadId, saveDraft.runId, {
        name: saveDraft.name,
        scope: saveDraft.scope,
        overwrite: saveDraft.overwrite,
      })
      setSaved({ runId: saveDraft.runId, workflow: result })
      setSaveDraft(null)
    } catch (reason) {
      setActionErrors((current) => ({
        ...current,
        [saveDraft.runId]: reason instanceof Error ? reason.message : 'Could not save the workflow.',
      }))
    } finally {
      setPending((current) => ({ ...current, [saveDraft.runId]: undefined }))
    }
  }

  return (
    <section aria-labelledby="workflow-runs-heading">
      <div className="flex items-center justify-between gap-2">
        <p
          id="workflow-runs-heading"
          className="flex items-center gap-1.5 font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint"
        >
          <Workflow size={10} />
          Workflows
        </p>
        {runs.length > 0 && (
          <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[8px] text-ghost-faint">
            {runs.length}
          </span>
        )}
      </div>
      <p className="mt-2 text-[9px] leading-4 text-ghost-dim">
        Pi prompts activate Dire Mux runs with “ultracode,” a direct workflow request, a saved /command, or session-scoped Ultracode effort.
      </p>

      {error && (
        <p className="mt-2 flex items-start gap-1.5 text-[9px] leading-4 text-ghost-bright-red" role="alert">
          <CircleAlert size={11} className="mt-0.5 shrink-0" />
          {error}
        </p>
      )}
      {!error && runs.length === 0 && (
        <p className="mt-2.5 rounded-lg border border-dashed border-ghost-border/55 px-3 py-2.5 text-[9px] leading-4 text-ghost-faint">
          No workflow runs in this thread yet.
        </p>
      )}

      {runs.length > 0 && (
        <ul className="mt-2.5 space-y-2" aria-label="Workflow runs">
          {runs.map((run) => {
            const isExpanded = expanded.has(run.id)
            const finished = run.agents.filter((agent) => agent.state === 'finished').length
            const failed = run.agents.filter((agent) => agent.state === 'failed').length
            const active = run.agents.filter((agent) => agent.state === 'starting' || agent.state === 'working').length
            const paused = run.agents.filter((agent) => agent.state === 'paused').length
            const large = run.agents.length > 25
            const phaseNames = [
              ...(run.phases ?? []).map((phase) => phase.title),
              ...run.agents.map((agent) => agent.phase).filter((phase): phase is string => Boolean(phase)),
            ].filter((phase, index, all) => all.indexOf(phase) === index)
            return (
              <li key={run.id} className="overflow-hidden rounded-xl border border-ghost-border/55 bg-ghost-black/20">
                <Button
                  type="button"
                  onClick={() => toggle(run.id)}
                  className="flex w-full items-start gap-2 px-2.5 py-2.5 text-left hover:bg-ghost-raised/45"
                  aria-expanded={isExpanded}
                >
                  {isExpanded
                    ? <ChevronDown size={12} className="mt-0.5 shrink-0 text-ghost-faint" />
                    : <ChevronRight size={12} className="mt-0.5 shrink-0 text-ghost-faint" />}
                  <span className="min-w-0 flex-1">
                    <span className="flex items-start justify-between gap-2">
                      <span className="min-w-0 truncate text-[10px] font-semibold text-ghost-white">{run.name}</span>
                      <span className={`shrink-0 rounded-full border px-1.5 py-0.5 font-mono text-[7px] uppercase ${workflowStateTone(run.state)}`}>
                        {run.state}
                      </span>
                    </span>
                    <span className="mt-1 block truncate font-mono text-[8px] text-ghost-faint">
                      {run.currentPhase || `${finished} done · ${failed} failed · ${active} active${paused > 0 ? ` · ${paused} paused` : ''}`} · {runElapsed(run)}
                    </span>
                  </span>
                </Button>

                {isExpanded && (
                  <div className="border-t border-ghost-border/50 px-2.5 py-2.5">
                    {run.description && <p className="text-[9px] leading-4 text-ghost-dim">{run.description}</p>}
                    {large && (
                      <p className="mt-2 flex items-center gap-1.5 rounded-lg border border-ghost-yellow/30 bg-ghost-yellow/[0.07] px-2 py-1.5 text-[8px] text-ghost-yellow">
                        <CircleAlert size={10} /> Large workflow · {run.agents.length} agents scheduled
                      </p>
                    )}

                    {phaseNames.length > 0 && (
                      <ul className="mt-2 space-y-1" aria-label={`${run.name} phases`}>
                        {phaseNames.map((phase) => {
                          const agents = run.agents.filter((agent) => agent.phase === phase)
                          return (
                            <li key={phase} className="flex items-center justify-between gap-2 rounded-md bg-ghost-raised/35 px-2 py-1.5">
                              <span className="truncate text-[8px] font-medium text-ghost-muted">{phase}</span>
                              <span className="shrink-0 font-mono text-[7px] text-ghost-faint">
                                {agents.filter((agent) => agent.state === 'finished').length}/{agents.length}
                              </span>
                            </li>
                          )
                        })}
                      </ul>
                    )}

                    {run.agents.length > 0 && (
                      <ul className="mt-2 space-y-1" aria-label={`${run.name} agents`}>
                        {run.agents.map((agent) => {
                          const child = agent.threadId ? threadsById.get(agent.threadId) : undefined
                          return (
                            <li key={agent.id}>
                              <Button
                                type="button"
                                disabled={!child}
                                onClick={() => child && onSelectThread(child)}
                                className="flex w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left hover:bg-ghost-raised/45 disabled:cursor-default"
                                title={agent.error || (child ? `Open ${child.title}` : agent.label)}
                              >
                                {agent.state === 'finished'
                                  ? <CircleCheck size={10} className="shrink-0 text-ghost-green" />
                                  : agent.state === 'failed'
                                    ? <CircleAlert size={10} className="shrink-0 text-ghost-bright-red" />
                                    : agent.state === 'paused'
                                      ? <Pause size={10} className="shrink-0 text-ghost-blue" />
                                      : <Bot size={10} className="shrink-0 text-ghost-cyan" />}
                                <span className="min-w-0 flex-1 truncate text-[8px] text-ghost-muted">{agent.label}</span>
                                <span className="shrink-0 font-mono text-[7px] text-ghost-faint">{agent.state}</span>
                              </Button>
                            </li>
                          )
                        })}
                      </ul>
                    )}

                    <p className="mt-2 break-all font-mono text-[7px] leading-3 text-ghost-faint" title={run.scriptPath}>
                      {run.scriptPath}
                    </p>
                    {run.error && <p className="mt-2 text-[8px] leading-4 text-ghost-bright-red">{run.error}</p>}
                    {actionErrors[run.id] && (
                      <p className="mt-2 text-[8px] leading-4 text-ghost-bright-red" role="alert">{actionErrors[run.id]}</p>
                    )}
                    {saved?.runId === run.id && (
                      <p className="mt-2 break-all text-[8px] leading-4 text-ghost-green" role="status">
                        Saved /{saved.workflow.name} · {saved.workflow.path}
                      </p>
                    )}

                    {saveDraft?.runId === run.id ? (
                      <form className="mt-2 space-y-2 rounded-lg border border-ghost-border/55 bg-ghost-background/65 p-2" onSubmit={(event) => void submitSave(event)}>
                        <TextInput
                          value={saveDraft.name}
                          onChange={(event) => setSaveDraft((current) => current && ({ ...current, name: event.target.value }))}
                          maxLength={80}
                          aria-label="Saved workflow command name"
                          className="font-mono text-[9px]"
                        />
                        <Select
                          value={saveDraft.scope}
                          options={[
                            { value: 'project', label: 'Project · .claude/workflows' },
                            { value: 'personal', label: 'Personal · Claude config' },
                          ]}
                          onChange={(scope) => setSaveDraft((current) => current && ({ ...current, scope: scope as SaveDraft['scope'] }))}
                          aria-label="Saved workflow location"
                          className="font-sans text-[9px]"
                          menuClassName="font-sans text-[9px]"
                        />
                        <label className="flex items-center gap-2 text-[8px] text-ghost-dim">
                          <input
                            type="checkbox"
                            checked={saveDraft.overwrite}
                            onChange={(event) => setSaveDraft((current) => current && ({ ...current, overwrite: event.target.checked }))}
                            className="size-3 accent-ghost-green"
                          />
                          Replace an existing file in this location
                        </label>
                        {!workflowNamePattern.test(saveDraft.name) && (
                          <p className="text-[8px] leading-3 text-ghost-bright-red">Use letters, numbers, hyphens, or underscores.</p>
                        )}
                        <div className="flex items-center justify-end gap-1.5">
                          <GhostButton type="button" size="sm" onClick={() => setSaveDraft(null)}>Cancel</GhostButton>
                          <PrimaryButton type="submit" size="sm" disabled={!workflowNamePattern.test(saveDraft.name) || Boolean(pending[run.id])}>
                            {pending[run.id] === 'save' ? <LoaderCircle size={10} className="animate-spin" /> : <Save size={10} />}
                            Save
                          </PrimaryButton>
                        </div>
                      </form>
                    ) : (
                      <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        {(run.state === 'queued' || run.state === 'running') && (
                          <GhostButton type="button" size="sm" disabled={Boolean(pending[run.id])} onClick={() => void runAction(run, 'pause')}>
                            <Pause size={10} /> Pause
                          </GhostButton>
                        )}
                        {run.state === 'paused' && (
                          <GhostButton type="button" size="sm" disabled={Boolean(pending[run.id])} onClick={() => void runAction(run, 'resume')}>
                            <Play size={10} /> Resume
                          </GhostButton>
                        )}
                        {(run.state === 'queued' || run.state === 'running' || run.state === 'paused') && (
                          <GhostButton type="button" size="sm" disabled={Boolean(pending[run.id])} onClick={() => void runAction(run, 'stop')}>
                            <Square size={9} fill="currentColor" /> Stop
                          </GhostButton>
                        )}
                        <GhostButton
                          type="button"
                          size="sm"
                          disabled={Boolean(pending[run.id])}
                          onClick={() => {
                            setSaved(null)
                            setSaveDraft({ runId: run.id, name: defaultSaveName(run), scope: 'project', overwrite: false })
                          }}
                        >
                          <Save size={10} /> Save command
                        </GhostButton>
                        {pending[run.id] && pending[run.id] !== 'save' && <LoaderCircle size={11} className="animate-spin text-ghost-faint" />}
                      </div>
                    )}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
