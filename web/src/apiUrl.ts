import { activeBackendOrigin } from './backends'

const apiOrigin = activeBackendOrigin

export function apiUrl(path: string) {
  return new URL(path, `${apiOrigin}/`).toString()
}

export function apiWebSocketUrl(path: string) {
  const url = new URL(path, `${apiOrigin}/`)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url
}
