import { describe, expect, it } from 'vitest'
import {
  codingAgentsForProfile,
  effectiveWorktreeBranchPrefix,
  codingAgentSelectionForTarget,
  codingAgentTargetForSelection,
  isNativeCodingAgentSelection,
  nativeCodingAgentLabel,
  piModelReasoningLevels,
  piThinkingLevelIds,
  supportedThinkingLevelIds,
  thinkingChoicesForModel,
} from './codingAgents'

describe('native coding-agent selection mappings', () => {
  it.each([
    ['pi-native', 'pi', 'native'],
    ['claude-native', 'claude', 'native'],
    ['codex-native', 'codex', 'native'],
    ['pi', 'pi', 'terminal'],
    ['claude', 'claude', 'terminal'],
    ['grok', 'grok', 'terminal'],
    ['claude-profile-work', 'claude-profile-work', 'terminal'],
  ] as const)('maps %s to its runtime target', (selection, agent, presentation) => {
    expect(codingAgentTargetForSelection(selection)).toEqual({ agent, presentation })
  })

  it('maps native-capable runtime targets back to selections', () => {
    expect(codingAgentSelectionForTarget('pi', 'native')).toBe('pi-native')
    expect(codingAgentSelectionForTarget('claude', 'native')).toBe('claude-native')
    expect(codingAgentSelectionForTarget('claude-profile-work', 'native')).toBe('claude-profile-work')
  })

  it('keeps native guards and labels on the same mapping', () => {
    expect(isNativeCodingAgentSelection('pi-native')).toBe(true)
    expect(isNativeCodingAgentSelection('claude')).toBe(false)
    expect(nativeCodingAgentLabel('claude-native')).toBe('Claude Native')
    expect(nativeCodingAgentLabel('claude')).toBeNull()
  })
})

describe('model thinking-level capabilities', () => {
  it('keeps every known level when a model has not declared a subset', () => {
    expect(supportedThinkingLevelIds(undefined)).toEqual([...piThinkingLevelIds])
    expect(supportedThinkingLevelIds([])).toEqual([...piThinkingLevelIds])
  })

  it('hides levels the model cannot run', () => {
    expect(supportedThinkingLevelIds(['low', 'high', 'unknown'])).toEqual(['low', 'high'])
  })

  it('keeps the empty default choice while filtering concrete levels', () => {
    const choices = thinkingChoicesForModel(
      ['low', 'medium'],
      [
        { id: '', label: 'Use Pi default' },
        { id: 'off', label: 'Off' },
        { id: 'low', label: 'Low' },
        { id: 'medium', label: 'Medium' },
        { id: 'high', label: 'High' },
      ],
    )
    expect(choices.map((choice) => choice.id)).toEqual(['', 'low', 'medium'])
  })

  it('mirrors Pi RPC reasoning maps, including models that cannot think', () => {
    expect(piModelReasoningLevels(false)).toEqual(['off'])
    expect(piModelReasoningLevels(true, {
      off: null,
      minimal: null,
      xhigh: 'xhigh',
    })).toEqual(['low', 'medium', 'high', 'xhigh'])
    expect(piModelReasoningLevels(true, { max: 'max' })).toEqual([
      'off',
      'minimal',
      'low',
      'medium',
      'high',
      'max',
    ])
  })
})

describe('workspace (profile) settings', () => {
  const agents = [
    { id: 'pi-native', kind: 'pi-native', name: 'Pi Native', isDefault: true },
    { id: 'personal', kind: 'claude', name: 'CC Personal', isDefault: false },
    { id: 'work', kind: 'claude', name: 'CC Work', isDefault: false },
  ] as const satisfies Parameters<typeof codingAgentsForProfile>[0]

  it('hides disabled agents and falls back to every agent when none would remain', () => {
    expect(codingAgentsForProfile([...agents], undefined).map((agent) => agent.id)).toEqual(['pi-native', 'personal', 'work'])
    expect(codingAgentsForProfile([...agents], { disabledCodingAgents: ['work'] }).map((agent) => agent.id))
      .toEqual(['pi-native', 'personal'])
    expect(codingAgentsForProfile([...agents], { disabledCodingAgents: ['pi-native', 'personal', 'work'] }))
      .toHaveLength(3)
  })

  it('resolves the branch prefix from project, then workspace, then the built-in default', () => {
    expect(effectiveWorktreeBranchPrefix({ worktreeBranchPrefix: 'team/' }, { worktreeBranchPrefix: 'ivan/' })).toBe('team/')
    expect(effectiveWorktreeBranchPrefix({ worktreeBranchPrefix: '' }, { worktreeBranchPrefix: 'ivan/' })).toBe('ivan/')
    expect(effectiveWorktreeBranchPrefix({ worktreeBranchPrefix: '' }, undefined)).toBe('kiwi-code/')
  })
})
