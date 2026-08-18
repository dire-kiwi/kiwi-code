// The slash-command vocabulary and the composer autocomplete built from it.
import { piModelReasoningLevels, piThinkingLevelIds } from '@/codingAgents'
import { suggestionID } from './agentFormat'
import { piSlashSourceLabel } from './PiNativeComposer'
import type { ComposerSuggestion, PiModel, PiSlashCommand } from './piTypes'

export const NATIVE_SLASH_COMMANDS: PiSlashCommand[] = [
  {
    name: 'compact',
    description: 'Summarize older context to make room for more work',
    source: 'native',
  },
  {
    name: 'reload',
    description: 'Restart Pi Native, reload extensions, and resume this conversation',
    source: 'native',
  },
  {
    name: 'restart',
    description: 'Restart Pi Native and resume this saved conversation',
    source: 'native',
  },
  {
    name: 'new',
    description: 'Start a new saved Pi session in this thread',
    source: 'native',
  },
  {
    name: 'model',
    description: 'Switch the model for this Pi session',
    source: 'native',
  },
  {
    name: 'thinking',
    description: 'Set Pi’s thinking level',
    source: 'native',
  },
  {
    name: 'session',
    description: 'Show message, tool, token, cache, and cost totals',
    source: 'native',
  },
]

export function normalizePiCommands(commands: PiSlashCommand[]): PiSlashCommand[] {
  const nativeNames = new Set(NATIVE_SLASH_COMMANDS.map((command) => command.name))
  const seen = new Set<string>()
  const normalized: PiSlashCommand[] = []
  for (const command of commands) {
    const name = typeof command?.name === 'string' ? command.name.trim() : ''
    if (!name || name.includes('/') || /\s/.test(name) || nativeNames.has(name) || seen.has(name)) continue
    const source = command.source
    if (source !== 'extension' && source !== 'prompt' && source !== 'skill') continue
    seen.add(name)
    normalized.push({
      name,
      source,
      ...(typeof command.description === 'string' && command.description.trim()
        ? { description: command.description.trim() }
        : {}),
    })
  }
  return normalized
}

export function normalizePiModels(models: PiModel[]): PiModel[] {
  const seen = new Set<string>()
  const normalized: PiModel[] = []
  for (const model of models) {
    const provider = typeof model?.provider === 'string' ? model.provider.trim() : ''
    const id = typeof model?.id === 'string' ? model.id.trim() : ''
    const identifier = provider && id ? `${provider}/${id}` : ''
    if (!identifier || seen.has(identifier)) continue
    seen.add(identifier)
    const reasoning = typeof model.reasoning === 'boolean' ? model.reasoning : undefined
    const thinkingLevelMap = model.thinkingLevelMap
    const reportedLevels = Array.isArray(model.reasoningLevels)
      ? model.reasoningLevels.filter((level): level is string => typeof level === 'string' && level.length > 0)
      : undefined
    const reasoningLevels = reportedLevels
      ?? ((reasoning !== undefined || thinkingLevelMap)
        ? piModelReasoningLevels(reasoning, thinkingLevelMap)
        : undefined)
    normalized.push({
      provider,
      id,
      ...(typeof model.name === 'string' && model.name.trim() ? { name: model.name.trim() } : {}),
      ...(reasoning !== undefined ? { reasoning } : {}),
      ...(thinkingLevelMap ? { thinkingLevelMap } : {}),
      ...(reasoningLevels ? { reasoningLevels } : {}),
    })
  }
  return normalized
}

export function buildComposerSuggestions(
  draft: string,
  piCommands: PiSlashCommand[],
  models: PiModel[],
  thinkingLevels: readonly string[] = piThinkingLevelIds,
): ComposerSuggestion[] {
  const commandMatch = draft.match(/^\/([^\s]*)$/)
  if (commandMatch) {
    const query = (commandMatch[1] ?? '').toLowerCase()
    return [...NATIVE_SLASH_COMMANDS, ...piCommands]
      .filter((command) => command.name.toLowerCase().startsWith(query))
      .slice(0, 12)
      .map((command, index) => ({
        id: suggestionID('command', command.source, command.name, String(index)),
        label: `/${command.name}`,
        description: command.description || `${piSlashSourceLabel(command.source)} command`,
        source: command.source,
        completion: command.name === 'model' || command.name === 'thinking'
          ? `/${command.name} `
          : `/${command.name}`,
      }))
  }

  const modelMatch = draft.match(/^\/model\s+([^\s]*)$/)
  if (modelMatch) {
    const query = (modelMatch[1] ?? '').toLowerCase()
    return models
      .map((model) => ({ model, identifier: modelIdentifier(model) }))
      .filter((candidate): candidate is { model: PiModel; identifier: string } => Boolean(candidate.identifier))
      .filter(({ identifier, model }) => (
        identifier.toLowerCase().includes(query)
        || (model.name ?? '').toLowerCase().includes(query)
      ))
      .slice(0, 12)
      .map(({ identifier, model }, index) => ({
        id: suggestionID('model', identifier, String(index)),
        label: `/model ${identifier}`,
        description: model.name || `Model from ${model.provider}`,
        source: 'model',
        completion: `/model ${identifier}`,
      }))
  }

  const thinkingMatch = draft.match(/^\/thinking\s+([^\s]*)$/)
  if (thinkingMatch) {
    const query = (thinkingMatch[1] ?? '').toLowerCase()
    return thinkingLevels
      .filter((level) => level.startsWith(query))
      .map((level, index) => ({
        id: suggestionID('level', level, String(index)),
        label: `/thinking ${level}`,
        description: `Use ${level} reasoning effort`,
        source: 'level',
        completion: `/thinking ${level}`,
      }))
  }

  return []
}

export function modelIdentifier(model: PiModel | undefined): string {
  const provider = typeof model?.provider === 'string' ? model.provider.trim() : ''
  const id = typeof model?.id === 'string' ? model.id.trim() : ''
  return provider && id ? `${provider}/${id}` : ''
}
