import { describe, expect, it } from 'vitest'
import {
  hydrateThreadWorkspace,
  resolveThreadWorkspace,
  threadCodingAgentChanged,
  threadWorkspaceKey,
  threadWorkspaceMounted,
  threadWorkspaceSlice,
  threadWorkspaceWriters,
  type ThreadWorkspaceRouting,
} from './threadWorkspace'
import type { RootState } from '@/store/rootReducer'

const routing: ThreadWorkspaceRouting = {}

describe('threadWorkspace slice', () => {
  it('resolves defaults and lets routed values override stored preferences', () => {
    expect(resolveThreadWorkspace(undefined, routing)).toEqual({
      codingAgent: 'pi',
      piPresentation: 'native',
      claudePresentation: 'terminal',
    })
    expect(resolveThreadWorkspace(
      { codingAgent: 'codex', piPresentation: 'terminal', claudePresentation: 'terminal' },
      { initialCodingAgent: 'claude', initialPresentation: 'native' },
    )).toEqual({
      codingAgent: 'claude',
      piPresentation: 'terminal',
      claudePresentation: 'native',
    })
  })

  it('hydrates and writes per-thread preferences', () => {
    const key = threadWorkspaceKey('project', 'thread')
    const storage = {
      length: 2,
      key: (index: number) => [
        `kiwi-code:coding-agent:${key}`,
        `kiwi-code:pi-presentation:${key}`,
      ][index] ?? null,
      getItem: (name: string) => name.includes('coding-agent') ? 'codex' : 'terminal',
      setItem: () => {},
      removeItem: () => {},
    }
    const hydrated = hydrateThreadWorkspace(storage)
    expect(hydrated.byThread[key]).toMatchObject({ codingAgent: 'codex', piPresentation: 'terminal' })

    let state = threadWorkspaceSlice.reducer(hydrated, threadWorkspaceMounted({ key, routing }))
    state = threadWorkspaceSlice.reducer(state, threadCodingAgentChanged({ key, codingAgent: 'pi' }))
    const root = { threadWorkspace: state } as RootState
    expect(threadWorkspaceWriters(root).map((writer) => writer.key)).toContain(`kiwi-code:coding-agent:${key}`)
  })
})
