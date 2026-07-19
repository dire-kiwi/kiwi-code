export const defaultVitePort: number
export const defaultGoPort: number
export const reservedProductionPort: number
export const productionTmuxSocket: string
export const legacyProductionTmuxSocket: string

export function defaultDevelopmentTmuxSocket(rootDirectory: string): string
export function assertDevelopmentPort(port: number, option: string): void
export function assertDevelopmentApiTarget(configuredPort?: string, configuredUrl?: string): void
export function usage(): string

export type DevelopmentStackOptions = {
  desktop: boolean
  loopback: boolean
  addCurrentDirectory: boolean
  help: boolean
  vitePort: number
  goPort: number
  tmuxSocket: string
}

export function parseArgs(args: string[]): DevelopmentStackOptions
