import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react'
import { Fzf, byLengthAsc, extendedMatch } from 'fzf'
import {
  Archive,
  Check,
  Folder,
  GitBranch,
  MessageSquare,
  Search,
  X,
} from 'lucide-react'
import type { Profile, Project, Thread } from '../../types'
import { BaseInput } from '../atoms/Input'
import { IconButton } from '../atoms/IconButton'
import { Surface } from '../atoms/Surface'

type ProjectResult = {
  id: string
  kind: 'project'
  profileName: string
  project: Project
}

type ThreadResult = {
  id: string
  kind: 'thread'
  profileName: string
  project: Project
  thread: Thread
}

type FinderResult = ProjectResult | ThreadResult

type ProjectThreadFinderProps = {
  profiles: Profile[]
  projects: Project[]
  currentProjectId: string | null
  currentThreadId: string | null
  onClose: () => void
  onSelectProject: (project: Project) => void
  onSelectThread: (project: Project, thread: Thread) => void
}

const optionIdPrefix = 'project-thread-finder-option'

function resultSearchText(result: FinderResult) {
  if (result.kind === 'project') {
    return [result.project.name, result.profileName, result.project.path, result.project.host]
      .filter(Boolean)
      .join(' ')
  }

  return [
    result.thread.title,
    result.project.name,
    result.profileName,
    result.thread.branch,
    result.thread.cwd,
    result.thread.archivedAt ? 'archived' : '',
    result.project.host,
  ].filter(Boolean).join(' ')
}

function optionId(index: number) {
  return `${optionIdPrefix}-${index}`
}

export function ProjectThreadFinder({
  profiles,
  projects,
  currentProjectId,
  currentThreadId,
  onClose,
  onSelectProject,
  onSelectThread,
}: ProjectThreadFinderProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')

  const allResults = useMemo(() => {
    const profileNames = new Map(profiles.map((profile) => [profile.id, profile.name]))
    const items: FinderResult[] = []
    for (const project of projects) {
      const profileName = profileNames.get(project.profileId) ?? 'Unknown profile'
      items.push({
        id: `project:${project.id}`,
        kind: 'project',
        profileName,
        project,
      })
      for (const thread of project.threads) {
        items.push({
          id: `thread:${project.id}:${thread.id}`,
          kind: 'thread',
          profileName,
          project,
          thread,
        })
      }
    }
    return items
  }, [profiles, projects])

  // Mirror fzf's defaults: v2 scoring, extended query syntax, then shorter matches on ties.
  const finder = useMemo(() => new Fzf(allResults, {
    selector: resultSearchText,
    fuzzy: 'v2',
    match: extendedMatch,
    tiebreakers: [byLengthAsc],
  }), [allResults])

  const results = useMemo(
    () => query.trim() ? finder.find(query).map((entry) => entry.item) : allResults,
    [allResults, finder, query],
  )

  const [activeIndex, setActiveIndex] = useState(() => {
    let index = 0
    for (const project of projects) {
      if (project.id === currentProjectId && !currentThreadId) return index
      index += 1
      for (const thread of project.threads) {
        if (project.id === currentProjectId && thread.id === currentThreadId) return index
        index += 1
      }
    }
    return 0
  })

  useEffect(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
    const frame = requestAnimationFrame(() => inputRef.current?.focus())
    return () => {
      cancelAnimationFrame(frame)
      if (previouslyFocused?.isConnected) previouslyFocused.focus({ preventScroll: true })
    }
  }, [])

  useEffect(() => {
    setActiveIndex((current) => Math.max(0, Math.min(current, results.length - 1)))
  }, [results.length])

  useEffect(() => {
    document.getElementById(optionId(activeIndex))?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex, results])

  function choose(result: FinderResult | undefined) {
    if (!result) return
    if (result.kind === 'project') onSelectProject(result.project)
    else onSelectThread(result.project, result.thread)
  }

  function handleInputKeyDown(event: ReactKeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex((current) => results.length ? (current + 1) % results.length : 0)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex((current) => results.length ? (current - 1 + results.length) % results.length : 0)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      choose(results[activeIndex])
    }
  }

  function handleDialogKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      onClose()
      return
    }
    if (event.key !== 'Tab') return

    const focusable = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(
      'input:not([disabled]), button:not([disabled]):not([tabindex="-1"])',
    ))
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (!first || !last) return
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  const activeResult = results[activeIndex]

  return (
    <div
      className="fixed inset-0 z-[70] flex items-start justify-center bg-ghost-black/75 px-3 pt-[8vh] backdrop-blur-[2px] sm:px-6 sm:pt-[12vh]"
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <Surface
        role="dialog"
        aria-modal="true"
        aria-labelledby="project-thread-finder-title"
        variant="dialog"
        className="flex max-h-[80vh] w-full max-w-[42rem] flex-col overflow-hidden"
        onKeyDown={handleDialogKeyDown}
      >
        <h2 id="project-thread-finder-title" className="sr-only">Find a project or thread</h2>
        <div className="relative flex h-16 shrink-0 items-center border-b border-ghost-border/65 px-3 transition-colors focus-within:border-ghost-green/45 sm:px-4">
          <Search size={18} className="pointer-events-none absolute left-6 text-ghost-green sm:left-7" />
          <BaseInput
            ref={inputRef}
            type="search"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value)
              setActiveIndex(0)
            }}
            onKeyDown={handleInputKeyDown}
            role="combobox"
            aria-label="Find projects and threads"
            aria-expanded="true"
            aria-autocomplete="list"
            aria-controls="project-thread-finder-results"
            aria-activedescendant={activeResult ? optionId(activeIndex) : undefined}
            autoComplete="off"
            spellCheck={false}
            placeholder="Find projects and threads…"
            className="h-full min-w-0 flex-1 bg-transparent pl-10 pr-20 text-sm text-ghost-bright-white placeholder:text-ghost-faint"
          />
          <span className="pointer-events-none absolute right-14 hidden items-center gap-1 sm:flex">
            <kbd className="rounded border border-ghost-border/80 bg-ghost-raised px-1.5 py-0.5 font-mono text-[9px] text-ghost-dim">Ctrl</kbd>
            <kbd className="rounded border border-ghost-border/80 bg-ghost-raised px-1.5 py-0.5 font-mono text-[9px] text-ghost-dim">F</kbd>
          </span>
          <IconButton
            type="button"
            size="sm"
            variant="subtle"
            onClick={onClose}
            aria-label="Close project and thread finder"
            className="shrink-0"
          >
            <X size={15} />
          </IconButton>
        </div>

        <div
          id="project-thread-finder-results"
          role="listbox"
          aria-label="Projects and threads"
          className="min-h-0 flex-1 overflow-y-auto p-2"
        >
          {results.length > 0 ? results.map((result, index) => {
            const active = index === activeIndex
            const current = result.kind === 'thread'
              && result.project.id === currentProjectId
              && result.thread.id === currentThreadId
            const location = result.kind === 'project'
              ? result.project.path
              : result.thread.branch ?? result.thread.cwd
            const Icon = result.kind === 'project'
              ? Folder
              : result.thread.archivedAt
                ? Archive
                : result.thread.worktree
                  ? GitBranch
                  : MessageSquare

            return (
              <button
                key={result.id}
                id={optionId(index)}
                type="button"
                role="option"
                aria-selected={active}
                tabIndex={-1}
                onPointerMove={() => setActiveIndex(index)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => choose(result)}
                className={`flex min-h-14 w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition ${
                  active
                    ? 'bg-ghost-green/[0.11] text-ghost-bright-white ring-1 ring-inset ring-ghost-green/20'
                    : 'text-ghost-muted'
                }`}
                title={result.kind === 'project'
                  ? result.project.path
                  : `${result.project.name}\n${result.thread.cwd}`}
              >
                <span className={`grid size-8 shrink-0 place-items-center rounded-lg ${
                  active ? 'bg-ghost-green/15 text-ghost-green' : 'bg-ghost-raised text-ghost-dim'
                }`}>
                  <Icon size={15} strokeWidth={1.7} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-xs font-medium">
                      {result.kind === 'project' ? result.project.name : result.thread.title}
                    </span>
                    <span className="shrink-0 rounded-full border border-ghost-border/65 px-1.5 py-0.5 text-[8px] font-semibold uppercase tracking-[0.1em] text-ghost-faint">
                      {result.kind}
                    </span>
                    {result.kind === 'thread' && result.thread.archivedAt && (
                      <span className="shrink-0 rounded-full border border-ghost-yellow/25 bg-ghost-yellow/[0.05] px-1.5 py-0.5 text-[8px] font-semibold uppercase tracking-[0.1em] text-ghost-yellow">
                        archived
                      </span>
                    )}
                  </span>
                  <span className="mt-1 flex min-w-0 items-center gap-1.5 font-mono text-[9px] text-ghost-faint">
                    {result.kind === 'thread' && (
                      <>
                        <span className="max-w-[12rem] shrink-0 truncate text-ghost-dim">{result.project.name}</span>
                        <span aria-hidden="true">·</span>
                      </>
                    )}
                    <span className="shrink-0">{result.profileName}</span>
                    <span aria-hidden="true">·</span>
                    <span className="truncate">{location}</span>
                  </span>
                </span>
                {current && <Check size={14} className="shrink-0 text-ghost-green" aria-label="Current thread" />}
              </button>
            )
          }) : (
            <div className="grid min-h-40 place-items-center px-6 text-center">
              <div>
                <Search size={20} className="mx-auto text-ghost-faint" />
                <p className="mt-3 text-xs text-ghost-muted">
                  {query.trim()
                    ? `No projects or threads match “${query.trim()}”.`
                    : 'No projects have been added yet.'}
                </p>
                {query.trim() && (
                  <p className="mt-1 text-[10px] text-ghost-faint">Try a name, path, profile, or branch.</p>
                )}
              </div>
            </div>
          )}
        </div>

        <div className="flex h-9 shrink-0 items-center border-t border-ghost-border/55 px-3.5 font-mono text-[9px] text-ghost-faint">
          <span aria-live="polite">{results.length} result{results.length === 1 ? '' : 's'}</span>
          <span className="ml-auto hidden items-center gap-3 sm:flex">
            <span><kbd className="text-ghost-dim">↑↓</kbd> navigate</span>
            <span><kbd className="text-ghost-dim">Enter</kbd> open</span>
            <span><kbd className="text-ghost-dim">Esc</kbd> close</span>
          </span>
        </div>
      </Surface>
    </div>
  )
}
