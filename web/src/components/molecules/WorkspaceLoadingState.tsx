import { LoaderCircle } from 'lucide-react'

export function WorkspaceLoadingState() {
  return (
    <div className="grid h-full place-items-center">
      <div className="text-center">
        <LoaderCircle size={20} className="mx-auto animate-spin text-ghost-green" />
        <p className="mt-3 font-mono text-[10px] uppercase tracking-[0.16em] text-ghost-dim">
          Loading workspace
        </p>
      </div>
    </div>
  )
}
