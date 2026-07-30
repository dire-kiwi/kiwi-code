import { createSlice, type PayloadAction } from '@reduxjs/toolkit'
import { isCodingAgent } from '../../codingAgents'
import { guardedStoredStateCodec } from '../../lib/storedState'
import type { CodingAgent, PiPresentation } from '../../types'
import { scanStoredKeys, type EnumerableStorage, type PersistWriter } from '../persistence'
import type { RootState } from '../rootReducer'

export type ThreadWorkspaceEntry = {
  codingAgent?: CodingAgent
  piPresentation?: PiPresentation
  claudePresentation?: PiPresentation
  // Subagent threads are read-only views of someone else's workspace, so their
  // choices must never be written back.
  persist: boolean
}

export type ThreadWorkspaceState = {
  byThread: Record<string, ThreadWorkspaceEntry>
}

export const initialThreadWorkspaceState: ThreadWorkspaceState = { byThread: {} }

export function threadWorkspaceKey(projectId: string, threadId: string) {
  return `${projectId}:${threadId}`
}

const codingAgentCodec = guardedStoredStateCodec((raw) => raw, isCodingAgent)

const presentationCodec = guardedStoredStateCodec(
  (raw) => raw,
  (value): value is PiPresentation => value === 'native' || value === 'terminal',
)

// Each field binds its own codec behind a uniform signature, so the table stays
// a single list while every entry keeps its own value type.
type ThreadWorkspaceField = {
  prefix: string
  hydrate: (entry: ThreadWorkspaceEntry, raw: string) => void
  encode: (entry: ThreadWorkspaceEntry) => string | undefined
}

// Storage keys are a compatibility contract with installed browsers. Never rename.
const persistedFields: ThreadWorkspaceField[] = [
  {
    prefix: 'kiwi-code:coding-agent:',
    hydrate: (entry, raw) => {
      const value = codingAgentCodec.decode(raw)
      if (value !== undefined) entry.codingAgent = value
    },
    encode: (entry) => entry.codingAgent && codingAgentCodec.encode(entry.codingAgent),
  },
  {
    prefix: 'kiwi-code:pi-presentation:',
    hydrate: (entry, raw) => {
      const value = presentationCodec.decode(raw)
      if (value !== undefined) entry.piPresentation = value
    },
    encode: (entry) => entry.piPresentation && presentationCodec.encode(entry.piPresentation),
  },
  {
    prefix: 'kiwi-code:claude-presentation:',
    hydrate: (entry, raw) => {
      const value = presentationCodec.decode(raw)
      if (value !== undefined) entry.claudePresentation = value
    },
    encode: (entry) => entry.claudePresentation
      && presentationCodec.encode(entry.claudePresentation),
  },
]

// These keys have unbounded cardinality, so they are discovered by scanning at
// boot rather than hydrated per mount: the panes seed their opened state from
// the presentation on the very first render, and a late correction would mount
// the wrong pane and start a real agent session behind it.
export function hydrateThreadWorkspace(storage: EnumerableStorage | null): ThreadWorkspaceState {
  const byThread: Record<string, ThreadWorkspaceEntry> = {}
  scanStoredKeys(storage, (key, raw) => {
    for (const field of persistedFields) {
      if (!key.startsWith(field.prefix)) continue
      const threadKey = key.slice(field.prefix.length)
      const entry = byThread[threadKey] ?? { persist: true }
      field.hydrate(entry, raw)
      byThread[threadKey] = entry
      return
    }
  })
  return { byThread }
}

export type ThreadWorkspaceRouting = {
  readOnlySubagent: boolean
  initialCodingAgent?: CodingAgent
  initialPresentation?: PiPresentation
}

export type ResolvedThreadWorkspace = {
  codingAgent: CodingAgent
  piPresentation: PiPresentation
  claudePresentation: PiPresentation
  persist: boolean
}

// One resolver shared by the reducer and the selector, so what a thread renders
// and what it commits can never drift apart. Each branch mirrors a load/save
// gate the hook used to carry at its call site: a routed value beats a stored
// one, and a subagent stores nothing.
export function resolveThreadWorkspace(
  stored: ThreadWorkspaceEntry | undefined,
  routing: ThreadWorkspaceRouting,
): ResolvedThreadWorkspace {
  const routedPi = routing.initialCodingAgent === 'pi' ? routing.initialPresentation : undefined
  const routedClaude = routing.initialCodingAgent === 'claude'
    ? routing.initialPresentation
    : undefined
  return {
    persist: !routing.readOnlySubagent,
    codingAgent: routing.readOnlySubagent
      ? 'pi'
      : routing.initialCodingAgent ?? stored?.codingAgent ?? 'pi',
    piPresentation: routing.readOnlySubagent
      ? 'native'
      : routedPi ?? stored?.piPresentation ?? 'native',
    // Unlike the other two, this one is still read for subagents; only an
    // explicitly routed Claude presentation suppresses the stored value.
    claudePresentation: routedClaude ?? stored?.claudePresentation ?? 'terminal',
  }
}

// The committed entry records only what is actually known -- a value read back
// from storage or handed over by routing -- and leaves the rest undefined for
// resolveThreadWorkspace to default. The old hook instead wrote all three keys
// on mount, so merely visiting a thread left three entries behind forever; with
// unbounded thread ids that is what grows localStorage without limit. Omitting
// an unset field reads back identically, since its absence yields that default.
export function mergeThreadWorkspace(
  stored: ThreadWorkspaceEntry | undefined,
  routing: ThreadWorkspaceRouting,
): ThreadWorkspaceEntry {
  const routedPi = routing.initialCodingAgent === 'pi' ? routing.initialPresentation : undefined
  const routedClaude = routing.initialCodingAgent === 'claude'
    ? routing.initialPresentation
    : undefined
  return {
    persist: !routing.readOnlySubagent,
    codingAgent: routing.readOnlySubagent
      ? undefined
      : routing.initialCodingAgent ?? stored?.codingAgent,
    piPresentation: routing.readOnlySubagent ? undefined : routedPi ?? stored?.piPresentation,
    claudePresentation: routedClaude ?? stored?.claudePresentation,
  }
}

function sameEntry(entry: ThreadWorkspaceEntry | undefined, next: ThreadWorkspaceEntry) {
  return entry !== undefined
    && entry.persist === next.persist
    && entry.codingAgent === next.codingAgent
    && entry.piPresentation === next.piPresentation
    && entry.claudePresentation === next.claudePresentation
}

// The mount effect always commits an entry before anything can be clicked, so
// this normally just returns it. Creating one rather than dropping the change
// keeps a mis-ordered dispatch from silently doing nothing; an entry that has
// not been through the mount gate is assumed persistable, which is the only
// state a thread reaches without one.
function entryFor(state: ThreadWorkspaceState, key: string): ThreadWorkspaceEntry {
  state.byThread[key] ??= { persist: true }
  return state.byThread[key]
}

export const threadWorkspaceSlice = createSlice({
  name: 'threadWorkspace',
  initialState: initialThreadWorkspaceState,
  reducers: {
    // Dispatched from a mount effect purely to commit what the selector already
    // resolved. StrictMode runs that effect twice, so an unchanged entry keeps
    // its identity rather than being replaced.
    threadWorkspaceMounted(state, action: PayloadAction<{
      key: string
      routing: ThreadWorkspaceRouting
    }>) {
      const { key, routing } = action.payload
      const next = mergeThreadWorkspace(state.byThread[key], routing)
      if (sameEntry(state.byThread[key], next)) return
      state.byThread[key] = next
    },
    threadCodingAgentChanged(state, action: PayloadAction<{
      key: string
      codingAgent: CodingAgent
    }>) {
      entryFor(state, action.payload.key).codingAgent = action.payload.codingAgent
    },
    threadPiPresentationChanged(state, action: PayloadAction<{
      key: string
      presentation: PiPresentation
    }>) {
      entryFor(state, action.payload.key).piPresentation = action.payload.presentation
    },
    threadClaudePresentationChanged(state, action: PayloadAction<{
      key: string
      presentation: PiPresentation
    }>) {
      entryFor(state, action.payload.key).claudePresentation = action.payload.presentation
    },
  },
})

export const {
  threadClaudePresentationChanged,
  threadCodingAgentChanged,
  threadPiPresentationChanged,
  threadWorkspaceMounted,
} = threadWorkspaceSlice.actions

export const selectThreadWorkspaceEntry = (state: RootState, key: string) =>
  state.threadWorkspace.byThread[key]

export function threadWorkspaceWriters(state: RootState): PersistWriter[] {
  const writers: PersistWriter[] = []
  for (const [key, entry] of Object.entries(state.threadWorkspace.byThread)) {
    if (!entry.persist) continue
    for (const field of persistedFields) {
      const encoded = field.encode(entry)
      if (encoded === undefined) continue
      writers.push({ key: `${field.prefix}${key}`, encode: () => encoded })
    }
  }
  return writers
}
