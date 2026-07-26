export async function reloadFrontend(windowValue = window) {
  const desktopBridge = windowValue.kiwiCodeDesktopApp ?? windowValue.direMuxDesktopApp
  if (desktopBridge?.reloadFrontend) {
    try {
      await desktopBridge.reloadFrontend()
      return 'desktop'
    } catch {
      // A newly loaded preload can briefly outlive an older Electron main
      // process that does not yet have the hard-reload handler.
    }
  }
  windowValue.location.reload()
  return 'browser'
}
