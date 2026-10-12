import { describe, expect, it } from 'vitest'
import { adjacentThread, threadCycleDirection, visibleThreadRows } from './threadCycleShortcut'

function keyEvent(init: KeyboardEventInit) {
  return new KeyboardEvent('keydown', init)
}

describe('threadCycleDirection', () => {
  it('uses the browser tab keys on every platform', () => {
    for (const mac of [true, false]) {
      expect(threadCycleDirection(keyEvent({ ctrlKey: true, key: 'Tab' }), mac)).toBe(1)
      expect(threadCycleDirection(keyEvent({ ctrlKey: true, shiftKey: true, key: 'Tab' }), mac)).toBe(-1)
      expect(threadCycleDirection(keyEvent({ ctrlKey: true, key: 'PageDown' }), mac)).toBe(1)
      expect(threadCycleDirection(keyEvent({ ctrlKey: true, key: 'PageUp' }), mac)).toBe(-1)
    }
  })

  it('adds the Mac browser keys on macOS only', () => {
    expect(threadCycleDirection(keyEvent({ metaKey: true, altKey: true, key: 'ArrowRight' }), true)).toBe(1)
    expect(threadCycleDirection(keyEvent({ metaKey: true, altKey: true, key: 'ArrowLeft' }), true)).toBe(-1)
    expect(threadCycleDirection(keyEvent({ metaKey: true, shiftKey: true, key: '}', code: 'BracketRight' }), true)).toBe(1)
    expect(threadCycleDirection(keyEvent({ metaKey: true, shiftKey: true, key: '{', code: 'BracketLeft' }), true)).toBe(-1)
    expect(threadCycleDirection(keyEvent({ metaKey: true, altKey: true, key: 'ArrowRight' }), false)).toBeNull()
  })

  it('ignores plain Tab and other chords', () => {
    expect(threadCycleDirection(keyEvent({ key: 'Tab' }), false)).toBeNull()
    expect(threadCycleDirection(keyEvent({ shiftKey: true, key: 'Tab' }), false)).toBeNull()
    expect(threadCycleDirection(keyEvent({ ctrlKey: true, altKey: true, key: 'Tab' }), false)).toBeNull()
    expect(threadCycleDirection(keyEvent({ ctrlKey: true, shiftKey: true, key: 'PageDown' }), false)).toBeNull()
    // Ctrl+Alt+Up/Down belongs to the profile rail.
    expect(threadCycleDirection(keyEvent({ ctrlKey: true, altKey: true, key: 'ArrowDown' }), false)).toBeNull()
  })
})

describe('adjacentThread', () => {
  const rows = ['a', 'b', 'c'].map((threadId) => ({ projectId: 'p', threadId }))

  it('steps and wraps at both ends', () => {
    expect(adjacentThread(rows, 'a', 1)?.threadId).toBe('b')
    expect(adjacentThread(rows, 'c', 1)?.threadId).toBe('a')
    expect(adjacentThread(rows, 'a', -1)?.threadId).toBe('c')
  })

  it('starts from an end when the selected thread is not listed', () => {
    expect(adjacentThread(rows, null, 1)?.threadId).toBe('a')
    expect(adjacentThread(rows, 'gone', -1)?.threadId).toBe('c')
    expect(adjacentThread([], 'a', 1)).toBeNull()
  })
})

describe('visibleThreadRows', () => {
  it('lists rows in document order, skipping collapsed and closed sections', () => {
    const container = document.createElement('div')
    container.innerHTML = `
      <ul><li data-thread-row data-project-id="p" data-thread-id="one"></li></ul>
      <ul hidden><li data-thread-row data-project-id="q" data-thread-id="collapsed"></li></ul>
      <details><summary>Settled</summary><ul><li data-thread-row data-project-id="p" data-thread-id="settled"></li></ul></details>
      <details open><summary>Settled</summary><ul><li data-thread-row data-project-id="r" data-thread-id="open"></li></ul></details>
    `
    expect(visibleThreadRows(container)).toEqual([
      { projectId: 'p', threadId: 'one' },
      { projectId: 'r', threadId: 'open' },
    ])
  })
})
