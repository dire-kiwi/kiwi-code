import { describe, expect, it, vi } from 'vitest'
import { createOscClipboardScanner, decodeOscClipboard } from './oscClipboard'

const encoder = new TextEncoder()

function clipboardSequence(text: string, terminator = '\x07') {
  const bytes = encoder.encode(text)
  const encoded = window.btoa(String.fromCharCode(...bytes))
  return `\x1b]52;c;${encoded}${terminator}`
}

function setup() {
  const onClipboard = vi.fn()
  const scan = createOscClipboardScanner(onClipboard)
  return { onClipboard, scan }
}

describe('decodeOscClipboard', () => {
  it('decodes UTF-8 selections and ignores clipboard queries', () => {
    expect(decodeOscClipboard(`c;${window.btoa('hello')}`)).toBe('hello')
    expect(decodeOscClipboard('c;?')).toBeNull()
    expect(decodeOscClipboard('no-separator')).toBeNull()
    expect(decodeOscClipboard('c;not base64!')).toBeNull()
  })
})

describe('createOscClipboardScanner', () => {
  it('reports BEL- and ST-terminated selections surrounded by other output', () => {
    const { onClipboard, scan } = setup()
    scan(encoder.encode(`\x1b[31mred\x1b[0m${clipboardSequence('first')}more\x1b]0;title\x07`))
    scan(`${clipboardSequence('second ✓', '\x1b\\')}\x1b[H`)
    expect(onClipboard.mock.calls).toEqual([['first'], ['second ✓']])
  })

  it('reassembles selections split across output chunks', () => {
    const { onClipboard, scan } = setup()
    const bytes = encoder.encode(`before${clipboardSequence('split across chunks', '\x1b\\')}after`)
    for (const byte of bytes) scan(Uint8Array.of(byte))
    expect(onClipboard).toHaveBeenCalledExactlyOnceWith('split across chunks')
  })

  it('does not keep views into reused output buffers', () => {
    const { onClipboard, scan } = setup()
    const sequence = encoder.encode(clipboardSequence('stable'))
    const buffer = new Uint8Array(sequence.length)
    const middle = Math.floor(sequence.length / 2)
    buffer.set(sequence.subarray(0, middle))
    scan(buffer.subarray(0, middle))
    buffer.fill(0x41)
    buffer.set(sequence.subarray(middle), 0)
    scan(buffer.subarray(0, sequence.length - middle))
    expect(onClipboard).toHaveBeenCalledExactlyOnceWith('stable')
  })

  it('ignores other escape sequences and aborted clipboard strings', () => {
    const { onClipboard, scan } = setup()
    scan('\x1b]520;c;aGk=\x07\x1b[52;1H\x1b]52;c;aGk=\x1b[0m')
    expect(onClipboard).not.toHaveBeenCalled()
    // The ESC that aborted the string can still start the next sequence.
    scan(`\x1b]52;c;aGk=${clipboardSequence('next')}`)
    expect(onClipboard).toHaveBeenCalledExactlyOnceWith('next')
  })
})
