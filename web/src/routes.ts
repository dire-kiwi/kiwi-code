import { generatePath } from 'react-router-dom'
import type { WorkspaceTool } from './types'

export const CLEANUP_ROUTE = '/cleanup'
export const SETTINGS_ROUTE = '/settings'
export const TMUX_ROUTE = '/tmux'
export const PROJECT_ROUTE = '/projects/:projectId'
export const PROJECT_SETTINGS_ROUTE = '/projects/:projectId/settings'
export const NEW_THREAD_ROUTE = '/projects/:projectId/threads/new'
export const THREAD_ROUTE = '/projects/:projectId/threads/:threadId'
export const WORKSPACE_ROUTE = '/projects/:projectId/threads/:threadId/:tool'

const routeSegmentByTool: Record<WorkspaceTool, string> = {
  pi: 'pi',
  terminal: 'shell',
  nvim: 'nvim',
  lazygit: 'lazygit',
  process: 'process',
  browser: 'browser',
  code: 'code',
}

const toolByRouteSegment = Object.fromEntries(
  Object.entries(routeSegmentByTool).map(([tool, segment]) => [segment, tool]),
) as Record<string, WorkspaceTool>

export function workspaceToolFromRoute(segment: string | undefined): WorkspaceTool | null {
  return segment ? toolByRouteSegment[segment] ?? null : null
}

export function workspacePath(projectId: string, threadId: string, tool: WorkspaceTool): string {
  return generatePath(WORKSPACE_ROUTE, {
    projectId,
    threadId,
    tool: routeSegmentByTool[tool],
  })
}

export function newThreadPath(projectId: string): string {
  return generatePath(NEW_THREAD_ROUTE, { projectId })
}

export function projectSettingsPath(projectId: string): string {
  return generatePath(PROJECT_SETTINGS_ROUTE, { projectId })
}
