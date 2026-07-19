/// <reference types="vite/client" />

declare module '*.css'
declare module '@fontsource-variable/jetbrains-mono'

type DireMuxDesktopBrowserIdentity = {
  projectId: string
  threadId: string
}

type DireMuxDesktopBrowserBounds = {
  x: number
  y: number
  width: number
  height: number
}

type DireMuxDesktopBrowserResult = void | Promise<unknown>

type DireMuxDesktopBrowserState = {
  projectId: string | null
  threadId: string | null
  visible: boolean
  currentTargetId: string | null
}

interface DireMuxDesktopBrowserBridge {
  show(input: DireMuxDesktopBrowserIdentity & { bounds: DireMuxDesktopBrowserBounds }): DireMuxDesktopBrowserResult
  hide(input: DireMuxDesktopBrowserIdentity): DireMuxDesktopBrowserResult
  setBounds(input: DireMuxDesktopBrowserIdentity & { bounds: DireMuxDesktopBrowserBounds }): DireMuxDesktopBrowserResult
  setBackendOrigin(origin: string): DireMuxDesktopBrowserResult
  onState(callback: (state: DireMuxDesktopBrowserState) => void): () => void
  onWorkspaceShortcut(callback: (index: number) => void): () => void
}

type DireMuxDesktopCodeServerStatus = 'idle' | 'starting' | 'loading' | 'ready' | 'error'

type DireMuxDesktopCodeServerState = {
  projectId: string
  threadId: string
  visible: boolean
  status: DireMuxDesktopCodeServerStatus
  error: string
}

interface DireMuxDesktopCodeServerBridge {
  show(input: DireMuxDesktopBrowserIdentity & {
    bounds: DireMuxDesktopBrowserBounds
    workspacePath: string
  }): Promise<DireMuxDesktopCodeServerState>
  hide(input: DireMuxDesktopBrowserIdentity): Promise<DireMuxDesktopCodeServerState>
  setBounds(input: DireMuxDesktopBrowserIdentity & {
    bounds: DireMuxDesktopBrowserBounds
  }): Promise<DireMuxDesktopCodeServerState>
  close(input: DireMuxDesktopBrowserIdentity): Promise<DireMuxDesktopCodeServerState>
  onState(callback: (state: DireMuxDesktopCodeServerState) => void): () => void
  onWorkspaceShortcut(callback: (index: number) => void): () => void
}

interface Window {
  direMuxDesktopBrowser?: DireMuxDesktopBrowserBridge
  direMuxDesktopCodeServer?: DireMuxDesktopCodeServerBridge
}
