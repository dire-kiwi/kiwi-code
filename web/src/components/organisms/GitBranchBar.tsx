import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import {
  ArrowLeft,
  Check,
  ChevronDown,
  GitBranch as GitBranchIcon,
  LoaderCircle,
  Plus,
  Search,
  X,
} from 'lucide-react'
import { createGitBranch, switchGitBranch } from '../../api'
import type { AgentContextStatus, GitBranchState } from '../../types'
import { Button, GhostButton, PrimaryButton } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { TextInput } from '../atoms/Input'
import { SelectionButton } from '../atoms/SelectionButton'
import { Surface } from '../atoms/Surface'
import { ContextStatus } from '../molecules/ContextStatus'
import { FeedbackMessage } from '../molecules/FeedbackMessage'

type GitBranchBarProps = {
  projectId: string
  threadId: string
  worktree?: boolean
  branchState: GitBranchState | null
  contextStatus: AgentContextStatus | null
  loading: boolean
  loadError: string
  onBranchStateChange: (state: GitBranchState) => void
  onRetry: () => void
  onOverlayOpenChange?: (open: boolean) => void
}

function messageFrom(reason: unknown) {
  return reason instanceof Error ? reason.message : 'Git could not complete that operation.'
}

export function GitBranchBar({
  projectId,
  threadId,
  worktree = false,
  branchState,
  contextStatus,
  loading,
  loadError,
  onBranchStateChange,
  onRetry,
  onOverlayOpenChange,
}: GitBranchBarProps) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState(false)
  const [newBranchName, setNewBranchName] = useState('')
  const [busyBranch, setBusyBranch] = useState('')
  const [actionError, setActionError] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const createRef = useRef<HTMLInputElement>(null)
  const requestSerialRef = useRef(0)

  useEffect(() => () => {
    requestSerialRef.current += 1
  }, [])

  useEffect(() => {
    onOverlayOpenChange?.(open)
    return () => onOverlayOpenChange?.(false)
  }, [onOverlayOpenChange, open])

  useEffect(() => {
    if (!open) return

    function handlePointerDown(event: PointerEvent) {
      const target = event.target
      if (target instanceof Node && !containerRef.current?.contains(target)) {
        setOpen(false)
        setCreating(false)
        setActionError('')
      }
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return
      event.preventDefault()
      if (creating) {
        setCreating(false)
        setActionError('')
      } else {
        setOpen(false)
      }
    }

    document.addEventListener('pointerdown', handlePointerDown)
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [creating, open])

  useEffect(() => {
    if (!open) return
    const frame = requestAnimationFrame(() => {
      if (creating) createRef.current?.focus()
      else searchRef.current?.focus()
    })
    return () => cancelAnimationFrame(frame)
  }, [creating, open])

  const filteredBranches = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return [...(branchState?.branches ?? [])]
      .sort((left, right) => Number(right.current) - Number(left.current))
      .filter((branch) => !needle || branch.name.toLowerCase().includes(needle))
  }, [branchState?.branches, query])

  const trimmedQuery = query.trim()
  const queryMatchesBranch = branchState?.branches.some((branch) => branch.name === trimmedQuery) ?? false

  async function selectBranch(name: string, current: boolean) {
    if (busyBranch) return
    if (current) {
      setOpen(false)
      return
    }

    const serial = ++requestSerialRef.current
    setBusyBranch(name)
    setActionError('')
    try {
      const next = await switchGitBranch(projectId, threadId, name)
      if (serial !== requestSerialRef.current) return
      onBranchStateChange(next)
      setOpen(false)
      setQuery('')
    } catch (reason) {
      if (serial === requestSerialRef.current) setActionError(messageFrom(reason))
    } finally {
      setBusyBranch('')
    }
  }

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = newBranchName.trim()
    if (!name || busyBranch) return

    const serial = ++requestSerialRef.current
    setBusyBranch(name)
    setActionError('')
    try {
      const next = await createGitBranch(projectId, threadId, name)
      if (serial !== requestSerialRef.current) return
      onBranchStateChange(next)
      setOpen(false)
      setCreating(false)
      setQuery('')
      setNewBranchName('')
    } catch (reason) {
      if (serial === requestSerialRef.current) setActionError(messageFrom(reason))
    } finally {
      setBusyBranch('')
    }
  }

  function startCreating(name = '') {
    setNewBranchName(name)
    setActionError('')
    setCreating(true)
  }

  const isRepository = branchState?.isRepository === true
  const branchLabel = branchState
    ? branchState.isRepository
      ? branchState.detached
        ? `${branchState.current || 'HEAD'} (detached)`
        : branchState.current || 'Unknown branch'
      : 'Not a Git repository'
    : loading ? 'Reading Git branch…' : loadError || 'Git status unavailable'

  return (
    <div
      ref={containerRef}
      className="relative z-30 flex h-8 shrink-0 items-center border-t border-ghost-border/70 bg-ghost-panel/95 px-2.5"
    >
      <Button
        type="button"
        onClick={() => {
          if (!isRepository) {
            onRetry()
            return
          }
          setOpen((current) => !current)
          setCreating(false)
          setActionError('')
        }}
        disabled={loading && !branchState && !loadError}
        aria-haspopup="dialog"
        aria-expanded={open}
        className={`flex h-6 min-w-0 max-w-[22rem] items-center gap-1.5 rounded-md px-2 text-[10px] transition ${
          isRepository
            ? 'text-ghost-muted hover:bg-ghost-raised hover:text-ghost-bright-white'
            : 'cursor-default text-ghost-faint'
        } disabled:cursor-wait`}
        title={isRepository ? `Switch branch · ${branchState.current}` : branchLabel}
      >
        {loading && !branchState && !loadError ? (
          <LoaderCircle size={11} className="shrink-0 animate-spin" />
        ) : (
          <GitBranchIcon size={11} className={`shrink-0 ${isRepository ? 'text-ghost-green' : ''}`} />
        )}
        <span className="truncate font-mono">{branchLabel}</span>
        {isRepository && <ChevronDown size={10} className="shrink-0 text-ghost-faint" />}
      </Button>

      <span className="ml-auto flex shrink-0 items-center gap-2 pl-2 text-[9px] text-ghost-faint">
        {contextStatus && <ContextStatus status={contextStatus} />}
        {contextStatus && isRepository && <span className="h-3 w-px bg-ghost-border/60" aria-hidden="true" />}
        {isRepository && (
          <span
            className="flex items-center gap-2"
            title={worktree ? 'Git worktree' : 'Git working tree'}
          >
            <span className="size-1.5 rounded-full bg-ghost-green/80" />
            <span className="hidden lg:inline">{worktree ? 'Git worktree' : 'Git working tree'}</span>
          </span>
        )}
      </span>

      {open && isRepository && (
        <Surface
          role="dialog"
          aria-label="Switch Git branch"
          variant="dialog"
          className="absolute bottom-full left-2 mb-2 flex w-[23rem] max-w-[calc(100vw-1rem)] flex-col overflow-hidden"
        >
          <div className="flex h-12 shrink-0 items-center gap-2 border-b border-ghost-border/60 px-3.5">
            {creating ? (
              <IconButton
                type="button"
                size="sm"
                variant="subtle"
                onClick={() => {
                  setCreating(false)
                  setActionError('')
                }}
                aria-label="Back to branches"
              >
                <ArrowLeft size={14} />
              </IconButton>
            ) : (
              <GitBranchIcon size={14} className="text-ghost-green" />
            )}
            <p className="min-w-0 flex-1 truncate text-xs font-semibold text-ghost-bright-white">
              {creating ? 'Create branch' : 'Switch branch'}
            </p>
            {!creating && (
              <GhostButton
                type="button"
                size="xs"
                onClick={() => startCreating()}
                className="flex items-center gap-1.5"
              >
                <Plus size={12} />
                New branch
              </GhostButton>
            )}
            <IconButton
              type="button"
              size="sm"
              variant="subtle"
              onClick={() => setOpen(false)}
              aria-label="Close branch picker"
            >
              <X size={13} />
            </IconButton>
          </div>

          {creating ? (
            <form onSubmit={(event) => void handleCreate(event)} className="p-3.5">
              <label className="block text-[9px] font-semibold uppercase tracking-[0.13em] text-ghost-dim">
                Branch name
                <TextInput
                  ref={createRef}
                  variant="code"
                  value={newBranchName}
                  onChange={(event) => setNewBranchName(event.target.value)}
                  maxLength={255}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="feature/my-branch"
                  className="mt-2"
                />
              </label>
              <p className="mt-2 text-[10px] leading-4 text-ghost-faint">
                The new branch starts from {branchState.current || 'the current HEAD'} and is checked out immediately.
              </p>
              {actionError && (
                <FeedbackMessage role="alert" tone="error" size="sm" className="mt-3">
                  {actionError}
                </FeedbackMessage>
              )}
              <div className="mt-4 flex justify-end gap-2 border-t border-ghost-border/55 pt-3">
                <GhostButton
                  type="button"
                  size="sm"
                  onClick={() => {
                    setCreating(false)
                    setActionError('')
                  }}
                  disabled={Boolean(busyBranch)}
                  className="disabled:opacity-40"
                >
                  Cancel
                </GhostButton>
                <PrimaryButton
                  type="submit"
                  size="sm"
                  disabled={!newBranchName.trim() || Boolean(busyBranch)}
                  className="flex min-w-28 items-center justify-center gap-1.5"
                >
                  {busyBranch ? <LoaderCircle size={12} className="animate-spin" /> : <Plus size={12} />}
                  Create branch
                </PrimaryButton>
              </div>
            </form>
          ) : (
            <>
              <div className="border-b border-ghost-border/55 p-2.5">
                <label className="relative block">
                  <span className="sr-only">Filter branches</span>
                  <Search size={12} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ghost-faint" />
                  <TextInput
                    ref={searchRef}
                    variant="search"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="Filter branches…"
                  />
                </label>
              </div>

              <div className="max-h-[19rem] overflow-y-auto p-1.5">
                {actionError && (
                  <FeedbackMessage role="alert" tone="error" size="sm" className="m-1 mb-2">
                    {actionError}
                  </FeedbackMessage>
                )}
                {filteredBranches.map((branch) => (
                  <SelectionButton
                    key={branch.name}
                    type="button"
                    selected={branch.current}
                    selectionVariant="menu-item"
                    onClick={() => void selectBranch(branch.name, branch.current)}
                    disabled={Boolean(busyBranch)}
                    aria-current={branch.current ? 'true' : undefined}
                  >
                    <GitBranchIcon size={12} className={`shrink-0 ${branch.current ? 'text-ghost-green' : 'text-ghost-faint'}`} />
                    <span className="min-w-0 flex-1 truncate">{branch.name}</span>
                    {busyBranch === branch.name ? (
                      <LoaderCircle size={12} className="shrink-0 animate-spin text-ghost-green" />
                    ) : branch.current ? (
                      <Check size={12} className="shrink-0 text-ghost-green" />
                    ) : null}
                  </SelectionButton>
                ))}

                {trimmedQuery && !queryMatchesBranch && (
                  <Button
                    type="button"
                    onClick={() => startCreating(trimmedQuery)}
                    disabled={Boolean(busyBranch)}
                    className="mt-1 flex min-h-9 w-full items-center gap-2 rounded-lg border border-dashed border-ghost-border/65 px-2.5 py-2 text-left text-[10px] text-ghost-muted transition hover:border-ghost-green/35 hover:bg-ghost-green/[0.06] hover:text-ghost-bright-white"
                  >
                    <Plus size={12} className="shrink-0 text-ghost-green" />
                    <span className="min-w-0 truncate">Create “{trimmedQuery}”</span>
                  </Button>
                )}

                {filteredBranches.length === 0 && !trimmedQuery && (
                  <p className="px-3 py-7 text-center text-[10px] text-ghost-faint">No local branches found.</p>
                )}
              </div>

              <div className="flex h-8 shrink-0 items-center border-t border-ghost-border/55 px-3 text-[9px] text-ghost-faint">
                {filteredBranches.length} of {branchState.branches.length} local branches
                <span className="ml-auto">Esc to close</span>
              </div>
            </>
          )}
        </Surface>
      )}
    </div>
  )
}
