import { describe, expect, it, vi } from 'vitest'
import { memoryStorage } from '../../lib/memoryStorage'
import { createAppStore } from '../index'
import {
  hydrateNewThreadPreferences,
  newThreadPreferencesCodec,
  newThreadPreferencesRemembered,
  newThreadPreferencesStorageKey,
  selectNewThreadPreferences,
} from './newThreadPreferences'

const projectId = 'project-1'
const storageKey = newThreadPreferencesStorageKey(projectId)
const afterDebounce = 200

describe('new thread preferences', () => {
  it('uses the historical storage key', () => {
    expect(storageKey).toBe('kiwi-code:new-thread-preferences:project-1')
  })

  it('migrates preferences saved before models were remembered per agent', () => {
    // Pinned against a literal payload written by the pre-per-agent-model build.
    // If this stops migrating, those users silently lose their model choice.
    const legacy = JSON.stringify({
      location: 'worktree',
      baseBranch: 'main',
      codingAgent: 'pi-native',
      model: 'sonnet',
      thinkingLevel: 'high',
    })

    expect(newThreadPreferencesCodec.decode(legacy)).toEqual({
      location: 'worktree',
      baseBranch: 'main',
      codingAgent: 'pi-native',
      agentModels: { pi: { model: 'sonnet', thinkingLevel: 'high' } },
    })
  })

  it('keeps a per-agent entry in preference to the legacy flat one', () => {
    const mixed = JSON.stringify({
      location: 'project',
      baseBranch: '',
      codingAgent: 'pi-native',
      model: 'legacy-model',
      thinkingLevel: 'low',
      agentModels: { pi: { model: 'current-model', thinkingLevel: 'high' } },
    })

    expect(newThreadPreferencesCodec.decode(mixed)?.agentModels).toEqual({
      pi: { model: 'current-model', thinkingLevel: 'high' },
    })
  })

  it('rejects payloads that fail validation', () => {
    expect(newThreadPreferencesCodec.decode('{')).toBeUndefined()
    expect(newThreadPreferencesCodec.decode('null')).toBeUndefined()
    expect(newThreadPreferencesCodec.decode(JSON.stringify({
      location: 'elsewhere',
      baseBranch: 'main',
      codingAgent: 'pi-native',
    }))).toBeUndefined()
  })

  it('hydrates per project by scanning storage', () => {
    const state = hydrateNewThreadPreferences(memoryStorage({
      [storageKey]: JSON.stringify({
        location: 'worktree',
        baseBranch: 'main',
        codingAgent: 'pi-native',
        agentModels: {},
      }),
      'kiwi-code.sidebar.view': 'tree',
    }))

    expect(Object.keys(state.byProject)).toEqual([projectId])
    expect(state.byProject[projectId].baseBranch).toBe('main')
  })

  it('writes remembered preferences back under the same key', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })
    const preferences = {
      location: 'worktree' as const,
      baseBranch: 'main',
      codingAgent: 'pi-native' as const,
      agentModels: { pi: { model: 'sonnet', thinkingLevel: 'high' } },
    }

    store.dispatch(newThreadPreferencesRemembered({ projectId, preferences }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(JSON.parse(storage.values.get(storageKey)!)).toEqual(preferences)
    expect(selectNewThreadPreferences(store.getState(), projectId)).toEqual(preferences)
    expect(selectNewThreadPreferences(store.getState(), 'other')).toBeNull()
    vi.useRealTimers()
  })
})
