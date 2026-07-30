import { apiUrl, apiWebSocketUrl } from './apiUrl'
import type {
  AgentSkillStatus,
  AppSettings,
  BrowserActionRequest,
  BrowserActionResponse,
  DirectorySuggestion,
  GitBranchState,
  LocalEnvironment,
  ProcessWindow,
  Profile,
  Project,
  SandboxConfig,
  SandboxConfigState,
  Thread,
  SavedWorkflow,
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

export async function decodeApiError(response: Response, fallback: string): Promise<string> {
  let text = ''
  try {
    text = (await response.text()).trim()
  } catch {
    return fallback
  }
  if (!text) return fallback
  try {
    const body = JSON.parse(text) as ErrorResponse
    return typeof body?.error === 'string' && body.error.trim()
      ? body.error.trim()
      : fallback
  } catch {
    return text
  }
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
    throw new Error(await decodeApiError(response, `Request failed (${response.status})`))
  }

  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

function jsonRequest<T>(
  path: string,
  method: 'POST' | 'PUT' | 'PATCH',
  body: unknown,
  init?: Omit<RequestInit, 'body' | 'method'>,
) {
  return request<T>(path, {
    ...init,
    method,
    body: JSON.stringify(body),
  })
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

export function updateSettings(input: string | Partial<Pick<
  AppSettings,
  'worktreeBasePath' | 'archivedThreadRetentionDays' | 'orphanedWorktreeRetentionDays' | 'subAgentNestingDepth'
  | 'disableWorkflows' | 'workflowKeywordTriggerEnabled' | 'workflowSizeGuideline' | 'codingAgents' | 'theme'
>>) {
  return jsonRequest<AppSettings>(
    '/api/settings',
    'PUT',
    typeof input === 'string' ? { worktreeBasePath: input } : input,
  )
}

export function updateGlobalSandboxConfig(config: SandboxConfig) {
  return jsonRequest<SandboxConfigState>('/api/sandbox/config', 'PUT', config)
}

export function updateThreadSandboxConfig(projectId: string, threadId: string, config: SandboxConfig) {
  return jsonRequest<SandboxConfigState>(
    `/api/projects/${projectId}/threads/${threadId}/sandbox/config`,
    'PUT',
    config,
  )
}

export function touchThreadTmuxActivity(projectId: string, threadId: string, signal?: AbortSignal) {
  return request<void>(`${threadPath(projectId, threadId)}/tmux/activity`, { method: 'PUT', signal })
}

export function installAgentSkill() {
  return request<AgentSkillStatus>('/api/settings/agent-skills', { method: 'POST' })
}

export function createProfile(name: string) {
  return jsonRequest<Profile>('/api/profiles', 'POST', { name })
}

export function createProject(input: { name: string; path: string; profileId: string }) {
  return jsonRequest<Project>('/api/projects', 'POST', input)
}

export function updateProject(
  id: string,
  input: {
    profileId?: string
    subAgentNestingDepthOverride?: number | null
    worktreeBranchPrefix?: string
    environment?: LocalEnvironment
    figmaMCPEnabled?: boolean
  },
) {
  return jsonRequest<Project>(`/api/projects/${encodeURIComponent(id)}`, 'PATCH', input)
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
  return jsonRequest<ProcessWindow>(
    `${threadPath(projectId, threadId)}/environment/actions/${encodeURIComponent(actionId)}`,
    'POST',
    {},
  )
}

export function updateProjectFigmaMCPEnabled(id: string, enabled: boolean) {
  return updateProject(id, { figmaMCPEnabled: enabled })
}

export function updateProjectOrder(profileId: string, projectIds: string[]) {
  return jsonRequest<void>('/api/projects/order', 'PUT', { profileId, projectIds })
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
  return jsonRequest<Thread>(
    `/api/projects/${encodeURIComponent(projectId)}/threads`,
    'POST',
    input,
  )
}

function threadPath(projectId: string, threadId: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}`
}

export function updateThreadTitle(projectId: string, threadId: string, title: string) {
  return jsonRequest<Thread>(
    threadPath(projectId, threadId),
    'PATCH',
    { title, autoGenerated: false },
  )
}

export function setThreadTitleLocked(projectId: string, threadId: string, titleLocked: boolean) {
  return jsonRequest<Thread>(threadPath(projectId, threadId), 'PATCH', { titleLocked })
}

function workflowPath(projectId: string, threadId: string, runId?: string) {
  const base = `${threadPath(projectId, threadId)}/workflows`
  return runId ? `${base}/${encodeURIComponent(runId)}` : base
}

export function pauseWorkflow(projectId: string, threadId: string, runId: string) {
  return jsonRequest<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/pause`, 'POST', {})
}

export function resumeWorkflow(projectId: string, threadId: string, runId: string) {
  return jsonRequest<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/resume`, 'POST', {})
}

export function stopWorkflow(projectId: string, threadId: string, runId: string) {
  return jsonRequest<WorkflowRun>(`${workflowPath(projectId, threadId, runId)}/stop`, 'POST', {})
}

export function saveWorkflow(
  projectId: string,
  threadId: string,
  runId: string,
  input: { name: string; scope: 'project' | 'personal'; overwrite?: boolean },
) {
  return jsonRequest<SavedWorkflow>(`${workflowPath(projectId, threadId, runId)}/save`, 'POST', input)
}

export function updateThreadLimits(
  projectId: string,
  threadId: string,
  limits: { tokenLimit: number | null; costLimitUsd: number | null },
) {
  return jsonRequest<Thread>(`${threadPath(projectId, threadId)}/limits`, 'PUT', limits)
}

export function setThreadArchived(projectId: string, threadId: string, archived: boolean) {
  return jsonRequest<Thread>(threadPath(projectId, threadId), 'PATCH', { archived })
}

export function deleteThread(projectId: string, threadId: string) {
  return request<void>(threadPath(projectId, threadId), { method: 'DELETE' })
}

export function updateThreadOrder(projectId: string, threadIds: string[]) {
  return jsonRequest<void>(
    `/api/projects/${encodeURIComponent(projectId)}/threads/order`,
    'PUT',
    { threadIds },
  )
}

export function acknowledgePiThreadActivity(projectId: string, threadId: string) {
  return request<void>(`${threadPath(projectId, threadId)}/pi/activity`, { method: 'DELETE' })
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
    throw new Error(await decodeApiError(response, `Could not load the plan (${response.status})`))
  }
  return response.text()
}

function browserPath(projectId: string, threadId: string) {
  return `${threadPath(projectId, threadId)}/browser`
}

export function performBrowserAction<Result = unknown>(
  projectId: string,
  threadId: string,
  action: BrowserActionRequest,
  signal?: AbortSignal,
) {
  return jsonRequest<BrowserActionResponse<Result>>(
    `${browserPath(projectId, threadId)}/actions`,
    'POST',
    action,
    { signal },
  )
}

export function browserStreamUrl(projectId: string, threadId: string) {
  return apiWebSocketUrl(`${browserPath(projectId, threadId)}/stream`).toString()
}

export function browserRecordingDownloadUrl(projectId: string, threadId: string, recordingId: string) {
  return apiUrl(`${browserPath(projectId, threadId)}/recordings/${encodeURIComponent(recordingId)}`)
}

export function browserRecordingPlaybackUrl(projectId: string, threadId: string, recordingId: string) {
  return `${browserRecordingDownloadUrl(projectId, threadId, recordingId)}?disposition=inline`
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
    throw new Error(await decodeApiError(response, `Browser preview failed (${response.status})`))
  }
  return response.blob()
}

function gitBranchesPath(projectId: string, threadId: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/git/branches`
}

export function createGitBranch(projectId: string, threadId: string, name: string) {
  return jsonRequest<GitBranchState>(gitBranchesPath(projectId, threadId), 'POST', { name })
}

export function switchGitBranch(projectId: string, threadId: string, name: string) {
  return jsonRequest<GitBranchState>(
    `${gitBranchesPath(projectId, threadId)}/switch`,
    'POST',
    { name },
  )
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
