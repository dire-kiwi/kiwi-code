import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  projectsSlice,
  projectsReceived,
  visibleProjectSnapshots,
  projectsReordered,
  sameProjects,
  selectSettlingThreadId,
  selectDeletingProjectId,
  selectProjects,
  threadSettled,
  threadsReordered,
  type ProjectsState,
} from './projects'
import { createTestStore } from '@/store/testing'
import type { RootState } from '@/store/rootReducer'
import type { Project, Thread } from '@/types'
import { ProjectSchema, ThreadSchema } from '@/wire/domain'

vi.mock('@/api', () => ({
  deleteProject: vi.fn(),
  deleteThread: vi.fn(),
  setThreadSettled: vi.fn(),
  updateProjectOrder: vi.fn(),
  updateThreadOrder: vi.fn(),
}))

// The socket retry is a no-op without a live client, which is what we want here.
vi.mock('@/store/socketAccess', () => ({ retryTopic: vi.fn() }))

const { updateProjectOrder, updateThreadOrder, setThreadSettled } = await import('@/api')

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
    relatedProjects: [],
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

// Every field set, so the schema walks below can change each one. Typed as
// Required so a field added to the schema fails to compile here until it has a
// value.
function fullThread(): Required<Thread> {
  return {
    id: 't1',
    title: 'Thread',
    cwd: '/w/a',
    createdAt: '2026-07-27T00:00:00Z',
    environmentSetupStatus: 'pending',
    codingAgent: 'pi',
    activeTab: 'pi',
    lastPromptAt: '2026-07-27T01:00:00Z',
    worktree: true,
    branch: 'kiwi/thread',
    worktreePath: '/w/a-thread',
    autoNamed: true,
    titleLocked: false,
    pinned: false,
    settledAt: '2026-07-27T02:00:00Z',
    lastActivityAt: '2026-07-27T03:00:00Z',
    unsettledAt: '2026-07-27T04:00:00Z',
    tokenLimit: 1000,
    costLimitUsd: 5,
    rollbackPending: false,
    rollbackCleanupReady: false,
  }
}

function fullProject(): Required<Project> {
  return {
    ...project('a', [fullThread(), { ...fullThread(), id: 't2' }]),
    worktreeBranchPrefix: 'kiwi/',
    relatedProjects: ['../related'],
    environment: {
      name: 'Local',
      setupScripts: { default: 'make setup', macos: '', linux: '', windows: '' },
      cleanupScripts: { default: 'make clean', macos: '', linux: '', windows: '' },
      variables: [{ name: 'PORT', value: '3000' }],
      actions: [{ id: 'test', name: 'Test', scripts: { default: 'make test', macos: '', linux: '', windows: '' } }],
    },
  }
}

/** A different value of the same shape, changed as deep down as possible. */
function changedValue(value: unknown): unknown {
  if (typeof value === 'string') return `${value}-changed`
  if (typeof value === 'number') return value + 1
  if (typeof value === 'boolean') return !value
  if (Array.isArray(value)) return [changedValue(value[0]), ...value.slice(1)]
  if (value && typeof value === 'object') {
    const [key, inner] = Object.entries(value).at(-1)!
    return { ...value, [key]: changedValue(inner) }
  }
  throw new Error(`fixture field has no value to change: ${String(value)}`)
}

function projectsRoot(projects: ProjectsState) {
  return { projects } as RootState
}

beforeEach(() => {
  vi.mocked(updateProjectOrder).mockReset()
  vi.mocked(updateThreadOrder).mockReset()
  vi.mocked(setThreadSettled).mockReset()
})

describe('projects slice', () => {
  it('retains server agent and tab changes from socket snapshots', () => {
    const first = reduce(undefined, projectsReceived([project('a', [thread('t1')])]))
    const updated = reduce(first, projectsReceived([
      project('a', [thread('t1', { codingAgent: 'codex', activeTab: 'terminal' })]),
    ]))
    expect(updated.projects).not.toBe(first.projects)
    expect(updated.projects[0].threads[0]).toMatchObject({ codingAgent: 'codex', activeTab: 'terminal' })
  })
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

  it('keeps unchanged projects and threads when a sibling changes', () => {
    const first = reduce(undefined, projectsReceived([
      project('a', [thread('t1'), thread('t2')]),
      project('b', [thread('t3')]),
    ]))
    const updated = reduce(first, projectsReceived([
      project('a', [thread('t1'), thread('t2', { title: 'renamed' })]),
      project('b', [thread('t3')]),
    ]))

    expect(updated.projects).not.toBe(first.projects)
    expect(updated.projects[0]).not.toBe(first.projects[0])
    expect(updated.projects[0]!.threads[0]).toBe(first.projects[0]!.threads[0])
    expect(updated.projects[0]!.threads[1]!.title).toBe('renamed')
    expect(updated.projects[1]).toBe(first.projects[1])
  })

  it('applies a push that only advances the environment setup status', () => {
    const first = reduce(undefined, projectsReceived([
      project('a', [thread('t1', { worktree: true, environmentSetupStatus: 'pending' })]),
    ]))
    const running = reduce(first, projectsReceived([
      project('a', [thread('t1', { worktree: true, environmentSetupStatus: 'running' })]),
    ]))
    const succeeded = reduce(running, projectsReceived([
      project('a', [thread('t1', { worktree: true, environmentSetupStatus: 'succeeded' })]),
    ]))

    expect(running.projects[0]!.threads[0]!.environmentSetupStatus).toBe('running')
    expect(succeeded.projects[0]!.threads[0]!.environmentSetupStatus).toBe('succeeded')
  })

  it('notices a change to every thread field the wire schema declares', () => {
    const base = fullThread()
    expect(Object.keys(base).sort()).toEqual(Object.keys(ThreadSchema.fields).sort())

    for (const field of Object.keys(ThreadSchema.fields) as (keyof Thread)[]) {
      const first = reduce(undefined, projectsReceived([project('a', [base])]))
      const changedThread = { ...base, [field]: changedValue(base[field]) } as Thread
      const pushed = [project('a', [changedThread])]
      const updated = reduce(first, projectsReceived(pushed))

      expect(updated.projects, `thread.${field}`).not.toBe(first.projects)
      expect(updated.projects, `thread.${field}`).toEqual(visibleProjectSnapshots(pushed))
    }
  })

  it('notices a change to every project field the wire schema declares', () => {
    const base = fullProject()
    expect(Object.keys(base).sort()).toEqual(Object.keys(ProjectSchema.fields).sort())

    for (const field of Object.keys(ProjectSchema.fields) as (keyof Project)[]) {
      const first = reduce(undefined, projectsReceived([base]))
      const pushed = [{ ...base, [field]: changedValue(base[field]) } as Project]
      const updated = reduce(first, projectsReceived(pushed))

      expect(updated.projects, `project.${field}`).not.toBe(first.projects)
      expect(updated.projects, `project.${field}`).toEqual(visibleProjectSnapshots(pushed))
    }
  })

  it('keeps fully populated projects when an identical copy is pushed', () => {
    const first = reduce(undefined, projectsReceived([fullProject()]))
    const again = reduce(first, projectsReceived([structuredClone(fullProject())]))

    expect(again.projects).toBe(first.projects)
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
    vi.mocked(setThreadSettled).mockReturnValueOnce(
      new Promise<Thread>((resolve) => { release = resolve }) as never,
    )

    const pending = store.dispatch(threadSettled({
      projectId: 'a',
      threadId: 't1',
      settled: true,
    }))
    // The sidebar's spinner is this flag; it must be set while the call is out.
    expect(selectSettlingThreadId(store.getState())).toBe('t1')

    release(thread('t1', { settledAt: '2026-07-27T01:00:00Z' }))
    await pending

    expect(selectSettlingThreadId(store.getState())).toBeNull()
    expect(selectProjects(store.getState())[0]!.threads[0]!.settledAt).toBe('2026-07-27T01:00:00Z')
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
