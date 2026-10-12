import { describe, expect, it } from 'vitest'
import { profileShortcutLabel, profileShortcutTarget } from '@/features/project-sidebar/profileShortcuts'
import { terminalClipboardAction, terminalControlSequence } from '@/terminalKeyBridge.mjs'
import {
  KEYBOARD_SHORTCUT_GROUPS,
  formatShortcut,
  shortcutCombos,
  type KeyboardShortcut,
  type ShortcutCombo,
} from './keyboardShortcuts'

function shortcut(id: string): KeyboardShortcut {
  const found = KEYBOARD_SHORTCUT_GROUPS.flatMap((group) => group.shortcuts).find((item) => item.id === id)
  if (!found) throw new Error(`No shortcut ${id}`)
  return found
}

const namedKeys: Record<string, { key: string; code: string }> = {
  Up: { key: 'ArrowUp', code: 'ArrowUp' },
  Down: { key: 'ArrowDown', code: 'ArrowDown' },
  Insert: { key: 'Insert', code: 'Insert' },
}

/** The keydown a user produces by pressing a documented combo. */
function keyEvent(combo: ShortcutCombo, mac: boolean) {
  const last = combo[combo.length - 1]
  const named = namedKeys[last]
  const init: KeyboardEventInit = named ?? {
    key: last.toLowerCase(),
    code: /^\d$/.test(last) ? `Digit${last}` : `Key${last}`,
  }
  init.ctrlKey = combo.includes('Ctrl') || (!mac && combo.includes('Mod'))
  init.metaKey = mac && combo.includes('Mod')
  init.altKey = combo.includes('Alt')
  init.shiftKey = combo.includes('Shift')
  return new KeyboardEvent('keydown', init)
}

describe('formatShortcut', () => {
  it('uses Mac glyphs on macOS and plus-joined names elsewhere', () => {
    expect(formatShortcut(['Mod', 'Alt', 'Up'], true)).toBe('⌘⌥↑')
    expect(formatShortcut(['Mod', 'Alt', 'Up'], false)).toBe('Ctrl+Alt+↑')
    expect(formatShortcut(['Ctrl', 'N'], true)).toBe('⌃N')
    expect(formatShortcut(['Shift', 'Enter'], true)).toBe('⇧↩')
    expect(formatShortcut(['Shift', 'Enter'], false)).toBe('Shift+Enter')
  })

  it('matches the profile rail tooltip', () => {
    for (const mac of [true, false]) {
      expect(formatShortcut(['Mod', 'Alt', '3'], mac)).toBe(profileShortcutLabel(3, mac))
    }
  })

  it('drops keys a Mac keyboard does not have', () => {
    expect(shortcutCombos(shortcut('terminal-paste'), false).map((combo) => formatShortcut(combo, false)))
      .toEqual(['Ctrl+V', 'Shift+Insert'])
    expect(shortcutCombos(shortcut('terminal-paste'), true).map((combo) => formatShortcut(combo, true)))
      .toEqual(['⌘V'])
  })
})

describe('documented shortcuts', () => {
  it('have unique ids', () => {
    const ids = KEYBOARD_SHORTCUT_GROUPS.flatMap((group) => group.shortcuts.map((item) => item.id))
    expect(new Set(ids).size).toBe(ids.length)
  })

  it.each([true, false])('reach the profile and terminal handlers (mac: %s)', (mac) => {
    const profiles = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }, { id: 'c', name: 'C' }]
    const target = (id: string) => profileShortcutTarget(keyEvent(shortcutCombos(shortcut(id), mac)[0], mac), profiles, 'b')
    expect(target('profile-previous')).toBe('a')
    expect(target('profile-next')).toBe('c')

    const clipboard = (id: string) => shortcutCombos(shortcut(id), mac)
      .map((combo) => terminalClipboardAction(keyEvent(combo, mac), true))
    expect(clipboard('terminal-copy').every((action) => action === 'copy')).toBe(true)
    expect(clipboard('terminal-cut')).toEqual(['cut'])
    expect(clipboard('terminal-paste').every((action) => action === 'paste')).toBe(true)

    expect(terminalControlSequence(keyEvent(shortcutCombos(shortcut('terminal-word-erase'), mac)[0], mac))).toBe('\x17')
  })
})
