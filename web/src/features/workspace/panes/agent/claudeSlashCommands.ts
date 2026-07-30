// Dispatch for the five client-side Claude slash commands. Mirrors
// piSlashCommands.ts; the two are NOT shared because the vocabularies differ --
// Pi has /compact and /reload and addresses models as provider/id, Claude does
// not and picks from a fixed alias list.
import { claudeModelChoices, claudeThinkingLevelIds } from '@/codingAgents'
import { formatSessionStats } from './claudeFormatting'
import type { ClaudeSessionStats } from './claudeTypes'

export type ClaudeSlashCommandContext = {
  isStreaming: boolean
  hasImageAttachments: boolean
  selectedModel: string
  selectedThinking: string
  sessionStats: ClaudeSessionStats | null
  /** Returns false when the socket refused the command; nothing else should happen then. */
  send: (payload: Record<string, unknown> & { type: string }) => boolean
  setSelectedModel: (model: string) => void
  setSelectedThinking: (level: string) => void
  setError: (message: string) => void
  setNotice: (message: string) => void
  clearSubmittedDraft: () => void
}

/**
 * Returns true when `message` was a slash command and has been handled, so the
 * caller must not also send it to Claude as a prompt.
 */
export function runClaudeSlashCommand(
  message: string,
  context: ClaudeSlashCommandContext,
): boolean {
  const match = message.match(/^\/(restart|new|model|thinking|session)(?:\s+([\s\S]*))?$/)
  if (!match) return false

  const commandName = match[1]
  const argument = (match[2] ?? '').trim()
  if (context.hasImageAttachments) {
    context.setError(`Remove image attachments before running /${commandName}.`)
    return true
  }
  if (context.isStreaming && commandName !== 'session') {
    context.setError(`Wait for Claude to finish before running /${commandName}.`)
    return true
  }

  if (commandName === 'restart') {
    if (argument) {
      context.setError('Use /restart without arguments.')
      return true
    }
    if (!context.send({
      type: 'restart',
      ...(context.selectedModel ? { modelId: context.selectedModel } : {}),
      ...(claudeThinkingLevelIds.some((level) => level === context.selectedThinking)
        ? { level: context.selectedThinking }
        : {}),
    })) return true
    context.clearSubmittedDraft()
    context.setNotice('Restarting the Claude session…')
    return true
  }

  if (commandName === 'new') {
    if (argument) {
      context.setError('Use /new without arguments.')
      return true
    }
    if (!context.send({ type: 'new_session' })) return true
    context.clearSubmittedDraft()
    context.setNotice('Starting a new Claude session…')
    return true
  }

  if (commandName === 'model') {
    if (!claudeModelChoices.some((choice) => choice.id === argument)) {
      context.setError(`Use /model <${claudeModelChoices.map((choice) => choice.id).join('|')}>.`)
      return true
    }
    if (!context.send({ type: 'set_model', modelId: argument })) return true
    context.setSelectedModel(argument)
    context.clearSubmittedDraft()
    context.setNotice(`Switching Claude to ${argument}…`)
    return true
  }

  if (commandName === 'thinking') {
    if (!claudeThinkingLevelIds.some((level) => level === argument)) {
      context.setError(`Use /thinking <${claudeThinkingLevelIds.join('|')}>.`)
      return true
    }
    if (!context.send({ type: 'set_thinking_level', level: argument })) return true
    context.setSelectedThinking(argument)
    context.clearSubmittedDraft()
    context.setNotice(`Setting Claude reasoning effort to ${argument}…`)
    return true
  }

  if (argument) {
    context.setError('Use /session without arguments.')
    return true
  }
  context.clearSubmittedDraft()
  context.setNotice(formatSessionStats(context.sessionStats))
  return true
}
