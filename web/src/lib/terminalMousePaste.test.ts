import { expect, it, vi } from 'vitest'
import { installTerminalMousePaste } from './terminalMousePaste'

function setup(platform = 'Linux x86_64') {
  const host = document.createElement('div')
  const terminal = host.appendChild(document.createElement('div'))
  const focus = vi.fn()
  const mouseReport = vi.fn()
  terminal.addEventListener('mousedown', mouseReport)
  const dispose = installTerminalMousePaste(host, focus, platform)
  return { terminal, focus, mouseReport, dispose }
}

function mouseDown(button: number) {
  return new MouseEvent('mousedown', { button, bubbles: true, cancelable: true })
}

it('keeps Linux selection paste without also reporting a middle press to tmux', () => {
  const { terminal, focus, mouseReport } = setup()
  const positionTextarea = vi.fn()
  const paste = vi.fn()
  terminal.addEventListener('auxclick', positionTextarea)
  terminal.addEventListener('paste', paste)

  // Repeat immediately: separate clicks must still paste independently.
  for (let i = 0; i < 2; i++) {
    const press = mouseDown(1)
    terminal.dispatchEvent(press)
    expect(press.defaultPrevented).toBe(false)
    terminal.dispatchEvent(new MouseEvent('auxclick', { button: 1, bubbles: true }))
    terminal.dispatchEvent(new Event('paste', { bubbles: true }))
  }

  expect(mouseReport).not.toHaveBeenCalled()
  expect(focus).toHaveBeenCalledTimes(2)
  expect(positionTextarea).toHaveBeenCalledTimes(2)
  expect(paste).toHaveBeenCalledTimes(2)
})

it('preserves left and right mouse reporting on Linux', () => {
  const { terminal, focus, mouseReport } = setup()
  terminal.dispatchEvent(mouseDown(0))
  terminal.dispatchEvent(mouseDown(2))
  expect(mouseReport).toHaveBeenCalledTimes(2)
  expect(focus).not.toHaveBeenCalled()
})

it.each(['MacIntel', 'Win32'])('preserves middle mouse reporting on %s', (platform) => {
  const { terminal, mouseReport } = setup(platform)
  terminal.dispatchEvent(mouseDown(1))
  expect(mouseReport).toHaveBeenCalledOnce()
})

it('removes the capture listener when the terminal is disposed', () => {
  const { terminal, focus, mouseReport, dispose } = setup()
  dispose()
  terminal.dispatchEvent(mouseDown(1))
  expect(mouseReport).toHaveBeenCalledOnce()
  expect(focus).not.toHaveBeenCalled()
})
