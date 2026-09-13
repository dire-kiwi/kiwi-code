import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { ArrowRight, Folder, Search, X } from 'lucide-react'
import { projectsByMostRecentThread } from '@/new-thread-project-order.mjs'
import type { Project } from '@/types'

/** Project picker shared by the sidebar action and the new-thread shortcut. */
export function NewThreadProjectDialog({ projects, preferredProjectId, onSelect, onClose }: {
  projects: Project[]
  preferredProjectId: string | null
  onSelect: (projectId: string) => void
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const id = useId()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const matches = useMemo(() => {
    const ordered = projectsByMostRecentThread(projects)
    const preferred = ordered.find((project) => project.id === preferredProjectId)
    return (preferred ? [preferred, ...ordered.filter((project) => project !== preferred)] : ordered)
      .filter((project) => `${project.name} ${project.path}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  }, [projects, preferredProjectId, query])
  const selectedIndex = Math.min(activeIndex, Math.max(0, matches.length - 1))

  useEffect(() => {
    const dialog = dialogRef.current
    dialog?.showModal()
    searchRef.current?.focus()
    return () => dialog?.close()
  }, [])

  return (
    <dialog ref={dialogRef} aria-labelledby={`${id}-title`} onCancel={onClose}
      onClick={(event) => { if (event.target === event.currentTarget) onClose() }}
      className="fixed inset-0 m-auto w-[calc(100%-2rem)] max-w-lg overflow-hidden rounded-2xl border border-ghost-border bg-ghost-panel p-0 text-ghost-white shadow-2xl backdrop:bg-ghost-black/70 backdrop:backdrop-blur-sm">
      <div>
        <div className="flex items-center justify-between px-4 pt-4">
          <h2 id={`${id}-title`} className="text-sm font-medium">New thread in…</h2>
          <button type="button" onClick={onClose} aria-label="Close project picker" className="rounded p-1 text-ghost-dim hover:bg-ghost-raised"><X size={15} /></button>
        </div>
        <div className="m-3 flex items-center gap-2 rounded-lg border border-ghost-border/60 px-3">
          <Search size={15} className="text-ghost-dim" />
          <input ref={searchRef} role="combobox" aria-label="Choose a project" aria-expanded="true"
            aria-controls={`${id}-projects`} aria-autocomplete="list"
            aria-activedescendant={matches.length ? `${id}-project-${selectedIndex}` : undefined}
            value={query} placeholder="Search projects…"
            onChange={(event) => { setQuery(event.target.value); setActiveIndex(0) }}
            onKeyDown={(event) => {
              if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                event.preventDefault()
                if (matches.length) setActiveIndex((selectedIndex + (event.key === 'ArrowDown' ? 1 : matches.length - 1)) % matches.length)
              }
              if (event.key === 'Enter' && matches[selectedIndex]) {
                event.preventDefault()
                onSelect(matches[selectedIndex].id)
              }
            }}
            className="h-10 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-ghost-faint" />
        </div>
        <div role="listbox" id={`${id}-projects`} aria-label="Projects" className="max-h-72 overflow-y-auto px-2 pb-2">
          {matches.map((project, index) => (
            <button type="button" role="option" aria-selected={index === selectedIndex} id={`${id}-project-${index}`}
              key={project.id} onClick={() => onSelect(project.id)} onMouseEnter={() => setActiveIndex(index)}
              className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left ${index === selectedIndex ? 'bg-ghost-selected' : 'hover:bg-ghost-raised/50'}`}>
              <Folder size={17} className="shrink-0 text-ghost-dim" />
              <span className="min-w-0 flex-1"><span className="block truncate text-sm">{project.name}</span><span className="block truncate text-xs text-ghost-dim">{project.path}</span></span>
              {index === selectedIndex && <ArrowRight size={14} className="text-ghost-dim" />}
            </button>
          ))}
          {matches.length === 0 && <p role="status" className="p-6 text-center text-sm text-ghost-dim">No projects found</p>}
        </div>
        <p className="border-t border-ghost-border/50 px-4 py-2 text-[10px] text-ghost-dim">↑ ↓ to navigate · Enter to select · Esc to close</p>
      </div>
    </dialog>
  )
}
