'use strict'

function isFrontendReloadShortcut(input, platform = process.platform) {
  if (
    !input ||
    input.type !== 'keyDown' ||
    typeof input.key !== 'string' ||
    input.key.toLowerCase() !== 'r' ||
    input.alt ||
    input.shift
  ) {
    return false
  }
  if (platform === 'darwin') return input.meta === true && input.control !== true
  return input.control === true && input.meta !== true
}

function interceptFrontendReloadShortcut(event, input, onReload, platform = process.platform) {
  if (!isFrontendReloadShortcut(input, platform)) return false
  event.preventDefault()
  onReload()
  return true
}

function reloadFrontend(appView, workspaces = []) {
  const contents = appView?.webContents
  if (!contents || contents.isDestroyed()) return false
  for (const workspace of workspaces) workspace?.detachActiveView()
  contents.reloadIgnoringCache()
  return true
}

module.exports = {
  interceptFrontendReloadShortcut,
  isFrontendReloadShortcut,
  reloadFrontend,
}
