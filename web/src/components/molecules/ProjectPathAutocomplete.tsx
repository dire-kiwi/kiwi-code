import {
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react'
import { Folder, LoaderCircle } from 'lucide-react'
import { listDirectorySuggestions } from '../../api'
import type { DirectorySuggestion } from '../../types'
import { Button } from '../atoms/Button'
import { TextInput } from '../atoms/Input'

const suggestionDelay = 120

type ProjectPathAutocompleteProps = {
  value: string
  disabled?: boolean
  onChange: (value: string) => void
}

type SuggestionResult = {
  value: string
  suggestions: DirectorySuggestion[]
}

export function ProjectPathAutocomplete({
  value,
  disabled = false,
  onChange,
}: ProjectPathAutocompleteProps) {
  const inputId = useId()
  const listboxId = useId()
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  const [result, setResult] = useState<SuggestionResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [expanded, setExpanded] = useState(true)
  const [activeIndex, setActiveIndex] = useState(-1)

  const suggestions = result?.value === value ? result.suggestions : []
  const open = expanded && suggestions.length > 0

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setActiveIndex(-1)

    const timeout = window.setTimeout(() => {
      void listDirectorySuggestions(value, controller.signal)
        .then((next) => {
          if (controller.signal.aborted) return
          setResult({ value, suggestions: next.suggestions })
        })
        .catch(() => {
          if (controller.signal.aborted) return
          setResult({ value, suggestions: [] })
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false)
        })
    }, suggestionDelay)

    return () => {
      controller.abort()
      window.clearTimeout(timeout)
    }
  }, [value])

  useEffect(() => {
    if (!open || activeIndex < 0) return
    optionRefs.current[activeIndex]?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex, open])

  function selectSuggestion(suggestion: DirectorySuggestion) {
    onChange(suggestion.path)
    setExpanded(false)
    setActiveIndex(-1)
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowDown') {
      if (suggestions.length === 0) return
      event.preventDefault()
      setExpanded(true)
      setActiveIndex((current) => current >= suggestions.length - 1 ? 0 : current + 1)
      return
    }

    if (event.key === 'ArrowUp') {
      if (suggestions.length === 0) return
      event.preventDefault()
      setExpanded(true)
      setActiveIndex((current) => current <= 0 ? suggestions.length - 1 : current - 1)
      return
    }

    if (event.key === 'Tab' && open) {
      event.preventDefault()
      selectSuggestion(suggestions[activeIndex >= 0 ? activeIndex : 0])
      return
    }

    if (event.key === 'Enter' && open && activeIndex >= 0) {
      event.preventDefault()
      selectSuggestion(suggestions[activeIndex])
      return
    }

    if (event.key === 'Escape' && expanded) {
      event.preventDefault()
      setExpanded(false)
      setActiveIndex(-1)
    }
  }

  return (
    <div className="relative mt-2">
      <label htmlFor={inputId} className="sr-only">Project path</label>
      <TextInput
        id={inputId}
        variant="compact-code"
        value={value}
        onChange={(event) => {
          onChange(event.target.value)
          setExpanded(true)
        }}
        onFocus={() => setExpanded(true)}
        onBlur={() => {
          setExpanded(false)
          setActiveIndex(-1)
        }}
        onKeyDown={handleKeyDown}
        required
        autoFocus
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        disabled={disabled}
        role="combobox"
        aria-autocomplete="list"
        aria-haspopup="listbox"
        aria-controls={listboxId}
        aria-expanded={open}
        aria-activedescendant={open && activeIndex >= 0 ? `${listboxId}-option-${activeIndex}` : undefined}
        aria-busy={loading}
        className="pr-8"
        placeholder="/Users/me/code/project"
      />
      {loading && (
        <LoaderCircle
          size={12}
          className="pointer-events-none absolute right-3 top-[18px] -translate-y-1/2 animate-spin text-ghost-faint"
          aria-hidden="true"
        />
      )}

      {open && (
        <div className="absolute inset-x-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-ghost-border bg-ghost-panel shadow-xl shadow-black/35">
          <ul id={listboxId} role="listbox" aria-label="Directory suggestions" className="max-h-52 overflow-y-auto p-1">
            {suggestions.map((suggestion, index) => {
              const active = index === activeIndex
              return (
                <li key={suggestion.path} role="presentation">
                  <Button
                    ref={(node) => {
                      optionRefs.current[index] = node
                    }}
                    id={`${listboxId}-option-${index}`}
                    type="button"
                    role="option"
                    tabIndex={-1}
                    aria-selected={active}
                    title={suggestion.path}
                    onPointerDown={(event) => event.preventDefault()}
                    onPointerMove={() => setActiveIndex(index)}
                    onClick={() => selectSuggestion(suggestion)}
                    className={`flex min-h-10 w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition ${
                      active
                        ? 'bg-ghost-green/[0.11] text-ghost-bright-white'
                        : 'text-ghost-muted hover:bg-ghost-raised hover:text-ghost-bright-white'
                    }`}
                  >
                    <Folder size={13} className={`shrink-0 ${active ? 'text-ghost-green' : 'text-ghost-faint'}`} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[10px] font-medium">{suggestion.name}</span>
                      <span className="block truncate font-mono text-[9px] text-ghost-faint">{suggestion.path}</span>
                    </span>
                  </Button>
                </li>
              )
            })}
          </ul>
          <div className="flex h-7 items-center border-t border-ghost-border/60 px-2.5 text-[8px] text-ghost-faint">
            <span>↑↓ navigate</span>
            <span className="ml-auto">Tab complete · Esc close</span>
          </div>
        </div>
      )}
    </div>
  )
}
