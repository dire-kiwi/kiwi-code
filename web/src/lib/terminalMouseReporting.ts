// DEC private modes an application (tmux, with `mouse on`) sets to receive
// mouse input instead of letting the terminal select text locally.
const X10_MOUSE = 9
const NORMAL_MOUSE = 1000
const BUTTON_EVENT_MOUSE = 1002
const ANY_EVENT_MOUSE = 1003
const SGR_MOUSE = 1006

const SHIFT = 4
const ALT = 8
const CTRL = 16
const MOTION = 32
const WHEEL_UP = 64
const WHEEL_DOWN = 65
const NO_BUTTON = 3
// Legacy encoding offsets coordinates by 32 into a single byte; keep them ASCII
// so the UTF-8 input path cannot widen them into multi-byte sequences.
const LEGACY_COORDINATE_LIMIT = 95
const DOM_DELTA_PIXEL = 0
const DOM_DELTA_PAGE = 2

type TrackingMode = 'x10' | 'normal' | 'button' | 'any'
type Cell = { col: number; row: number }

export type TerminalMouseGeometry = {
  left: number
  top: number
  cellWidth: number
  cellHeight: number
  cols: number
  rows: number
}

export type TerminalMouseTarget = {
  getMode: (mode: number) => boolean
  getGeometry: () => TerminalMouseGeometry | null
  send: (data: string) => void
  focus: () => void
}

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max)
}

function consume(event: Event) {
  event.preventDefault()
  event.stopImmediatePropagation()
}

// Replaces ghostty-web's mouse reporting, which also starts a local selection
// on every drag, reports Shift-drags meant for local selection, turns wheel
// events into arrow keys whenever the alternate screen is active, and loses
// releases outside the canvas. Install this on the terminal host before the
// terminal opens so it runs ahead of ghostty-web's own listeners. Like
// xterm.js, holding Shift bypasses reporting for local selection.
export function installTerminalMouseReporting(host: HTMLElement, terminal: TerminalMouseTarget) {
  let pressedButtons = 0
  let lastMotionCell = ''
  let wheelPartialLines = 0

  function trackingMode(): TrackingMode | null {
    if (terminal.getMode(ANY_EVENT_MOUSE)) return 'any'
    if (terminal.getMode(BUTTON_EVENT_MOUSE)) return 'button'
    if (terminal.getMode(NORMAL_MOUSE)) return 'normal'
    if (terminal.getMode(X10_MOUSE)) return 'x10'
    return null
  }

  function cellAt(event: MouseEvent): Cell | null {
    const geometry = terminal.getGeometry()
    if (!geometry || geometry.cellWidth <= 0 || geometry.cellHeight <= 0) return null
    return {
      col: clamp(Math.floor((event.clientX - geometry.left) / geometry.cellWidth) + 1, 1, geometry.cols),
      row: clamp(Math.floor((event.clientY - geometry.top) / geometry.cellHeight) + 1, 1, geometry.rows),
    }
  }

  function modifiers(event: MouseEvent, mode: TrackingMode) {
    if (mode === 'x10') return 0
    return (event.shiftKey ? SHIFT : 0) | (event.altKey ? ALT : 0) | (event.ctrlKey ? CTRL : 0)
  }

  function report(code: number, cell: Cell, released = false) {
    if (terminal.getMode(SGR_MOUSE)) {
      terminal.send(`\x1b[<${code};${cell.col};${cell.row}${released ? 'm' : 'M'}`)
      return
    }
    if (cell.col > LEGACY_COORDINATE_LIMIT || cell.row > LEGACY_COORDINATE_LIMIT) return
    // Legacy releases do not identify the button.
    const legacyCode = released ? (code & ~NO_BUTTON) | NO_BUTTON : code
    terminal.send(`\x1b[M${String.fromCharCode(legacyCode + 32, cell.col + 32, cell.row + 32)}`)
  }

  function isInside(event: Event) {
    return event.target instanceof Node && host.contains(event.target)
  }

  function handleMouseDown(event: MouseEvent) {
    const mode = trackingMode()
    if (!mode || event.shiftKey || event.button > 2) return
    const cell = cellAt(event)
    if (!cell) return

    consume(event)
    terminal.focus()
    pressedButtons |= 1 << event.button
    lastMotionCell = `${cell.col}:${cell.row}`
    report(event.button | modifiers(event, mode), cell)
  }

  function handleMouseMove(event: MouseEvent) {
    const mode = trackingMode()
    if (mode !== 'button' && mode !== 'any') return
    const dragging = pressedButtons !== 0
    if (!dragging && (mode !== 'any' || event.shiftKey || !isInside(event))) return
    const cell = cellAt(event)
    if (!cell) return
    if (isInside(event)) consume(event)
    const cellKey = `${cell.col}:${cell.row}`
    if (cellKey === lastMotionCell) return

    lastMotionCell = cellKey
    const button = [0, 1, 2].find((candidate) => pressedButtons & (1 << candidate)) ?? NO_BUTTON
    report(MOTION | button | modifiers(event, mode), cell)
  }

  function handleMouseUp(event: MouseEvent) {
    const bit = 1 << event.button
    if (!(pressedButtons & bit)) return
    pressedButtons &= ~bit

    const mode = trackingMode()
    if (isInside(event)) consume(event)
    if (!mode || mode === 'x10') return
    const cell = cellAt(event)
    if (cell) report(event.button | modifiers(event, mode), cell, true)
  }

  function handleWheel(event: WheelEvent) {
    const mode = trackingMode()
    if (!mode) return
    consume(event)
    // Shift+wheel is horizontal scrolling; like xterm.js, do not report it.
    if (event.shiftKey || event.deltaY === 0) return
    const geometry = terminal.getGeometry()
    const cell = cellAt(event)
    if (!geometry || !cell) return

    let lines = event.deltaY
    if (event.deltaMode === DOM_DELTA_PIXEL) {
      lines /= geometry.cellHeight
      // Trackpads send many small deltas; damp them as xterm.js does.
      if (Math.abs(event.deltaY) < 50) lines *= 0.3
    } else if (event.deltaMode === DOM_DELTA_PAGE) {
      lines *= geometry.rows
    }
    if (Math.sign(lines) !== Math.sign(wheelPartialLines)) wheelPartialLines = 0
    wheelPartialLines += lines
    const wholeLines = Math.trunc(wheelPartialLines)
    if (wholeLines === 0) return
    wheelPartialLines -= wholeLines
    // One report per wheel event, as xterm.js sends; tmux scrolls several lines each.
    report((wholeLines < 0 ? WHEEL_UP : WHEEL_DOWN) | modifiers(event, mode), cell)
  }

  host.addEventListener('mousedown', handleMouseDown, true)
  host.addEventListener('wheel', handleWheel, { capture: true, passive: false })
  // Drags keep reporting, and must release, outside the terminal.
  document.addEventListener('mousemove', handleMouseMove, true)
  document.addEventListener('mouseup', handleMouseUp, true)
  return () => {
    host.removeEventListener('mousedown', handleMouseDown, true)
    host.removeEventListener('wheel', handleWheel, true)
    document.removeEventListener('mousemove', handleMouseMove, true)
    document.removeEventListener('mouseup', handleMouseUp, true)
  }
}
