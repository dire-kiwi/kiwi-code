import type { PromptPaste } from '@/prompt-pastes.mjs'

// Keep text drafts while React unmounts a workspace or form during navigation.
// This is deliberately in-memory: a draft should survive switching views, but
// not be written to durable browser storage.
const piNativeDrafts = new Map<string, string>()
const piNativePastes = new Map<string, PromptPaste[]>()
const piNativeWorkflowDismissals = new Set<string>()
const claudeNativeDrafts = new Map<string, string>()
const newThreadDrafts = new Map<string, string>()
const newThreadPastes = new Map<string, PromptPaste[]>()

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

function readPastes(drafts: Map<string, PromptPaste[]>, key: string) {
  return (drafts.get(key) ?? []).map((paste) => ({ ...paste }))
}

function writePastes(drafts: Map<string, PromptPaste[]>, key: string, pastes: PromptPaste[]) {
  if (pastes.length > 0) drafts.set(key, pastes.map((paste) => ({ ...paste })))
  else drafts.delete(key)
}

export function readPiNativeDraft(projectId: string, threadId: string) {
  return readDraft(piNativeDrafts, draftKey(projectId, threadId))
}

export function writePiNativeDraft(projectId: string, threadId: string, value: string) {
  writeDraft(piNativeDrafts, draftKey(projectId, threadId), value)
}

export function readPiNativePastes(projectId: string, threadId: string) {
  return readPastes(piNativePastes, draftKey(projectId, threadId))
}

export function writePiNativePastes(projectId: string, threadId: string, pastes: PromptPaste[]) {
  writePastes(piNativePastes, draftKey(projectId, threadId), pastes)
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

export function readNewThreadPastes(projectId: string) {
  return readPastes(newThreadPastes, projectId)
}

export function writeNewThreadPastes(projectId: string, pastes: PromptPaste[]) {
  writePastes(newThreadPastes, projectId, pastes)
}
