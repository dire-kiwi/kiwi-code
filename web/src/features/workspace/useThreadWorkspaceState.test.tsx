import { act, renderHook, waitFor } from '@testing-library/react'
import { Provider } from 'react-redux'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppStore } from '@/store'
import { updateThreadWorkspace } from '@/api'
import type { Thread } from '@/types'
import { resolveServerThreadWorkspace, useThreadWorkspaceState } from './useThreadWorkspaceState'

vi.mock('@/api', async (original) => ({
  ...await original<typeof import('@/api')>(),
  updateThreadWorkspace: vi.fn(),
}))

const thread: Thread = { id: 'thread', title: 'Thread', cwd: '/tmp', createdAt: '' }
function setup(saved: Thread = thread) {
  const store = createAppStore({ storage: null, persist: false })
  return renderHook(({ current }) => useThreadWorkspaceState('project', current, {}, 'pi'), {
    initialProps: { current: saved },
    wrapper: ({ children }) => <Provider store={store}>{children}</Provider>,
  })
}

beforeEach(() => { vi.mocked(updateThreadWorkspace).mockReset().mockResolvedValue(thread) })

describe('server thread workspace', () => {
  it('uses the server agent and presentation before mounting a pane', () => {
    expect(resolveServerThreadWorkspace(
      { codingAgent: 'codex', piPresentation: 'terminal' },
      { initialCodingAgent: 'claude' }, 'pi-native',
    )).toMatchObject({ codingAgent: 'pi', piPresentation: 'native' })
    expect(resolveServerThreadWorkspace(undefined, {}, 'claude-native'))
      .toMatchObject({ codingAgent: 'claude', claudePresentation: 'native' })
    expect(resolveServerThreadWorkspace(undefined, {}, 'claude-profile-personal'))
      .toMatchObject({ codingAgent: 'claude-profile-personal' })
  })

  it('initializes legacy preferences without overwriting saved state', async () => {
    const view = setup()
    await waitFor(() => expect(updateThreadWorkspace).toHaveBeenCalledWith('project', 'thread', {
      codingAgent: 'pi-native', activeTab: 'pi', initialize: true,
    }))
    view.rerender({ current: { ...thread, codingAgent: 'codex', activeTab: 'terminal' } })
    expect(view.result.current.codingAgent).toBe('codex')
    expect(view.result.current.activeTool).toBe('terminal')
    expect(updateThreadWorkspace).toHaveBeenCalledTimes(1)
  })

  it('follows remote agent and tab updates without echoing a write', () => {
    const view = setup({ ...thread, codingAgent: 'codex', activeTab: 'terminal' })
    view.rerender({ current: { ...thread, codingAgent: 'pi-native', activeTab: 'process' } })
    expect(view.result.current).toMatchObject({ codingAgent: 'pi', piPresentation: 'native', activeTool: 'process' })
    expect(updateThreadWorkspace).not.toHaveBeenCalled()
  })

  it('serializes local changes and waits for the authoritative socket snapshot', async () => {
    let complete!: (value: Thread) => void
    vi.mocked(updateThreadWorkspace).mockImplementationOnce(() => new Promise((resolve) => { complete = resolve }))
    const view = setup({ ...thread, codingAgent: 'codex', activeTab: 'pi' })
    act(() => {
      view.result.current.saveWorkspace({ activeTab: 'terminal' })
      view.result.current.saveWorkspace({ activeTab: 'process' })
    })
    await waitFor(() => expect(updateThreadWorkspace).toHaveBeenCalledTimes(1))
    expect(view.result.current.activeTool).toBe('pi')
    await act(async () => { complete({ ...thread, activeTab: 'terminal' }) })
    await waitFor(() => expect(updateThreadWorkspace).toHaveBeenCalledTimes(2))
    expect(updateThreadWorkspace).toHaveBeenLastCalledWith('project', 'thread', { activeTab: 'process' })
    expect(view.result.current.activeTool).toBe('pi')
  })

  it('shows save failures without changing the selected tab', async () => {
    vi.mocked(updateThreadWorkspace).mockRejectedValueOnce(new Error('Offline'))
    const view = setup({ ...thread, codingAgent: 'codex', activeTab: 'pi' })
    act(() => view.result.current.saveWorkspace({ activeTab: 'terminal' }))
    await waitFor(() => expect(view.result.current.workspaceError).toBe('Offline'))
    expect(view.result.current.activeTool).toBe('pi')
  })
})
