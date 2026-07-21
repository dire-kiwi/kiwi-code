import {
  backendHostLabel,
  forgetBackendOrigin as forgetStoredBackendOrigin,
  listBackendOrigins,
  readActiveBackendOrigin,
  rememberBackendOrigin as rememberStoredBackendOrigin,
  selectBackendOrigin as selectStoredBackendOrigin,
} from './backend-config.mjs'

function resolveDefaultBackendOrigin() {
  const configuredUrl = import.meta.env.VITE_KIWI_CODE_API_URL?.trim()
  if (configuredUrl) return new URL(configuredUrl, window.location.href).origin

  const configuredPort = import.meta.env.VITE_KIWI_CODE_API_PORT?.trim()
  if (!configuredPort) return window.location.origin

  const origin = new URL(window.location.origin)
  origin.port = configuredPort
  return origin.origin
}

function backendStorage(): Storage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export const defaultBackendOrigin = resolveDefaultBackendOrigin()
export const activeBackendOrigin = readActiveBackendOrigin(backendStorage(), defaultBackendOrigin)

export type BackendChoice = {
  origin: string
  label: string
  isDefault: boolean
}

export function listBackendChoices(): BackendChoice[] {
  return listBackendOrigins(backendStorage(), defaultBackendOrigin).map((origin) => ({
    origin,
    label: backendHostLabel(origin),
    isDefault: origin === defaultBackendOrigin,
  }))
}

export function rememberBackendOrigin(input: string) {
  return rememberStoredBackendOrigin(backendStorage(), defaultBackendOrigin, input)
}

export function selectBackendOrigin(origin: string) {
  return selectStoredBackendOrigin(backendStorage(), defaultBackendOrigin, origin)
}

export function forgetBackendOrigin(origin: string) {
  return forgetStoredBackendOrigin(backendStorage(), defaultBackendOrigin, origin)
}

export function isDefaultBackendActive() {
  return activeBackendOrigin === defaultBackendOrigin
}
