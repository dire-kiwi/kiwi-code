import { describe, expect, it } from 'vitest'
import { profileShortcutLabel, profileShortcutTarget } from './profileShortcuts'

const profiles = [
  { id: 'personal', name: 'Personal' },
  { id: 'work', name: 'Work' },
  { id: 'client', name: 'Client' },
]

function keyEvent(init: Partial<KeyboardEventInit> & { altGraph?: boolean }) {
  const event = new KeyboardEvent('keydown', init)
  return Object.assign(event, { getModifierState: (key: string) => key === 'AltGraph' && Boolean(init.altGraph) })
}

describe('profileShortcutTarget', () => {
  it('jumps to a numbered profile with Ctrl+Alt or Cmd+Option', () => {
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, code: 'Digit2', key: '2' }), profiles, 'personal')).toBe('work')
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, code: 'Numpad3', key: '3' }), profiles, 'personal')).toBe('client')
    // macOS reports Option+1 as "¡", so the physical key decides.
    expect(profileShortcutTarget(keyEvent({ metaKey: true, altKey: true, code: 'Digit1', key: '¡' }), profiles, 'work')).toBe('personal')
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, code: 'Digit9', key: '9' }), profiles, 'personal')).toBeNull()
  })

  it('steps through profiles with Up and Down, wrapping at the ends', () => {
    const down = keyEvent({ ctrlKey: true, altKey: true, key: 'ArrowDown', code: 'ArrowDown' })
    const up = keyEvent({ ctrlKey: true, altKey: true, key: 'ArrowUp', code: 'ArrowUp' })
    expect(profileShortcutTarget(down, profiles, 'personal')).toBe('work')
    expect(profileShortcutTarget(down, profiles, 'client')).toBe('personal')
    expect(profileShortcutTarget(up, profiles, 'personal')).toBe('client')
    expect(profileShortcutTarget(up, profiles, 'work')).toBe('personal')
    expect(profileShortcutTarget(down, profiles, 'missing')).toBe('personal')
  })

  it('leaves other key presses, and AltGr typing, alone', () => {
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, code: 'Digit2', key: '2' }), profiles, 'personal')).toBeNull()
    expect(profileShortcutTarget(keyEvent({ altKey: true, key: 'ArrowDown', code: 'ArrowDown' }), profiles, 'personal')).toBeNull()
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, shiftKey: true, key: 'ArrowDown', code: 'ArrowDown' }), profiles, 'personal')).toBeNull()
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, code: 'Digit7', key: '{' }), profiles, 'personal')).toBeNull()
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, altGraph: true, code: 'Digit2', key: '2' }), profiles, 'personal')).toBeNull()
    expect(profileShortcutTarget(keyEvent({ ctrlKey: true, altKey: true, key: 'ArrowDown', code: 'ArrowDown' }), [], 'personal')).toBeNull()
  })
})

describe('profileShortcutLabel', () => {
  it('names the shortcut for the first nine profiles only', () => {
    expect(profileShortcutLabel(1, false)).toBe('Ctrl+Alt+1')
    expect(profileShortcutLabel(2, true)).toBe('⌘⌥2')
    expect(profileShortcutLabel(10, false)).toBe('')
  })
})
