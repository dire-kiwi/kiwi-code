import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Project } from '@/types'
import { NewThreadProjectDialog } from './NewThreadProjectDialog'

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.open = true } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value: function (this: HTMLDialogElement) { this.open = false } })
})
afterEach(() => { cleanup(); Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal'); Reflect.deleteProperty(HTMLDialogElement.prototype, 'close') })

it('filters projects and chooses the keyboard selection with the current project first', () => {
  const onSelect = vi.fn()
  const projects = [
    { id: 'alpha', name: 'Alpha', path: '/tmp/alpha', threads: [] },
    { id: 'beta', name: 'Beta', path: '/tmp/beta', threads: [] },
  ] as unknown as Project[]
  render(<NewThreadProjectDialog projects={projects} preferredProjectId="beta" onSelect={onSelect} onClose={() => {}} />)
  const search = screen.getByRole('combobox', { name: 'Choose a project' })
  fireEvent.keyDown(search, { key: 'Enter' })
  expect(onSelect).toHaveBeenLastCalledWith('beta')
  fireEvent.keyDown(search, { key: 'ArrowDown' })
  fireEvent.keyDown(search, { key: 'Enter' })
  expect(onSelect).toHaveBeenLastCalledWith('alpha')
  fireEvent.change(search, { target: { value: '/tmp/beta' } })
  expect(screen.queryByRole('option', { name: /Alpha/ })).toBeNull()
  fireEvent.keyDown(search, { key: 'Enter' })
  expect(onSelect).toHaveBeenLastCalledWith('beta')
  fireEvent.change(search, { target: { value: 'missing' } })
  expect(screen.getByRole('status').textContent).toBe('No projects found')
})
