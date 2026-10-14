const ESC = 0x1b
const BEL = 0x07
const BACKSLASH = 0x5c
// "]52;" after ESC introduces an OSC 52 clipboard write.
const OSC_CLIPBOARD_PREFIX = [0x5d, 0x35, 0x32, 0x3b]
// Matches xterm.js's OSC payload limit, so oversized writes are still dropped.
const PAYLOAD_LIMIT = 10_000_000

type ScannerState = 'ground' | 'prefix' | 'payload' | 'payload-escape'

export function decodeOscClipboard(data: string): string | null {
  const separator = data.indexOf(';')
  if (separator < 0) return null

  const encoded = data.slice(separator + 1)
  if (encoded === '?') return null

  try {
    const binary = window.atob(encoded)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return null
  }
}

// Finds OSC 52 clipboard writes in a terminal output stream, including ones
// split across WebSocket messages, and reports each decoded selection. The
// output itself is left untouched for the terminal emulator to parse.
export function createOscClipboardScanner(onClipboard: (text: string) => void) {
  const textEncoder = new TextEncoder()
  let state: ScannerState = 'ground'
  let prefixIndex = 0
  let payload: Uint8Array[] = []
  let payloadLength = 0

  function reset() {
    state = 'ground'
    prefixIndex = 0
    payload = []
    payloadLength = 0
  }

  function appendPayload(bytes: Uint8Array) {
    if (bytes.length === 0) return
    payloadLength += bytes.length
    if (payloadLength > PAYLOAD_LIMIT) {
      reset()
      return
    }
    // Chunks are views into the caller's buffer, which may be reused.
    payload.push(bytes.slice())
  }

  function finishPayload() {
    const bytes = new Uint8Array(payloadLength)
    let offset = 0
    for (const chunk of payload) {
      bytes.set(chunk, offset)
      offset += chunk.length
    }
    reset()
    const text = decodeOscClipboard(new TextDecoder().decode(bytes))
    if (text !== null) onClipboard(text)
  }

  return function scan(output: Uint8Array | string) {
    const bytes = typeof output === 'string' ? textEncoder.encode(output) : output
    let index = 0
    while (index < bytes.length) {
      if (state === 'ground') {
        const escape = bytes.indexOf(ESC, index)
        if (escape < 0) return
        state = 'prefix'
        prefixIndex = 0
        index = escape + 1
        continue
      }

      if (state === 'prefix') {
        if (bytes[index] !== OSC_CLIPBOARD_PREFIX[prefixIndex]) {
          // Re-examine this byte: it may be the ESC of the next sequence.
          state = 'ground'
          continue
        }
        index += 1
        prefixIndex += 1
        if (prefixIndex === OSC_CLIPBOARD_PREFIX.length) state = 'payload'
        continue
      }

      if (state === 'payload-escape') {
        if (bytes[index] === BACKSLASH) {
          index += 1
          finishPayload()
          continue
        }
        // Any other byte after ESC aborts the OSC string, and that ESC may
        // introduce the next sequence.
        reset()
        state = 'prefix'
        continue
      }

      let end = index
      while (end < bytes.length && bytes[end] !== BEL && bytes[end] !== ESC) end += 1
      appendPayload(bytes.subarray(index, end))
      index = end
      // An oversized payload resets to ground; keep scanning after it.
      if (state !== 'payload' || end === bytes.length) continue
      index = end + 1
      if (bytes[end] === BEL) finishPayload()
      else state = 'payload-escape'
    }
  }
}
