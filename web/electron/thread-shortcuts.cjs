'use strict'

// Browser tab-switching keys step between threads in sidebar order. The native
// browser view swallows them before the app renderer sees them, so they are
// forwarded over IPC. Keep in step with threadCycleDirection in
// web/src/features/project-sidebar/threadCycleShortcut.ts.
function threadCycleDirection(input, platform = process.platform) {
  if (!input || input.type !== 'keyDown') return null
  if (input.control && !input.meta && !input.alt) {
    if (input.key === 'Tab') return input.shift ? -1 : 1
    if (!input.shift && input.key === 'PageDown') return 1
    if (!input.shift && input.key === 'PageUp') return -1
  }
  if (platform === 'darwin' && input.meta && !input.control) {
    if (input.alt && !input.shift && input.key === 'ArrowRight') return 1
    if (input.alt && !input.shift && input.key === 'ArrowLeft') return -1
    if (input.shift && !input.alt && input.code === 'BracketRight') return 1
    if (input.shift && !input.alt && input.code === 'BracketLeft') return -1
  }
  return null
}

function interceptThreadCycleShortcut(event, input, onThreadShortcut, platform = process.platform) {
  const direction = threadCycleDirection(input, platform)
  if (!direction) return false
  event.preventDefault()
  onThreadShortcut(direction)
  return true
}

module.exports = {
  interceptThreadCycleShortcut,
  threadCycleDirection,
}
