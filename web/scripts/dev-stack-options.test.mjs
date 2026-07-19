import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertDevelopmentApiTarget,
  assertDevelopmentPort,
  defaultDevelopmentTmuxSocket,
  parseArgs,
  productionTmuxSocket,
  reservedProductionPort,
} from './dev-stack-options.mjs'

test('parseArgs uses the development defaults', () => {
  assert.deepEqual(parseArgs([]), {
    desktop: false,
    loopback: false,
    addCurrentDirectory: false,
    help: false,
    vitePort: 5173,
    goPort: 8080,
    tmuxSocket: '',
  })
})

test('defaultDevelopmentTmuxSocket is stable and isolated per checkout', () => {
  const first = defaultDevelopmentTmuxSocket('/tmp/worktree-a')
  assert.match(first, /^dmdev-[a-f0-9]{12}$/)
  assert.notEqual(first, productionTmuxSocket)
  assert.equal(first, defaultDevelopmentTmuxSocket('/tmp/worktree-a'))
  assert.notEqual(first, defaultDevelopmentTmuxSocket('/tmp/worktree-b'))
})

test('assertDevelopmentPort protects the production listener', () => {
  assert.doesNotThrow(() => assertDevelopmentPort(14000, 'test server'))
  assert.throws(
    () => assertDevelopmentPort(reservedProductionPort, 'test server'),
    /test server may not use reserved production port 4000/,
  )
})

test('assertDevelopmentApiTarget cannot point Vite at production', () => {
  assert.doesNotThrow(() => assertDevelopmentApiTarget('18080', 'http://127.0.0.1:18080'))
  assert.throws(() => assertDevelopmentApiTarget('4000'), /Vite API target.*production port 4000/)
  assert.throws(
    () => assertDevelopmentApiTarget('', 'http://127.0.0.1:4000/api'),
    /Vite API target.*production port 4000/,
  )
})

test('parseArgs accepts custom ports and an isolated tmux socket', () => {
  assert.deepEqual(
    parseArgs([
      '--desktop',
      '--loopback',
      '--add-current-directory',
      '--vite-port',
      '15173',
      '--go-port=18080',
      '--tmux-socket',
      'dmv-agent-a1',
    ]),
    {
      desktop: true,
      loopback: true,
      addCurrentDirectory: true,
      help: false,
      vitePort: 15173,
      goPort: 18080,
      tmuxSocket: 'dmv-agent-a1',
    },
  )
})

test('parseArgs accepts help flags', () => {
  assert.equal(parseArgs(['--help']).help, true)
  assert.equal(parseArgs(['-h']).help, true)
})

test('parseArgs rejects invalid development options', () => {
  const cases = [
    { args: ['--vite-port'], message: /requires a numeric port/ },
    { args: ['--vite-port', 'abc'], message: /requires a numeric port/ },
    { args: ['--vite-port', '0'], message: /between 1 and 65535/ },
    { args: ['--go-port', '65536'], message: /between 1 and 65535/ },
    { args: ['--vite-port', '8080'], message: /must be different/ },
    { args: ['--vite-port', String(reservedProductionPort)], message: /reserved production port 4000/ },
    { args: ['--go-port=4000'], message: /reserved production port 4000/ },
    { args: ['--tmux-socket', ''], message: /tmux-socket must be/ },
    { args: ['--tmux-socket', '../dire-mux'], message: /tmux-socket must be/ },
    { args: ['--tmux-socket', productionTmuxSocket], message: /production tmux server dire-mux/ },
    { args: ['--unknown'], message: /unknown option/ },
  ]

  for (const { args, message } of cases) {
    assert.throws(() => parseArgs(args), message)
  }
})
