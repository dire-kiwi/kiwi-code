import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isTerminalEscapeKey,
  shouldBridgeTerminalControl,
  shouldForwardTerminalBlurAsEscape,
  terminalControlSequence,
  terminalClipboardAction,
} from '../src/terminalKeyBridge.mjs'

function key(overrides = {}) {
  return {
    key: '',
    code: '',
    keyCode: 0,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    ...overrides,
  }
}

test('Omarchy clipboard chords copy and cut selections, preserving terminal controls otherwise', () => {
  for (const [letter, action] of [['c', 'copy'], ['x', 'cut']]) {
    const event = key({ key: letter, ctrlKey: true })
    assert.equal(terminalClipboardAction(event, true), action)
    assert.equal(terminalClipboardAction(event, false), null)
  }
  assert.equal(terminalClipboardAction(key({ key: 'v', ctrlKey: true }), false), 'paste')
})

test('supports native clipboard alternatives without stealing plain typing or Alt chords', () => {
  assert.equal(terminalClipboardAction(key({ key: 'Insert', ctrlKey: true }), true), 'copy')
  assert.equal(terminalClipboardAction(key({ key: 'Insert', shiftKey: true }), false), 'paste')
  assert.equal(terminalClipboardAction(key({ key: 'C', ctrlKey: true, shiftKey: true }), true), 'copy')
  assert.equal(terminalClipboardAction(key({ key: 'v', metaKey: true }), false), 'paste')
  assert.equal(terminalClipboardAction(key({ key: 'c', metaKey: true }), true), 'copy')
  assert.equal(terminalClipboardAction(key({ key: 'v' }), true), null)
  assert.equal(terminalClipboardAction(key({ key: 'v', ctrlKey: true, altKey: true }), true), null)
})

test('normalizes modern and legacy browser Escape events', () => {
  for (const event of [
    key({ key: 'Escape', code: 'Escape' }),
    key({ key: 'Esc' }),
    key({ keyCode: 27 }),
  ]) {
    assert.equal(isTerminalEscapeKey(event), true)
    assert.equal(terminalControlSequence(event), '\x1b')
  }
})

test('bridges browser word erase chords without consuming other keys', () => {
  assert.equal(terminalControlSequence(key({ key: 'w', code: 'KeyW', metaKey: true })), '\x17')
  assert.equal(terminalControlSequence(key({ key: 'w', code: 'KeyW', ctrlKey: true })), '\x17')
  assert.equal(terminalControlSequence(key({ key: 'w', code: 'KeyW', metaKey: true, shiftKey: true })), null)
  assert.equal(terminalControlSequence(key({ key: 'Enter', code: 'Enter' })), null)
})

test('allows Escape when Claude returns browser focus to the page body', () => {
  assert.equal(shouldBridgeTerminalControl('\x1b', false, true), true)
  assert.equal(shouldBridgeTerminalControl('\x17', false, true), false)
  assert.equal(shouldBridgeTerminalControl('\x1b', false, false), false)
  assert.equal(shouldBridgeTerminalControl('\x17', true, false), true)
})

test('forwards a browser-swallowed Escape only for a keyboard-caused Pi terminal blur', () => {
  const swallowedEscape = {
    active: true,
    isPiTerminal: true,
    pageStillFocused: true,
    pageHasNeutralFocus: true,
    recentPointerDown: false,
    recentEscapeEvent: false,
  }
  assert.equal(shouldForwardTerminalBlurAsEscape(swallowedEscape), true)
  assert.equal(shouldForwardTerminalBlurAsEscape({ ...swallowedEscape, recentPointerDown: true }), false)
  assert.equal(shouldForwardTerminalBlurAsEscape({ ...swallowedEscape, recentEscapeEvent: true }), false)
  assert.equal(shouldForwardTerminalBlurAsEscape({ ...swallowedEscape, pageStillFocused: false }), false)
  assert.equal(shouldForwardTerminalBlurAsEscape({ ...swallowedEscape, isPiTerminal: false }), false)
})
