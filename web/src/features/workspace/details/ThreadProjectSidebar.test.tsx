import { fireEvent, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setThreadTitleLocked, updateThreadTitle } from '@/api'
import { renderWithStore } from '@/store/testing'
import type { Project, Thread } from '@/types'
import { ThreadProjectSidebar } from './ThreadProjectSidebar'

vi.mock('@/api', () => ({
  setThreadTitleLocked: vi.fn(),
  updateThreadTitle: vi.fn(),
}))

vi.mock('./ThreadUsageLimits', () => ({ ThreadUsageLimits: () => null }))
vi.mock('./ThreadRecordingsPanel', () => ({ ThreadRecordingsPanel: () => null }))

const thread: Thread = {
  id: 'thread-1',
  title: 'Keep this title',
  cwd: '/workspace/project',
  createdAt: '2026-07-28T00:00:00Z',
}

const project = {
  id: 'project-1',
  name: 'Project',
  threads: [thread],
} as Project

function sidebar(currentThread: Thread, onThreadUpdated = vi.fn()) {
  return (
    <MemoryRouter>
      <ThreadProjectSidebar
        project={{ ...project, threads: [currentThread] }}
        thread={currentThread}
        expanded
        onExpandedChange={() => {}}
        onThreadUpdated={onThreadUpdated}
      />
    </MemoryRouter>
  )
}

describe('ThreadProjectSidebar title lock', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('locks and unlocks the title from the thread details panel', async () => {
    const onThreadUpdated = vi.fn()
    const locked = { ...thread, titleLocked: true }
    vi.mocked(setThreadTitleLocked).mockResolvedValueOnce(locked)

    const view = renderWithStore(sidebar(thread, onThreadUpdated))
    fireEvent.click(screen.getByRole('button', { name: 'Lock thread title' }))

    await waitFor(() => {
      expect(setThreadTitleLocked).toHaveBeenCalledWith('project-1', 'thread-1', true)
      expect(onThreadUpdated).toHaveBeenCalledWith(locked)
    })

    view.rerender(sidebar(locked, onThreadUpdated))
    expect(screen.getByText(/agents cannot override it/i)).toBeTruthy()
    expect((screen.getByRole('button', { name: /thread name locked/i }) as HTMLButtonElement).disabled).toBe(true)

    vi.mocked(setThreadTitleLocked).mockResolvedValueOnce(thread)
    fireEvent.click(screen.getByRole('button', { name: 'Unlock thread title' }))
    await waitFor(() => {
      expect(setThreadTitleLocked).toHaveBeenLastCalledWith('project-1', 'thread-1', false)
    })
    expect(updateThreadTitle).not.toHaveBeenCalled()
  })
})
