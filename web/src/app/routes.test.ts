import { describe, expect, it } from 'vitest'
import { workspacePath, workspaceToolFromRoute } from './routes'

describe('agent workspace routes', () => {
  it('generates /agent while preserving the internal pi tool identity', () => {
    expect(workspacePath('project', 'thread', 'pi')).toBe('/projects/project/threads/thread/agent')
    expect(workspaceToolFromRoute('agent')).toBe('pi')
  })

  it('accepts old /pi bookmarks and generates their canonical replacement', () => {
    const tool = workspaceToolFromRoute('pi')
    expect(tool).toBe('pi')
    expect(workspacePath('project', 'thread', tool!)).toBe('/projects/project/threads/thread/agent')
  })

  it('keeps the other workspace routes unchanged', () => {
    expect(workspaceToolFromRoute('shell')).toBe('terminal')
    expect(workspacePath('project', 'thread', 'terminal')).toBe('/projects/project/threads/thread/shell')
    expect(workspaceToolFromRoute('unknown')).toBeNull()
  })
})
