// The shape of everything Pi Native sends over its RPC socket, plus the view
// models derived from it. Kept in its own module so the session hook, the pure
// helpers, and the pane can all import downward without forming a cycle.

export type PiRenderedImage = {
  data: string
  mimeType: string
}

export type PiContentBlock = {
  type?: string
  text?: string
  thinking?: string
  id?: string
  name?: string
  arguments?: unknown
  data?: string
  mimeType?: string
}

export type PiAgentMessage = {
  role?: string
  content?: string | PiContentBlock[]
  summary?: string
  timestamp?: number | string
  toolCallId?: string
  toolName?: string
  isError?: boolean
  stopReason?: string
  errorMessage?: string
  command?: string
  output?: string
  exitCode?: number
  usage?: {
    input?: number
    output?: number
    cacheRead?: number
    cacheWrite?: number
  }
}

export type PiToolState = {
  callId: string
  name: string
  args?: unknown
  output?: unknown
  status: 'running' | 'success' | 'error'
  timestamp: number
}

export type PiSlashCommandSource = 'native' | 'extension' | 'prompt' | 'skill'

export type PiSlashCommand = {
  name: string
  description?: string
  source: PiSlashCommandSource
}

export type PiModel = {
  provider?: string
  id?: string
  name?: string
}

export type PiContextUsage = {
  tokens: number | null
  contextWindow: number
  percent: number | null
}

export type PiSessionStats = {
  totalMessages?: number
  toolCalls?: number
  tokens?: {
    input?: number
    output?: number
    cacheRead?: number
    cacheWrite?: number
    total?: number
  }
  cost?: number
  contextUsage?: PiContextUsage
}

export type PiEventStamp = {
  at: number
  label: string
}

export type ComposerSuggestion = {
  id: string
  label: string
  description: string
  source: PiSlashCommandSource | 'model' | 'level'
  completion: string
}

export type PiRunDiagnostic = {
  label: string
  text: string
  tone: 'warning' | 'error'
}

export type PiRpcEvent = {
  type?: string
  command?: string
  success?: boolean
  error?: string
  message?: PiAgentMessage | string
  data?: {
    messages?: PiAgentMessage[]
    isStreaming?: boolean
    isCompacting?: boolean
    pendingMessageCount?: number
    commands?: PiSlashCommand[]
    models?: PiModel[]
    model?: PiModel
    cancelled?: boolean
    provider?: string
    id?: string
    name?: string
    thinkingLevel?: string
    totalMessages?: number
    toolCalls?: number
    tokens?: PiSessionStats['tokens']
    cost?: number
    contextUsage?: PiContextUsage
  }
  toolCallId?: string
  toolName?: string
  args?: unknown
  partialResult?: unknown
  result?: unknown
  isError?: boolean
  steering?: string[]
  followUp?: string[]
  notifyType?: string
  method?: string
  assistantMessageEvent?: { type?: string }
  willRetry?: boolean
  errorMessage?: string
  finalError?: string
  aborted?: boolean
}
