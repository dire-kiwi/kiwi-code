// Pure helpers for the browser pane: picking the current page out of a status
// payload, turning what the user typed into a URL, and reducing a status
// payload to a connection state.
import type { BrowserActionOperation, ConnectionStatus } from '@/types'
import type { BrowserCurrentPage, BrowserPage, BrowserStatusResult } from '@/wire/domain'

export const framePollIntervalMs = 5_000

export function currentPageFor(
  status: BrowserStatusResult | null,
  pages: BrowserPage[],
): BrowserCurrentPage | null {
  if (status?.current?.id) return status.current
  const page = pages.find((candidate) => candidate.id === status?.currentTargetId) ?? pages[0]
  return page ? { ...page } : null
}

export function pageLabel(page: BrowserPage) {
  const title = page.title?.trim()
  if (title) return title
  const url = page.url?.trim()
  if (!url || url === 'about:blank') return 'New tab'
  try {
    return new URL(url).hostname || url
  } catch {
    return url
  }
}

/** Anything that is not recognisably a host becomes a web search. */
export function navigationURL(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed)) return trimmed
  if (/^(localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:\/|$)/i.test(trimmed)) {
    return `http://${trimmed}`
  }
  if (/^[a-z][a-z\d+.-]*:/i.test(trimmed)) return trimmed
  const looksLikeHost = /^(?:[a-z\d-]+\.)+[a-z\d-]+(?::\d+)?(?:\/|$)/i.test(trimmed)
    || /^\[[a-f\d:]+\](?::\d+)?(?:\/|$)/i.test(trimmed)
  if (/\s/.test(trimmed) || !looksLikeHost) {
    return `https://www.google.com/search?q=${encodeURIComponent(trimmed)}`
  }
  return `https://${trimmed}`
}

export function connectionStatusFor(
  status: BrowserStatusResult | null,
  loading: boolean,
  error: string,
): ConnectionStatus {
  if (error || status?.error) return 'error'
  if (loading && !status) return 'connecting'
  if (status?.reachable === false && status.running !== false) return 'error'
  if (!status || status.running === false) return 'closed'
  if (
    status.reachable === true
    || status.running === true
    || Boolean(status.current)
    || Boolean(status.pages?.length)
  ) {
    return 'open'
  }
  return 'closed'
}

/** Which action a submitted address bar means, given what is currently open. */
export function navigationOperation(
  sessionRunning: boolean,
  hasCurrentPage: boolean,
): BrowserActionOperation {
  if (!sessionRunning) return 'session.start'
  return hasCurrentPage ? 'navigate.goto' : 'tabs.new'
}

export function errorMessage(reason: unknown, fallback: string) {
  return reason instanceof Error && reason.message ? reason.message : fallback
}

export function inputModifiers(
  event: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean },
) {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0)
}

/** Returns the normalised title, or '' when it fails the 2-12 word rule. */
export function validRecordingTitle(value: string) {
  const title = value.replace(/\s+/g, ' ').trim()
  const words = title.split(' ').filter(Boolean)
  return title.length >= 3 && title.length <= 80 && words.length >= 2 && words.length <= 12 ? title : ''
}
