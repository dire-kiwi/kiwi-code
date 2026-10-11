import type { Profile } from '@/types'

type ProfileShortcutEvent = Pick<
  KeyboardEvent,
  'code' | 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'getModifierState'
>

// Ctrl/Cmd+1-6 already switch workspace tools, so profiles take the Alt/Option
// layer on top of them: Discord's Ctrl+Alt+Up/Down to step, and a digit to jump.
const maxNumberedProfiles = 9

export function isMacPlatform(platform: string = navigator.platform) {
  return /^(Mac|iPhone|iPad)/.test(platform)
}

/** The label shown in the rail tooltip for a profile's jump shortcut. */
export function profileShortcutLabel(position: number, mac = isMacPlatform()) {
  if (position < 1 || position > maxNumberedProfiles) return ''
  return mac ? `⌘⌥${position}` : `Ctrl+Alt+${position}`
}

/**
 * Which profile a key press asks for, or null when it is not a profile
 * shortcut. Ctrl+Alt (Cmd+Option on macOS) with 1-9 picks that profile in rail
 * order; with Up/Down it steps to the previous/next one, wrapping at the ends.
 */
export function profileShortcutTarget(
  event: ProfileShortcutEvent,
  profiles: readonly Profile[],
  activeProfileId: string,
): string | null {
  if (!(event.ctrlKey || event.metaKey) || !event.altKey || event.shiftKey) return null
  // On Windows AltGr arrives as Ctrl+Alt, and on layouts such as German
  // AltGr+7-9 types braces and brackets. Typing must win over switching.
  if (event.getModifierState?.('AltGraph')) return null
  if (profiles.length === 0) return null

  const digit = /^(?:Digit|Numpad)([1-9])$/.exec(event.code)?.[1]
  if (digit) {
    // Read the physical key because Option+1 reports "¡" as its key on macOS,
    // but off macOS insist the key really is the digit: Ctrl+Alt is AltGr on
    // Windows, and a digit that types something else is the user typing.
    if (!event.metaKey && event.key !== digit) return null
    return profiles[Number(digit) - 1]?.id ?? null
  }

  if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return null
  const step = event.key === 'ArrowDown' ? 1 : -1
  const current = profiles.findIndex((profile) => profile.id === activeProfileId)
  if (current === -1) return profiles[step === 1 ? 0 : profiles.length - 1].id
  return profiles[(current + step + profiles.length) % profiles.length].id
}
