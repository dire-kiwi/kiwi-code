import { useEffect, useRef, useState } from 'react'
import {
  Code2,
  ExternalLink,
  LoaderCircle,
  Monitor,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { isDefaultBackendActive } from '@/backends'
import { useDesktopSurfaceBounds } from '@/lib/useDesktopSurfaceBounds'
import type { ConnectionStatus } from '@/types'
import { Button } from '@/ui/buttons'

export type CodeServerPaneProps = {
  projectId: string
  threadId: string
  threadTitle: string
  workspacePath: string
  active: boolean
  suppressed?: boolean
  onStatusChange?: (status: ConnectionStatus) => void
  onWorkspaceShortcut?: (index: number) => void
}

function errorMessage(reason: unknown) {
  return reason instanceof Error && reason.message
    ? reason.message
    : 'The desktop Code workspace could not be displayed.'
}

function statusFor(
  bridge: KiwiCodeDesktopCodeServerBridge | undefined,
  state: KiwiCodeDesktopCodeServerState,
): ConnectionStatus {
  if (!bridge || state.status === 'idle') return 'closed'
  if (state.status === 'error') return 'error'
  if (state.status === 'ready') return 'open'
  return 'connecting'
}

export function CodeServerPane({
  projectId,
  threadId,
  threadTitle,
  workspacePath,
  active,
  suppressed = false,
  onStatusChange,
  onWorkspaceShortcut,
}: CodeServerPaneProps) {
  const localBackendActive = isDefaultBackendActive()
  const desktopBridge = localBackendActive
    ? window.kiwiCodeDesktopCodeServer ?? window.direMuxDesktopCodeServer
    : undefined
  const surfaceRef = useRef<HTMLDivElement>(null)
  const [viewState, setViewState] = useState<KiwiCodeDesktopCodeServerState>({
    projectId,
    threadId,
    visible: false,
    status: desktopBridge ? 'starting' : 'idle',
    error: '',
  })
  const [bridgeError, setBridgeError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const connectionStatus = statusFor(desktopBridge, {
    ...viewState,
    ...(bridgeError ? { status: 'error' as const, error: bridgeError } : {}),
  })
  const visibleError = bridgeError || viewState.error

  useEffect(() => {
    onStatusChange?.(connectionStatus)
  }, [connectionStatus, onStatusChange])

  useEffect(() => {
    if (!desktopBridge) return
    const removeStateListener = desktopBridge.onState((nextState) => {
      if (nextState.projectId !== projectId || nextState.threadId !== threadId) return
      setViewState(nextState)
      if (nextState.status !== 'error') setBridgeError('')
    })
    const removeShortcutListener = desktopBridge.onWorkspaceShortcut((index) => {
      if (active) onWorkspaceShortcut?.(index)
    })
    return () => {
      removeStateListener()
      removeShortcutListener()
    }
  }, [active, desktopBridge, onWorkspaceShortcut, projectId, threadId])

  useEffect(() => {
    if (!desktopBridge) return
    const identity = { projectId, threadId }
    return () => {
      void desktopBridge.close(identity).catch(() => {})
    }
  }, [desktopBridge, projectId, threadId])

  useDesktopSurfaceBounds<KiwiCodeDesktopCodeServerState>({
    surfaceRef,
    owner: desktopBridge,
    identityKey: `${projectId}\0${threadId}\0${workspacePath}\0${retryKey}`,
    enabled: Boolean(desktopBridge && active && !suppressed),
    show: (bounds) => desktopBridge!.show({ projectId, threadId, workspacePath, bounds }),
    setBounds: (bounds) => desktopBridge!.setBounds({ projectId, threadId, bounds }),
    hide: () => desktopBridge!.hide({ projectId, threadId }),
    onBeforeShow: () => {
      setViewState((current) => ({ ...current, status: 'starting', error: '' }))
      setBridgeError('')
    },
    onResult: (result) => {
      setViewState(result)
      setBridgeError(result.status === 'error' ? result.error : '')
    },
    onError: (reason) => setBridgeError(errorMessage(reason)),
  })

  function retry() {
    setBridgeError('')
    setViewState((current) => ({ ...current, status: 'starting', error: '' }))
    setRetryKey((value) => value + 1)
  }

  const loading = desktopBridge && !visibleError && viewState.status !== 'ready'

  return (
    <section
      role="tabpanel"
      aria-label={`${threadTitle} Code workspace`}
      aria-hidden={!active}
      className={`absolute inset-0 bg-ghost-black transition-opacity duration-150 ${
        active ? 'visible opacity-100' : 'pointer-events-none invisible opacity-0'
      }`}
    >
      <div
        ref={surfaceRef}
        className="absolute inset-0 overflow-hidden bg-ghost-black"
        aria-label="Code editor surface"
      >
        {!desktopBridge && (
          <CodeServerEmptyState
            icon={<Monitor size={22} />}
            title={localBackendActive
              ? 'Code is available in the desktop app'
              : 'Code is available for the desktop backend'}
            description={localBackendActive
              ? 'Open this workspace with Kiwi Code Desktop to run code-server in an isolated native view.'
              : 'Switch to the backend paired with this Kiwi Code Desktop app to open its workspace in code-server.'}
            showInstallLink
          />
        )}

        {desktopBridge && suppressed && (
          <CodeServerEmptyState
            icon={<Monitor size={22} />}
            title="Code view temporarily hidden"
            description="Close the open sidebar, finder, or branch menu to return to the native editor surface."
          />
        )}

        {desktopBridge && !suppressed && visibleError && (
          <CodeServerEmptyState
            icon={<TriangleAlert size={22} />}
            tone="error"
            title="Code workspace unavailable"
            description={visibleError}
            actionLabel="Retry"
            onAction={retry}
            showInstallLink
          />
        )}

        {desktopBridge && !suppressed && loading && (
          <CodeServerEmptyState
            icon={<LoaderCircle size={22} className="animate-spin" />}
            title={viewState.status === 'starting' ? 'Starting code-server' : 'Loading Code workspace'}
            description={`Opening ${workspacePath}`}
          />
        )}

        <p className="sr-only" aria-live="polite">
          {!localBackendActive
            ? 'The Code workspace requires the backend paired with Kiwi Code Desktop.'
            : !desktopBridge
              ? 'The Code workspace requires Kiwi Code Desktop.'
              : visibleError
              ? `Code workspace unavailable. ${visibleError}`
              : viewState.status === 'ready'
                ? 'Code workspace ready.'
                : 'Code workspace loading.'}
        </p>
      </div>
    </section>
  )
}

type CodeServerEmptyStateProps = {
  icon: React.ReactNode
  title: string
  description: string
  tone?: 'neutral' | 'error'
  actionLabel?: string
  onAction?: () => void
  showInstallLink?: boolean
}

function CodeServerEmptyState({
  icon,
  title,
  description,
  tone = 'neutral',
  actionLabel,
  onAction,
  showInstallLink = false,
}: CodeServerEmptyStateProps) {
  return (
    <div className="absolute inset-0 grid place-items-center bg-ghost-black px-6 text-center">
      <div className="max-w-lg">
        <span className={`mx-auto grid size-12 place-items-center rounded-xl border bg-ghost-panel ${
          tone === 'error'
            ? 'border-ghost-bright-red/35 text-ghost-bright-red'
            : 'border-ghost-border/80 text-ghost-green'
        }`}>
          {icon}
        </span>
        <p className="mt-4 text-sm font-medium text-ghost-bright-white">{title}</p>
        <p className="mt-1.5 break-words text-[11px] leading-5 text-ghost-muted">{description}</p>
        <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
          {actionLabel && onAction && (
            <Button
              type="button"
              variant="bordered"
              onClick={onAction}
              className="flex h-8 items-center gap-2 rounded-lg px-3 text-[10px]"
            >
              <RefreshCw size={11} />
              {actionLabel}
            </Button>
          )}
          {showInstallLink && (
            <a
              href="https://github.com/coder/code-server"
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-8 items-center gap-2 rounded-lg border border-ghost-border/80 px-3 text-[10px] font-medium text-ghost-dim transition hover:border-ghost-green/45 hover:text-ghost-bright-white"
            >
              <Code2 size={11} />
              code-server setup
              <ExternalLink size={10} />
            </a>
          )}
        </div>
      </div>
    </div>
  )
}
