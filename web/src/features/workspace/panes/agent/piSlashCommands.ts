// Dispatch for the seven client-side slash commands. Pulled out of the pane so
// the set of things a slash command is allowed to touch is the context type
// below and nothing else -- previously each branch could reach any of the
// pane's forty pieces of state.
import { piThinkingLevelIds } from '@/codingAgents'

export type PiSlashCommandContext = {
  isStreaming: boolean
  hasImageAttachments: boolean
  selectedModel: string
  selectedThinking: string
  /** Returns false when the socket refused the command; nothing else should happen then. */
  send: (payload: Record<string, unknown> & { type: string }) => boolean
  setError: (message: string) => void
  setNotice: (message: string) => void
  clearSubmittedDraft: () => void
  markSessionStatsPending: () => void
}

const COMMAND = /^\/(compact|reload|restart|new|model|thinking|session)(?:\s+([\s\S]*))?$/

/**
 * Returns true when `message` was a slash command and has been handled, so the
 * caller must not also send it to Pi as a prompt.
 */
export function runPiSlashCommand(message: string, context: PiSlashCommandContext): boolean {
  // A read-only pane swallows the command rather than falling through to a
  // prompt it is equally not allowed to send.
  const match = message.match(COMMAND)
  if (!match) return false

  const commandName = match[1]
  const argument = (match[2] ?? '').trim()
  if (context.hasImageAttachments) {
    context.setError(`Remove image attachments before running /${commandName}.`)
    return true
  }
  if (context.isStreaming && commandName !== 'session') {
    context.setError(`Wait for Pi to finish before running /${commandName}.`)
    return true
  }

  if (commandName === 'compact') {
    if (!context.send({
      type: 'compact',
      ...(argument ? { customInstructions: argument } : {}),
    })) return true
    context.clearSubmittedDraft()
    context.setNotice('Compacting conversation context…')
    return true
  }

  if (commandName === 'reload' || commandName === 'restart') {
    if (argument) {
      context.setError(`Use /${commandName} without arguments.`)
      return true
    }
    const modelSeparator = context.selectedModel.indexOf('/')
    const provider = modelSeparator > 0 ? context.selectedModel.slice(0, modelSeparator) : ''
    const modelId = modelSeparator > 0 ? context.selectedModel.slice(modelSeparator + 1) : ''
    if (!context.send({
      type: commandName,
      ...(provider && modelId ? { provider, modelId } : {}),
      ...(piThinkingLevelIds.some((level) => level === context.selectedThinking)
        ? { level: context.selectedThinking }
        : {}),
    })) return true
    context.clearSubmittedDraft()
    context.setNotice(commandName === 'reload'
      ? 'Restarting Pi to reload extensions…'
      : 'Restarting Pi Native…')
    return true
  }

  if (commandName === 'new') {
    if (argument) {
      context.setError('Use /new without arguments.')
      return true
    }
    if (!context.send({ type: 'new_session' })) return true
    context.clearSubmittedDraft()
    context.setNotice('Starting a new Pi session…')
    return true
  }

  if (commandName === 'model') {
    const separator = argument.indexOf('/')
    const provider = separator > 0 ? argument.slice(0, separator) : ''
    const modelId = separator > 0 ? argument.slice(separator + 1) : ''
    if (!provider || !modelId) {
      context.setError('Use /model <provider/model>.')
      return true
    }
    if (!context.send({ type: 'set_model', provider, modelId })) return true
    context.clearSubmittedDraft()
    context.setNotice(`Switching Pi to ${provider}/${modelId}…`)
    return true
  }

  if (commandName === 'thinking') {
    if (!piThinkingLevelIds.some((level) => level === argument)) {
      context.setError(`Use /thinking <${piThinkingLevelIds.join('|')}>.`)
      return true
    }
    if (!context.send({ type: 'set_thinking_level', level: argument })) return true
    context.clearSubmittedDraft()
    context.setNotice(`Setting Pi thinking to ${argument}…`)
    return true
  }

  if (argument) {
    context.setError('Use /session without arguments.')
    return true
  }
  if (!context.send({ type: 'get_session_stats' })) return true
  context.markSessionStatsPending()
  context.clearSubmittedDraft()
  context.setNotice('Loading Pi session totals…')
  return true
}
