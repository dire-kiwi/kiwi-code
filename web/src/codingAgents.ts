import type {
  CodingAgent,
  CodingAgentChoice,
  CodingAgentConfig,
  CodingAgentSelection,
  CodingAgentSetting,
  ConfiguredClaudeAgent,
  PiPresentation,
} from './types'

const configuredClaudeAgentPattern = /^claude-(?:gpt-)?profile-[A-Za-z0-9_-]{1,64}$/

export function configuredCodingAgentId(agent: CodingAgentSetting): ConfiguredClaudeAgent {
  return agent.kind === 'claude-gpt'
    ? `claude-gpt-profile-${agent.id}`
    : `claude-profile-${agent.id}`
}

export function codingAgentSelectionForSetting(agent: CodingAgentSetting): CodingAgentSelection {
  if (agent.kind === 'pi' || agent.kind === 'pi-native' || agent.kind === 'codex' || agent.kind === 'grok') {
    return agent.kind
  }
  return configuredCodingAgentId(agent)
}

export function defaultCodingAgentSelection(agents: CodingAgentSetting[]): CodingAgentSelection {
  const configuredDefault = agents.find((agent) => agent.isDefault)
  return configuredDefault ? codingAgentSelectionForSetting(configuredDefault) : 'pi-native'
}

export function isCodingAgent(value: unknown): value is CodingAgent {
  return value === 'pi'
    || value === 'codex'
    || value === 'grok'
    || value === 'claude'
    || value === 'claude-gpt'
    || (typeof value === 'string' && configuredClaudeAgentPattern.test(value))
}

export function isClaudeGPTCodingAgent(value: unknown) {
  return value === 'claude-gpt'
    || (typeof value === 'string' && value.startsWith('claude-gpt-profile-'))
}

export function isCodingAgentSelection(value: unknown): value is CodingAgentSelection {
  return value === 'pi-native' || value === 'claude-native' || isCodingAgent(value)
}

export function isNativeCodingAgentSelection(
  selection: CodingAgentSelection,
): selection is 'pi-native' | 'claude-native' {
  return selection === 'pi-native' || selection === 'claude-native'
}

export function codingAgentTargetForSelection(selection: CodingAgentSelection): {
  agent: CodingAgent
  presentation: PiPresentation
} {
  if (selection === 'pi-native') return { agent: 'pi', presentation: 'native' }
  if (selection === 'claude-native') return { agent: 'claude', presentation: 'native' }
  return { agent: selection, presentation: 'terminal' }
}

export function codingAgentSelectionForTarget(
  agent: CodingAgent,
  presentation: PiPresentation = 'terminal',
): CodingAgentSelection {
  if (presentation === 'native' && agent === 'pi') return 'pi-native'
  if (presentation === 'native' && agent === 'claude') return 'claude-native'
  return agent
}

export function nativeCodingAgentLabel(selection: CodingAgentSelection): string | null {
  if (selection === 'pi-native') return 'Pi Native'
  if (selection === 'claude-native') return 'Claude Native'
  return null
}

export function configuredCodingAgentChoices(agents: CodingAgentSetting[]) {
  return agents.map((agent) => ({
    id: codingAgentSelectionForSetting(agent),
    label: agent.name,
  }))
}

export const piThinkingLevelIds = [
  'off',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const

export const codexThinkingLevelIds = [
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
] as const

export const grokThinkingLevelIds = [
  'low',
  'medium',
  'high',
  'xhigh',
] as const

export const claudeThinkingLevelIds = [
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
  'ultracode',
] as const

export const claudeModelChoices: CodingAgentChoice[] = [
  { id: 'sonnet', label: 'Claude Sonnet (latest)' },
  { id: 'opus', label: 'Claude Opus (latest)' },
  { id: 'haiku', label: 'Claude Haiku (latest)' },
  { id: 'fable', label: 'Claude Fable (latest)' },
]

type ThinkingLevelId = typeof piThinkingLevelIds[number] | typeof claudeThinkingLevelIds[number]

const thinkingLevelLabels: Record<ThinkingLevelId, string> = {
  off: 'Off',
  minimal: 'Minimal',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  xhigh: 'Extra high',
  max: 'Maximum',
  ultracode: 'Ultracode (Claude built-in)',
}

function thinkingLevels(
  defaultLabel: string,
  ids: readonly ThinkingLevelId[] = piThinkingLevelIds,
): CodingAgentChoice[] {
  return [
    { id: '', label: defaultLabel },
    ...ids.map((id) => ({ id, label: thinkingLevelLabels[id] })),
  ]
}

export const fallbackCodingAgentConfigs: CodingAgentConfig[] = [
  {
    id: 'pi',
    label: 'Pi',
    models: [{ id: '', label: 'Use Pi default' }],
    thinkingLevels: thinkingLevels('Use Pi default'),
  },
  {
    id: 'codex',
    label: 'Codex CLI',
    models: [{ id: '', label: 'Use Codex default' }],
    thinkingLevels: thinkingLevels('Use Codex default', codexThinkingLevelIds),
  },
  {
    id: 'grok',
    label: 'Grok CLI',
    models: [{ id: '', label: 'Use Grok default' }],
    thinkingLevels: thinkingLevels('Use Grok default', grokThinkingLevelIds),
  },
]
