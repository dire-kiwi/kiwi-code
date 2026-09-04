import { useCallback, useEffect, useRef, useState } from 'react'
import { shallowEqual } from 'react-redux'
import { updateThreadWorkspace } from '@/api'
import { codingAgentSelectionForTarget, codingAgentTargetForSelection, isCodingAgentSelection } from '@/codingAgents'
import { useAppSelector } from '@/store/hooks'
import { resolveThreadWorkspace, threadWorkspaceKey, type ThreadWorkspaceRouting, type ThreadWorkspaceEntry } from '@/store/slices/threadWorkspace'
import type { CodingAgentSelection, Thread, WorkspaceTool } from '@/types'

export function resolveServerThreadWorkspace(
  stored: ThreadWorkspaceEntry | undefined,
  routing: ThreadWorkspaceRouting,
  selection: string | undefined,
) {
  const legacy = resolveThreadWorkspace(stored, routing)
  if (!isCodingAgentSelection(selection)) return legacy
  const { agent, presentation } = codingAgentTargetForSelection(selection)
  return {
    ...legacy,
    codingAgent: agent,
    ...(agent === 'pi' ? { piPresentation: presentation } : {}),
    ...(agent === 'claude' ? { claudePresentation: presentation } : {}),
  }
}

type WorkspaceUpdate = { codingAgent?: CodingAgentSelection; activeTab?: WorkspaceTool; initialize?: boolean }

export function useThreadWorkspaceState(projectId: string, thread: Thread, routing: ThreadWorkspaceRouting, routeTool: WorkspaceTool) {
  const key = threadWorkspaceKey(projectId, thread.id)
  const resolved = useAppSelector(
    (state) => resolveServerThreadWorkspace(state.threadWorkspace.byThread[key], routing, thread.codingAgent),
    shallowEqual,
  )
  const [error, setError] = useState('')
  const mounted = useRef(false)
  const queue = useRef(Promise.resolve())
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
  const save = useCallback((update: WorkspaceUpdate) => {
    // Preserve click order on this client. The state socket is authoritative;
    // HTTP responses must not replace newer snapshots from another client.
    queue.current = queue.current.then(async () => {
      try {
        await updateThreadWorkspace(projectId, thread.id, update)
        if (mounted.current) setError('')
      } catch (reason) {
        if (mounted.current) setError(reason instanceof Error ? reason.message : 'Could not save the thread workspace.')
      }
    })
  }, [projectId, thread.id])
  const initial = useRef({
    codingAgent: codingAgentSelectionForTarget(resolved.codingAgent,
      resolved.codingAgent === 'claude' ? resolved.claudePresentation : resolved.piPresentation),
    activeTab: routeTool,
  })
  useEffect(() => {
    if (!thread.codingAgent || !thread.activeTab) {
      save({ ...initial.current, initialize: true })
    }
  }, [save, thread.codingAgent, thread.activeTab])
  return { ...resolved, activeTool: thread.activeTab ?? routeTool, saveWorkspace: save, workspaceError: error }
}
