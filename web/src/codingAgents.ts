import type { CodingAgentChoice, CodingAgentConfig } from './types'

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
