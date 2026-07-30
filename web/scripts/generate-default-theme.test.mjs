import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  readDefaultTheme,
  renderDefaultThemeCSS,
  renderDefaultThemeTypeScript,
  renderIndexHTMLThemeColor,
} from './generate-default-theme.mjs'

const typescriptUrl = new URL('../src/defaultTheme.generated.ts', import.meta.url)
const cssUrl = new URL('../src/default-theme.generated.css', import.meta.url)
const htmlUrl = new URL('../index.html', import.meta.url)

test('default theme generated artifacts match the canonical project theme', async () => {
  const [theme, typescript, css, html] = await Promise.all([
    readDefaultTheme(),
    readFile(typescriptUrl, 'utf8'),
    readFile(cssUrl, 'utf8'),
    readFile(htmlUrl, 'utf8'),
  ])

  assert.equal(typescript, renderDefaultThemeTypeScript(theme))
  assert.equal(css, renderDefaultThemeCSS(theme))
  assert.equal(html, renderIndexHTMLThemeColor(html, theme))
})
