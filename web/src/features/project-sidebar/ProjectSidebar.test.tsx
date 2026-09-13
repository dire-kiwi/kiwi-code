import { cleanup, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { createTestStore, renderWithStore } from '@/store/testing'
import { projectsReceived } from '@/store/slices/projects'
import type { Project } from '@/types'
import { ProjectSidebar } from './ProjectSidebar'

vi.mock('@/wire/serverData', () => ({ useThreadUsage: () => [] }))
afterEach(cleanup)

function project(id: string, profileId = 'personal'): Project {
  return {
    id, name: id, profileId, path: `/tmp/${id}`, host: 'fixture', isGitRepo: true,
    createdAt: new Date().toISOString(), worktreeBranchPrefix: '', relatedProjects: [],
    environment: { name: 'Local', setupScripts: { default: '', macos: '', linux: '', windows: '' }, cleanupScripts: { default: '', macos: '', linux: '', windows: '' }, variables: [], actions: [] },
    figmaMCPEnabled: false,
    threads: [{ id: `${id}-thread`, title: `${id} search task`, cwd: `/tmp/${id}`, createdAt: new Date().toISOString() }],
  }
}

function Location() {
  return <output data-testid="location">{useLocation().pathname}</output>
}

function renderSidebar() {
  const store = createTestStore()
  store.dispatch(projectsReceived([project('Alpha'), project('Beta'), project('Private', 'work')]))
  const onSelectThread = vi.fn()
  renderWithStore(
    <MemoryRouter>
      <ProjectSidebar onSelectProfile={() => {}} onProfileCreated={() => {}} onProjectCreated={() => {}}
        onSelectThread={onSelectThread} onDeleteProject={() => {}} onArchiveThread={() => {}} onDeleteThread={() => {}} />
      <Location />
    </MemoryRouter>, { store },
  )
  return { store, onSelectThread }
}

describe('sidebar search and project scope', () => {
  it('searches the active profile, opens a result with Enter, and clears an empty search', () => {
    const { onSelectThread } = renderSidebar()
    const input = screen.getByRole('searchbox', { name: 'Search threads' })
    fireEvent.change(input, { target: { value: 'search task' } })
    const results = screen.getByRole('region', { name: 'Search results' })
    expect(within(results).getByText('Alpha search task')).toBeTruthy()
    expect(within(results).getByText('Beta search task')).toBeTruthy()
    expect(within(results).queryByText('Private search task')).toBeNull()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onSelectThread).toHaveBeenCalledWith('Alpha', 'Alpha-thread')
    fireEvent.change(input, { target: { value: 'unmatched' } })
    expect(screen.getByText('No threads found')).toBeTruthy()
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(screen.queryByRole('region', { name: 'Search results' })).toBeNull()
  })

  it('scopes the list and new thread action to the chosen project', () => {
    renderSidebar()
    fireEvent.click(screen.getByRole('combobox', { name: 'Filter threads by project' }))
    fireEvent.click(screen.getByRole('option', { name: 'Beta' }))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search threads' }), { target: { value: 'search task' } })
    const results = screen.getByRole('region', { name: 'Search results' })
    expect(within(results).queryByText('Alpha search task')).toBeNull()
    expect(within(results).getByText('Beta search task')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'New thread' }))
    expect(screen.getByTestId('location').textContent).toBe('/projects/Beta/threads/new')
  })
})
