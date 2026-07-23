#!/usr/bin/env node

import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const webDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
await build({
  entryPoints: [path.join(webDirectory, 'browser-host', 'browser-host.cjs')],
  outfile: path.join(webDirectory, '..', 'internal', 'browserhost', 'assets', 'browser-host.cjs'),
  bundle: true,
  platform: 'node',
  target: 'node20',
  format: 'cjs',
  legalComments: 'linked',
  logLevel: 'warning',
})
