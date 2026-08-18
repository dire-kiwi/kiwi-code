import { describe, expect, it } from 'vitest'
import { buildComposerSuggestions, normalizePiModels } from './piCommands'

describe('normalizePiModels', () => {
  it('derives reasoning levels from the Pi RPC map and leaves unknown models unscoped', () => {
    expect(normalizePiModels([
      {
        provider: 'custom',
        id: 'mapped',
        name: 'Mapped model',
        reasoning: true,
        thinkingLevelMap: { off: null, minimal: null, xhigh: 'xhigh' },
      },
      {
        provider: 'custom',
        id: 'plain',
        name: 'Plain model',
        reasoning: false,
      },
      {
        provider: 'custom',
        id: 'legacy',
        name: 'Legacy model',
      },
    ])).toEqual([
      {
        provider: 'custom',
        id: 'mapped',
        name: 'Mapped model',
        reasoning: true,
        thinkingLevelMap: { off: null, minimal: null, xhigh: 'xhigh' },
        reasoningLevels: ['low', 'medium', 'high', 'xhigh'],
      },
      {
        provider: 'custom',
        id: 'plain',
        name: 'Plain model',
        reasoning: false,
        reasoningLevels: ['off'],
      },
      {
        provider: 'custom',
        id: 'legacy',
        name: 'Legacy model',
      },
    ])
  })
})

describe('buildComposerSuggestions thinking completions', () => {
  it('only offers thinking levels the selected model can run', () => {
    expect(buildComposerSuggestions('/thinking ', [], [], ['low', 'xhigh']).map((item) => item.completion)).toEqual([
      '/thinking low',
      '/thinking xhigh',
    ])
  })
})
