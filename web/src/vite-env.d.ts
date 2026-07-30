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

interface KiwiCodeDesktopAppBridge {
  platform: string
  reloadFrontend(): Promise<unknown>
}

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
  onState(callback: (state: KiwiCodeDesktopBrowserState) => void): () => void
  onWorkspaceShortcut(callback: (index: number) => void): () => void
}

interface Window {
  kiwiCodeDesktopApp?: KiwiCodeDesktopAppBridge
  kiwiCodeDesktopBrowser?: KiwiCodeDesktopBrowserBridge
  /** Compatibility bridge exposed by a desktop renderer loaded before the rename. */
  direMuxDesktopApp?: KiwiCodeDesktopAppBridge
  /** Compatibility bridge exposed by a desktop renderer loaded before the rename. */
  direMuxDesktopBrowser?: KiwiCodeDesktopBrowserBridge
}
