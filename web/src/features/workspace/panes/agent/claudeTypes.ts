// The Claude Code stream-JSON event shapes and the view models derived from
// them. Mirrors piTypes.ts: types at the bottom of the dependency graph so
// the pane and the pure helpers both import downward.
import type { PiNativeComposerSuggestion } from './PiNativeComposer'

export type ClaudeContentBlock = {
  type?: string
  text?: string
  thinking?: string
  id?: string
  name?: string
  input?: unknown
  tool_use_id?: string
  content?: unknown
  is_error?: boolean
  source?: {
    type?: string
    media_type?: string
    data?: string
  }
}

export type ClaudeUsage = {
  input_tokens?: number
  output_tokens?: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
}

export type ClaudeApiMessage = {
  id?: string
  role?: string
  content?: string | ClaudeContentBlock[]
  stop_reason?: string | null
  usage?: ClaudeUsage
}

export type ClaudeStreamInnerEvent = {
  type?: string
  content_block?: { type?: string; name?: string }
  delta?: { type?: string; text?: string; thinking?: string }
}

export type ClaudeEvent = {
  type?: string
  subtype?: string
  uuid?: string
  session_id?: string
  parent_tool_use_id?: string | null
  message?: ClaudeApiMessage
  event?: ClaudeStreamInnerEvent
  model?: string
  slash_commands?: unknown
  result?: string
  is_error?: boolean
  usage?: ClaudeUsage
  total_cost_usd?: number
  num_turns?: number
  // claude_native_* envelope fields from the Kiwi Code bridge.
  isStreaming?: boolean
  sessionId?: string
  effort?: string
  events?: Array<{ at?: number; event?: ClaudeEvent }>
}

export type ClaudeChatMessage = {
  key: string
  role: 'user' | 'assistant'
  at: number
  blocks: ClaudeContentBlock[]
  pending?: boolean
}

export type ClaudeToolResult = {
  output: unknown
  isError: boolean
  at: number
}

export type ClaudeRunSummary = {
  key: string
  at: number
  label: string
  text: string
  tone: 'warning' | 'error'
}

export type ClaudeSessionStats = {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  cost: number
  turns: number
}

export type ClaudeEventStamp = {
  at: number
  label: string
}

export type ComposerSuggestion = PiNativeComposerSuggestion & { completion: string }

// A prompt echoed back by Claude within this window is matched to the pending
// local message rather than appended as a second copy.
export const CLAUDE_PENDING_PROMPT_MATCH_MS = 30_000
export const CLAUDE_DEFAULT_CONTEXT_WINDOW = 200_000
