const backendOriginsStorageKey = 'dire-mux-backend-origins-v1'
const activeBackendStorageKey = 'dire-mux-active-backend-v1'

export function normalizeBackendOrigin(value) {
  if (typeof value !== 'string' || !value.trim()) {
    throw new Error('Enter a backend URL.')
  }

  const input = value.trim()
  const hasScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(input)
  let url
  try {
    url = new URL(hasScheme ? input : `http://${input}`)
  } catch {
    throw new Error('Enter a valid backend URL, such as http://machine-name:4000.')
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('Backend URLs must use HTTP or HTTPS.')
  }
  if (url.username || url.password) {
    throw new Error('Backend URLs cannot include a username or password.')
  }
  if (url.pathname !== '/' || url.search || url.hash) {
    throw new Error('Enter the backend origin without a path, query, or fragment.')
  }
  if (!hasScheme && !url.port) url.port = '4000'
  return url.origin
}

function storedOrigins(storage, defaultOrigin) {
  if (!storage) return []
  let parsed
  try {
    parsed = JSON.parse(storage.getItem(backendOriginsStorageKey) || '[]')
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []

  const origins = []
  const seen = new Set([defaultOrigin])
  for (const value of parsed) {
    try {
      const origin = normalizeBackendOrigin(value)
      if (seen.has(origin)) continue
      seen.add(origin)
      origins.push(origin)
    } catch {
      // Ignore malformed persisted entries so one bad value cannot hide the picker.
    }
  }
  return origins
}

export function listBackendOrigins(storage, defaultValue) {
  const defaultOrigin = normalizeBackendOrigin(defaultValue)
  return [defaultOrigin, ...storedOrigins(storage, defaultOrigin)]
}

export function readActiveBackendOrigin(storage, defaultValue) {
  const origins = listBackendOrigins(storage, defaultValue)
  let selected = ''
  try {
    selected = normalizeBackendOrigin(storage?.getItem(activeBackendStorageKey) || '')
  } catch {
    return origins[0]
  }
  return origins.includes(selected) ? selected : origins[0]
}

function requireStorage(storage) {
  if (!storage) throw new Error('Browser storage is unavailable, so backend choices cannot be saved.')
  return storage
}

export function rememberBackendOrigin(storageValue, defaultValue, input) {
  const storage = requireStorage(storageValue)
  const defaultOrigin = normalizeBackendOrigin(defaultValue)
  const origin = normalizeBackendOrigin(input)
  const saved = storedOrigins(storage, defaultOrigin)
  if (origin !== defaultOrigin && !saved.includes(origin)) saved.push(origin)
  storage.setItem(backendOriginsStorageKey, JSON.stringify(saved))
  storage.setItem(activeBackendStorageKey, origin)
  return origin
}

export function selectBackendOrigin(storageValue, defaultValue, input) {
  const storage = requireStorage(storageValue)
  const origin = normalizeBackendOrigin(input)
  if (!listBackendOrigins(storage, defaultValue).includes(origin)) {
    throw new Error('That backend is not in the saved backend list.')
  }
  storage.setItem(activeBackendStorageKey, origin)
  return origin
}

export function forgetBackendOrigin(storageValue, defaultValue, input) {
  const storage = requireStorage(storageValue)
  const defaultOrigin = normalizeBackendOrigin(defaultValue)
  const origin = normalizeBackendOrigin(input)
  const previousActiveOrigin = readActiveBackendOrigin(storage, defaultOrigin)
  const saved = storedOrigins(storage, defaultOrigin).filter((candidate) => candidate !== origin)
  storage.setItem(backendOriginsStorageKey, JSON.stringify(saved))
  if (previousActiveOrigin === origin) {
    storage.setItem(activeBackendStorageKey, defaultOrigin)
    return defaultOrigin
  }
  return previousActiveOrigin
}

export function backendHostLabel(value) {
  const url = new URL(normalizeBackendOrigin(value))
  return url.host
}

export const backendStorageKeys = Object.freeze({
  active: activeBackendStorageKey,
  origins: backendOriginsStorageKey,
})
