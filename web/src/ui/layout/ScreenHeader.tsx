import { ArrowLeft } from 'lucide-react'
import { IconButton } from '@/ui/buttons/IconButton'
import { OpenSidebarButton } from '@/ui/buttons/OpenSidebarButton'

type ScreenHeaderProps = {
  title: string
  subtitle: string
  backLabel: string
  backDisabled?: boolean
  onOpenSidebar: () => void
  onBack: () => void
}

export function ScreenHeader({
  title,
  subtitle,
  backLabel,
  backDisabled = false,
  onOpenSidebar,
  onBack,
}: ScreenHeaderProps) {
  return (
    <header className="desktop-titlebar-drag-region desktop-titlebar-right-safe flex h-[4.5rem] shrink-0 items-center gap-3 border-b border-ghost-border/70 bg-ghost-panel/95 px-3 sm:px-5">
      <OpenSidebarButton onClick={onOpenSidebar} shrink />
      <IconButton
        type="button"
        size="lg"
        shrink
        variant="ghost"
        onClick={onBack}
        disabled={backDisabled}
        className="disabled:pointer-events-none disabled:opacity-40"
        aria-label={backLabel}
      >
        <ArrowLeft size={17} />
      </IconButton>
      <div className="min-w-0">
        <p className="truncate text-xs font-semibold text-ghost-bright-white">{title}</p>
        <p className="mt-1 truncate font-mono text-[9px] text-ghost-faint">{subtitle}</p>
      </div>
    </header>
  )
}
