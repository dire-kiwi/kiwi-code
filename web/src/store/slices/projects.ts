import { createAsyncThunk, createSlice, original, type PayloadAction } from '@reduxjs/toolkit'
import {
  deleteProject,
  deleteThread,
  setThreadPinned,
  setThreadSettled,
  updateProjectOrder,
  updateThreadOrder,
} from '@/api'
import type { RootState } from '@/store/rootReducer'
import { retryTopic } from '@/store/socketAccess'
import type { Project, Thread } from '@/types'
import { ProjectsTopic } from '@/wire/topics'

// The project tree, and the "one of these is in flight" ids the sidebar
// uses to show spinners.
//
// Every mutation here is optimistic: the list moves first and rolls back if the
// server refuses. That is why they are thunks rather than plain calls -- the
// rollback needs the pre-mutation value, and createAsyncThunk's pending/rejected
// lifecycle is exactly the in-flight flags the sidebar used to be handed.
//
// Not persisted: the server owns this, and the socket re-sends it on connect.

export type ProjectsState = {
  projects: Project[]
  hydrated: boolean
  deletingProjectId: string | null
  deletingThreadId: string | null
  settlingThreadId: string | null
  pinningThreadId: string | null
}

export const initialProjectsState: ProjectsState = {
  projects: [],
  hydrated: false,
  deletingProjectId: null,
  deletingThreadId: null,
  settlingThreadId: null,
  pinningThreadId: null,
}

// --- pure helpers, lifted out of App unchanged -----------------------------

/** Threads mid-rollback are the server's business, not the sidebar's. */
export function visibleProjectSnapshots(items: Project[]) {
  return items.map((project) => {
    const threads = project.threads.filter((thread) => !thread.rollbackPending)
    return threads.length === project.threads.length ? project : { ...project, threads }
  })
}

/**
 * Structural equality for decoded wire values: JSON primitives, arrays, and
 * plain objects. A key holding `undefined` counts as absent.
 */
function sameWireValue(current: unknown, next: unknown): boolean {
  if (current === next) return true
  if (typeof current !== 'object' || typeof next !== 'object' || current === null || next === null) {
    return false
  }
  if (Array.isArray(current) || Array.isArray(next)) {
    if (!Array.isArray(current) || !Array.isArray(next) || current.length !== next.length) return false
    return current.every((item, index) => sameWireValue(item, next[index]))
  }
  const currentRecord = current as Record<string, unknown>
  const nextRecord = next as Record<string, unknown>
  const keys = new Set([...Object.keys(currentRecord), ...Object.keys(nextRecord)])
  for (const key of keys) {
    if (!sameWireValue(currentRecord[key], nextRecord[key])) return false
  }
  return true
}

/**
 * Builds `next`, but reuses each item of `current` that `reconcile` says is
 * unchanged, and returns `current` itself when nothing changed at all.
 */
function reuseUnchanged<T extends { id: string }>(
  current: T[],
  next: T[],
  reconcile: (previous: T, candidate: T) => T,
) {
  const byId = new Map(current.map((item) => [item.id, item]))
  let changed = current.length !== next.length
  const items = next.map((candidate, index) => {
    const previous = byId.get(candidate.id)
    const item = previous ? reconcile(previous, candidate) : candidate
    if (item !== current[index]) changed = true
    return item
  })
  return changed ? items : current
}

function reconcileThread(previous: Thread, candidate: Thread) {
  return sameWireValue(previous, candidate) ? previous : candidate
}

function reconcileProject(previous: Project, candidate: Project) {
  const threads = reuseUnchanged(previous.threads, candidate.threads, reconcileThread)
  if (threads === previous.threads && sameWireValue({ ...previous, threads: candidate.threads }, candidate)) {
    return previous
  }
  return { ...candidate, threads }
}

/**
 * Identity guard for socket pushes. Load-bearing: without it every push replaces
 * the array and re-renders the whole sidebar tree, which under useSelector is
 * worse than it was under useState.
 *
 * The comparison covers every field structurally rather than a hand-kept list:
 * a field missing from such a list made a push that changed only that field
 * look identical, so the server's update was silently dropped. Unchanged
 * projects and threads keep their previous object identity, so only what
 * changed re-renders.
 */
export function reconcileProjects(current: Project[], next: Project[]) {
  return reuseUnchanged(current, next, reconcileProject)
}

export function sameProjects(current: Project[], next: Project[]) {
  return reconcileProjects(current, next) === current
}

export function projectsWithProfileOrder(current: Project[], profileId: string, projectIds: string[]) {
  const profileProjects = current.filter((project) => project.profileId === profileId)
  if (profileProjects.length !== projectIds.length || new Set(projectIds).size !== projectIds.length) return current

  const byId = new Map(profileProjects.map((project) => [project.id, project]))
  const ordered = projectIds.map((id) => byId.get(id))
  if (ordered.some((project) => !project)) return current

  let orderedIndex = 0
  return current.map((project) =>
    project.profileId === profileId ? ordered[orderedIndex++]! : project,
  )
}

export function projectsWithNewProjectFirst(current: Project[], project: Project) {
  if (current.some((item) => item.id === project.id)) return current

  const updated = [...current, project]
  const profileProjectIds = [
    project.id,
    ...current
      .filter((item) => item.profileId === project.profileId)
      .map((item) => item.id),
  ]
  return projectsWithProfileOrder(updated, project.profileId, profileProjectIds)
}

export function projectsWithThreadOrder(current: Project[], projectId: string, threadIds: string[]) {
  return current.map((project) => {
    if (project.id !== projectId || project.threads.length !== threadIds.length) return project
    if (new Set(threadIds).size !== threadIds.length) return project

    const byId = new Map(project.threads.map((thread) => [thread.id, thread]))
    const ordered = threadIds.map((id) => byId.get(id))
    if (ordered.some((thread) => !thread)) return project
    return { ...project, threads: ordered as Thread[] }
  })
}

function withThread(projects: Project[], projectId: string, updated: Thread) {
  return projects.map((project) =>
    project.id === projectId
      ? {
          ...project,
          threads: project.threads.map((thread) => thread.id === updated.id ? updated : thread),
        }
      : project,
  )
}

function errorMessage(reason: unknown, fallback: string) {
  return reason instanceof Error ? reason.message : fallback
}

// --- thunks ----------------------------------------------------------------

type ThunkConfig = { state: RootState; rejectValue: string }

export const projectsReordered = createAsyncThunk<
  void,
  { profileId: string; projectIds: string[] },
  ThunkConfig
>('projects/reordered', async ({ profileId, projectIds }, { dispatch, getState, rejectWithValue }) => {
  const previous = getState().projects.projects
    .filter((project) => project.profileId === profileId)
    .map((project) => project.id)

  dispatch(projectOrderApplied({ profileId, projectIds }))
  try {
    await updateProjectOrder(profileId, projectIds)
  } catch (reason) {
    dispatch(projectOrderApplied({ profileId, projectIds: previous }))
    // The optimistic move is undone locally; ask the server for the truth in
    // case it moved for some other reason too.
    retryTopic(ProjectsTopic, undefined)
    return rejectWithValue(errorMessage(reason, 'Could not save the project order.'))
  }
})

export const threadsReordered = createAsyncThunk<
  void,
  { projectId: string; threadIds: string[] },
  ThunkConfig
>('projects/threadsReordered', async ({ projectId, threadIds }, { dispatch, getState, rejectWithValue }) => {
  const previous = getState().projects.projects
    .find((project) => project.id === projectId)
    ?.threads.map((thread) => thread.id) ?? []

  dispatch(threadOrderApplied({ projectId, threadIds }))
  try {
    await updateThreadOrder(projectId, threadIds)
  } catch (reason) {
    dispatch(threadOrderApplied({ projectId, threadIds: previous }))
    retryTopic(ProjectsTopic, undefined)
    return rejectWithValue(errorMessage(reason, 'Could not save the thread order.'))
  }
})

export const projectRemoved = createAsyncThunk<string, string, ThunkConfig>(
  'projects/removed',
  async (projectId, { rejectWithValue }) => {
    try {
      await deleteProject(projectId)
      return projectId
    } catch (reason) {
      return rejectWithValue(errorMessage(reason, 'Could not remove that project.'))
    }
  },
)

export const threadSettled = createAsyncThunk<
  { projectId: string; thread: Thread },
  { projectId: string; threadId: string; settled: boolean },
  ThunkConfig
>('projects/threadSettled', async ({ projectId, threadId, settled }, { rejectWithValue }) => {
  try {
    return { projectId, thread: await setThreadSettled(projectId, threadId, settled) }
  } catch (reason) {
    return rejectWithValue(errorMessage(reason, `Could not ${settled ? 'settle' : 'un-settle'} that thread.`))
  }
})

export const threadPinned = createAsyncThunk<
  { projectId: string; thread: Thread },
  { projectId: string; threadId: string; pinned: boolean },
  ThunkConfig
>('projects/threadPinned', async ({ projectId, threadId, pinned }, { rejectWithValue }) => {
  try {
    return { projectId, thread: await setThreadPinned(projectId, threadId, pinned) }
  } catch (reason) {
    return rejectWithValue(errorMessage(reason, `Could not ${pinned ? 'pin' : 'unpin'} that thread.`))
  }
})

export const threadRemoved = createAsyncThunk<
  { projectId: string; threadIds: string[] },
  { projectId: string; threadId: string },
  ThunkConfig
>('projects/threadRemoved', async ({ projectId, threadId }, { rejectWithValue }) => {
  try {
    await deleteThread(projectId, threadId)
    return { projectId, threadIds: [threadId] }
  } catch (reason) {
    return rejectWithValue(errorMessage(reason, 'Could not delete that thread.'))
  }
})

// --- slice ------------------------------------------------------------------

export const projectsSlice = createSlice({
  name: 'projects',
  initialState: initialProjectsState,
  reducers: {
    projectsReceived(state, action: PayloadAction<Project[]>) {
      // Reconcile against the plain previous value, so reused projects and
      // threads are the committed objects rather than draft proxies.
      const current = original(state.projects) ?? state.projects
      const next = reconcileProjects(current, visibleProjectSnapshots(action.payload))
      if (next !== current) state.projects = next
      state.hydrated = true
    },
    projectCreated(state, action: PayloadAction<Project>) {
      state.projects = projectsWithNewProjectFirst(state.projects, action.payload)
    },
    projectUpdated(state, action: PayloadAction<Project>) {
      const visible = visibleProjectSnapshots([action.payload])[0]!
      state.projects = state.projects.map((project) =>
        project.id === visible.id ? visible : project)
    },
    threadCreated(state, action: PayloadAction<{ projectId: string; thread: Thread }>) {
      const { projectId, thread } = action.payload
      state.projects = state.projects.map((project) => {
        if (project.id !== projectId || project.threads.some((item) => item.id === thread.id)) return project
        return { ...project, threads: [thread, ...project.threads] }
      })
    },
    // Dispatched from wherever a thread was edited -- the details sidebar, the
    // usage-limits editor -- rather than climbing back up to App.
    threadUpdated(state, action: PayloadAction<{ projectId: string; thread: Thread }>) {
      state.projects = withThread(state.projects, action.payload.projectId, action.payload.thread)
    },
    projectOrderApplied(state, action: PayloadAction<{ profileId: string; projectIds: string[] }>) {
      state.projects = projectsWithProfileOrder(
        state.projects,
        action.payload.profileId,
        action.payload.projectIds,
      )
    },
    threadOrderApplied(state, action: PayloadAction<{ projectId: string; threadIds: string[] }>) {
      state.projects = projectsWithThreadOrder(
        state.projects,
        action.payload.projectId,
        action.payload.threadIds,
      )
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(projectRemoved.pending, (state, action) => {
        state.deletingProjectId = action.meta.arg
      })
      .addCase(projectRemoved.fulfilled, (state, action) => {
        state.projects = state.projects.filter((project) => project.id !== action.payload)
        state.deletingProjectId = null
      })
      .addCase(projectRemoved.rejected, (state) => {
        state.deletingProjectId = null
      })

      .addCase(threadSettled.pending, (state, action) => {
        state.settlingThreadId = action.meta.arg.threadId
      })
      .addCase(threadSettled.fulfilled, (state, action) => {
        state.projects = withThread(state.projects, action.payload.projectId, action.payload.thread)
        state.settlingThreadId = null
      })
      .addCase(threadSettled.rejected, (state) => {
        state.settlingThreadId = null
      })
      .addCase(threadPinned.pending, (state, action) => {
        state.pinningThreadId = action.meta.arg.threadId
      })
      .addCase(threadPinned.fulfilled, (state, action) => {
        state.projects = withThread(state.projects, action.payload.projectId, action.payload.thread)
        state.pinningThreadId = null
      })
      .addCase(threadPinned.rejected, (state) => {
        state.pinningThreadId = null
      })
      .addCase(threadRemoved.pending, (state, action) => {
        state.deletingThreadId = action.meta.arg.threadId
      })
      .addCase(threadRemoved.fulfilled, (state, action) => {
        const { projectId, threadIds } = action.payload
        const removed = new Set(threadIds)
        state.projects = state.projects.map((project) =>
          project.id === projectId
            ? { ...project, threads: project.threads.filter((thread) => !removed.has(thread.id)) }
            : project,
        )
        state.deletingThreadId = null
      })
      .addCase(threadRemoved.rejected, (state) => {
        state.deletingThreadId = null
      })
  },
})

export const {
  projectCreated,
  projectOrderApplied,
  projectUpdated,
  projectsReceived,
  threadCreated,
  threadOrderApplied,
  threadUpdated,
} = projectsSlice.actions

export const selectProjects = (state: RootState) => state.projects.projects
export const selectProjectsHydrated = (state: RootState) => state.projects.hydrated
export const selectDeletingProjectId = (state: RootState) => state.projects.deletingProjectId
export const selectDeletingThreadId = (state: RootState) => state.projects.deletingThreadId

export const selectSettlingThreadId = (state: RootState) => state.projects.settlingThreadId
export const selectPinningThreadId = (state: RootState) => state.projects.pinningThreadId
