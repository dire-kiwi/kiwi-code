import {
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
} from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PiThreadActivity, Profile, Project, Thread } from '@/types'
import { workspacePath } from './routes'
import App from './App'
import { renderWithStore } from '@/store/testing'

const mocks = vi.hoisted(() => {
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  return {
    acknowledgePiThreadActivity: vi.fn(),
    subscriptions: {} as Record<string, {
      state: 'ready'
      data: unknown
      retry: ReturnType<typeof vi.fn>
    }>,
  }
})

vi.mock('@/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api')>(),
  acknowledgePiThreadActivity: mocks.acknowledgePiThreadActivity,
}))

vi.mock('@/wire/react', () => ({
  useApplicationInstance: () => {},
  useConnectionStatus: () => ({ state: 'open', instanceId: 'fixture' }),
  useLastReadySubscriptionData: (
    subscription: { state: string; data?: unknown },
  ) => subscription.state === 'ready' ? subscription.data : null,
  useSubscription: (topic: { tag: string }) => mocks.subscriptions[topic.tag],
}))

vi.mock('@/features/project-sidebar/ProjectSidebar', () => ({
  ProjectSidebar: () => null,
}))

vi.mock('@/features/workspace/TerminalWorkspace', () => ({
  TerminalWorkspace: ({ thread }: { thread: Thread }) => (
    <div data-testid="workspace-thread">{thread.id}</div>
  ),
}))

function ready(data: unknown) {
  return {
    state: 'ready' as const,
    data,
    retry: vi.fn(),
  }
}

function thread(id: string): Thread {
  return {
    id,
    title: id,
    cwd: '/workspace',
    createdAt: '2026-07-27T00:00:00Z',
  }
}

const firstThread = thread('first')
const secondThread = thread('second')
const childThread: Thread = {
  ...thread('child'),
  parentThreadId: secondThread.id,
}
const project: Project = {
  id: 'project',
  name: 'Project',
  path: '/workspace',
  profileId: 'personal',
  host: 'fixture',
  isGitRepo: true,
  createdAt: '2026-07-27T00:00:00Z',
  threads: [firstThread, secondThread],
  worktreeBranchPrefix: 'codex/',
  environment: {
    name: 'Local',
    setupScripts: { default: '', macos: '', linux: '', windows: '' },
    cleanupScripts: { default: '', macos: '', linux: '', windows: '' },
    variables: [],
    actions: [],
  },
  figmaMCPEnabled: false,
}
const profile: Profile = { id: 'personal', name: 'Personal' }

function RouteChangeButton() {
  const navigate = useNavigate()
  return (
    <button
      type="button"
      onClick={() => navigate(workspacePath(project.id, secondThread.id, 'pi'))}
    >
      Open second thread
    </button>
  )
}

beforeEach(() => {
  mocks.acknowledgePiThreadActivity.mockReset()
  mocks.acknowledgePiThreadActivity.mockResolvedValue(undefined)
  mocks.subscriptions = {
    projects: ready([project]),
    profiles: ready([profile]),
    agentActivity: ready([]),
    threadUsage: ready([]),
    processWebServers: ready([]),
    settings: ready({}),
  }
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('App activity acknowledgement', () => {
  it('acknowledges a finished snapshot received in the same commit as an active route change', async () => {
    renderWithStore(
      <MemoryRouter initialEntries={[workspacePath(project.id, firstThread.id, 'pi')]}>
        <RouteChangeButton />
        <App />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('workspace-thread').textContent).toBe(firstThread.id)
    })
    mocks.acknowledgePiThreadActivity.mockClear()

    const finished: PiThreadActivity = {
      projectId: project.id,
      threadId: childThread.id,
      state: 'finished',
      updatedAt: '2026-07-27T00:01:00Z',
    }
    await act(async () => {
      mocks.subscriptions.projects = ready([{
        ...project,
        threads: [...project.threads, childThread],
      }])
      mocks.subscriptions.agentActivity = ready([finished])
      fireEvent.click(screen.getByRole('button', { name: 'Open second thread' }))
    })

    expect(screen.getByTestId('workspace-thread').textContent).toBe(secondThread.id)
    expect(mocks.acknowledgePiThreadActivity).toHaveBeenCalledOnce()
    expect(mocks.acknowledgePiThreadActivity).toHaveBeenCalledWith(
      project.id,
      childThread.id,
    )
  })
})
