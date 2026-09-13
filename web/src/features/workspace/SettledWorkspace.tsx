import type { Project, Thread } from '@/types'
import { Menu } from 'lucide-react'
import { useAppDispatch } from '@/store/hooks'
import { sidebarOpened } from '@/store/slices/ui'
import { IconButton, PrimaryButton } from '@/ui/buttons'
import { useThreadSettlement } from '@/features/project-sidebar/useThreadSettlement'
import { TerminalWorkspace } from './TerminalWorkspace'

export function SettledWorkspace({ project, thread }: { project: Project; thread: Thread }) {
  const dispatch = useAppDispatch()
  const { settlingThreadId, toggleSettlement } = useThreadSettlement()
  if (!thread.settledAt) return <TerminalWorkspace project={project} thread={thread} />
  return (
    <div className="relative flex h-full flex-col items-center justify-center gap-4 p-6 text-center">
      <IconButton aria-label="Open project navigation" onClick={() => dispatch(sidebarOpened())} className="absolute left-4 top-4 md:hidden"><Menu size={18} /></IconButton>
      <h1 className="text-lg font-semibold text-ghost-bright-white">{thread.title}</h1>
      <p className="max-w-md text-sm text-ghost-muted">This thread is settled. Its sessions are closed; its saved conversations and workspace are kept.</p>
      <PrimaryButton disabled={Boolean(settlingThreadId)} onClick={() => void toggleSettlement(project, thread)}>
        Un-settle and resume
      </PrimaryButton>
    </div>
  )
}
