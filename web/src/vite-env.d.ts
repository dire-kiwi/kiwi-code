/// <reference types="vite/client" />

declare module '*.css'
declare module '@fontsource-variable/jetbrains-mono'

type KiwiCodeDesktopBrowserIdentity = {
  projectId: string
  threadId: string
}

type KiwiCodeDesktopBrowserBounds = {
  x: number
  y: number
  width: number
  height: number
}

type KiwiCodeDesktopBrowserResult = void | Promise<unknown>

type KiwiCodeDesktopBrowserState = {
  projectId: string | null
  threadId: string | null
  visible: boolean
  currentTargetId: string | null
}

interface KiwiCodeDesktopBrowserBridge {
  show(input: KiwiCodeDesktopBrowserIdentity & { bounds: KiwiCodeDesktopBrowserBounds }): KiwiCodeDesktopBrowserResult
  hide(input: KiwiCodeDesktopBrowserIdentity): KiwiCodeDesktopBrowserResult
  setBounds(input: KiwiCodeDesktopBrowserIdentity & { bounds: KiwiCodeDesktopBrowserBounds }): KiwiCodeDesktopBrowserResult
  setBackendOrigin(origin: string): KiwiCodeDesktopBrowserResult
  onState(callback: (state: KiwiCodeDesktopBrowserState) => void): () => void
  onWorkspaceShortcut(callback: (index: number) => void): () => void
}

type KiwiCodeDesktopCodeServerStatus = 'idle' | 'starting' | 'loading' | 'ready' | 'error'

type KiwiCodeDesktopCodeServerState = {
  projectId: string
  threadId: string
  visible: boolean
  status: KiwiCodeDesktopCodeServerStatus
  error: string
}

interface KiwiCodeDesktopCodeServerBridge {
  show(input: KiwiCodeDesktopBrowserIdentity & {
    bounds: KiwiCodeDesktopBrowserBounds
    workspacePath: string
  }): Promise<KiwiCodeDesktopCodeServerState>
  hide(input: KiwiCodeDesktopBrowserIdentity): Promise<KiwiCodeDesktopCodeServerState>
  setBounds(input: KiwiCodeDesktopBrowserIdentity & {
    bounds: KiwiCodeDesktopBrowserBounds
  }): Promise<KiwiCodeDesktopCodeServerState>
  close(input: KiwiCodeDesktopBrowserIdentity): Promise<KiwiCodeDesktopCodeServerState>
  onState(callback: (state: KiwiCodeDesktopCodeServerState) => void): () => void
  onWorkspaceShortcut(callback: (index: number) => void): () => void
}

interface Window {
  kiwiCodeDesktopBrowser?: KiwiCodeDesktopBrowserBridge
  kiwiCodeDesktopCodeServer?: KiwiCodeDesktopCodeServerBridge
}
