import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fallbackCodingAgentConfigs } from '@/codingAgents'
import type { Project } from '@/types'
import { renderWithStore } from '@/store/testing'
import { NewThreadScreen } from './NewThreadScreen'

const mocks = vi.hoisted(() => ({
  subscriptions: {} as Record<string, unknown>,
}))

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
