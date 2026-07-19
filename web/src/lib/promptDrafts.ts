// Keep text drafts while React unmounts a workspace or form during navigation.
// This is deliberately in-memory: a draft should survive switching views, but
// not be written to durable browser storage.
const piNativeDrafts = new Map<string, string>()
const piNativeWorkflowDismissals = new Set<string>()
const claudeNativeDrafts = new Map<string, string>()
const newThreadDrafts = new Map<string, string>()

function draftKey(projectId: string, threadId: string) {
  return `${projectId}:${threadId}`
}

function readDraft(drafts: Map<string, string>, key: string) {
  return drafts.get(key) ?? ''
}

function writeDraft(drafts: Map<string, string>, key: string, value: string) {
  if (value) drafts.set(key, value)
  else drafts.delete(key)
}

export function readPiNativeDraft(projectId: string, threadId: string) {
  return readDraft(piNativeDrafts, draftKey(projectId, threadId))
}

export function writePiNativeDraft(projectId: string, threadId: string, value: string) {
  writeDraft(piNativeDrafts, draftKey(projectId, threadId), value)
}

export function readPiNativeWorkflowDismissed(projectId: string, threadId: string) {
  return piNativeWorkflowDismissals.has(draftKey(projectId, threadId))
}

export function writePiNativeWorkflowDismissed(projectId: string, threadId: string, dismissed: boolean) {
  const key = draftKey(projectId, threadId)
  if (dismissed) piNativeWorkflowDismissals.add(key)
  else piNativeWorkflowDismissals.delete(key)
}

export function readClaudeNativeDraft(projectId: string, threadId: string) {
  return readDraft(claudeNativeDrafts, draftKey(projectId, threadId))
}

export function writeClaudeNativeDraft(projectId: string, threadId: string, value: string) {
  writeDraft(claudeNativeDrafts, draftKey(projectId, threadId), value)
}

export function readNewThreadDraft(projectId: string) {
  return readDraft(newThreadDrafts, projectId)
}

export function writeNewThreadDraft(projectId: string, value: string) {
  writeDraft(newThreadDrafts, projectId, value)
}
