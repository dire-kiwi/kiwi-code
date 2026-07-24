#!/usr/bin/env node

import { createHash } from 'node:crypto'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import concurrently from 'concurrently'
import { defaultDevelopmentTmuxSocket, parseArgs, usage } from './dev-stack-options.mjs'

let options
try {
  options = parseArgs(process.argv.slice(2))
} catch (error) {
  console.error(`dev stack: ${error.message}\n`)
  console.error(usage())
  process.exit(1)
}

if (options.help) {
  process.stdout.write(usage())
  process.exit(0)
}

const webDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const rootDirectory = path.resolve(webDirectory, '..')
const listenHost = options.desktop || options.loopback ? '127.0.0.1' : '0.0.0.0'
const browserHost = '127.0.0.1'
const viteUrl = `http://${browserHost}:${options.vitePort}`
const goUrl = `http://${browserHost}:${options.goPort}`
const tmuxSocket = options.tmuxSocket || defaultDevelopmentTmuxSocket(rootDirectory)
const currentDirectoryFlag = options.addCurrentDirectory ? ' -add-current-directory' : ''
const providerInstance = createHash('sha256')
  .update(`${rootDirectory}\0${options.vitePort}\0${options.goPort}\0${process.pid}`)
  .digest('hex')
  .slice(0, 16)
const providerConfig = path.resolve(
  rootDirectory,
  process.env.KIWI_CODE_BROWSER_PROVIDER_CONFIG ||
    path.join(os.tmpdir(), `kiwi-code-browser-provider-${providerInstance}.json`),
)
const sharedEnvironment = {
  KIWI_CODE_BROWSER_BACKEND: options.desktop ? 'electron' : 'headless',
  KIWI_CODE_BROWSER_PROVIDER_CONFIG: providerConfig,
  KIWI_CODE_API_ORIGIN: goUrl,
  KIWI_CODE_ELECTRON_USER_DATA: path.join(os.tmpdir(), `kiwi-code-electron-${providerInstance}`),
}

console.log(`Vite: ${viteUrl}`)
console.log(`Go:   ${goUrl}`)
console.log(`tmux: ${tmuxSocket}`)
if (options.addCurrentDirectory) console.log(`Project: ${rootDirectory}`)

const commands = [
  {
    name: 'go',
    command: `go run ./cmd/dev -addr ${listenHost}:${options.goPort} -allowed-origin-port ${options.vitePort} -tmux-socket ${tmuxSocket}${currentDirectoryFlag}`,
    cwd: rootDirectory,
    env: sharedEnvironment,
  },
  {
    name: 'vite',
    command: `vite --host ${listenHost} --port ${options.vitePort} --strictPort`,
    cwd: webDirectory,
    env: { VITE_KIWI_CODE_API_PORT: String(options.goPort) },
  },
]

if (options.desktop) {
  commands.push({
    name: 'desktop',
    command: `wait-on --timeout 30000 http-get://${browserHost}:${options.vitePort}/ http-get://${browserHost}:${options.goPort}/api/health && node scripts/electron-launcher.mjs`,
    cwd: webDirectory,
    env: { ...sharedEnvironment, KIWI_CODE_DESKTOP_URL: viteUrl },
  })
}

const { result } = concurrently(commands, {
  prefix: 'name',
  killOthersOn: ['failure', 'success'],
  successCondition: options.desktop ? 'command-desktop' : 'all',
})

try {
  await result
} catch {
  process.exitCode = 1
}
