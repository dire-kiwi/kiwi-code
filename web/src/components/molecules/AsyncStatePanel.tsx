import { LoaderCircle } from 'lucide-react'
import { PrimaryButton } from '../atoms/Button'
import { Surface } from '../atoms/Surface'

type LoadingPanelProps = {
  label: string
}

export function LoadingPanel({ label }: LoadingPanelProps) {
  return (
    <Surface className="grid min-h-52 place-items-center">
      <div className="text-center">
        <LoaderCircle size={18} className="mx-auto animate-spin text-ghost-green" />
        <p className="mt-3 text-[10px] uppercase tracking-[0.14em] text-ghost-dim">{label}</p>
      </div>
    </Surface>
  )
}

type LoadErrorPanelProps = {
  message: string
  onRetry: () => void
}

export function LoadErrorPanel({ message, onRetry }: LoadErrorPanelProps) {
  return (
    <Surface variant="elevated-panel" className="p-6 text-center">
      <p className="text-xs text-ghost-bright-red">{message}</p>
      <PrimaryButton type="button" size="md" onClick={onRetry} className="mt-4">
        Try again
      </PrimaryButton>
    </Surface>
  )
}
