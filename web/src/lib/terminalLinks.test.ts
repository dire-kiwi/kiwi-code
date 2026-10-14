import type { CellData, TerminalCore } from '@wterm/dom'
import { describe, expect, it, vi } from 'vitest'
import { installTerminalLinks, urlAtColumn } from './terminalLinks'

function cells(text: string) {
  return Array.from(text)
}

describe('urlAtColumn', () => {
  const line = cells('see https://example.com/path?q=1, then more')

  it('finds the URL under any of its columns', () => {
    expect(urlAtColumn(line, 4)).toBe('https://example.com/path?q=1')
    expect(urlAtColumn(line, 31)).toBe('https://example.com/path?q=1')
  })

  it('leaves trailing punctuation and surrounding text unlinked', () => {
    expect(urlAtColumn(line, 32)).toBeNull()
    expect(urlAtColumn(line, 0)).toBeNull()
  })

  it('maps columns after wide glyphs to the right text offset', () => {
    const wide = ['界', '', ' ', ...cells('http://a.test')]
    expect(urlAtColumn(wide, 3)).toBe('http://a.test')
    expect(urlAtColumn(wide, 1)).toBeNull()
  })

  it('ignores columns outside the row', () => {
    expect(urlAtColumn(line, -1)).toBeNull()
    expect(urlAtColumn(line, line.length)).toBeNull()
  })
})

function setup(text: string, platform = 'Linux x86_64') {
  const cols = 40
  const row = text.padEnd(cols)
  const core = {
    getCols: () => cols,
    getRows: () => 1,
    getCell: (_row: number, col: number): CellData => ({ char: row.codePointAt(col) ?? 0, fg: 0, bg: 0, flags: 0 }),
  } as unknown as TerminalCore

  const element = document.createElement('div')
  element.style.setProperty('--term-cell-width', '10px')
  const grid = element.appendChild(document.createElement('div'))
  const rowElement = grid.appendChild(document.createElement('div'))
  rowElement.className = 'term-row'
  rowElement.getBoundingClientRect = () => new DOMRect(0, 0, cols * 10, 20)
  const mouseReport = vi.fn()
  rowElement.addEventListener('mousedown', mouseReport)

  const open = vi.fn()
  const dispose = installTerminalLinks(element, () => core, open, platform)
  return { element, rowElement, open, mouseReport, dispose }
}

function press(target: Element, column: number, init: MouseEventInit) {
  const event = new MouseEvent('mousedown', {
    bubbles: true,
    cancelable: true,
    clientX: column * 10 + 5,
    clientY: 10,
    ...init,
  })
  target.dispatchEvent(event)
  return event
}

describe('installTerminalLinks', () => {
  it('opens a plain URL on Ctrl-click without reporting the press', () => {
    const { rowElement, open, mouseReport } = setup('go to https://kiwi.test now')
    const event = press(rowElement, 8, { ctrlKey: true })
    expect(open).toHaveBeenCalledWith('https://kiwi.test')
    expect(event.defaultPrevented).toBe(true)
    expect(mouseReport).not.toHaveBeenCalled()
  })

  it('uses Cmd-click on macOS', () => {
    const { rowElement, open } = setup('https://kiwi.test', 'MacIntel')
    press(rowElement, 2, { ctrlKey: true })
    expect(open).not.toHaveBeenCalled()
    press(rowElement, 2, { metaKey: true })
    expect(open).toHaveBeenCalledWith('https://kiwi.test')
  })

  it('leaves unmodified clicks and clicks away from URLs to the terminal', () => {
    const { rowElement, open, mouseReport } = setup('go to https://kiwi.test now')
    press(rowElement, 8, {})
    press(rowElement, 1, { ctrlKey: true })
    expect(open).not.toHaveBeenCalled()
    expect(mouseReport).toHaveBeenCalledTimes(2)
  })

  it('leaves OSC 8 hyperlinks to wterm', () => {
    const { rowElement, open } = setup('https://kiwi.test')
    const link = rowElement.appendChild(document.createElement('a'))
    link.className = 'term-link'
    press(link, 2, { ctrlKey: true })
    expect(open).not.toHaveBeenCalled()
  })

  it('shows a pointer while a modifier hovers a URL', () => {
    const { element, rowElement, dispose } = setup('https://kiwi.test')
    const move = (init: MouseEventInit) => rowElement.dispatchEvent(new MouseEvent('mousemove', {
      bubbles: true,
      clientX: 25,
      clientY: 10,
      ...init,
    }))
    move({ ctrlKey: true })
    expect(element.classList.contains('terminal-url-hover')).toBe(true)
    move({})
    expect(element.classList.contains('terminal-url-hover')).toBe(false)
    move({ ctrlKey: true })
    dispose()
    expect(element.classList.contains('terminal-url-hover')).toBe(false)
  })
})
