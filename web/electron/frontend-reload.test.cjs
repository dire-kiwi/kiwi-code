'use strict'

const assert = require('node:assert/strict')
const test = require('node:test')
const {
  interceptFrontendReloadShortcut,
  isFrontendReloadShortcut,
  reloadFrontend,
} = require('./frontend-reload.cjs')

test('frontend reload shortcut follows the native platform modifier', () => {
  const keyDown = { type: 'keyDown', key: 'r', alt: false, shift: false }

  assert.equal(isFrontendReloadShortcut({ ...keyDown, meta: true, control: false }, 'darwin'), true)
  assert.equal(isFrontendReloadShortcut({ ...keyDown, meta: false, control: true }, 'darwin'), false)
  assert.equal(isFrontendReloadShortcut({ ...keyDown, meta: false, control: true }, 'linux'), true)
  assert.equal(isFrontendReloadShortcut({ ...keyDown, meta: true, control: false }, 'win32'), false)
})

test('frontend reload shortcut rejects modified and non-keydown input', () => {
  const shortcut = { type: 'keyDown', key: 'R', meta: true, control: false, alt: false, shift: false }

  assert.equal(isFrontendReloadShortcut({ ...shortcut, type: 'keyUp' }, 'darwin'), false)
  assert.equal(isFrontendReloadShortcut({ ...shortcut, key: 'x' }, 'darwin'), false)
  assert.equal(isFrontendReloadShortcut({ ...shortcut, alt: true }, 'darwin'), false)
  assert.equal(isFrontendReloadShortcut({ ...shortcut, shift: true }, 'darwin'), false)
  assert.equal(isFrontendReloadShortcut({ ...shortcut, control: true }, 'darwin'), false)
})

test('frontend reload shortcut is consumed before requesting a hard reload', () => {
  const calls = []
  const event = { preventDefault: () => calls.push('prevent') }
  const input = { type: 'keyDown', key: 'r', meta: true, control: false, alt: false, shift: false }

  assert.equal(interceptFrontendReloadShortcut(event, input, () => calls.push('reload'), 'darwin'), true)
  assert.deepEqual(calls, ['prevent', 'reload'])
  assert.equal(interceptFrontendReloadShortcut(event, { ...input, key: 'x' }, () => calls.push('reload'), 'darwin'), false)
  assert.deepEqual(calls, ['prevent', 'reload'])
})

test('hard reload detaches native views and ignores the frontend cache', () => {
  const calls = []
  const appView = {
    webContents: {
      isDestroyed: () => false,
      reloadIgnoringCache: () => calls.push('reload'),
    },
  }
  const browserWorkspace = { detachActiveView: () => calls.push('browser') }

  assert.equal(reloadFrontend(appView, [browserWorkspace]), true)
  assert.deepEqual(calls, ['browser', 'reload'])
})

test('hard reload leaves workspaces alone after the frontend is destroyed', () => {
  let detached = false
  const appView = {
    webContents: {
      isDestroyed: () => true,
      reloadIgnoringCache: () => assert.fail('destroyed contents must not reload'),
    },
  }

  assert.equal(reloadFrontend(appView, [{ detachActiveView: () => { detached = true } }]), false)
  assert.equal(detached, false)
})
