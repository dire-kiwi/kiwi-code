function resolveApiOrigin() {
  const configuredPort = import.meta.env.VITE_KIWI_CODE_API_PORT?.trim()
  if (!configuredPort) return window.location.origin

  const origin = new URL(window.location.origin)
  origin.port = configuredPort
  return origin.origin
}

const apiOrigin = resolveApiOrigin()

export function apiUrl(path: string) {
  return new URL(path, `${apiOrigin}/`).toString()
}

export function apiWebSocketUrl(path: string) {
  const url = new URL(path, `${apiOrigin}/`)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url
}
