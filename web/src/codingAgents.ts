import type {
  CodingAgent,
  CodingAgentChoice,
  CodingAgentConfig,
  CodingAgentSelection,
  CodingAgentSetting,
  ConfiguredClaudeAgent,
} from './types'

const configuredClaudeAgentPattern = /^claude-(?:gpt-)?profile-[A-Za-z0-9_-]{1,64}$/

export function configuredCodingAgentId(agent: CodingAgentSetting): ConfiguredClaudeAgent {
  return agent.kind === 'claude-gpt'
    ? `claude-gpt-profile-${agent.id}`
    : `claude-profile-${agent.id}`
}

export function isCodingAgent(value: unknown): value is CodingAgent {
  return value === 'pi'
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

export function configuredCodingAgentChoices(agents: CodingAgentSetting[]) {
  return agents.map((agent) => ({
    id: configuredCodingAgentId(agent),
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
]
