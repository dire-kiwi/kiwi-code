import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { classNames } from '../../lib/classNames'
import { formatDuration } from '../../lib/formatDuration'
import { AgentMarkdown } from '../molecules/AgentMarkdown'
import {
  agentToolIcon,
  agentToolLabel,
  AgentToolOutput,
  AgentToolStatusIcon,
  formatAgentToolValue,
} from '../molecules/AgentToolPresentation'
import { NativeAgentMessage } from '../molecules/NativeAgentMessage'
import {
  piNativeStyles,
  piNativeSummaryToneStyles,
  piNativeToolStatusStyles,
} from './piNativeStyles'

export type PiTimelineEntryValue =
  | {
      kind: 'user'
      key: string
      text: string
      images: Array<{ mimeType: string; data: string }>
      timestamp: number
    }
  | { kind: 'assistant'; key: string; text: string; timestamp: number }
  | {
      kind: 'summary'
      key: string
      label: string
      text: string
      timestamp: number
      tone?: 'warning' | 'error'
    }
  | {
      kind: 'tool'
      key: string
      timestamp: number
      callId: string
      name: string
      args?: unknown
      output?: unknown
      status: 'running' | 'success' | 'error'
    }
  | { kind: 'turn-marker'; key: string; durationMs: number; timestamp: number }

export function PiNativeTimelineEntry({ entry }: { entry: PiTimelineEntryValue }) {
  if (entry.kind === 'user') {
    return (
      <NativeAgentMessage
        role="user"
        text={entry.text}
        images={entry.images}
      />
    )
  }
  if (entry.kind === 'assistant') {
    return <NativeAgentMessage role="assistant" text={entry.text} />
  }
  if (entry.kind === 'summary') {
    return (
      <article className={classNames(
        piNativeStyles.summary,
        entry.tone && piNativeSummaryToneStyles[entry.tone],
      )}>
        <span>{entry.label}</span>
        <AgentMarkdown text={entry.text} />
      </article>
    )
  }
  if (entry.kind === 'turn-marker') {
    return <div className={piNativeStyles.turnMarker}>Worked for {formatDuration(entry.durationMs)}</div>
  }
  return <PiToolEntry entry={entry} />
}

function PiToolEntry({ entry }: { entry: Extract<PiTimelineEntryValue, { kind: 'tool' }> }) {
  const [expanded, setExpanded] = useState(false)
  const content = [entry.args, entry.output]
    .filter((value) => value !== undefined)
    .map(formatAgentToolValue)
    .filter(Boolean)
    .join('\n\n')
  const label = agentToolLabel(entry.name, entry.args)
  const Icon = agentToolIcon(entry.name)

  return (
    <article className={classNames(piNativeStyles.tool, piNativeToolStatusStyles[entry.status])}>
      <button
        type="button"
        className={piNativeStyles.toolHeader}
        aria-expanded={expanded}
        disabled={!content}
        onClick={() => setExpanded((value) => !value)}
      >
        <Icon size={13} />
        {content && (
          <ChevronRight
            size={12}
            className={classNames(
              piNativeStyles.toolChevron,
              expanded && piNativeStyles.toolChevronOpen,
            )}
          />
        )}
        <span className={piNativeStyles.toolLabel}>{label}</span>
        <span className={piNativeStyles.toolMeta}>
          <AgentToolStatusIcon status={entry.status} size={11} errorIcon="alert" />
          {entry.name} · {entry.status === 'success' ? 'done' : entry.status}
        </span>
      </button>
      {expanded && content && (
        <AgentToolOutput
          sections={[{ value: content }]}
          copyText={content}
        />
      )}
    </article>
  )
}
