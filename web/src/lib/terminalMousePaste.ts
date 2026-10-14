export function installTerminalMousePaste(
  host: HTMLElement,
  focus: () => void,
  platform: string = navigator.platform,
) {
  function handleMouseDown(event: MouseEvent) {
    if (!platform.startsWith('Linux') || event.button !== 1) return

    // Linux supplies PRIMARY through a native paste event. Forwarding this
    // press to tmux as well can invoke its MouseDown2Pane paste-buffer binding.
    // Leave the default action intact so the browser still pastes.
    event.stopPropagation()
    focus()
  }

  function handleAuxClick(event: MouseEvent) {
    if (!platform.startsWith('Linux') || event.button !== 1) return

    // Chromium pastes PRIMARY into the focused editable element after
    // auxclick. The press itself can blur the terminal's hidden textarea, so
    // focus it again here to receive the selection paste.
    focus()
  }

  host.addEventListener('mousedown', handleMouseDown, true)
  host.addEventListener('auxclick', handleAuxClick, true)
  return () => {
    host.removeEventListener('mousedown', handleMouseDown, true)
    host.removeEventListener('auxclick', handleAuxClick, true)
  }
}
