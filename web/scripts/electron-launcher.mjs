#!/usr/bin/env node

import { spawn } from 'node:child_process'
import fs from 'node:fs'
import { readFile, unlink } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)

export function defaultElectronUserData(environment = process.env, platform = process.platform) {
  if (environment.KIWI_CODE_ELECTRON_USER_DATA) {
    return path.resolve(environment.KIWI_CODE_ELECTRON_USER_DATA)
  }
  if (platform === 'darwin') return path.join(os.homedir(), 'Library', 'Application Support', 'kiwi-code')
  if (platform === 'win32') return path.join(environment.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming'), 'kiwi-code')
  return path.join(environment.XDG_CONFIG_HOME || path.join(os.homedir(), '.config'), 'kiwi-code')
}

export function browserProviderConfigPath(
  environment = process.env,
  platform = process.platform,
  baseDirectory = process.cwd(),
) {
  if (environment.KIWI_CODE_BROWSER_PROVIDER_CONFIG) {
    return path.resolve(baseDirectory, environment.KIWI_CODE_BROWSER_PROVIDER_CONFIG)
  }
  if (environment.KIWI_CODE_DATA_DIR) {
    return path.join(path.resolve(baseDirectory, environment.KIWI_CODE_DATA_DIR), 'browser-provider.json')
  }
  return path.join(defaultElectronUserData(environment, platform), 'browser-provider.json')
}

function matchingConfig(value, expected) {
  return value?.version === expected?.version &&
    value?.pid === expected?.pid &&
    value?.port === expected?.port &&
    value?.token === expected?.token
}

async function readOwnedConfig(configPath, pid) {
  try {
    const value = JSON.parse(await readFile(configPath, 'utf8'))
    return value?.pid === pid ? value : null
  } catch {
    return null
  }
}

async function removeMatchingConfig(configPath, expected) {
  if (!expected) return
  try {
    const current = JSON.parse(await readFile(configPath, 'utf8'))
    if (matchingConfig(current, expected)) await unlink(configPath)
  } catch {
    // The Electron main process normally removes the file first.
  }
}

function removeMatchingConfigSync(configPath, expected) {
  if (!expected) return
  try {
    const current = JSON.parse(fs.readFileSync(configPath, 'utf8'))
    if (matchingConfig(current, expected)) fs.unlinkSync(configPath)
  } catch {
    // Best-effort fallback for abrupt launcher shutdown.
  }
}

async function main() {
  const electronPath = require('electron')
  const webDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
  const rootDirectory = path.resolve(webDirectory, '..')
  const configPath = browserProviderConfigPath(process.env, process.platform, rootDirectory)
  const childEnvironment = {
    ...process.env,
    KIWI_CODE_BROWSER_PROVIDER_CONFIG: configPath,
  }
  const child = spawn(electronPath, ['.'], { cwd: webDirectory, env: childEnvironment, stdio: 'inherit' })
  let expectedConfig = null
  let settled = false

  const discover = async () => {
    const current = await readOwnedConfig(configPath, child.pid)
    if (current) expectedConfig = current
  }
  const discoveryTimer = setInterval(() => { void discover() }, 100)

  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      if (!settled) {
        try { child.kill(signal) } catch { /* The process group may have delivered it already. */ }
      }
    })
  }

  process.once('exit', () => removeMatchingConfigSync(configPath, expectedConfig))
  child.once('error', async (error) => {
    settled = true
    clearInterval(discoveryTimer)
    await discover()
    await removeMatchingConfig(configPath, expectedConfig)
    console.error('Could not start Electron:', error)
    process.exitCode = 1
  })
  child.once('close', async (code, signal) => {
    settled = true
    clearInterval(discoveryTimer)
    await discover()
    await removeMatchingConfig(configPath, expectedConfig)
    if (signal) console.error(`Electron exited with signal ${signal}`)
    process.exitCode = code ?? (signal ? 1 : 0)
  })
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main()
}
