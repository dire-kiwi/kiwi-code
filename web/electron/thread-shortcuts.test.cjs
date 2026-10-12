'use strict'

const assert = require('node:assert/strict')
const test = require('node:test')
const { interceptThreadCycleShortcut, threadCycleDirection } = require('./thread-shortcuts.cjs')

const keyDown = { type: 'keyDown', control: false, meta: false, alt: false, shift: false }

test('browser tab keys step threads on every platform', () => {
  for (const platform of ['linux', 'win32', 'darwin']) {
    assert.equal(threadCycleDirection({ ...keyDown, control: true, key: 'Tab' }, platform), 1)
    assert.equal(threadCycleDirection({ ...keyDown, control: true, shift: true, key: 'Tab' }, platform), -1)
    assert.equal(threadCycleDirection({ ...keyDown, control: true, key: 'PageDown' }, platform), 1)
    assert.equal(threadCycleDirection({ ...keyDown, control: true, key: 'PageUp' }, platform), -1)
  }
})

test('macOS also accepts Cmd+Option+arrows and Cmd+Shift+brackets', () => {
  assert.equal(threadCycleDirection({ ...keyDown, meta: true, alt: true, key: 'ArrowRight' }, 'darwin'), 1)
  assert.equal(threadCycleDirection({ ...keyDown, meta: true, alt: true, key: 'ArrowLeft' }, 'darwin'), -1)
  assert.equal(threadCycleDirection({ ...keyDown, meta: true, shift: true, key: '}', code: 'BracketRight' }, 'darwin'), 1)
  assert.equal(threadCycleDirection({ ...keyDown, meta: true, shift: true, key: '{', code: 'BracketLeft' }, 'darwin'), -1)
  assert.equal(threadCycleDirection({ ...keyDown, meta: true, alt: true, key: 'ArrowRight' }, 'linux'), null)
})

test('other chords and key releases pass through', () => {
  assert.equal(threadCycleDirection({ ...keyDown, key: 'Tab' }, 'linux'), null)
  assert.equal(threadCycleDirection({ ...keyDown, control: true, alt: true, key: 'Tab' }, 'linux'), null)
  assert.equal(threadCycleDirection({ ...keyDown, control: true, shift: true, key: 'PageDown' }, 'linux'), null)
  assert.equal(threadCycleDirection({ ...keyDown, type: 'keyUp', control: true, key: 'Tab' }, 'linux'), null)
})

test('intercepting forwards the direction and blocks the guest page', () => {
  const calls = []
  const event = { preventDefault: () => calls.push('prevented') }
  assert.equal(interceptThreadCycleShortcut(event, { ...keyDown, control: true, key: 'Tab' }, (direction) => calls.push(direction), 'linux'), true)
  assert.deepEqual(calls, ['prevented', 1])
  assert.equal(interceptThreadCycleShortcut(event, { ...keyDown, key: 'x' }, () => calls.push('wrong'), 'linux'), false)
  assert.deepEqual(calls, ['prevented', 1])
})
