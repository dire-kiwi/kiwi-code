import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fallbackCodingAgentConfigs } from '@/codingAgents'
import type { AppSettings, Project } from '@/types'
import { createTestStore, renderWithStore } from '@/store/testing'
import { settingsReceived } from '@/store/slices/settings'
import { NewThreadScreen } from './NewThreadScreen'

const mocks = vi.hoisted(() => ({
  saveSelection: vi.fn().mockResolvedValue({}),
  subscriptions: {} as Record<string, unknown>,
}))

vi.mock('@/api', () => ({ rememberNewThreadSelection: mocks.saveSelection }))

vi.mock('@/wire/react', () => ({
  useSubscription: (topic: { tag: string }) => mocks.subscriptions[topic.tag],
}))

const project: Project = {
  id: 'project',
  name: 'Project',
  path: '/workspace',
  profileId: 'personal',
  host: 'fixture',
  // Reproduce a temporarily stale project snapshot. The Git branch topic is
  // authoritative and should still make worktree creation available.
  isGitRepo: false,
  createdAt: '2026-07-28T00:00:00Z',
  threads: [{
    id: 'existing-worktree',
    title: 'Existing worktree',
    cwd: '/worktrees/existing',
    createdAt: '2026-07-28T00:00:00Z',
    worktree: true,
    branch: 'kiwi-code/existing',
  }],
  worktreeBranchPrefix: 'kiwi-code/',
  relatedProjects: [],
  environment: {
    name: 'Local',
    setupScripts: { default: '', macos: '', linux: '', windows: '' },
    cleanupScripts: { default: '', macos: '', linux: '', windows: '' },
    variables: [],
    actions: [],
  },
  figmaMCPEnabled: false,
}

function ready(data: unknown) {
  return { state: 'ready' as const, data, retry: vi.fn() }
}

afterEach(() => {
  cleanup()
})

beforeEach(() => {
  window.localStorage.clear()
  mocks.subscriptions = {
    codingAgents: ready(fallbackCodingAgentConfigs),
    settings: { state: 'loading', retry: vi.fn() },
    'git.branches': ready({
      isRepository: true,
      current: 'main',
      detached: false,
      branches: [
        { name: 'main', current: true },
        { name: 'kiwi-code/existing', current: false },
        { name: 'release/next', current: false },
      ],
    }),
  }
})

describe('NewThreadScreen worktree controls', () => {
  it('shows a separate start location and searchable authoritative branch list', async () => {
    renderWithStore(
      <NewThreadScreen
        project={project}
        onOpenSidebar={() => {}}
        onCancel={() => {}}
        onCreated={() => {}}
      />,
    )

    const locationPicker = await screen.findByRole('combobox', { name: 'Start in' })
    const branchPicker = screen.getByRole('combobox', { name: 'Base branch' })

    await waitFor(() => {
      expect((locationPicker as HTMLButtonElement).disabled).toBe(false)
      expect(locationPicker.textContent).toContain('New worktree')
      expect((branchPicker as HTMLButtonElement).disabled).toBe(false)
      expect(branchPicker.textContent).toContain('main (current)')
    })

    fireEvent.click(locationPicker)
    const locationList = screen.getByRole('listbox', { name: 'Start in' })
    expect(within(locationList).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Work locally',
      'New worktree',
    ])
    fireEvent.click(within(locationList).getByRole('option', { name: 'Work locally' }))
    expect(locationPicker.textContent).toContain('Work locally')

    fireEvent.click(branchPicker)
    const branchList = screen.getByRole('listbox', { name: 'Base branch' })
    expect(within(branchList).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'main (current)',
      'kiwi-code/existing',
      'release/next',
    ])
    expect(within(branchList).queryByText(/^worktree\b/i)).toBeNull()

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search branches…' }), {
      target: { value: 'release' },
    })
    expect(within(branchList).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'release/next',
    ])
  })
})

describe('NewThreadScreen thinking levels', () => {
    const piThinkingChoices = [
      { id: '', label: 'Use Pi default' },
      { id: 'off', label: 'Off' },
      { id: 'minimal', label: 'Minimal' },
      { id: 'low', label: 'Low' },
      { id: 'medium', label: 'Medium' },
      { id: 'high', label: 'High' },
      { id: 'xhigh', label: 'Extra high' },
      { id: 'max', label: 'Maximum' },
    ]

    function modelPicker() {
      return document.getElementById('thread-agent-model') as HTMLButtonElement
    }

    function thinkingPicker() {
      return document.getElementById('thread-agent-thinking') as HTMLButtonElement
    }

    function thinkingOptions() {
      const picker = thinkingPicker()
      fireEvent.click(picker)
      const options = within(screen.getByRole('listbox', { name: 'Thinking' }))
        .getAllByRole('option')
        .map((option) => option.textContent)
      fireEvent.keyDown(picker, { key: 'Escape' })
      return options
    }

    it('limits thinking choices to the selected model and keeps the default option', async () => {
        mocks.subscriptions.codingAgents = ready([
          {
            id: 'pi',
            label: 'Pi',
            models: [
              { id: '', label: 'Use Pi default' },
              {
                id: 'custom/mapped',
                label: 'Mapped model',
                reasoningLevels: ['low', 'medium', 'high', 'xhigh'],
              },
              {
                id: 'custom/plain',
                label: 'Plain model',
                reasoningLevels: ['off'],
              },
            ],
            thinkingLevels: piThinkingChoices,
          },
        ])

        renderWithStore(
          <NewThreadScreen
            project={project}
            onOpenSidebar={() => {}}
            onCancel={() => {}}
            onCreated={() => {}}
          />,
        )

        await waitFor(() => {
          expect(modelPicker()).toBeTruthy()
          expect(thinkingPicker()).toBeTruthy()
        })
        expect(thinkingOptions()).toEqual([
          'Use Pi default',
          'Off',
          'Minimal',
          'Low',
          'Medium',
          'High',
          'Extra high',
          'Maximum',
        ])

        fireEvent.click(modelPicker())
        fireEvent.click(within(screen.getByRole('listbox', { name: 'Model' })).getByRole('option', {
          name: 'Mapped model',
        }))
        expect(thinkingOptions()).toEqual([
          'Use Pi default',
          'Low',
          'Medium',
          'High',
          'Extra high',
        ])

        fireEvent.click(thinkingPicker())
        fireEvent.click(within(screen.getByRole('listbox', { name: 'Thinking' })).getByRole('option', {
          name: 'High',
        }))
        expect(thinkingPicker().textContent).toContain('High')

        fireEvent.click(modelPicker())
        fireEvent.click(within(screen.getByRole('listbox', { name: 'Model' })).getByRole('option', {
          name: 'Plain model',
        }))
        expect(thinkingPicker().textContent).toContain('Use Pi default')
        expect(thinkingOptions()).toEqual(['Use Pi default', 'Off'])
      })
  })

describe('server thread selection', () => {
  it('restores delayed server settings over the default and saves changes before submit', async () => {
    mocks.subscriptions.codingAgents = ready(fallbackCodingAgentConfigs.map((config) => (
      config.id === 'codex'
        ? { ...config, models: [...config.models, { id: 'saved-model', label: 'Saved model' }] }
        : config
    )))
    const store = createTestStore()
    const settings = {
      codingAgents: [
        { id: 'pi-native', kind: 'pi-native', name: 'Pi', isDefault: true },
        { id: 'codex', kind: 'codex', name: 'Codex', isDefault: false },
      ],
      newThreadSelection: { codingAgent: 'codex', model: 'saved-model', thinkingLevel: 'high' },
    } as unknown as AppSettings
    const props = { project, onOpenSidebar: () => {}, onCancel: () => {}, onCreated: () => {} }
    const rendered = renderWithStore(<NewThreadScreen {...props} />, { store })
    act(() => { store.dispatch(settingsReceived(settings)) })
    const thinking = () => document.getElementById('thread-agent-thinking') as HTMLButtonElement
    await waitFor(() => expect(thinking().textContent).toContain('High'))
    expect(document.getElementById('thread-coding-agent')?.textContent).toContain('Codex')
    expect(document.getElementById('thread-agent-model')?.textContent).toContain('Saved model')
    fireEvent.click(thinking())
    fireEvent.click(within(screen.getByRole('listbox', { name: 'Thinking' })).getByRole('option', { name: /^Low$/ }))
    await waitFor(() => expect(mocks.saveSelection).toHaveBeenLastCalledWith({ codingAgent: 'codex', model: 'saved-model', thinkingLevel: 'low' }))
    rendered.unmount()
    const freshStore = createTestStore()
    freshStore.dispatch(settingsReceived({ ...settings, newThreadSelection: mocks.saveSelection.mock.calls.at(-1)![0] }))
    renderWithStore(<NewThreadScreen {...props} />, { store: freshStore })
    await waitFor(() => expect(thinking().textContent).toContain('Low'))
  })
})
