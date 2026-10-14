import { afterEach, expect, it, vi } from 'vitest'
import { installTerminalMouseReporting } from './terminalMouseReporting'

const cleanups: (() => void)[] = []

afterEach(() => {
  while (cleanups.length > 0) cleanups.pop()?.()
})

function setup(modes: number[] = [1000, 1002, 1006]) {
  const host = document.body.appendChild(document.createElement('div'))
  const canvas = host.appendChild(document.createElement('canvas'))
  const send = vi.fn()
  const focus = vi.fn()
  const localHandler = vi.fn()
  // Stands in for ghostty-web's own selection and mouse listeners.
  canvas.addEventListener('mousedown', localHandler)
  canvas.addEventListener('mousemove', localHandler)
  canvas.addEventListener('mouseup', localHandler)
  canvas.addEventListener('wheel', localHandler)
  const enabledModes = new Set(modes)
  const dispose = installTerminalMouseReporting(host, {
    getMode: (mode) => enabledModes.has(mode),
    getGeometry: () => ({ left: 100, top: 50, cellWidth: 10, cellHeight: 20, cols: 80, rows: 24 }),
    send,
    focus,
  })
  cleanups.push(() => {
    dispose()
    host.remove()
  })
  return { host, canvas, send, focus, localHandler, enabledModes, dispose }
}

function mouse(type: string, clientX: number, clientY: number, init: MouseEventInit = {}) {
  return new MouseEvent(type, { clientX, clientY, bubbles: true, cancelable: true, ...init })
}

function wheel(deltaY: number, init: WheelEventInit = {}) {
  return new WheelEvent('wheel', { clientX: 105, clientY: 55, deltaY, bubbles: true, cancelable: true, ...init })
}

it('reports presses, drags, and releases in SGR encoding instead of selecting locally', () => {
  const { canvas, send, focus, localHandler } = setup()
  const press = mouse('mousedown', 125, 95, { button: 0 })
  canvas.dispatchEvent(press)
  canvas.dispatchEvent(mouse('mousemove', 129, 99, { buttons: 1 }))
  canvas.dispatchEvent(mouse('mousemove', 145, 95, { buttons: 1, ctrlKey: true }))
  canvas.dispatchEvent(mouse('mouseup', 145, 95, { button: 0 }))

  expect(press.defaultPrevented).toBe(true)
  expect(focus).toHaveBeenCalledOnce()
  expect(localHandler).not.toHaveBeenCalled()
  // The move within the same cell is not repeated.
  expect(send.mock.calls).toEqual([
    ['\x1b[<0;3;3M'],
    ['\x1b[<48;5;3M'],
    ['\x1b[<0;5;3m'],
  ])
})

it('keeps reporting a drag outside the terminal and clamps it to the grid', () => {
  const { canvas, send } = setup()
  canvas.dispatchEvent(mouse('mousedown', 105, 55, { button: 0 }))
  document.body.dispatchEvent(mouse('mousemove', 2_000, 10, { buttons: 1 }))
  document.body.dispatchEvent(mouse('mouseup', 2_000, 10, { button: 0 }))
  expect(send.mock.calls).toEqual([
    ['\x1b[<0;1;1M'],
    ['\x1b[<32;80;1M'],
    ['\x1b[<0;80;1m'],
  ])
})

it('leaves Shift-drags and untracked terminals to local selection', () => {
  const { canvas, send, localHandler, enabledModes } = setup()
  canvas.dispatchEvent(mouse('mousedown', 105, 55, { button: 0, shiftKey: true }))
  canvas.dispatchEvent(mouse('mouseup', 105, 55, { button: 0, shiftKey: true }))
  enabledModes.clear()
  canvas.dispatchEvent(mouse('mousedown', 105, 55, { button: 0 }))
  canvas.dispatchEvent(wheel(100))
  expect(send).not.toHaveBeenCalled()
  expect(localHandler).toHaveBeenCalledTimes(4)
})

it('reports hover motion only in any-event mode and outside a local selection', () => {
  const { canvas, send, enabledModes } = setup()
  canvas.dispatchEvent(mouse('mousemove', 105, 55))
  expect(send).not.toHaveBeenCalled()

  enabledModes.add(1003)
  canvas.dispatchEvent(mouse('mousemove', 115, 55, { altKey: true }))
  canvas.dispatchEvent(mouse('mousemove', 125, 55, { shiftKey: true }))
  document.body.dispatchEvent(mouse('mousemove', 135, 55))
  expect(send.mock.calls).toEqual([['\x1b[<43;2;1M']])
})

it('does not report a release whose press was handled elsewhere', () => {
  const { canvas, send } = setup()
  canvas.dispatchEvent(mouse('mouseup', 105, 55, { button: 1 }))
  expect(send).not.toHaveBeenCalled()
})

it('turns wheel events into one wheel report each instead of arrow keys', () => {
  const { canvas, send, localHandler } = setup()
  const mouseWheel = wheel(-100)
  canvas.dispatchEvent(mouseWheel)
  canvas.dispatchEvent(wheel(3, { deltaMode: 1 }))
  expect(mouseWheel.defaultPrevented).toBe(true)
  expect(localHandler).not.toHaveBeenCalled()
  expect(send.mock.calls).toEqual([['\x1b[<64;1;1M'], ['\x1b[<65;1;1M']])
})

it('accumulates small trackpad deltas and ignores horizontal Shift+wheel', () => {
  const { canvas, send } = setup()
  for (let i = 0; i < 5; i++) canvas.dispatchEvent(wheel(20))
  canvas.dispatchEvent(wheel(-100, { shiftKey: true }))
  // 20px / 20px cells * 0.3 damping crosses one line on the fourth event.
  expect(send.mock.calls).toEqual([['\x1b[<65;1;1M']])
})

it('uses legacy encoding when SGR mode is off', () => {
  const { canvas, send } = setup([1000])
  canvas.dispatchEvent(mouse('mousedown', 105, 55, { button: 2 }))
  canvas.dispatchEvent(mouse('mousemove', 125, 55, { buttons: 4 }))
  canvas.dispatchEvent(mouse('mouseup', 105, 55, { button: 2 }))
  expect(send.mock.calls).toEqual([['\x1b[M"!!'], ['\x1b[M#!!']])
})

it('removes its listeners when disposed', () => {
  const { canvas, send, localHandler, dispose } = setup()
  dispose()
  canvas.dispatchEvent(mouse('mousedown', 105, 55, { button: 0 }))
  expect(send).not.toHaveBeenCalled()
  expect(localHandler).toHaveBeenCalledOnce()
})
