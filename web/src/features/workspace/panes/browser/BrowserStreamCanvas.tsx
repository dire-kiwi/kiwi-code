// The interactive CDP stream surface: a canvas the socket paints into, with
// every pointer, wheel, key, paste, and IME event translated into a CDP input
// message. Split out because this is the one place in the pane where DOM
// coordinates are converted to page coordinates, and it was easy to lose among
// the other four preview modes.
import type { RefObject } from 'react'
import { inputModifiers } from './browserHelpers'

/** Canvas pixels for a pointer event, which is what CDP expects. */
function pagePoint(canvas: HTMLCanvasElement, clientX: number, clientY: number) {
  const rect = canvas.getBoundingClientRect()
  return {
    x: (clientX - rect.left) * canvas.width / rect.width,
    y: (clientY - rect.top) * canvas.height / rect.height,
  }
}

function mouseButton(button: number) {
  return button === 2 ? 'right' : button === 1 ? 'middle' : 'left'
}

export type BrowserStreamCanvasProps = {
  canvasRef: RefObject<HTMLCanvasElement | null>
  /** False while another viewer holds control; the canvas stays view-only. */
  controller: boolean
  /** Non-zero once the socket has painted a frame; guards the move stream. */
  generation: number
  sendInput: (message: Record<string, unknown>) => void
  onFocusAddressBar: () => void
  onReload: () => void
  onCopySelection: () => void
  onWorkspaceShortcut?: (index: number) => void
}

export function BrowserStreamCanvas({
  canvasRef,
  controller,
  generation,
  sendInput,
  onFocusAddressBar,
  onReload,
  onCopySelection,
  onWorkspaceShortcut,
}: BrowserStreamCanvasProps) {
  return (
    <canvas
      ref={canvasRef}
      tabIndex={controller ? 0 : -1}
      aria-label={controller ? 'Interactive browser content' : 'Browser stream (controlled by another viewer)'}
      className={`h-full w-full object-contain outline-none ${controller ? 'cursor-default focus:ring-2 focus:ring-inset focus:ring-ghost-green/45' : 'cursor-not-allowed'}`}
      onFocus={() => sendInput({ type: 'focus' })}
      onBlur={() => sendInput({ type: 'blur' })}
      onContextMenu={(event) => event.preventDefault()}
      onPointerDown={(event) => {
        if (!controller) return
        event.currentTarget.focus()
        event.currentTarget.setPointerCapture(event.pointerId)
        const { x, y } = pagePoint(event.currentTarget, event.clientX, event.clientY)
        sendInput({ type: 'pointer', event: 'mousePressed', x, y, button: mouseButton(event.button), buttons: event.buttons, clickCount: event.detail || 1, modifiers: inputModifiers(event) })
      }}
      onPointerMove={(event) => {
        if (!controller || !generation) return
        const { x, y } = pagePoint(event.currentTarget, event.clientX, event.clientY)
        sendInput({ type: 'pointer', event: 'mouseMoved', x, y, button: 'none', buttons: event.buttons, clickCount: 0, modifiers: inputModifiers(event) })
      }}
      onPointerUp={(event) => {
        if (!controller) return
        const { x, y } = pagePoint(event.currentTarget, event.clientX, event.clientY)
        sendInput({ type: 'pointer', event: 'mouseReleased', x, y, button: mouseButton(event.button), buttons: event.buttons, clickCount: event.detail || 1, modifiers: inputModifiers(event) })
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
      }}
      onWheel={(event) => {
        if (!controller) return
        event.preventDefault()
        const { x, y } = pagePoint(event.currentTarget, event.clientX, event.clientY)
        sendInput({ type: 'wheel', x, y, deltaX: event.deltaX, deltaY: event.deltaY, modifiers: inputModifiers(event) })
      }}
      onKeyDown={(event) => {
        const command = event.ctrlKey || event.metaKey
        if (command && event.key.toLowerCase() === 'l') {
          event.preventDefault()
          onFocusAddressBar()
          return
        }
        if (command && event.key.toLowerCase() === 'r') {
          event.preventDefault()
          onReload()
          return
        }
        // The chord still goes through to the page; this only mirrors the
        // selection into the host clipboard.
        if (command && (event.key.toLowerCase() === 'c' || event.key.toLowerCase() === 'x')) {
          onCopySelection()
        }
        if (command && !event.altKey && !event.shiftKey && /^[1-7]$/.test(event.key)) {
          event.preventDefault()
          onWorkspaceShortcut?.(Number(event.key))
          return
        }
        event.preventDefault()
        const text = event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey ? event.key : undefined
        sendInput({ type: 'key', event: text ? 'keyDown' : 'rawKeyDown', key: event.key, code: event.code, text, modifiers: inputModifiers(event) })
      }}
      onKeyUp={(event) => {
        event.preventDefault()
        sendInput({ type: 'key', event: 'keyUp', key: event.key, code: event.code, modifiers: inputModifiers(event) })
      }}
      onPaste={(event) => {
        event.preventDefault()
        sendInput({ type: 'text', text: event.clipboardData.getData('text/plain') })
      }}
      onCompositionEnd={(event) => {
        if (event.data) sendInput({ type: 'text', text: event.data })
      }}
    />
  )
}
