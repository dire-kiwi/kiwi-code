export function installTerminalMousePaste(
  host: HTMLElement,
  focus: () => void,
  platform: string = navigator.platform,
) {
  function handleMouseDown(event: MouseEvent) {
    if (!platform.startsWith('Linux') || event.button !== 1) return

    // Linux supplies PRIMARY through a native paste event. Forwarding this
    // press to tmux as well can invoke its MouseDown2Pane paste-buffer binding.
    // Leave the default action and auxclick intact so xterm can position its
    // textarea under the pointer and receive the browser's selection paste.
    event.stopPropagation()
    focus()
  }

  host.addEventListener('mousedown', handleMouseDown, true)
  return () => host.removeEventListener('mousedown', handleMouseDown, true)
}
