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
import { ServerStateBridge } from '@/store/ServerStateBridge'
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
    // The real useSubscription is a useSyncExternalStore: a new snapshot
    // re-renders its subscribers by itself. ServerStateBridge uses no router
    // hooks, so without that push it would never see a snapshot delivered during
    // a navigation, and this test would pass or fail on an artefact of the mock
    // rather than on the behaviour it is about.
    listeners: new Set<() => void>(),
    publish() {
      for (const listener of mocks.listeners) listener()
    },
  }
})

vi.mock('@/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api')>(),
  acknowledgePiThreadActivity: mocks.acknowledgePiThreadActivity,
}))

vi.mock('@/wire/react', async () => {
  const { useEffect, useState } = await import('react')
  return {
    useApplicationInstance: () => {},
    useConnectionStatus: () => ({ state: 'open', instanceId: 'fixture' }),
    useLastReadySubscriptionData: (
      subscription: { state: string; data?: unknown },
    ) => subscription.state === 'ready' ? subscription.data : null,
    useSubscription: (topic: { tag: string }) => {
      const [, bump] = useState(0)
      useEffect(() => {
        const listener = () => bump((value) => value + 1)
        mocks.listeners.add(listener)
        return () => {
          mocks.listeners.delete(listener)
        }
      }, [])
      return mocks.subscriptions[topic.tag]
    },
  }
})

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
  relatedProjects: [],
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
    settings: ready({}),
  }
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('App activity acknowledgement', () => {
  it('acknowledges a finished snapshot received in the same commit as an active route change', async () => {
    // ServerStateBridge is what copies the socket topics into the store, and it
    // sits beside App in main.tsx rather than inside it. Rendering App without it
    // would leave every slice empty.
    renderWithStore(
      <MemoryRouter initialEntries={[workspacePath(project.id, firstThread.id, 'pi')]}>
        <ServerStateBridge />
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
      threadId: secondThread.id,
      state: 'finished',
      updatedAt: '2026-07-27T00:01:00Z',
    }
    await act(async () => {
      mocks.subscriptions.agentActivity = ready([finished])
      mocks.publish()
      fireEvent.click(screen.getByRole('button', { name: 'Open second thread' }))
    })

    expect(screen.getByTestId('workspace-thread').textContent).toBe(secondThread.id)
    expect(mocks.acknowledgePiThreadActivity).toHaveBeenCalledOnce()
    expect(mocks.acknowledgePiThreadActivity).toHaveBeenCalledWith(
      project.id,
      secondThread.id,
    )
  })
})
