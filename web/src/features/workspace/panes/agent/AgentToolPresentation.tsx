import type { LucideIcon } from 'lucide-react'
import {
  Check,
  CircleAlert,
  Copy,
  FilePenLine,
  FileSearch,
  FolderSearch,
  LoaderCircle,
  Search,
  SquareTerminal,
  Wrench,
  X,
} from 'lucide-react'

export type AgentToolStatus = 'running' | 'success' | 'error'

export function AgentToolStatusIcon({
  status,
  size = 12,
  errorIcon = 'x',
}: {
  status: AgentToolStatus
  size?: number
  errorIcon?: 'x' | 'alert'
}) {
  if (status === 'running') {
    return <LoaderCircle size={size} className="shrink-0 animate-spin text-ghost-yellow" />
  }
  if (status === 'success') {
    return <Check size={size} className="shrink-0 text-ghost-green" />
  }
  const ErrorIcon = errorIcon === 'alert' ? CircleAlert : X
  return <ErrorIcon size={size} className="shrink-0 text-ghost-bright-red" />
}

export function agentToolIcon(name: string): LucideIcon {
  if (/write|edit|patch|apply/i.test(name)) return FilePenLine
  if (/bash|shell|exec|terminal|command|run/i.test(name)) return SquareTerminal
  if (/grep|search/i.test(name)) return Search
  if (/find|glob|ls/i.test(name)) return FolderSearch
  if (/read|view|cat|open|file/i.test(name)) return FileSearch
  return Wrench
}

export function agentToolLabel(name: string, args: unknown): string {
  const values = objectRecord(args)
  const path = stringValue(values?.path ?? values?.file_path ?? values?.filePath ?? values?.filename)
  if (/write|edit|patch|apply/i.test(name) && path) return `Edited ${shortPath(path)}`
  if (/read|view|open/i.test(name) && path) return `Read ${shortPath(path)}`
  const command = stringValue(values?.command)
  if (command) return command.split('\n')[0]?.slice(0, 100) || name
  return name.replaceAll('_', ' ')
}

export function formatAgentToolValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    const text = textFromContent(value)
    if (text) return text
  }
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>
    if (Array.isArray(record.content)) {
      const text = textFromContent(record.content)
      if (text) return text
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

type AgentToolOutputSection = {
  value: string
}

type AgentToolOutputProps = {
  sections: AgentToolOutputSection[]
  copyText?: string
}

export function AgentToolOutput({ sections, copyText }: AgentToolOutputProps) {
  const value = sections.map((section) => section.value).filter(Boolean).join('\n\n')
  return (
    <div className="relative border-t border-ghost-border/60 bg-ghost-black/50">
      {copyText && (
        <button
          type="button"
          className="absolute right-2 top-2 z-[1] grid size-[25px] place-items-center rounded-md border border-ghost-border/70 bg-ghost-panel text-ghost-dim"
          aria-label="Copy tool details"
          onClick={() => {
            const write = navigator.clipboard?.writeText(copyText)
            if (write) void write.catch(() => {})
          }}
        >
          <Copy size={12} />
        </button>
      )}
      <pre className="m-0 max-h-[420px] overflow-auto whitespace-pre-wrap break-words pb-3.5 pl-3.5 pr-10 pt-[13px] font-mono text-[10px] leading-[1.62] text-ghost-muted">
        {value}
      </pre>
    </div>
  )
}

function textFromContent(value: unknown[]): string {
  return value
    .map((part) => objectRecord(part))
    .filter((part): part is Record<string, unknown> => part !== null)
    .map((part) => stringValue(part.text))
    .filter(Boolean)
    .join('\n')
}

function objectRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' ? value as Record<string, unknown> : null
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function shortPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length > 3 ? parts.slice(-3).join('/') : path
}
