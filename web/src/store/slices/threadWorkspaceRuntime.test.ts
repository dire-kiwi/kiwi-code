import { describe, expect, it } from 'vitest'
import {
  branchStateReported,
  initialThreadWorkspaceRuntimeState,
  piPresentationStatusReported,
  processSelected,
  processStarted,
  selectProcessWindows,
  selectSelectedProcessId,
  selectToolStatuses,
  threadRuntimeKey,
  threadStatusSnapshotReceived,
  threadWorkspaceRuntimeSlice,
  toolStatusReported,
  workspaceEntered,
  type ThreadWorkspaceRuntimeState,
} from './threadWorkspaceRuntime'
import type { RootState } from '@/store/rootReducer'
import type { ProcessWindow, ThreadStatusSnapshot } from '@/types'

const reduce = threadWorkspaceRuntimeSlice.reducer

const openThread = threadRuntimeKey('project-1', 'thread-1')
const otherThread = threadRuntimeKey('project-1', 'thread-2')

function runtimeRoot(threadWorkspaceRuntime: ThreadWorkspaceRuntimeState) {
  return { threadWorkspaceRuntime } as RootState
}

function entered() {
  return reduce(undefined, workspaceEntered({ threadKey: openThread, activeTool: 'pi' }))
}

function process(id: string): ProcessWindow {
  return { id, index: 1, name: id } as ProcessWindow
}

function snapshot(overrides: Partial<ThreadStatusSnapshot> = {}) {
  return {
    processes: [],
    shellWindows: [],
    gitBranches: null,
    contextStatuses: {},
    errors: {},
    ...overrides,
  } as unknown as ThreadStatusSnapshot
}

describe('threadWorkspaceRuntime slice', () => {
  it('seeds the entry tool as connecting and clears the previous thread', () => {
    const stale = reduce(entered(), toolStatusReported({
      threadKey: openThread,
      tool: 'browser',
      status: 'open',
    }))
    expect(selectToolStatuses(runtimeRoot(stale))).toEqual({ pi: 'connecting', browser: 'open' })

    const next = reduce(stale, workspaceEntered({ threadKey: otherThread, activeTool: 'terminal' }))
    // Nothing from the previous thread may survive: its browser pane was a
    // different socket to a different workspace.
    expect(selectToolStatuses(runtimeRoot(next))).toEqual({ terminal: 'connecting' })
    expect(next.branchState).toBeNull()
    expect(next.processWindows).toEqual([])
    expect(next.threadKey).toBe(otherThread)
  })

  it('drops reports that name a thread other than the one that is open', () => {
    const state = entered()

    const stray = reduce(state, toolStatusReported({
      threadKey: otherThread,
      tool: 'browser',
      status: 'error',
    }))
    const strayBranch = reduce(stray, branchStateReported({
      threadKey: otherThread,
      branchState: { isRepository: true, current: 'wrong-branch' } as never,
    }))
    const strayPresentation = reduce(strayBranch, piPresentationStatusReported({
      threadKey: otherThread,
      presentation: 'native',
      status: 'error',
    }))

    expect(selectToolStatuses(runtimeRoot(strayPresentation))).toEqual({ pi: 'connecting' })
    expect(strayPresentation.branchState).toBeNull()
    expect(strayPresentation.piPresentationStatuses.native).toBe('connecting')
  })

  it('does not produce a new state for a status that has not changed', () => {
    // TerminalWorkspace used to guard this by hand before calling setState. The
    // guard now rests on immer skipping no-op assignments; if that ever stops
    // being true, every pane heartbeat re-renders the whole workspace.
    const state = reduce(entered(), toolStatusReported({
      threadKey: openThread,
      tool: 'browser',
      status: 'open',
    }))
    const repeated = reduce(state, toolStatusReported({
      threadKey: openThread,
      tool: 'browser',
      status: 'open',
    }))
    expect(repeated).toBe(state)

    const changed = reduce(state, toolStatusReported({
      threadKey: openThread,
      tool: 'browser',
      status: 'closed',
    }))
    expect(changed).not.toBe(state)
  })

  it('keeps a running process selected across snapshots and falls back when it exits', () => {
    const withProcesses = reduce(entered(), threadStatusSnapshotReceived({
      threadKey: openThread,
      snapshot: snapshot({ processes: [process('a'), process('b')] as never }),
    }))
    expect(selectSelectedProcessId(runtimeRoot(withProcesses))).toBe('a')

    const chosen = reduce(withProcesses, processSelected('b'))
    const stillRunning = reduce(chosen, threadStatusSnapshotReceived({
      threadKey: openThread,
      snapshot: snapshot({ processes: [process('a'), process('b')] as never }),
    }))
    expect(selectSelectedProcessId(runtimeRoot(stillRunning))).toBe('b')

    const exited = reduce(stillRunning, threadStatusSnapshotReceived({
      threadKey: openThread,
      snapshot: snapshot({ processes: [process('a')] as never }),
    }))
    expect(selectSelectedProcessId(runtimeRoot(exited))).toBe('a')

    const allGone = reduce(exited, threadStatusSnapshotReceived({
      threadKey: openThread,
      snapshot: snapshot({ processes: [] }),
    }))
    expect(selectSelectedProcessId(runtimeRoot(allGone))).toBeNull()
  })

  it('adds an environment-action shell before the socket catches up', () => {
    const started = reduce(entered(), processStarted({ threadKey: openThread, process: process('env-1') }))
    expect(selectProcessWindows(runtimeRoot(started)).map((window) => window.id)).toEqual(['env-1'])
    expect(selectSelectedProcessId(runtimeRoot(started))).toBe('env-1')

    // A second snapshot that already contains it must not duplicate the row.
    const again = reduce(started, processStarted({ threadKey: openThread, process: process('env-1') }))
    expect(selectProcessWindows(runtimeRoot(again))).toHaveLength(1)
  })

  it('starts with nothing resident, so no consumer reads another thread', () => {
    expect(initialThreadWorkspaceRuntimeState.threadKey).toBeNull()
    expect(initialThreadWorkspaceRuntimeState.toolStatuses).toEqual({})
  })
})
