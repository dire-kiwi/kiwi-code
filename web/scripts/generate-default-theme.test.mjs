import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  readDefaultTheme,
  renderCodeServerUserSettings,
  renderDefaultThemeCSS,
  renderDefaultThemeTypeScript,
  renderIndexHTMLThemeColor,
  renderKiwiCodeVSCodeTheme,
} from './generate-default-theme.mjs'

const typescriptUrl = new URL('../src/defaultTheme.generated.ts', import.meta.url)
const cssUrl = new URL('../src/default-theme.generated.css', import.meta.url)
const htmlUrl = new URL('../index.html', import.meta.url)
const vscodeThemeUrl = new URL('../electron/code-server-extension/themes/kiwi-code-color-theme.json', import.meta.url)
const vscodeSettingsUrl = new URL('../electron/code-server-user-settings.json', import.meta.url)
const extensionManifestUrl = new URL('../electron/code-server-extension/package.json', import.meta.url)

test('default theme generated artifacts match the canonical project theme', async () => {
  const [theme, typescript, css, html, vscodeTheme, vscodeSettings] = await Promise.all([
    readDefaultTheme(),
    readFile(typescriptUrl, 'utf8'),
    readFile(cssUrl, 'utf8'),
    readFile(htmlUrl, 'utf8'),
    readFile(vscodeThemeUrl, 'utf8'),
    readFile(vscodeSettingsUrl, 'utf8'),
  ])

  assert.equal(typescript, renderDefaultThemeTypeScript(theme))
  assert.equal(css, renderDefaultThemeCSS(theme))
  assert.equal(html, renderIndexHTMLThemeColor(html, theme))
  assert.equal(vscodeTheme, renderKiwiCodeVSCodeTheme(theme))
  assert.equal(vscodeSettings, renderCodeServerUserSettings(theme))
})

test('kiwi code VS Code theme uses the canonical palette', async () => {
  const theme = await readDefaultTheme()
  const vscodeTheme = JSON.parse(renderKiwiCodeVSCodeTheme(theme))

  assert.equal(vscodeTheme.name, 'Kiwi Code')
  assert.equal(vscodeTheme.type, 'dark')
  assert.equal(vscodeTheme.colors['editor.background'], theme.colors.background)
  assert.equal(vscodeTheme.colors['sideBar.background'], theme.colors.sidebar)
  assert.equal(vscodeTheme.colors['activityBar.background'], theme.colors.canvas)
  assert.equal(vscodeTheme.colors['terminal.ansiRed'], theme.colors.red)
  assert.equal(vscodeTheme.colors['terminal.ansiBrightCyan'], theme.colors.brightCyan)
  assert.ok(Array.isArray(vscodeTheme.tokenColors) && vscodeTheme.tokenColors.length > 0)
})

test('code-server extension manifest points at the generated theme', async () => {
  const manifest = JSON.parse(await readFile(extensionManifestUrl, 'utf8'))
  const contributed = manifest.contributes.themes[0]
  const settings = JSON.parse(renderCodeServerUserSettings(await readDefaultTheme()))

  assert.equal(contributed.label, 'Kiwi Code')
  assert.equal(contributed.uiTheme, 'vs-dark')
  assert.equal(contributed.path, './themes/kiwi-code-color-theme.json')
  assert.equal(settings['workbench.colorTheme'], contributed.label)
})
