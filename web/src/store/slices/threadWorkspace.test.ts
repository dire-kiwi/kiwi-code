import { describe, expect, it, vi } from 'vitest'
import { memoryStorage } from '../../lib/memoryStorage'
import { createAppStore } from '../index'
import {
  hydrateThreadWorkspace,
  resolveThreadWorkspace,
  threadCodingAgentChanged,
  threadPiPresentationChanged,
  threadWorkspaceKey,
  threadWorkspaceMounted,
  threadWorkspaceSlice,
  type ThreadWorkspaceRouting,
} from './threadWorkspace'

const reduce = threadWorkspaceSlice.reducer
const key = threadWorkspaceKey('project-1', 'thread-1')
const afterDebounce = 200

const openThread: ThreadWorkspaceRouting = { readOnlySubagent: false }

function storedThread(fields: Record<string, string>) {
  return memoryStorage(Object.fromEntries(
    Object.entries(fields).map(([prefix, value]) => [`${prefix}${key}`, value]),
  ))
}

describe('resolveThreadWorkspace', () => {
  it('falls back to the same defaults the hook used', () => {
    expect(resolveThreadWorkspace(undefined, openThread)).toEqual({
      persist: true,
      codingAgent: 'pi',
      piPresentation: 'native',
      claudePresentation: 'terminal',
    })
  })

  it('prefers stored values when nothing was routed', () => {
    const stored = {
      persist: true,
      codingAgent: 'claude' as const,
      piPresentation: 'terminal' as const,
      claudePresentation: 'native' as const,
    }
    expect(resolveThreadWorkspace(stored, openThread)).toEqual(stored)
  })

  it('lets a routed agent and presentation beat the stored value', () => {
    const stored = {
      persist: true,
      codingAgent: 'pi' as const,
      piPresentation: 'terminal' as const,
      claudePresentation: 'terminal' as const,
    }
    const resolved = resolveThreadWorkspace(stored, {
      readOnlySubagent: false,
      initialCodingAgent: 'claude',
      initialPresentation: 'native',
    })

    expect(resolved.codingAgent).toBe('claude')
    expect(resolved.claudePresentation).toBe('native')
    // Routing named Claude, so the stored Pi presentation is untouched.
    expect(resolved.piPresentation).toBe('terminal')
  })

  it('routes a Pi presentation only onto Pi', () => {
    const resolved = resolveThreadWorkspace(
      { persist: true, claudePresentation: 'native' },
      { readOnlySubagent: false, initialCodingAgent: 'pi', initialPresentation: 'terminal' },
    )

    expect(resolved.piPresentation).toBe('terminal')
    expect(resolved.claudePresentation).toBe('native')
  })

  it('pins a subagent to Pi native and refuses to persist it', () => {
    const stored = {
      persist: true,
      codingAgent: 'claude' as const,
      piPresentation: 'terminal' as const,
      claudePresentation: 'native' as const,
    }
    const resolved = resolveThreadWorkspace(stored, { readOnlySubagent: true })

    expect(resolved.persist).toBe(false)
    expect(resolved.codingAgent).toBe('pi')
    expect(resolved.piPresentation).toBe('native')
    // Claude's presentation was always loaded even for subagents.
    expect(resolved.claudePresentation).toBe('native')
  })
})

describe('threadWorkspace slice', () => {
  it('keeps entry identity when a repeated mount resolves the same way', () => {
    const mounted = reduce(undefined, threadWorkspaceMounted({ key, routing: openThread }))
    const remounted = reduce(mounted, threadWorkspaceMounted({ key, routing: openThread }))

    // StrictMode invokes the mount effect twice.
    expect(remounted.byThread[key]).toBe(mounted.byThread[key])
  })

  it('records changes against the mounted entry', () => {
    const mounted = reduce(undefined, threadWorkspaceMounted({ key, routing: openThread }))
    const changed = reduce(mounted, threadPiPresentationChanged({ key, presentation: 'terminal' }))

    expect(changed.byThread[key].piPresentation).toBe('terminal')
  })
})

describe('threadWorkspace hydration', () => {
  it('discovers per-thread keys by scanning storage at boot', () => {
    const state = hydrateThreadWorkspace(storedThread({
      'kiwi-code:coding-agent:': 'claude',
      'kiwi-code:pi-presentation:': 'terminal',
      'kiwi-code:claude-presentation:': 'native',
    }))

    expect(state.byThread[key]).toEqual({
      persist: true,
      codingAgent: 'claude',
      piPresentation: 'terminal',
      claudePresentation: 'native',
    })
  })

  it('ignores malformed values and unrelated keys', () => {
    const state = hydrateThreadWorkspace(memoryStorage({
      [`kiwi-code:coding-agent:${key}`]: 'not-an-agent',
      [`kiwi-code:pi-presentation:${key}`]: 'terminal',
      'kiwi-code.sidebar.view': 'tree',
    }))

    expect(state.byThread[key]).toEqual({ persist: true, piPresentation: 'terminal' })
  })

  it('survives storage that cannot be enumerated', () => {
    expect(hydrateThreadWorkspace(null).byThread).toEqual({})
  })
})

describe('threadWorkspace persistence', () => {
  it('writes a change back under the historical per-thread key', async () => {
    vi.useFakeTimers()
    const storage = storedThread({ 'kiwi-code:coding-agent:': 'pi' })
    const store = createAppStore({ storage })

    store.dispatch(threadWorkspaceMounted({ key, routing: openThread }))
    store.dispatch(threadCodingAgentChanged({ key, codingAgent: 'claude' }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.values.get(`kiwi-code:coding-agent:${key}`)).toBe('claude')
    vi.useRealTimers()
  })

  it('writes a routed choice back, as the load:false save:true gate did', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(threadWorkspaceMounted({
      key,
      routing: { readOnlySubagent: false, initialCodingAgent: 'claude', initialPresentation: 'native' },
    }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.values.get(`kiwi-code:coding-agent:${key}`)).toBe('claude')
    expect(storage.values.get(`kiwi-code:claude-presentation:${key}`)).toBe('native')
    vi.useRealTimers()
  })

  it('writes nothing at all for a subagent thread', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(threadWorkspaceMounted({ key, routing: { readOnlySubagent: true } }))
    store.dispatch(threadCodingAgentChanged({ key, codingAgent: 'claude' }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.writes).toEqual([])
    vi.useRealTimers()
  })

  it('does not rewrite a thread whose stored choice is unchanged', async () => {
    vi.useFakeTimers()
    const storage = storedThread({ 'kiwi-code:coding-agent:': 'claude' })
    const store = createAppStore({ storage })

    store.dispatch(threadWorkspaceMounted({ key, routing: openThread }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    expect(storage.writes).toEqual([])
    vi.useRealTimers()
  })

  it('leaves no trace for a thread that never deviates from the defaults', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage()
    const store = createAppStore({ storage })

    store.dispatch(threadWorkspaceMounted({ key, routing: openThread }))
    await vi.advanceTimersByTimeAsync(afterDebounce)

    // Merely visiting a thread must not leave three keys behind forever;
    // an absent key reads back as the same default.
    expect(storage.writes).toEqual([])
    expect(store.getState().threadWorkspace.byThread[key]).toEqual({
      persist: true,
      codingAgent: undefined,
      piPresentation: undefined,
      claudePresentation: undefined,
    })
    vi.useRealTimers()
  })

  it('still resolves defaults for an entry that stores nothing', () => {
    const store = createAppStore({ storage: null, persist: false })
    store.dispatch(threadWorkspaceMounted({ key, routing: openThread }))
    const entry = store.getState().threadWorkspace.byThread[key]

    expect(resolveThreadWorkspace(entry, openThread)).toEqual({
      persist: true,
      codingAgent: 'pi',
      piPresentation: 'native',
      claudePresentation: 'terminal',
    })
  })
})
