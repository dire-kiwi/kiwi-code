// The Claude slash vocabulary and the composer autocomplete built from it.
import { claudeModelChoices, claudeThinkingLevelIds } from '@/codingAgents'
import { suggestionID } from './agentFormat'
import type { ComposerSuggestion } from './claudeTypes'

export const NATIVE_SLASH_COMMANDS: Array<{ name: string; description: string }> = [
  {
    name: 'restart',
    description: 'Restart Claude Code and resume this saved conversation',
  },
  {
    name: 'new',
    description: 'Start a new saved Claude session in this thread',
  },
  {
    name: 'model',
    description: 'Switch the model for this Claude session',
  },
  {
    name: 'thinking',
    description: 'Set Claude’s reasoning effort',
  },
  {
    name: 'session',
    description: 'Show token, cache, turn, and cost totals',
  },
]

export function normalizeClaudeCommands(commands: unknown): string[] {
  if (!Array.isArray(commands)) return []
  const seen = new Set<string>()
  const normalized: string[] = []
  for (const command of commands) {
    const name = typeof command === 'string'
      ? command.trim()
      : typeof (command as { name?: unknown })?.name === 'string'
        ? ((command as { name: string }).name).trim()
        : ''
    if (!name || name.includes('/') || /\s/.test(name) || seen.has(name)) continue
    if (NATIVE_SLASH_COMMANDS.some((native) => native.name === name)) continue
    seen.add(name)
    normalized.push(name)
  }
  return normalized
}

export function buildComposerSuggestions(draft: string, claudeCommands: string[]): ComposerSuggestion[] {
  const commandMatch = draft.match(/^\/([^\s]*)$/)
  if (commandMatch) {
    const query = (commandMatch[1] ?? '').toLowerCase()
    const native = NATIVE_SLASH_COMMANDS
      .filter((command) => command.name.toLowerCase().startsWith(query))
      .map((command, index) => ({
        id: suggestionID('native', command.name, String(index)),
        label: `/${command.name}`,
        description: command.description,
        source: 'native' as const,
        completion: command.name === 'model' || command.name === 'thinking'
          ? `/${command.name} `
          : `/${command.name}`,
      }))
    const claude = claudeCommands
      .filter((name) => name.toLowerCase().startsWith(query))
      .map((name, index) => ({
        id: suggestionID('claude', name, String(index)),
        label: `/${name}`,
        description: 'Claude Code command',
        source: 'skill' as const,
        completion: `/${name}`,
      }))
    return [...native, ...claude].slice(0, 12)
  }

  const modelMatch = draft.match(/^\/model\s+([^\s]*)$/)
  if (modelMatch) {
    const query = (modelMatch[1] ?? '').toLowerCase()
    return claudeModelChoices
      .filter((choice) => choice.id.toLowerCase().includes(query)
        || choice.label.toLowerCase().includes(query))
      .map((choice, index) => ({
        id: suggestionID('model', choice.id, String(index)),
        label: `/model ${choice.id}`,
        description: choice.label,
        source: 'model' as const,
        completion: `/model ${choice.id}`,
      }))
  }

  const thinkingMatch = draft.match(/^\/thinking\s+([^\s]*)$/)
  if (thinkingMatch) {
    const query = (thinkingMatch[1] ?? '').toLowerCase()
    return claudeThinkingLevelIds
      .filter((level) => level.startsWith(query))
      .map((level, index) => ({
        id: suggestionID('level', level, String(index)),
        label: `/thinking ${level}`,
        description: `Use ${level} reasoning effort`,
        source: 'level' as const,
        completion: `/thinking ${level}`,
      }))
  }

  return []
}
