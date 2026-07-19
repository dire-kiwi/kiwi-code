export type BackendStorage = Pick<Storage, 'getItem' | 'setItem'>

export function normalizeBackendOrigin(value: string): string
export function listBackendOrigins(storage: BackendStorage | null, defaultValue: string): string[]
export function readActiveBackendOrigin(storage: BackendStorage | null, defaultValue: string): string
export function rememberBackendOrigin(storage: BackendStorage | null, defaultValue: string, input: string): string
export function selectBackendOrigin(storage: BackendStorage | null, defaultValue: string, input: string): string
export function forgetBackendOrigin(storage: BackendStorage | null, defaultValue: string, input: string): string
export function backendHostLabel(value: string): string
export const backendStorageKeys: Readonly<{ active: string; origins: string }>
