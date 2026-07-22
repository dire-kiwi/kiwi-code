import type {
  ClaudeCodeProfile,
  ClaudeCodeProfileAgent,
  CodingAgent,
  CodingAgentChoice,
  CodingAgentConfig,
  CodingAgentSelection,
} from './types'

const claudeCodeProfileAgentPattern = /^claude-profile-[A-Za-z0-9_-]{1,64}$/

export function claudeCodeProfileAgentId(profileId: string): ClaudeCodeProfileAgent {
  return `claude-profile-${profileId}`
}

export function isCodingAgent(value: unknown): value is CodingAgent {
  return value === 'pi'
    || value === 'claude'
    || value === 'claude-gpt'
    || (typeof value === 'string' && claudeCodeProfileAgentPattern.test(value))
}

export function isCodingAgentSelection(value: unknown): value is CodingAgentSelection {
  return value === 'pi-native' || value === 'claude-native' || isCodingAgent(value)
}

export function claudeCodeProfileChoices(profiles: ClaudeCodeProfile[]) {
  return profiles.map((profile) => ({
    id: claudeCodeProfileAgentId(profile.id),
    label: `Claude Code · ${profile.name}`,
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
  {
    id: 'claude',
    label: 'Claude Code',
    models: [
      { id: '', label: 'Use Claude default' },
      ...claudeModelChoices,
    ],
    thinkingLevels: thinkingLevels('Use Claude default', claudeThinkingLevelIds),
  },
  {
    id: 'claude-gpt',
    label: 'Claude Code (with gpt)',
    models: [],
    thinkingLevels: thinkingLevels('Use model default', claudeThinkingLevelIds),
  },
]
