import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import {
  codingAgentTargetForSelection,
  isCodingAgent,
  isCodingAgentSelection,
} from '../../codingAgents'
import { guardedStoredStateCodec } from '../../lib/storedState'
import type { CodingAgent, CodingAgentSelection } from '../../types'
import { scanStoredKeys, type EnumerableStorage, type PersistWriter } from '../persistence'
import type { RootState } from '../rootReducer'

export type ThreadLocation = 'project' | 'worktree'

export type AgentModelPreferences = {
  model: string
  thinkingLevel: string
}

export type NewThreadPreferences = {
  location: ThreadLocation
  baseBranch: string
  codingAgent: CodingAgentSelection
  agentModels: Partial<Record<CodingAgent, AgentModelPreferences>>
}

// Storage keys are a compatibility contract with installed browsers. Never rename.
const newThreadPreferencesPrefix = 'kiwi-code:new-thread-preferences:'

export function newThreadPreferencesStorageKey(projectId: string) {
  return `${newThreadPreferencesPrefix}${projectId}`
}

// Lifted from the component unchanged, migration included: rewriting it would
// risk dropping preferences saved by an older build, so it is only relocated.
function parseNewThreadPreferences(raw: string): NewThreadPreferences | null {
  const value: unknown = JSON.parse(raw)
  if (!value || typeof value !== 'object') return null
  const candidate = value as Partial<NewThreadPreferences> & Partial<AgentModelPreferences>
  if (
    (candidate.location !== 'project' && candidate.location !== 'worktree')
    || !isCodingAgentSelection(candidate.codingAgent)
    || typeof candidate.baseBranch !== 'string'
  ) {
    return null
  }

  const agentModels: Partial<Record<CodingAgent, AgentModelPreferences>> = {}
  if (candidate.agentModels && typeof candidate.agentModels === 'object') {
    for (const [agent, preferences] of Object.entries(candidate.agentModels)) {
      if (
        isCodingAgent(agent)
        && preferences
        && typeof preferences.model === 'string'
        && typeof preferences.thinkingLevel === 'string'
      ) {
        agentModels[agent] = preferences
      }
    }
  }

  // Migrate preferences saved before model settings were remembered per agent.
  if (typeof candidate.model === 'string' && typeof candidate.thinkingLevel === 'string') {
    const agent = codingAgentTargetForSelection(candidate.codingAgent).agent
    agentModels[agent] ??= {
      model: candidate.model,
      thinkingLevel: candidate.thinkingLevel,
    }
  }

  return {
    location: candidate.location,
    baseBranch: candidate.baseBranch,
    codingAgent: candidate.codingAgent,
    agentModels,
  }
}

export const newThreadPreferencesCodec = guardedStoredStateCodec<NewThreadPreferences>(
  parseNewThreadPreferences,
  (value): value is NewThreadPreferences => value !== null,
  JSON.stringify,
)

export type NewThreadPreferencesState = {
  byProject: Record<string, NewThreadPreferences>
}

export const initialNewThreadPreferencesState: NewThreadPreferencesState = { byProject: {} }

export function hydrateNewThreadPreferences(
  storage: EnumerableStorage | null,
): NewThreadPreferencesState {
  const byProject: Record<string, NewThreadPreferences> = {}
  scanStoredKeys(storage, (key, raw) => {
    if (!key.startsWith(newThreadPreferencesPrefix)) return
    const decoded = newThreadPreferencesCodec.decode(raw)
    if (decoded !== undefined) byProject[key.slice(newThreadPreferencesPrefix.length)] = decoded
  })
  return { byProject }
}

export const newThreadPreferencesSlice = createSlice({
  name: 'newThreadPreferences',
  initialState: initialNewThreadPreferencesState,
  reducers: {
    // Dispatched on submit only. The form's own fields stay in the component:
    // persisting each keystroke would turn this into "what you last typed"
    // rather than "what you last created with".
    newThreadPreferencesRemembered(state, action: PayloadAction<{
      projectId: string
      preferences: NewThreadPreferences
    }>) {
      state.byProject[action.payload.projectId] = action.payload.preferences
    },
  },
})

export const { newThreadPreferencesRemembered } = newThreadPreferencesSlice.actions

export const selectNewThreadPreferences = (
  state: RootState,
  projectId: string,
): NewThreadPreferences | null => state.newThreadPreferences.byProject[projectId] ?? null

export function newThreadPreferencesWriters(state: RootState): PersistWriter[] {
  return Object.entries(state.newThreadPreferences.byProject).map(([projectId, preferences]) => ({
    key: newThreadPreferencesStorageKey(projectId),
    encode: () => newThreadPreferencesCodec.encode(preferences),
  }))
}
