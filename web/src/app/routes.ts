import { generatePath } from 'react-router-dom'
import type { WorkspaceTool } from '@/types'

export const CLEANUP_ROUTE = '/cleanup'
export const SESSION_LOG_ROUTE = '/session-log'
export const SETTINGS_ROUTE = '/settings'
export const SETTINGS_SECTION_ROUTE = '/settings/:section'
export const TMUX_ROUTE = '/tmux'
export const PROJECT_ROUTE = '/projects/:projectId'
export const PROJECT_SETTINGS_ROUTE = '/projects/:projectId/settings'
export const PROJECT_SETTINGS_SECTION_ROUTE = '/projects/:projectId/settings/:section'
export const NEW_THREAD_ROUTE = '/projects/:projectId/threads/new'
export const THREAD_ROUTE = '/projects/:projectId/threads/:threadId'
export const WORKSPACE_ROUTE = '/projects/:projectId/threads/:threadId/:tool'

const routeSegmentByTool: Record<WorkspaceTool, string> = {
  pi: 'agent',
  terminal: 'shell',
  nvim: 'nvim',
  lazygit: 'lazygit',
  process: 'process',
  browser: 'browser',
}

const toolByRouteSegment = Object.fromEntries(
  Object.entries(routeSegmentByTool).map(([tool, segment]) => [segment, tool]),
) as Record<string, WorkspaceTool>

export function workspaceToolFromRoute(segment: string | undefined): WorkspaceTool | null {
  // Keep old bookmarks valid while the internal tool/tmux identity stays pi.
  if (segment === 'pi') return 'pi'
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

export function projectSettingsPath(projectId: string, section?: string): string {
  return section
    ? generatePath(PROJECT_SETTINGS_SECTION_ROUTE, { projectId, section })
    : generatePath(PROJECT_SETTINGS_ROUTE, { projectId })
}

export function settingsPath(section?: string): string {
  return section ? generatePath(SETTINGS_SECTION_ROUTE, { section }) : SETTINGS_ROUTE
}
