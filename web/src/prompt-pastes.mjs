export const LARGE_PASTE_LINE_THRESHOLD = 10
export const LARGE_PASTE_CHARACTER_THRESHOLD = 1_000

const PASTE_MARKER_PATTERN = /\[paste #(\d+)(?: (?:\+\d+ lines|\d+ chars))?\]/g

function normalizePastedText(value) {
  return value.replace(/\r\n?/g, '\n')
}

function pasteMap(pastes) {
  return new Map(pastes.map((paste) => [paste.id, paste.content]))
}

export function expandPromptPastes(value, pastes) {
  const contentById = pasteMap(pastes)
  return value.replace(PASTE_MARKER_PATTERN, (marker, idText) => (
    contentById.get(Number(idText)) ?? marker
  ))
}

export function prunePromptPastes(value, pastes) {
  const referencedIds = new Set()
  for (const match of value.matchAll(PASTE_MARKER_PATTERN)) {
    referencedIds.add(Number(match[1]))
  }
  return pastes.filter((paste) => referencedIds.has(paste.id))
}

export function collapsePromptPaste({
  value,
  selectionStart,
  selectionEnd,
  pastedText,
  pastes,
  maxExpandedLength,
}) {
  const start = Math.max(0, Math.min(selectionStart, value.length))
  const end = Math.max(start, Math.min(selectionEnd, value.length))
  const before = value.slice(0, start)
  const after = value.slice(end)
  let content = normalizePastedText(pastedText)
  const originalLineCount = content.split('\n').length
  const isLargePaste = originalLineCount > LARGE_PASTE_LINE_THRESHOLD
    || content.length > LARGE_PASTE_CHARACTER_THRESHOLD
  if (!isLargePaste) return null

  if (typeof maxExpandedLength === 'number') {
    const retainedLength = expandPromptPastes(before + after, pastes).length
    content = content.slice(0, Math.max(0, maxExpandedLength - retainedLength))
  }

  const retainedPastes = prunePromptPastes(before + after, pastes)
  const lineCount = content.split('\n').length
  const shouldCollapse = lineCount > LARGE_PASTE_LINE_THRESHOLD
    || content.length > LARGE_PASTE_CHARACTER_THRESHOLD
  if (!shouldCollapse) {
    return {
      value: before + content + after,
      pastes: retainedPastes,
      selectionStart: start + content.length,
    }
  }

  const nextId = pastes.reduce((largest, paste) => Math.max(largest, paste.id), 0) + 1
  const marker = lineCount > LARGE_PASTE_LINE_THRESHOLD
    ? `[paste #${nextId} +${lineCount} lines]`
    : `[paste #${nextId} ${content.length} chars]`

  return {
    value: before + marker + after,
    pastes: [...retainedPastes, { id: nextId, content }],
    selectionStart: start + marker.length,
  }
}
