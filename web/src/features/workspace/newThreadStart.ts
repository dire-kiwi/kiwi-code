import { isCodingAgent } from '@/codingAgents'
import type { CodingAgentStart } from '@/types'

// The hand-off from "create thread" to the workspace that opens it: which agent,
// model and prompt to start with. It travels in router location state rather
// than the URL because a prompt has no business being in a path, and it is
// cleared once the pane has consumed it.
//
// This lives beside the workspace rather than in App because the workspace is
// its only reader. App's job is to put it in the location state; unpacking it
// one field at a time and passing seven props down was App doing the reader's
// work for it.
export type NewThreadStart = CodingAgentStart & {
  kind: 'new-thread-start'
  projectId: string
  threadId: string
}

/**
 * Location state is attacker-controllable in the sense that it survives history
 * navigation and reloads, so every field is checked before it reaches a pane.
 */
export function newThreadStartFromState(state: unknown): NewThreadStart | null {
  if (!state || typeof state !== 'object') return null
  const candidate = state as Partial<NewThreadStart>
  const hasImagePaths = Array.isArray(candidate.imagePaths)
    && candidate.imagePaths.length > 0
    && candidate.imagePaths.every((path) => typeof path === 'string' && path.trim().length > 0)
  if (
    candidate.kind !== 'new-thread-start'
    || typeof candidate.projectId !== 'string'
    || typeof candidate.threadId !== 'string'
    || !isCodingAgent(candidate.agent)
    || (candidate.presentation !== undefined
      && candidate.presentation !== 'native'
      && candidate.presentation !== 'terminal')
    || (candidate.agent !== 'pi' && candidate.agent !== 'claude'
      && candidate.presentation !== undefined
      && candidate.presentation !== 'terminal')
    || typeof candidate.model !== 'string'
    || typeof candidate.thinkingLevel !== 'string'
    || typeof candidate.prompt !== 'string'
    || (candidate.imagePaths !== undefined && !hasImagePaths)
    || (hasImagePaths && (
      (candidate.agent !== 'pi' && candidate.agent !== 'claude') || candidate.presentation !== 'native'
    ))
  ) {
    return null
  }
  return candidate as NewThreadStart
}
