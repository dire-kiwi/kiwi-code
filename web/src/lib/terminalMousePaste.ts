export function installTerminalMousePaste(
  host: HTMLElement,
  focus: () => void,
  platform: string = navigator.platform,
) {
  function handleMouseDown(event: MouseEvent) {
    if (!platform.startsWith('Linux') || event.button !== 1) return

    // Linux supplies PRIMARY through a native paste event. Forwarding this
    // press to tmux as well can invoke its MouseDown2Pane paste-buffer binding.
    // Stop the terminal's other host listeners too (install this first), but
    // leave the default action and auxclick intact so the focused terminal
    // receives the browser's selection paste.
    event.stopImmediatePropagation()
    focus()
  }

  host.addEventListener('mousedown', handleMouseDown, true)
  return () => host.removeEventListener('mousedown', handleMouseDown, true)
}
