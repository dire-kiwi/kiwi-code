import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  projectsSlice,
  projectsReceived,
  projectsReordered,
  sameProjects,
  selectArchivingThreadId,
  selectDeletingProjectId,
  selectProjects,
  threadArchived,
  threadsReordered,
  type ProjectsState,
} from './projects'
import { createTestStore } from '@/store/testing'
import type { RootState } from '@/store/rootReducer'
import type { Project, Thread } from '@/types'

vi.mock('@/api', () => ({
  deleteProject: vi.fn(),
  deleteThread: vi.fn(),
  setThreadArchived: vi.fn(),
  setThreadBookmarked: vi.fn(),
  updateProjectOrder: vi.fn(),
  updateThreadOrder: vi.fn(),
}))

// The socket retry is a no-op without a live client, which is what we want here.
vi.mock('@/store/socketAccess', () => ({ retryTopic: vi.fn() }))

const { updateProjectOrder, updateThreadOrder, setThreadArchived } = await import('@/api')

const reduce = projectsSlice.reducer

function thread(id: string, extra: Partial<Thread> = {}): Thread {
  return { id, title: id, cwd: '/w', createdAt: '2026-07-27T00:00:00Z', ...extra } as Thread
}

function project(id: string, threads: Thread[], profileId = 'personal'): Project {
  return {
    id,
    name: id,
    path: `/w/${id}`,
    profileId,
    host: 'fixture',
    isGitRepo: true,
    createdAt: '2026-07-27T00:00:00Z',
    threads,
    worktreeBranchPrefix: '',
    environment: {
      name: 'Local',
      setupScripts: { default: '', macos: '', linux: '', windows: '' },
      cleanupScripts: { default: '', macos: '', linux: '', windows: '' },
      variables: [],
      actions: [],
    },
    figmaMCPEnabled: false,
  } as Project
}

function projectsRoot(projects: ProjectsState) {
  return { projects } as RootState
}

beforeEach(() => {
  vi.mocked(updateProjectOrder).mockReset()
  vi.mocked(updateThreadOrder).mockReset()
  vi.mocked(setThreadArchived).mockReset()
})

describe('projects slice', () => {
  it('keeps the same array when a socket push changes nothing', () => {
    // This is the guard that stops every push re-rendering the sidebar tree.
    const first = reduce(undefined, projectsReceived([project('a', [thread('t1')])]))
    const again = reduce(first, projectsReceived([project('a', [thread('t1')])]))

    expect(selectProjects(projectsRoot(again))).toBe(selectProjects(projectsRoot(first)))
    expect(sameProjects(first.projects, again.projects)).toBe(true)
  })

  it('replaces the array when a thread field actually changes', () => {
    const first = reduce(undefined, projectsReceived([project('a', [thread('t1')])]))
    const renamed = reduce(first, projectsReceived([
      project('a', [thread('t1', { title: 'renamed' })]),
    ]))

    expect(selectProjects(projectsRoot(renamed))).not.toBe(selectProjects(projectsRoot(first)))
    expect(renamed.projects[0]!.threads[0]!.title).toBe('renamed')
  })

  it('hides threads the server is still rolling back', () => {
    const state = reduce(undefined, projectsReceived([
      project('a', [thread('kept'), thread('rolling', { rollbackPending: true } as Partial<Thread>)]),
    ]))
    expect(state.projects[0]!.threads.map((item) => item.id)).toEqual(['kept'])
  })
})

describe('optimistic mutations', () => {
  it('rolls the project order back when the server refuses', async () => {
    const store = createTestStore()
    store.dispatch(projectsReceived([
      project('a', [thread('t')]),
      project('b', [thread('t')]),
      project('c', [thread('t')]),
    ]))
    vi.mocked(updateProjectOrder).mockRejectedValueOnce(new Error('nope'))

    const result = await store.dispatch(projectsReordered({
      profileId: 'personal',
      projectIds: ['c', 'a', 'b'],
    }))

    expect(projectsReordered.rejected.match(result)).toBe(true)
    expect(result.payload).toBe('nope')
    expect(selectProjects(store.getState()).map((item) => item.id)).toEqual(['a', 'b', 'c'])
  })

  it('keeps the new project order when the server accepts', async () => {
    const store = createTestStore()
    store.dispatch(projectsReceived([
      project('a', [thread('t')]),
      project('b', [thread('t')]),
      project('c', [thread('t')]),
    ]))
    vi.mocked(updateProjectOrder).mockResolvedValueOnce(undefined as never)

    await store.dispatch(projectsReordered({ profileId: 'personal', projectIds: ['c', 'a', 'b'] }))

    expect(selectProjects(store.getState()).map((item) => item.id)).toEqual(['c', 'a', 'b'])
  })

  it('rolls the thread order back when the server refuses', async () => {
    const store = createTestStore()
    store.dispatch(projectsReceived([project('a', [thread('t1'), thread('t2'), thread('t3')])]))
    vi.mocked(updateThreadOrder).mockRejectedValueOnce(new Error('denied'))

    await store.dispatch(threadsReordered({ projectId: 'a', threadIds: ['t3', 't1', 't2'] }))

    expect(selectProjects(store.getState())[0]!.threads.map((item) => item.id))
      .toEqual(['t1', 't2', 't3'])
  })

  it('drives the in-flight flag from the thunk lifecycle and clears it on failure', async () => {
    const store = createTestStore()
    store.dispatch(projectsReceived([project('a', [thread('t1')])]))

    let release: (value: Thread) => void = () => {}
    vi.mocked(setThreadArchived).mockReturnValueOnce(
      new Promise<Thread>((resolve) => { release = resolve }) as never,
    )

    const pending = store.dispatch(threadArchived({
      projectId: 'a',
      threadId: 't1',
      archived: true,
    }))
    // The sidebar's spinner is this flag; it must be set while the call is out.
    expect(selectArchivingThreadId(store.getState())).toBe('t1')

    release(thread('t1', { archivedAt: '2026-07-27T01:00:00Z' }))
    await pending

    expect(selectArchivingThreadId(store.getState())).toBeNull()
    expect(selectProjects(store.getState())[0]!.threads[0]!.archivedAt).toBe('2026-07-27T01:00:00Z')
  })

  it('clears the deleting flag even when the request fails', async () => {
    const { deleteProject } = await import('@/api')
    const store = createTestStore()
    store.dispatch(projectsReceived([project('a', [thread('t1')])]))
    vi.mocked(deleteProject).mockRejectedValueOnce(new Error('busy'))

    await store.dispatch((await import('./projects')).projectRemoved('a'))

    expect(selectDeletingProjectId(store.getState())).toBeNull()
    // The project stays, because the delete did not happen.
    expect(selectProjects(store.getState()).map((item) => item.id)).toEqual(['a'])
  })
})
