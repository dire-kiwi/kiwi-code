import { apiUrl } from './apiUrl'
import type {
  AgentSkillStatus,
  AppSettings,
  BrowserActionRequest,
  BrowserActionResponse,
  BrowserStatusResult,
  CleanupOverview,
  CodingAgentConfig,
  DirectorySuggestion,
  GitBranchState,
  LocalEnvironment,
  ProcessWindow,
  Profile,
  Project,
  Thread,
  SavedWorkflow,
  TmuxBrowserSession,
  TmuxWindow,
  WorkflowRun,
} from './types'

type ErrorResponse = {
  error?: string
}

type ApplicationHealth = {
  status: string
  instanceId: string
}

type ApplicationRestart = {
  status: string
  instanceId: string
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiUrl(path), {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })

  if (!response.ok) {
    let message = `Request failed (${response.status})`
    try {
      const body = (await response.json()) as ErrorResponse
      if (body.error) message = body.error
    } catch {
      // The fallback message already contains the useful HTTP status.
    }
    throw new Error(message)
  }

  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export function getApplicationHealth(signal?: AbortSignal) {
  return request<ApplicationHealth>('/api/health', { signal, cache: 'no-store' })
}

export function restartApplication() {
  return request<ApplicationRestart>('/api/restart', { method: 'POST' })
}

export async function waitForApplicationRestart(instanceId: string, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 250))
    try {
      const health = await getApplicationHealth()
      if (health.status === 'ok' && health.instanceId && health.instanceId !== instanceId) return
    } catch {
      // The old process is expected to be unavailable before its launcher starts
      // the replacement.
    }
  }
  throw new Error('Timed out waiting for Kiwi Code to restart.')
}

export function getSettings(signal?: AbortSignal) {
  return request<AppSettings>('/api/settings', { signal })
}

export function updateSettings(input: string | Partial<Pick<
  AppSettings,
  'worktreeBasePath' | 'archivedThreadRetentionDays' | 'orphanedWorktreeRetentionDays' | 'subAgentNestingDepth'
  | 'disableWorkflows' | 'workflowKeywordTriggerEnabled' | 'workflowSizeGuideline' | 'claudeCodeProfiles' | 'theme'
>>) {
  return request<AppSettings>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify(typeof input === 'string' ? { worktreeBasePath: input } : input),
  })
}

export function getCleanupOverview(signal?: AbortSignal) {
  return request<CleanupOverview>('/api/cleanup', { signal })
}

export function getAgentSkillStatus(signal?: AbortSignal) {
  return request<AgentSkillStatus>('/api/settings/agent-skills', { signal })
}

export function installAgentSkill() {
  return request<AgentSkillStatus>('/api/settings/agent-skills', { method: 'POST' })
}

export function listCodingAgents(signal?: AbortSignal, projectId?: string) {
  const query = projectId ? `?${new URLSearchParams({ projectId }).toString()}` : ''
  return request<CodingAgentConfig[]>(`/api/coding-agents${query}`, { signal })
}

export function listProfiles(signal?: AbortSignal) {
  return request<Profile[]>('/api/profiles', { signal })
}

export function createProfile(name: string) {
  return request<Profile>('/api/profiles', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function listProjects(signal?: AbortSignal) {
  return request<Project[]>('/api/projects', { signal })
}

export function listTmuxSessions(signal?: AbortSignal) {
  return request<TmuxBrowserSession[]>('/api/tmux/sessions', { signal })
}

export function createProject(input: { name: string; path: string; profileId: string }) {
  return request<Project>('/api/projects', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function updateProject(
  id: string,
  input: {
    profileId?: string
    subAgentNestingDepthOverride?: number | null
    worktreeBranchPrefix?: string
    environment?: LocalEnvironment
  },
) {
  return request<Project>(`/api/projects/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  })
}

export function updateProjectProfile(id: string, profileId: string) {
  return updateProject(id, { profileId })
}

export function updateProjectSubAgentNestingDepth(id: string, depth: number | null) {
  return updateProject(id, { subAgentNestingDepthOverride: depth })
}

export function updateProjectWorktreeBranchPrefix(id: string, prefix: string) {
  return updateProject(id, { worktreeBranchPrefix: prefix })
}

export function updateProjectEnvironment(id: string, environment: LocalEnvironment) {
  return updateProject(id, { environment })
}

export function runEnvironmentAction(projectId: string, threadId: string, actionId: string) {
  return request<ProcessWindow>(
    `${threadPath(projectId, threadId)}/environment/actions/${encodeURIComponent(actionId)}`,
    { method: 'POST', body: '{}' },
  )
}

export function updateProjectOrder(profileId: string, projectIds: string[]) {
  return request<void>('/api/projects/order', {
    method: 'PUT',
    body: JSON.stringify({ profileId, projectIds }),
  })
}

export function listDirectorySuggestions(path: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ path })
  return request<{ suggestions: DirectorySuggestion[] }>(
    `/api/filesystem/directories?${query.toString()}`,
    { signal },
  )
}

export function deleteProject(id: string) {
  return request<void>(`/api/projects/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
}

export function createThread(
  projectId: string,
  input: {
    title?: string
    worktree: boolean
    baseBranch?: string
    nestedDepth?: number
  },
) {
  return request<Thread>(`/api/projects/${encodeURIComponent(projectId)}/threads`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

function threadPath(projectId: string, threadId: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}`
}

export function updateThreadTitle(projectId: string, threadId: string, title: string) {
  return request<Thread>(threadPath(projectId, threadId), {
    method: 'PATCH',
    body: JSON.stringify({ title, autoGenerated: false }),
  })
}

function workflowPath(projectId: string, threadId: string, runId?: string) {
  const base = `${threadPath(projectId, threadId)}/workflows`
  return runId ? `${base}/${encodeURIComponent(runId)}` : base
}

export function getWorkflow(projectId: string, threadId: string, runId: string, signal?: AbortSignal) {
  return request<WorkflowRun>(workflowPath(projectId, threadId, runId), { signal, cache: 'no-store' })
}

export function pauseWorkflow(projectId: string, threadId: string, runId: string) {
  return request<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/pause`, {
    method: 'POST',
    body: '{}',
  })
}

export function resumeWorkflow(projectId: string, threadId: string, runId: string) {
  return request<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/resume`, {
    method: 'POST',
    body: '{}',
  })
}

export function stopWorkflow(projectId: string, threadId: string, runId: string) {
  return request<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/stop`, {
    method: 'POST',
    body: '{}',
  })
}

export function saveWorkflow(
  projectId: string,
  threadId: string,
  runId: string,
  input: { name: string; scope: 'project' | 'personal'; overwrite?: boolean },
) {
  return request<SavedWorkflow>(`${workflowPath(projectId, threadId, runId)}/save`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export function updateThreadLimits(
  projectId: string,
  threadId: string,
  limits: { tokenLimit: number | null; costLimitUsd: number | null },
) {
  return request<Thread>(`${threadPath(projectId, threadId)}/limits`, {
    method: 'PUT',
    body: JSON.stringify(limits),
  })
}

export function setThreadArchived(projectId: string, threadId: string, archived: boolean) {
  return request<Thread>(threadPath(projectId, threadId), {
    method: 'PATCH',
    body: JSON.stringify({ archived }),
  })
}

export function setThreadBookmarked(projectId: string, threadId: string, bookmarked: boolean) {
  return request<Thread>(threadPath(projectId, threadId), {
    method: 'PATCH',
    body: JSON.stringify({ bookmarked }),
  })
}

export function deleteThread(projectId: string, threadId: string) {
  return request<void>(threadPath(projectId, threadId), { method: 'DELETE' })
}

export function updateThreadOrder(projectId: string, threadIds: string[]) {
  return request<void>(`/api/projects/${encodeURIComponent(projectId)}/threads/order`, {
    method: 'PUT',
    body: JSON.stringify({ threadIds }),
  })
}

export function acknowledgePiThreadActivity(projectId: string, threadId: string) {
  return request<void>(`${threadPath(projectId, threadId)}/pi/activity`, { method: 'DELETE' })
}

export function threadEventsPath(projectId: string, threadId: string) {
  return apiUrl(`${threadPath(projectId, threadId)}/events`)
}

export function threadPlanDownloadUrl(projectId: string, threadId: string, planId: string) {
  return apiUrl(`${threadPath(projectId, threadId)}/plans/${encodeURIComponent(planId)}`)
}

export async function getThreadPlanMarkdown(
  projectId: string,
  threadId: string,
  planId: string,
  signal?: AbortSignal,
) {
  const response = await fetch(threadPlanDownloadUrl(projectId, threadId, planId), {
    headers: { Accept: 'text/markdown' },
    cache: 'no-store',
    signal,
  })
  if (!response.ok) {
    let message = `Could not load the plan (${response.status})`
    try {
      const body = (await response.json()) as ErrorResponse
      if (body.error) message = body.error
    } catch {
      // Keep the status-bearing fallback when the response is not JSON.
    }
    throw new Error(message)
  }
  return response.text()
}

function browserPath(projectId: string, threadId: string) {
  return `${threadPath(projectId, threadId)}/browser`
}

export function getBrowserStatus(projectId: string, threadId: string, signal?: AbortSignal) {
  return request<BrowserStatusResult | BrowserActionResponse<BrowserStatusResult>>(browserPath(projectId, threadId), {
    signal,
    cache: 'no-store',
  })
}

export function performBrowserAction<Result = unknown>(
  projectId: string,
  threadId: string,
  action: BrowserActionRequest,
  signal?: AbortSignal,
) {
  return request<BrowserActionResponse<Result>>(`${browserPath(projectId, threadId)}/actions`, {
    method: 'POST',
    body: JSON.stringify(action),
    signal,
  })
}

export async function getBrowserFrame(
  projectId: string,
  threadId: string,
  signal?: AbortSignal,
): Promise<Blob | null> {
  const query = new URLSearchParams({ t: String(Date.now()) })
  const response = await fetch(apiUrl(`${browserPath(projectId, threadId)}/frame?${query}`), {
    method: 'GET',
    headers: { Accept: 'image/jpeg' },
    cache: 'no-store',
    signal,
  })

  // A session may not exist yet, or it may not have produced its first frame.
  if (response.status === 404 || response.status === 204) return null
  if (!response.ok) {
    let message = `Browser preview failed (${response.status})`
    try {
      const body = (await response.json()) as ErrorResponse
      if (body.error) message = body.error
    } catch {
      // Keep the status-bearing fallback when the response is not JSON.
    }
    throw new Error(message)
  }
  return response.blob()
}

export function listProjectGitBranches(projectId: string, signal?: AbortSignal) {
  return request<GitBranchState>(`/api/projects/${encodeURIComponent(projectId)}/git/branches`, { signal })
}

function gitBranchesPath(projectId: string, threadId: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/git/branches`
}

export function createGitBranch(projectId: string, threadId: string, name: string) {
  return request<GitBranchState>(gitBranchesPath(projectId, threadId), {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function switchGitBranch(projectId: string, threadId: string, name: string) {
  return request<GitBranchState>(`${gitBranchesPath(projectId, threadId)}/switch`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export function uploadPiImage(id: string, image: Blob, signal?: AbortSignal) {
  return request<{ path: string }>(`/api/projects/${encodeURIComponent(id)}/pi/images`, {
    method: 'POST',
    headers: { 'Content-Type': image.type || 'application/octet-stream' },
    body: image,
    signal,
  })
}

function shellWindowsPath(id: string, threadId: string) {
  return `/api/projects/${encodeURIComponent(id)}/threads/${encodeURIComponent(threadId)}/shell/windows`
}

export function createShellWindow(id: string, threadId: string) {
  return request<TmuxWindow[]>(shellWindowsPath(id, threadId), { method: 'POST' })
}

export function selectShellWindow(id: string, threadId: string, index: number) {
  return request<TmuxWindow[]>(
    `${shellWindowsPath(id, threadId)}/${index}/select`,
    { method: 'POST' },
  )
}
