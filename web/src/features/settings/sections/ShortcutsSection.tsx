import { Fragment } from 'react'
import { Keyboard } from 'lucide-react'
import { isMacPlatform } from '@/features/project-sidebar/profileShortcuts'
import {
  KEYBOARD_SHORTCUT_GROUPS,
  formatShortcut,
  shortcutCombos,
  shortcutKeyLabel,
  type ShortcutCombo,
} from '@/features/settings/keyboardShortcuts'
import { StatusBadge } from '@/ui/feedback'
import { SectionHeader, Surface } from '@/ui/layout'

type ShortcutsSectionProps = {
  /** Defaults to the running platform; tests pass it to pin the key labels. */
  mac?: boolean
}

function ShortcutKeys({ combo, mac }: { combo: ShortcutCombo; mac: boolean }) {
  return (
    <span className="flex items-center gap-0.5">
      <span className="sr-only">{formatShortcut(combo, mac)}</span>
      {combo.map((key, index) => (
        <kbd
          key={`${key}-${index}`}
          aria-hidden="true"
          className="grid h-5 min-w-5 place-items-center rounded-md border border-ghost-border/80 bg-ghost-raised px-1.5 font-mono text-[9px] leading-none text-ghost-bright-white shadow-[inset_0_-1px_0_rgba(0,0,0,0.35)]"
        >
          {shortcutKeyLabel(key, mac)}
        </kbd>
      ))}
    </span>
  )
}

export function ShortcutsSection({ mac = isMacPlatform() }: ShortcutsSectionProps) {
  return (
    <Surface as="section" variant="elevated-panel" className="overflow-hidden">
      <SectionHeader
        icon={<Keyboard size={16} />}
        title="Keyboard shortcuts"
        description="Shortcuts available across Kiwi Code. Terminal and composer shortcuts apply while that pane has focus."
        tone="yellow"
        badge={<StatusBadge>{mac ? 'macOS' : 'Windows & Linux'}</StatusBadge>}
      />

      <div className="space-y-5 p-4 sm:p-5">
        {KEYBOARD_SHORTCUT_GROUPS.map((group) => (
          <section key={group.id} aria-labelledby={`shortcut-group-${group.id}`}>
            <h3
              id={`shortcut-group-${group.id}`}
              className="text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint"
            >
              {group.label}
            </h3>
            <dl className="mt-1.5 divide-y divide-ghost-border/40 rounded-xl border border-ghost-border/55 bg-ghost-black/25">
              {group.shortcuts.map((shortcut) => (
                <div key={shortcut.id} className="flex items-center justify-between gap-4 px-3.5 py-2.5">
                  <dt className="min-w-0">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-[10px] font-medium text-ghost-white">{shortcut.label}</span>
                      {shortcut.context && <StatusBadge>{shortcut.context}</StatusBadge>}
                    </span>
                    {shortcut.detail && (
                      <span className="mt-0.5 block text-[9px] leading-4 text-ghost-faint">{shortcut.detail}</span>
                    )}
                  </dt>
                  <dd className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                    {shortcutCombos(shortcut, mac).map((combo, index) => (
                      <Fragment key={combo.join('+')}>
                        {index > 0 && <span className="text-[9px] text-ghost-faint">or</span>}
                        <ShortcutKeys combo={combo} mac={mac} />
                      </Fragment>
                    ))}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Surface>
  )
}
