import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { Link } from 'react-router-dom'
import {
  Check,
  Folder,
  FolderGit2,
  GitBranch,
  LoaderCircle,
  Lock,
  LockOpen,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  Shield,
  SquareTerminal,
  X,
} from 'lucide-react'
import { setThreadTitleLocked, updateThreadTitle } from '@/api'
import { threadSandboxPath } from '@/app/routes'
import type { Project, Thread, ThreadUsageSnapshot } from '@/types'
import { Button, GhostButton, IconButton, PrimaryButton } from '@/ui/buttons'
import { TextInput } from '@/ui/inputs'
import { ThreadUsageLimits } from './ThreadUsageLimits'

type ThreadProjectSidebarProps = {
  project: Project
  thread: Thread
  usage?: ThreadUsageSnapshot
  expanded: boolean
  onExpandedChange: (expanded: boolean) => void
  onThreadUpdated: (thread: Thread) => void
}

export function ThreadProjectSidebar({
  project,
  thread,
  usage,
  expanded,
  onExpandedChange,
  onThreadUpdated,
}: ThreadProjectSidebarProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(thread.title)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [locking, setLocking] = useState(false)
  const [lockError, setLockError] = useState('')

  useEffect(() => {
    if (!editing) setTitle(thread.title)
  }, [editing, thread.title])

  useEffect(() => {
    if (thread.titleLocked) setEditing(false)
    setLockError('')
  }, [thread.id, thread.titleLocked])

  useEffect(() => {
    if (!editing || !expanded) return
    const frame = requestAnimationFrame(() => inputRef.current?.select())
    return () => cancelAnimationFrame(frame)
  }, [editing, expanded])

  function beginEditing() {
    if (thread.titleLocked) return
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

  async function toggleTitleLock() {
    if (locking) return
    setLocking(true)
    setLockError('')
    try {
      const updated = await setThreadTitleLocked(project.id, thread.id, !thread.titleLocked)
      onThreadUpdated(updated)
      if (updated.titleLocked) setEditing(false)
    } catch (reason) {
      setLockError(reason instanceof Error ? reason.message : 'Could not update the title lock.')
    } finally {
      setLocking(false)
    }
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
      <header className={`desktop-titlebar-drag-region desktop-titlebar-right-column-safe flex h-[4.5rem] shrink-0 items-center border-b border-ghost-border/70 ${
        expanded ? 'justify-between gap-2 pl-4 pr-2' : 'justify-center px-2'
      }`}>
        {expanded && (
          <div className="min-w-0">
            <p className="truncate text-xs font-semibold text-ghost-bright-white" title={thread.title}>
              {thread.title}
            </p>
            <p className="mt-1 truncate font-mono text-[8px] uppercase tracking-[0.14em] text-ghost-faint" title={project.name}>
              {project.name} · thread
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
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="px-4 py-4">
                <section>
                  <div className="flex items-center justify-between gap-2">
                    <p className="font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint">
                      Thread
                    </p>
                    <IconButton
                      type="button"
                      shrink
                      variant="ghost"
                      onClick={() => void toggleTitleLock()}
                      disabled={locking}
                      aria-pressed={Boolean(thread.titleLocked)}
                      aria-label={thread.titleLocked ? 'Unlock thread title' : 'Lock thread title'}
                      title={thread.titleLocked ? 'Unlock title' : 'Lock title so agents cannot rename it'}
                      className={thread.titleLocked ? 'text-ghost-green' : 'text-ghost-dim'}
                    >
                      {locking
                        ? <LoaderCircle size={13} className="animate-spin" />
                        : thread.titleLocked ? <Lock size={13} /> : <LockOpen size={13} />}
                    </IconButton>
                  </div>

                  {thread.titleLocked && (
                    <p className="mt-1.5 px-2 text-[9px] leading-4 text-ghost-dim">
                      Title locked. Unlock it to rename; agents cannot override it.
                    </p>
                  )}
                  {lockError && (
                    <p role="alert" className="mt-1.5 px-2 text-[10px] leading-4 text-ghost-bright-red">
                      {lockError}
                    </p>
                  )}

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
                      disabled={Boolean(thread.titleLocked)}
                      aria-label={thread.titleLocked ? `Thread name locked: ${thread.title}` : `Edit thread name: ${thread.title}`}
                      className="group mt-2.5 flex w-full items-start gap-2 rounded-xl border border-transparent px-2 py-2 text-left transition enabled:hover:border-ghost-border/70 enabled:hover:bg-ghost-raised/55 disabled:cursor-default"
                      title={thread.titleLocked ? 'Unlock the title to edit it' : 'Edit thread name'}
                    >
                      <SquareTerminal size={15} className="mt-0.5 shrink-0 text-ghost-green" />
                      <span className="min-w-0 flex-1 break-words text-sm font-semibold leading-5 text-ghost-bright-white">
                        {thread.title}
                      </span>
                      {thread.titleLocked
                        ? <Lock size={12} className="mt-1 shrink-0 text-ghost-green" />
                        : <Pencil size={12} className="mt-1 shrink-0 text-ghost-faint transition group-hover:text-ghost-green" />}
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
                  <Link
                    to={threadSandboxPath(project.id, thread.id)}
                    className="mt-2.5 flex items-center gap-1.5 rounded-lg border border-ghost-border/70 px-2.5 py-1.5 text-[10px] font-medium text-ghost-muted transition hover:border-ghost-green/45 hover:text-ghost-bright-white"
                    title="Configure the Kiwi Sandbox for this thread"
                  >
                    <Shield size={11} className="text-ghost-green" />
                    Sandbox settings
                  </Link>
                </section>

                {sectionDivider}

                <ThreadUsageLimits
                  projectId={project.id}
                  thread={thread}
                  usage={usage}
                  onThreadUpdated={onThreadUpdated}
                />
              </div>

            </div>
          </div>
        )}
      </aside>
    </>
  )
}
