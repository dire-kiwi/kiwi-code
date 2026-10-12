import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { createProfile } from '@/api'
import { createTestStore, renderWithStore } from '@/store/testing'
import { activitiesReceived } from '@/store/slices/agentActivity'
import { profilesReceived } from '@/store/slices/profiles'
import { projectsReceived } from '@/store/slices/projects'
import { sidebarViewChanged } from '@/store/slices/sidebar'
import type { Project } from '@/types'
import { ProjectSidebar } from './ProjectSidebar'
import { profileInitials } from './profileInitials'

vi.mock('@/wire/serverData', () => ({ useThreadUsage: () => [] }))
vi.mock('@/api', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api')>(), createProfile: vi.fn() }))
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value: function (this: HTMLDialogElement) { this.open = false } })
})
afterEach(() => {
  cleanup()
  vi.mocked(createProfile).mockReset()
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'close')
})

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

function renderSidebar(initialPath = '/') {
  const store = createTestStore()
  store.dispatch(profilesReceived([{ id: 'personal', name: 'Personal' }, { id: 'work', name: 'Client work' }]))
  store.dispatch(projectsReceived([project('Alpha'), project('Beta'), project('Private', 'work')]))
  const onSelectThread = vi.fn()
  const onSelectProfile = vi.fn()
  const onProfileCreated = vi.fn()
  renderWithStore(
    <MemoryRouter initialEntries={[initialPath]}>
      <ProjectSidebar onSelectProfile={onSelectProfile} onProfileCreated={onProfileCreated} onProjectCreated={() => {}}
        onSelectThread={onSelectThread} onDeleteProject={() => {}} onDeleteThread={() => {}} />
      <Location />
    </MemoryRouter>, { store },
  )
  return { store, onSelectThread, onSelectProfile, onProfileCreated }
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

describe('profile rail', () => {
  it('lists every profile down the side, marks the current one, and switches on click', () => {
    const { onSelectProfile } = renderSidebar()
    const rail = screen.getByRole('navigation', { name: 'Profiles' })
    const personal = within(rail).getByRole('button', { name: 'Personal' })
    const work = within(rail).getByRole('button', { name: 'Client work' })
    expect(personal.getAttribute('aria-current')).toBe('true')
    expect(work.getAttribute('aria-current')).toBeNull()
    expect(work.textContent).toBe('CW')
    expect(screen.getByRole('heading', { name: 'Personal' })).toBeTruthy()
    fireEvent.click(personal)
    expect(onSelectProfile).not.toHaveBeenCalled()
    fireEvent.click(work)
    expect(onSelectProfile).toHaveBeenCalledWith('work')
  })

  it('switches profiles from the keyboard, even from inside a terminal', () => {
    const { onSelectProfile } = renderSidebar()
    const terminalInput = document.createElement('textarea')
    const terminalKeyDown = vi.fn()
    terminalInput.addEventListener('keydown', terminalKeyDown)
    document.body.append(terminalInput)
    fireEvent.keyDown(terminalInput, { ctrlKey: true, altKey: true, key: '2', code: 'Digit2' })
    expect(onSelectProfile).toHaveBeenLastCalledWith('work')
    expect(terminalKeyDown).not.toHaveBeenCalled()
    fireEvent.keyDown(terminalInput, { ctrlKey: true, altKey: true, key: 'ArrowUp', code: 'ArrowUp' })
    expect(onSelectProfile).toHaveBeenLastCalledWith('work')
    // The current profile is a no-op rather than a reload of the workspace.
    fireEvent.keyDown(terminalInput, { ctrlKey: true, altKey: true, key: '1', code: 'Digit1' })
    expect(onSelectProfile).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: 'Client work' }).getAttribute('aria-keyshortcuts')).toMatch(/\+Alt\+2$/)
    terminalInput.remove()
  })

  it('flags agent activity in another profile', () => {
    const { store } = renderSidebar()
    act(() => {
      store.dispatch(activitiesReceived([
        { projectId: 'Private', threadId: 'Private-thread', state: 'finished', updatedAt: new Date().toISOString() },
      ]))
    })
    const rail = screen.getByRole('navigation', { name: 'Profiles' })
    expect(within(rail).getByRole('button', { name: 'Client work, 1 thread needs review' })).toBeTruthy()
  })

  it('creates a profile from the rail without a native prompt', async () => {
    const created = { id: 'side', name: 'Side project' }
    vi.mocked(createProfile).mockResolvedValue(created)
    const prompt = vi.spyOn(window, 'prompt')
    const { onProfileCreated } = renderSidebar()
    fireEvent.click(screen.getByRole('button', { name: 'Create a profile' }))
    const dialog = screen.getByRole('dialog', { name: 'Create a profile' })
    const create = within(dialog).getByRole('button', { name: 'Create profile' }) as HTMLButtonElement
    expect(create.disabled).toBe(true)
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Profile name' }), { target: { value: '  Side project ' } })
    fireEvent.click(create)
    await waitFor(() => expect(onProfileCreated).toHaveBeenCalledWith(created))
    expect(createProfile).toHaveBeenCalledWith('Side project')
    expect(screen.queryByRole('dialog', { name: 'Create a profile' })).toBeNull()
    expect(prompt).not.toHaveBeenCalled()
    prompt.mockRestore()
  })

  it('keeps the dialog open with the server error when creation fails', async () => {
    vi.mocked(createProfile).mockRejectedValue(new Error('a profile with that name already exists'))
    const { onProfileCreated } = renderSidebar()
    fireEvent.click(screen.getByRole('button', { name: 'Create a profile' }))
    const dialog = screen.getByRole('dialog', { name: 'Create a profile' })
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Profile name' }), { target: { value: 'Personal' } })
    fireEvent.submit(within(dialog).getByRole('textbox', { name: 'Profile name' }))
    expect((await within(dialog).findByRole('alert')).textContent).toBe('a profile with that name already exists')
    expect(onProfileCreated).not.toHaveBeenCalled()
  })
})

describe('thread cycling', () => {
  afterEach(() => {
    Reflect.deleteProperty(window, 'kiwiCodeDesktopBrowser')
  })

  it('steps through the listed threads with the browser tab keys, even from a terminal', () => {
    const { store, onSelectThread } = renderSidebar('/projects/Alpha/threads/Alpha-thread/agent')
    act(() => {
      store.dispatch(sidebarViewChanged('tree'))
    })
    const terminalInput = document.createElement('textarea')
    const terminalKeyDown = vi.fn()
    terminalInput.addEventListener('keydown', terminalKeyDown)
    document.body.append(terminalInput)

    fireEvent.keyDown(terminalInput, { ctrlKey: true, key: 'Tab', code: 'Tab' })
    expect(onSelectThread).toHaveBeenLastCalledWith('Beta', 'Beta-thread')
    expect(terminalKeyDown).not.toHaveBeenCalled()
    // Previous from the first thread wraps to the last.
    fireEvent.keyDown(terminalInput, { ctrlKey: true, key: 'PageUp', code: 'PageUp' })
    expect(onSelectThread).toHaveBeenLastCalledWith('Beta', 'Beta-thread')
    expect(onSelectThread).toHaveBeenCalledTimes(2)

    // A collapsed project's threads drop out, leaving nothing to step to.
    fireEvent.click(screen.getByTitle('Collapse Beta'))
    fireEvent.keyDown(terminalInput, { ctrlKey: true, shiftKey: true, key: 'Tab', code: 'Tab' })
    expect(onSelectThread).toHaveBeenCalledTimes(2)
    terminalInput.remove()
  })

  it('follows shortcuts the desktop shell forwards from the native browser view', () => {
    let forward: ((direction: 1 | -1) => void) | undefined
    const unsubscribe = vi.fn()
    Object.assign(window, {
      kiwiCodeDesktopBrowser: {
        onThreadShortcut(callback: (direction: 1 | -1) => void) {
          forward = callback
          return unsubscribe
        },
      },
    })
    const { store, onSelectThread } = renderSidebar('/projects/Beta/threads/Beta-thread/agent')
    act(() => {
      store.dispatch(sidebarViewChanged('tree'))
    })
    act(() => forward?.(-1))
    expect(onSelectThread).toHaveBeenLastCalledWith('Alpha', 'Alpha-thread')
    cleanup()
    expect(unsubscribe).toHaveBeenCalledOnce()
  })
})

describe('profileInitials', () => {
  it('uses the first letter of up to two words', () => {
    expect(profileInitials('Personal')).toBe('P')
    expect(profileInitials('client work stuff')).toBe('CW')
    expect(profileInitials('   ')).toBe('?')
  })
})
