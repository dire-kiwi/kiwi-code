import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { ShortcutsSection } from './ShortcutsSection'

afterEach(cleanup)
it('lists shortcuts by group with Windows and Linux key names', () => {
  render(<ShortcutsSection mac={false} />)
  expect(screen.getByText('Windows & Linux')).toBeTruthy()
  const terminal = screen.getByRole('region', { name: 'Terminal' })
  expect(within(terminal).getByText('Ctrl+C')).toBeTruthy()
  expect(within(terminal).getByText('Shift+Insert')).toBeTruthy()
  const threads = screen.getByRole('region', { name: 'Threads' })
  expect(within(threads).getByText('Ctrl+N')).toBeTruthy()
})
it('shows Mac glyphs and omits keys Macs lack', () => {
  render(<ShortcutsSection mac />)
  expect(screen.getByText('macOS')).toBeTruthy()
  const workspace = screen.getByRole('region', { name: 'Workspace tools' })
  expect(within(workspace).getByText('⌘6')).toBeTruthy()
  // New thread is Control+N on every platform, not Cmd+N.
  expect(screen.getByText('⌃N')).toBeTruthy()
  expect(screen.queryByText('⇧Insert')).toBeNull()
})
