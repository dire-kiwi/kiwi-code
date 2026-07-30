import { describe, expect, it, vi } from 'vitest'
import { memoryStorage } from '@/lib/memoryStorage'
import { createAppStore } from './index'
import { newThreadPreferencesRemembered } from '@/store/slices/newThreadPreferences'
import { preferencesPersistence } from '@/store/slices/preferences'
import { sidebarPersistence } from '@/store/slices/sidebar'
import {
  threadPiPresentationChanged,
  threadWorkspaceKey,
  threadWorkspaceMounted,
} from '@/store/slices/threadWorkspace'

// Every installed browser holds real values under these keys, and the
// end-to-end suite seeds `kiwi-code-active-profile` directly. Renaming a key or
// changing an encoding silently resets that preference for every existing user,
// so both are pinned here on purpose: this test is meant to fail loudly rather
// than let a rename through.
describe('storage contract', () => {
  it('persists exactly the historical key names', () => {
    const keys = [
      ...Object.values(preferencesPersistence),
      ...Object.values(sidebarPersistence),
    ].map((entry) => entry?.key)

    expect(new Set(keys)).toEqual(new Set([
      'kiwi-code-active-profile',
      'kiwi-code.sidebar.view',
      'kiwi-code.sidebar.width',
      'kiwi-code.sidebar.collapsed-projects',
      'kiwi-code.sidebar.collapsed-child-threads',
      'kiwi-code.sidebar.web-servers-collapsed',
    ]))
  })

  it('persists exactly the historical dynamic key prefixes', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })
    const threadKey = threadWorkspaceKey('project-1', 'thread-1')

    store.dispatch(threadWorkspaceMounted({
      key: threadKey,
      routing: { readOnlySubagent: false, initialCodingAgent: 'claude', initialPresentation: 'native' },
    }))
    store.dispatch(threadPiPresentationChanged({ key: threadKey, presentation: 'terminal' }))
    store.dispatch(newThreadPreferencesRemembered({
      projectId: 'project-1',
      preferences: {
        location: 'worktree',
        baseBranch: 'main',
        codingAgent: 'pi-native',
        agentModels: {},
      },
    }))
    await vi.advanceTimersByTimeAsync(200)

    expect(new Set(storage.writes)).toEqual(new Set([
      'kiwi-code:coding-agent:project-1:thread-1',
      'kiwi-code:pi-presentation:project-1:thread-1',
      'kiwi-code:claude-presentation:project-1:thread-1',
      'kiwi-code:new-thread-preferences:project-1',
    ]))
    vi.useRealTimers()
  })

  it('round-trips every stored encoding unchanged', () => {
    const stored = {
      'kiwi-code-active-profile': 'work',
      'kiwi-code.sidebar.view': 'tree',
      'kiwi-code.sidebar.width': '320',
      'kiwi-code.sidebar.collapsed-projects': '["a","b"]',
      'kiwi-code.sidebar.collapsed-child-threads': '["thread-1"]',
      'kiwi-code.sidebar.web-servers-collapsed': 'true',
    }
    const storage = memoryStorage(stored)
    const state = createAppStore({ storage, persist: false }).getState()

    const reencoded = Object.fromEntries(
      [
        ...Object.entries(preferencesPersistence).map(([field, entry]) =>
          [entry!.key, entry!.codec.encode((state.preferences as never)[field])] as const),
        ...Object.entries(sidebarPersistence).map(([field, entry]) =>
          [entry!.key, entry!.codec.encode((state.sidebar as never)[field])] as const),
      ],
    )

    expect(reencoded).toEqual(stored)
  })
})
