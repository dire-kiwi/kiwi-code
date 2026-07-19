import assert from 'node:assert/strict'
import path from 'node:path'
import test from 'node:test'
import {
  browserProviderConfigPath,
  defaultElectronUserData,
} from './electron-launcher.mjs'

test('Electron launcher resolves an exact provider override consistently', () => {
  assert.equal(
    browserProviderConfigPath({ DIRE_MUX_BROWSER_PROVIDER_CONFIG: '/tmp/dire-mux/provider.json' }),
    '/tmp/dire-mux/provider.json',
  )
})

test('Electron launcher resolves relative overrides from the Go project root', () => {
  assert.equal(
    browserProviderConfigPath({ DIRE_MUX_BROWSER_PROVIDER_CONFIG: 'data/provider.json' }, 'linux', '/srv/dire-mux'),
    path.join('/srv/dire-mux', 'data/provider.json'),
  )
  assert.equal(
    browserProviderConfigPath({ DIRE_MUX_DATA_DIR: 'data' }, 'linux', '/srv/dire-mux'),
    path.join('/srv/dire-mux', 'data/browser-provider.json'),
  )
})

test('Electron launcher follows data-dir and user-data provider defaults', () => {
  assert.equal(
    browserProviderConfigPath({ DIRE_MUX_DATA_DIR: '/tmp/dire-mux-data' }),
    path.join('/tmp/dire-mux-data', 'browser-provider.json'),
  )
  assert.equal(
    browserProviderConfigPath({ DIRE_MUX_ELECTRON_USER_DATA: '/tmp/electron-data' }),
    path.join('/tmp/electron-data', 'browser-provider.json'),
  )
})

test('Electron launcher derives platform user-data directories', () => {
  assert.equal(
    defaultElectronUserData({ XDG_CONFIG_HOME: '/tmp/xdg' }, 'linux'),
    path.join('/tmp/xdg', 'dire-mux'),
  )
  assert.equal(
    defaultElectronUserData({ APPDATA: 'C:\\Users\\dire\\AppData\\Roaming' }, 'win32'),
    path.join('C:\\Users\\dire\\AppData\\Roaming', 'dire-mux'),
  )
})
