import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ThreadActionsMenu } from './ThreadActionsMenu'

afterEach(cleanup)
it('disables settlement while working and removes archive actions', () => {
  const settle = vi.fn()
  render(<ThreadActionsMenu threadTitle="Task" working open disabled={false} deleting={false}
    onOpenChange={() => {}} onDelete={() => {}} onSettle={settle} />)
  const action = screen.getByRole('menuitem', { name: 'Settle thread' }) as HTMLButtonElement
  expect(action.disabled).toBe(true)
  fireEvent.click(action)
  expect(settle).not.toHaveBeenCalled()
  expect(screen.queryByText('Archive thread')).toBeNull()
})
it('offers an un-settle action for a retained thread', () => {
  const settle = vi.fn()
  render(<ThreadActionsMenu threadTitle="Task" settled open disabled={false} deleting={false}
    onOpenChange={() => {}} onDelete={() => {}} onSettle={settle} />)
  fireEvent.click(screen.getByRole('menuitem', { name: 'Un-settle thread' }))
  expect(settle).toHaveBeenCalledOnce()
})
it('blocks settlement while pinned and offers to unpin', () => {
  const settle = vi.fn()
  const pin = vi.fn()
  render(<ThreadActionsMenu threadTitle="Task" pinned open disabled={false} deleting={false}
    onOpenChange={() => {}} onDelete={() => {}} onSettle={settle} onPin={pin} />)
  expect((screen.getByRole('menuitem', { name: 'Settle thread' }) as HTMLButtonElement).disabled).toBe(true)
  expect((screen.getByRole('button', { name: 'Settle Task' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('menuitem', { name: 'Unpin thread' }))
  expect(pin).toHaveBeenCalledOnce()
  expect(settle).not.toHaveBeenCalled()
})
it('does not offer pinning for a settled thread', () => {
  render(<ThreadActionsMenu threadTitle="Task" settled open disabled={false} deleting={false}
    onOpenChange={() => {}} onDelete={() => {}} onSettle={() => {}} onPin={() => {}} />)
  expect(screen.queryByRole('menuitem', { name: 'Pin thread' })).toBeNull()
})
