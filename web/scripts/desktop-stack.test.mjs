import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const packageJson = JSON.parse(
  await readFile(new URL('../package.json', import.meta.url), 'utf8'),
)

test('production desktop launcher preserves the network listener configuration', () => {
  const command = packageJson.scripts['run:desktop']

  assert.match(command, /cd \.\. && make run/)
  assert.doesNotMatch(command, /KIWI_CODE_ADDR\s*=/)
  assert.match(command, /KIWI_CODE_DESKTOP_URL=http:\/\/127\.0\.0\.1:4000/)
  assert.match(command, /node scripts\/electron-launcher\.mjs/)
})
