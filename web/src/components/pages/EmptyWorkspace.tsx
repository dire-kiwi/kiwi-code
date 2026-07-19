import { FolderPlus, SquareTerminal } from 'lucide-react'
import { PrimaryButton } from '../atoms/Button'
import { OpenSidebarButton } from '../molecules/OpenSidebarButton'

type EmptyWorkspaceProps = {
  loadError: string
  projectCount: number
  profileName: string
  onOpenSidebar: () => void
}

export function EmptyWorkspace({ loadError, projectCount, profileName, onOpenSidebar }: EmptyWorkspaceProps) {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-[4.5rem] shrink-0 items-center border-b border-ghost-border/70 bg-ghost-panel px-3 md:hidden">
        <OpenSidebarButton onClick={onOpenSidebar} responsive={false} />
      </header>
      <div className="relative grid min-h-0 flex-1 place-items-center overflow-hidden px-6">
        <div className="empty-grid absolute inset-0 opacity-30" />
        <div className="relative max-w-sm text-center">
          <div className="mx-auto grid size-14 place-items-center rounded-2xl border border-ghost-border/80 bg-ghost-panel text-ghost-dim shadow-2xl shadow-ghost-black/40">
            {loadError ? <SquareTerminal size={23} /> : <FolderPlus size={23} />}
          </div>
          <h1 className="mt-5 text-base font-semibold tracking-[-0.02em] text-ghost-bright-white">
            {loadError ? 'The server is out of reach' : projectCount === 0 ? `Add a project to ${profileName}` : 'Create a thread'}
          </h1>
          <p className="mt-2 text-xs leading-5 text-ghost-muted">
            {loadError
              ? `${loadError} Start the Go backend, then refresh this page.`
              : projectCount === 0
                ? `This profile is empty. Add a local folder to keep ${profileName} projects together.`
                : 'Use the plus button beside a project to create a thread in the project folder or a Git worktree.'}
          </p>
          {!loadError && projectCount === 0 && (
            <PrimaryButton
              type="button"
              size="md"
              onClick={onOpenSidebar}
              className="mx-auto mt-5 flex items-center gap-2 md:hidden"
            >
              <FolderPlus size={14} />
              Add project
            </PrimaryButton>
          )}
        </div>
      </div>
    </div>
  )
}
