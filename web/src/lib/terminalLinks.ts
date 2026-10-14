import type { TerminalCore } from '@wterm/dom'

// The same pattern as xterm's web-links addon: stop at whitespace, quotes and
// brackets, and leave trailing sentence punctuation outside the link.
const URL_PATTERN = /https?:\/\/[^\s"'!*(){}|\\^<>`]*[^\s"':,.!?{}|\\^~[\]`()<>]/g

/**
 * Finds the http(s) URL covering a column. Each entry in `cells` is the text of
 * one terminal column, with an empty string for wide-glyph continuations.
 */
export function urlAtColumn(cells: readonly string[], col: number): string | null {
  while (col > 0 && cells[col] === '') col -= 1
  if (col < 0 || col >= cells.length) return null

  let text = ''
  let offset = 0
  for (let index = 0; index < cells.length; index += 1) {
    if (index === col) offset = text.length
    text += cells[index]
  }
  for (const match of text.matchAll(URL_PATTERN)) {
    if (offset >= match.index && offset < match.index + match[0].length) return match[0]
  }
  return null
}

function rowCells(core: TerminalCore, row: number) {
  const cells: string[] = []
  for (let col = 0; col < core.getCols(); col += 1) {
    const cell = core.getCell(row, col)
    if (cell.width === 0) cells.push('')
    else cells.push(cell.chars ?? (cell.char > 0x20 ? String.fromCodePoint(cell.char) : ' '))
  }
  return cells
}

function isLinkModifier(event: MouseEvent, platform: string) {
  return platform.startsWith('Mac') ? event.metaKey : event.ctrlKey
}

/**
 * Opens plain-text URLs in the live screen with Cmd-click on macOS and
 * Ctrl-click elsewhere, matching wterm's own OSC 8 hyperlinks. wterm only links
 * OSC 8 output, while agents and shells usually print bare URLs.
 */
export function installTerminalLinks(
  element: HTMLElement,
  getCore: () => TerminalCore | null,
  open: (url: string) => void,
  platform: string = navigator.platform,
) {
  function urlAtPoint(event: MouseEvent) {
    const core = getCore()
    if (!core || !(event.target instanceof Element) || event.target.closest('.term-link')) return null
    const firstRow = element.querySelector('.term-row:not(.term-scrollback-row)')
    const cellWidth = Number.parseFloat(element.style.getPropertyValue('--term-cell-width'))
    if (!firstRow || !(cellWidth > 0)) return null

    const rowRect = firstRow.getBoundingClientRect()
    if (rowRect.height <= 0) return null
    const row = Math.floor((event.clientY - rowRect.top) / rowRect.height)
    const col = Math.floor((event.clientX - rowRect.left) / cellWidth)
    if (row < 0 || row >= core.getRows() || col < 0 || col >= core.getCols()) return null
    return urlAtColumn(rowCells(core, row), col)
  }

  function handleMouseDown(event: MouseEvent) {
    if (event.button !== 0 || !isLinkModifier(event, platform)) return
    const url = urlAtPoint(event)
    if (!url) return

    // Keep the modified press away from the application's mouse reporting.
    event.preventDefault()
    event.stopPropagation()
    open(url)
  }

  function handleMouseMove(event: MouseEvent) {
    const hovering = isLinkModifier(event, platform) && urlAtPoint(event) !== null
    element.classList.toggle('terminal-url-hover', hovering)
  }

  function handleMouseLeave() {
    element.classList.remove('terminal-url-hover')
  }

  element.addEventListener('mousedown', handleMouseDown, true)
  element.addEventListener('mousemove', handleMouseMove)
  element.addEventListener('mouseleave', handleMouseLeave)
  return () => {
    element.removeEventListener('mousedown', handleMouseDown, true)
    element.removeEventListener('mousemove', handleMouseMove)
    element.removeEventListener('mouseleave', handleMouseLeave)
    element.classList.remove('terminal-url-hover')
  }
}
