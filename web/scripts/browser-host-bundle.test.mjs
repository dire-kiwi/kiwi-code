import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const webDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

test('embedded headless browser host matches its bundled source', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'kiwi-code-browser-host-test-'))
  const output = path.join(directory, 'browser-host.cjs')
  try {
    await build({
      entryPoints: [path.join(webDirectory, 'browser-host', 'browser-host.cjs')],
      outfile: output,
      bundle: true,
      platform: 'node',
      target: 'node20',
      format: 'cjs',
      legalComments: 'linked',
      logLevel: 'silent',
    })
    const embeddedPath = path.join(webDirectory, '..', 'internal', 'browserhost', 'assets', 'browser-host.cjs')
    const [generated, embedded, generatedLegal, embeddedLegal] = await Promise.all([
      readFile(output),
      readFile(embeddedPath),
      readFile(`${output}.LEGAL.txt`),
      readFile(`${embeddedPath}.LEGAL.txt`),
    ])
    assert.deepEqual(embedded, generated)
    assert.deepEqual(embeddedLegal, generatedLegal)
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})
