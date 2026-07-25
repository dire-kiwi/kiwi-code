export type PromptPaste = {
  id: number
  content: string
}

export type CollapsedPromptPaste = {
  value: string
  pastes: PromptPaste[]
  selectionStart: number
}

export const LARGE_PASTE_LINE_THRESHOLD: number
export const LARGE_PASTE_CHARACTER_THRESHOLD: number

export function expandPromptPastes(value: string, pastes: PromptPaste[]): string
export function prunePromptPastes(value: string, pastes: PromptPaste[]): PromptPaste[]
export function collapsePromptPaste(options: {
  value: string
  selectionStart: number
  selectionEnd: number
  pastedText: string
  pastes: PromptPaste[]
  maxExpandedLength?: number
}): CollapsedPromptPaste | null
