import assert from 'node:assert/strict'
import path from 'node:path'
import test from 'node:test'
import {
  browserProviderConfigPath,
  defaultElectronUserData,
} from './electron-launcher.mjs'

test('Electron launcher resolves an exact provider override consistently', () => {
  assert.equal(
    browserProviderConfigPath({ KIWI_CODE_BROWSER_PROVIDER_CONFIG: '/tmp/kiwi-code/provider.json' }),
    '/tmp/kiwi-code/provider.json',
  )
})

test('Electron launcher resolves relative overrides from the Go project root', () => {
  assert.equal(
    browserProviderConfigPath({ KIWI_CODE_BROWSER_PROVIDER_CONFIG: 'data/provider.json' }, 'linux', '/srv/kiwi-code'),
    path.join('/srv/kiwi-code', 'data/provider.json'),
  )
  assert.equal(
    browserProviderConfigPath({ KIWI_CODE_DATA_DIR: 'data' }, 'linux', '/srv/kiwi-code'),
    path.join('/srv/kiwi-code', 'data/browser-provider.json'),
  )
})

test('Electron launcher follows data-dir and user-data provider defaults', () => {
  assert.equal(
    browserProviderConfigPath({ KIWI_CODE_DATA_DIR: '/tmp/kiwi-code-data' }),
    path.join('/tmp/kiwi-code-data', 'browser-provider.json'),
  )
  assert.equal(
    browserProviderConfigPath({ KIWI_CODE_ELECTRON_USER_DATA: '/tmp/electron-data' }),
    path.join('/tmp/electron-data', 'browser-provider.json'),
  )
})

test('Electron launcher derives platform user-data directories', () => {
  assert.equal(
    defaultElectronUserData({ XDG_CONFIG_HOME: '/tmp/xdg' }, 'linux'),
    path.join('/tmp/xdg', 'kiwi-code'),
  )
  assert.equal(
    defaultElectronUserData({ APPDATA: 'C:\\Users\\dire\\AppData\\Roaming' }, 'win32'),
    path.join('C:\\Users\\dire\\AppData\\Roaming', 'kiwi-code'),
  )
})
