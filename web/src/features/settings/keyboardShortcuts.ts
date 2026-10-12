/**
 * The keyboard shortcuts listed on the Shortcuts settings page. The handlers
 * live with their features (App, TerminalWorkspace, the profile rail, the
 * terminal key bridge, the Electron shell); keep this catalog in step with them.
 *
 * `Mod` is Cmd on macOS and Ctrl elsewhere, for handlers that accept either.
 * `Ctrl` is the Control key on every platform.
 */
export type ShortcutKey = 'Mod' | 'Ctrl' | 'Alt' | 'Shift' | (string & {})

export type ShortcutCombo = ShortcutKey[]

export type KeyboardShortcut = {
  id: string
  label: string
  /** Alternative combos; any of them triggers the action. */
  keys: ShortcutCombo[]
  /** Replaces `keys` on macOS when the Mac keyboard has no equivalent. */
  macKeys?: ShortcutCombo[]
  /** Where the shortcut applies, when it is not everywhere. */
  context?: string
  detail?: string
}

export type KeyboardShortcutGroup = {
  id: string
  label: string
  shortcuts: KeyboardShortcut[]
}

export const KEYBOARD_SHORTCUT_GROUPS: KeyboardShortcutGroup[] = [
  {
    id: 'threads',
    label: 'Threads',
    shortcuts: [
      {
        id: 'new-thread',
        label: 'New thread in the current project',
        keys: [['Ctrl', 'N']],
        detail: 'Opens the project picker when more than one project is active.',
      },
      {
        id: 'thread-next',
        label: 'Next thread',
        keys: [['Ctrl', 'Tab'], ['Ctrl', 'PgDn']],
        macKeys: [['Ctrl', 'Tab'], ['Mod', 'Alt', 'Right'], ['Mod', 'Shift', ']']],
        detail: 'The keys browsers use to switch tabs. Follows the sidebar order and wraps at the end.',
      },
      {
        id: 'thread-previous',
        label: 'Previous thread',
        keys: [['Ctrl', 'Shift', 'Tab'], ['Ctrl', 'PgUp']],
        macKeys: [['Ctrl', 'Shift', 'Tab'], ['Mod', 'Alt', 'Left'], ['Mod', 'Shift', '[']],
      },
      {
        id: 'start-thread',
        label: 'Start the thread',
        keys: [['Mod', 'Enter']],
        context: 'New thread prompt',
      },
      {
        id: 'reorder',
        label: 'Move a project or thread up or down',
        keys: [['Up'], ['Down']],
        context: 'Sidebar',
        detail: 'While the item’s drag handle has keyboard focus.',
      },
      {
        id: 'reload-app',
        label: 'Reload Kiwi Code',
        keys: [['Mod', 'R']],
        context: 'Desktop app',
      },
    ],
  },
  {
    id: 'workspace',
    label: 'Workspace tools',
    shortcuts: [
      { id: 'tool-agent', label: 'Agent', keys: [['Mod', '1']] },
      { id: 'tool-shell', label: 'Shell', keys: [['Mod', '2']] },
      { id: 'tool-nvim', label: 'Neovim', keys: [['Mod', '3']] },
      { id: 'tool-lazygit', label: 'Lazygit', keys: [['Mod', '4']] },
      { id: 'tool-process', label: 'Process', keys: [['Mod', '5']] },
      { id: 'tool-browser', label: 'Browser', keys: [['Mod', '6']] },
    ],
  },
  {
    id: 'profiles',
    label: 'Coding agent profiles',
    shortcuts: [
      {
        id: 'profile-jump',
        label: 'Switch to profile 1–9',
        keys: [['Mod', 'Alt', '1–9']],
        detail: 'Profiles are numbered in sidebar rail order.',
      },
      { id: 'profile-previous', label: 'Previous profile', keys: [['Mod', 'Alt', 'Up']] },
      { id: 'profile-next', label: 'Next profile', keys: [['Mod', 'Alt', 'Down']] },
    ],
  },
  {
    id: 'composer',
    label: 'Agent composer',
    shortcuts: [
      {
        id: 'composer-send',
        label: 'Send the message',
        keys: [['Enter']],
        detail: 'While the agent is working, the message is queued as a follow-up.',
      },
      { id: 'composer-newline', label: 'Insert a new line', keys: [['Shift', 'Enter']] },
      {
        id: 'composer-steer',
        label: 'Steer the running agent',
        keys: [['Mod', 'Enter']],
        context: 'Pi Native',
      },
      { id: 'composer-suggestion-move', label: 'Move through command suggestions', keys: [['Up'], ['Down']] },
      { id: 'composer-suggestion-accept', label: 'Accept the selected suggestion', keys: [['Tab'], ['Enter']] },
      { id: 'composer-suggestion-dismiss', label: 'Dismiss suggestions', keys: [['Esc']] },
    ],
  },
  {
    id: 'terminal',
    label: 'Terminal',
    shortcuts: [
      {
        id: 'terminal-copy',
        label: 'Copy the selection',
        keys: [['Mod', 'C'], ['Ctrl', 'Insert']],
        macKeys: [['Mod', 'C']],
        detail: 'With nothing selected, Ctrl+C reaches the program as an interrupt.',
      },
      {
        id: 'terminal-cut',
        label: 'Copy and clear the selection',
        keys: [['Mod', 'X']],
      },
      {
        id: 'terminal-paste',
        label: 'Paste',
        keys: [['Mod', 'V'], ['Shift', 'Insert']],
        macKeys: [['Mod', 'V']],
      },
      {
        id: 'terminal-word-erase',
        label: 'Delete the previous word',
        keys: [['Mod', 'W']],
        detail: 'Sent to the terminal instead of closing the tab or window.',
      },
    ],
  },
  {
    id: 'browser',
    label: 'Browser',
    shortcuts: [
      {
        id: 'browser-address',
        label: 'Focus the address bar',
        keys: [['Mod', 'L']],
        context: 'Web app',
      },
      {
        id: 'browser-reload',
        label: 'Reload the page',
        keys: [['Mod', 'R']],
        context: 'Web app',
      },
    ],
  },
]

const macKeyLabels: Record<string, string> = {
  Mod: '⌘',
  Ctrl: '⌃',
  Alt: '⌥',
  Shift: '⇧',
  Enter: '↩',
}

const otherKeyLabels: Record<string, string> = {
  Mod: 'Ctrl',
}

const sharedKeyLabels: Record<string, string> = {
  Up: '↑',
  Down: '↓',
  Left: '←',
  Right: '→',
}

/** How one key of a combo is printed on a keycap for this platform. */
export function shortcutKeyLabel(key: ShortcutKey, mac: boolean): string {
  return (mac ? macKeyLabels[key] : otherKeyLabels[key]) ?? sharedKeyLabels[key] ?? key
}

/**
 * A combo as one string: Mac-style glyphs run together (⌘⌥1), everything else
 * is joined with plus signs (Ctrl+Alt+1), matching the profile rail tooltip.
 */
export function formatShortcut(combo: ShortcutCombo, mac: boolean): string {
  return combo.map((key) => shortcutKeyLabel(key, mac)).join(mac ? '' : '+')
}

export function shortcutCombos(shortcut: KeyboardShortcut, mac: boolean): ShortcutCombo[] {
  return mac && shortcut.macKeys ? shortcut.macKeys : shortcut.keys
}
