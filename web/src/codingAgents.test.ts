import { describe, expect, it } from 'vitest'
import {
  codingAgentSelectionForTarget,
  codingAgentTargetForSelection,
  isNativeCodingAgentSelection,
  nativeCodingAgentLabel,
} from './codingAgents'

describe('native coding-agent selection mappings', () => {
  it.each([
    ['pi-native', 'pi', 'native'],
    ['claude-native', 'claude', 'native'],
    ['pi', 'pi', 'terminal'],
    ['claude', 'claude', 'terminal'],
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
