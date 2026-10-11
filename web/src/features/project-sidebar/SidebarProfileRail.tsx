import { useEffect, useRef, useState, type FocusEvent, type PointerEvent } from 'react'
import { createPortal } from 'react-dom'
import { LoaderCircle, Plus } from 'lucide-react'
import { classNames } from '@/lib/classNames'
import { useAppSelector } from '@/store/hooks'
import { selectProfileActivityCounts } from '@/store/selectors/workspace'
import type { Profile } from '@/types'
import { CreateProfileDialog } from './CreateProfileDialog'
import { profileInitials } from './profileInitials'
import { isMacPlatform, profileShortcutLabel, profileShortcutTarget } from './profileShortcuts'

type RailTooltip = {
  label: string
  detail: string
  shortcut: string
  top: number
  left: number
}

type ProfileActivity = { working: number; finished: number }

function activityDetail({ working, finished }: ProfileActivity) {
  return [
    finished > 0 ? `${finished} ${finished === 1 ? 'thread needs' : 'threads need'} review` : '',
    working > 0 ? `${working} ${working === 1 ? 'agent' : 'agents'} working` : '',
  ].filter(Boolean).join(', ')
}

// Discord-style: the circle squares off on hover and stays squared while it is
// the current profile. Pixel radii rather than rounded-full so the change animates.
const iconShape = 'relative grid size-10 place-items-center transition-[border-radius,background-color,color] duration-200'
const idleIconShape = 'rounded-[20px] hover:rounded-xl focus-visible:rounded-xl'

export type SidebarProfileRailProps = {
  profiles: Profile[]
  activeProfileId: string
  onSelectProfile: (profileId: string) => void
  onProfileCreated: (profile: Profile) => void
}

/** The column of profile icons down the left edge of the sidebar. */
export function SidebarProfileRail({
  profiles,
  activeProfileId,
  onSelectProfile,
  onProfileCreated,
}: SidebarProfileRailProps) {
  const activityByProfile = useAppSelector(selectProfileActivityCounts)
  const [tooltip, setTooltip] = useState<RailTooltip | null>(null)
  const [creating, setCreating] = useState(false)
  const mac = isMacPlatform()
  const onSelectProfileRef = useRef(onSelectProfile)
  onSelectProfileRef.current = onSelectProfile

  useEffect(() => {
    function handleProfileShortcut(event: KeyboardEvent) {
      const profileId = profileShortcutTarget(event, profiles, activeProfileId)
      if (!profileId) return
      // Capture phase, so a focused terminal or browser stream never receives
      // the keystroke as input.
      event.preventDefault()
      event.stopPropagation()
      if (profileId !== activeProfileId) onSelectProfileRef.current(profileId)
    }

    window.addEventListener('keydown', handleProfileShortcut, true)
    return () => window.removeEventListener('keydown', handleProfileShortcut, true)
  }, [activeProfileId, profiles])

  // The list scrolls, which would clip an absolutely positioned tooltip, so it
  // is portalled out and placed beside the icon it describes.
  function showTooltip(event: PointerEvent<HTMLElement> | FocusEvent<HTMLElement>, label: string, detail: string, shortcut: string) {
    if ('pointerType' in event && event.pointerType === 'touch') return
    const rect = event.currentTarget.getBoundingClientRect()
    setTooltip({ label, detail, shortcut, top: rect.top + rect.height / 2, left: rect.right + 12 })
  }

  const hideTooltip = () => setTooltip(null)

  function tooltipHandlers(label: string, detail = '', shortcut = '') {
    return {
      onPointerEnter: (event: PointerEvent<HTMLElement>) => showTooltip(event, label, detail, shortcut),
      onPointerLeave: hideTooltip,
      onFocus: (event: FocusEvent<HTMLElement>) => showTooltip(event, label, detail, shortcut),
      onBlur: hideTooltip,
    }
  }

  return (
    <nav aria-label="Profiles" className="flex w-16 shrink-0 flex-col border-r border-ghost-border/40 bg-ghost-black">
      <div className="desktop-titlebar-drag-region desktop-profile-rail-titlebar-safe h-2 shrink-0" />
      <ul
        onScroll={hideTooltip}
        onKeyDown={(event) => { if (event.key === 'Escape') hideTooltip() }}
        className="flex min-h-0 flex-1 flex-col items-center gap-2 overflow-y-auto overflow-x-hidden pb-3 [scrollbar-width:none]"
      >
        {profiles.map((profile, index) => {
          const active = profile.id === activeProfileId
          const activity = activityByProfile.get(profile.id) ?? { working: 0, finished: 0 }
          const detail = activityDetail(activity)
          const shortcut = profileShortcutLabel(index + 1, mac)
          return (
            <li key={profile.id} className="group/profile relative flex w-full shrink-0 justify-center">
              <span
                aria-hidden="true"
                className={classNames(
                  'absolute left-0 top-1/2 w-1 -translate-y-1/2 rounded-r-full bg-ghost-bright-white transition-[height] duration-200',
                  active
                    ? 'h-10'
                    : activity.finished > 0
                      ? 'h-2 group-hover/profile:h-5'
                      : 'h-0 group-hover/profile:h-5',
                )}
              />
              <button
                type="button"
                aria-label={detail ? `${profile.name}, ${detail}` : profile.name}
                aria-current={active ? 'true' : undefined}
                aria-keyshortcuts={shortcut ? `${mac ? 'Meta' : 'Control'}+Alt+${index + 1}` : undefined}
                onClick={() => {
                  hideTooltip()
                  if (!active) onSelectProfile(profile.id)
                }}
                {...tooltipHandlers(profile.name, detail, shortcut)}
                className={classNames(
                  iconShape,
                  'text-sm font-semibold',
                  active
                    ? 'rounded-xl bg-ghost-green text-ghost-black'
                    : classNames(idleIconShape, 'bg-ghost-raised text-ghost-white hover:bg-ghost-green hover:text-ghost-black'),
                )}
              >
                <span aria-hidden="true">{profileInitials(profile.name)}</span>
                {activity.finished > 0 ? (
                  <span
                    aria-hidden="true"
                    className="absolute -bottom-1 -right-1 min-w-[1.125rem] rounded-full bg-ghost-green px-1 text-center font-mono text-[9px] font-bold leading-[1.125rem] text-ghost-black ring-[3px] ring-ghost-black"
                  >
                    {activity.finished > 99 ? '99+' : activity.finished}
                  </span>
                ) : activity.working > 0 ? (
                  <span aria-hidden="true" className="absolute -bottom-1 -right-1 grid size-[1.125rem] place-items-center rounded-full bg-ghost-black">
                    <LoaderCircle size={11} className="animate-spin text-ghost-green" />
                  </span>
                ) : null}
              </button>
            </li>
          )
        })}
        <li className="flex w-full shrink-0 justify-center">
          <button
            type="button"
            aria-label="Create a profile"
            onClick={() => {
              hideTooltip()
              setCreating(true)
            }}
            {...tooltipHandlers('Create a profile')}
            className={classNames(iconShape, idleIconShape, 'bg-ghost-raised text-ghost-green hover:bg-ghost-green hover:text-ghost-black')}
          >
            <Plus size={18} />
          </button>
        </li>
      </ul>

      {tooltip && createPortal(
        <div
          aria-hidden="true"
          style={{ top: tooltip.top, left: tooltip.left }}
          className="pointer-events-none fixed z-[100] max-w-56 -translate-y-1/2 rounded-md border border-ghost-border/80 bg-ghost-panel px-2.5 py-1.5 shadow-lg"
        >
          <span className="absolute -left-[5px] top-1/2 size-2 -translate-y-1/2 rotate-45 border-b border-l border-ghost-border/80 bg-ghost-panel" />
          <span className="flex items-baseline gap-2">
            <span className="min-w-0 truncate text-xs font-semibold text-ghost-bright-white">{tooltip.label}</span>
            {tooltip.shortcut && <kbd className="shrink-0 font-mono text-[9px] text-ghost-faint">{tooltip.shortcut}</kbd>}
          </span>
          {tooltip.detail && <span className="block text-[10px] text-ghost-dim">{tooltip.detail}</span>}
        </div>,
        document.body,
      )}

      {creating && (
        <CreateProfileDialog
          onClose={() => setCreating(false)}
          onCreated={(profile) => {
            setCreating(false)
            onProfileCreated(profile)
          }}
        />
      )}
    </nav>
  )
}
