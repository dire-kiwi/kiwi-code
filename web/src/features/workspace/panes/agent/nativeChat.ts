export const nativeChatProviders = { codex: { name: 'Codex' } } as const
// Provider-neutral view model. Provider adapters live on the server; a future
// Claude adapter can emit this same contract without changing the chat surface.
export type ChatItem = {
  id: string
  kind: 'user' | 'assistant' | 'reasoning' | 'tool'
  text: string
  title?: string
  status?: string
}
export type ChatQuestion = {
  id: string
  header: string
  question: string
  isSecret?: boolean
  options?: { label: string; description: string }[]
}
export type ChatRequest = {
  id: number | string
  kind: 'approval' | 'input'
  title: string
  details: string
  questions?: ChatQuestion[]
}
export type ChatUsage = { inputTokens: number; outputTokens: number; totalTokens: number }
export type ChatState = {
  items: ChatItem[]
  requests: ChatRequest[]
  queuedMessages?: string[]
  working: boolean
  error?: string
  usage?: ChatUsage
  model?: string
  effort?: string
  sequence: number
}
export type ChatEvent =
  | { type: 'chat_snapshot' | 'chat_state'; sequence: number; value: Omit<ChatState, 'sequence'> }
  | { type: 'chat_item'; sequence: number; value: ChatItem }
  | { type: 'chat_error'; message: string }
  | { type: 'chat_sent' }
  | { type: 'chat_usage'; sequence: number; value: ChatUsage }
export const emptyChat: ChatState = { items: [], requests: [], working: false, sequence: -1 }
export function reduceChat(state: ChatState, event: ChatEvent): ChatState {
  if (event.type === 'chat_sent') return state
  if (event.type === 'chat_error') return { ...state, error: event.message }
  if (event.type !== 'chat_snapshot' && event.sequence <= state.sequence) return state
  if (event.type === 'chat_usage') return { ...state, usage: event.value, sequence: event.sequence }
  if (event.type === 'chat_item') {
    const index = state.items.findIndex((item) => item.id === event.value.id)
    const items = [...state.items]
    if (index < 0) items.push(event.value)
    else items[index] = event.value
    return { ...state, items, sequence: event.sequence }
  }
  return {
    ...event.value,
    items: event.value.items ?? [],
    requests: event.value.requests ?? [],
    sequence: event.sequence,
  }
}
