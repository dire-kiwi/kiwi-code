import { spawn } from 'node:child_process'
import { once } from 'node:events'
import {
  access,
  copyFile,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  stat,
  writeFile,
} from 'node:fs/promises'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { randomBytes } from 'node:crypto'
import { fileURLToPath } from 'node:url'

import puppeteer from 'puppeteer-core'

const supportDirectory = path.dirname(fileURLToPath(import.meta.url))
export const webDirectory = path.resolve(supportDirectory, '..', '..')
export const repositoryRoot = path.resolve(webDirectory, '..')

const productionPort = 4000
const productionTmuxSocket = 'kiwi-code'
const maximumCapturedLogBytes = 512 * 1024
const fixtureProviderSource = path.join(supportDirectory, 'pi-fixture-provider.ts')
const chatFixtureSource = path.join(webDirectory, 'e2e', 'fixtures', 'real-chat.json')

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

async function reserveLoopbackPort() {
  const server = net.createServer()
  server.unref()
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  if (!address || typeof address === 'string' || address.port === productionPort) {
    await new Promise((resolve) => server.close(resolve))
    throw new Error(`Could not allocate a safe E2E loopback port: ${JSON.stringify(address)}`)
  }
  await new Promise((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve())
  })
  return address.port
}

function isolatedTmuxSocket() {
  const socket = `kce-${process.pid.toString(36)}-${randomBytes(3).toString('hex')}`
  if (!socket || socket === productionTmuxSocket || !/^[A-Za-z0-9._-]{1,64}$/.test(socket)) {
    throw new Error(`Refusing unsafe E2E tmux socket ${JSON.stringify(socket)}`)
  }
  return socket
}

function appendBounded(current, chunk) {
  const next = current + String(chunk)
  return next.length <= maximumCapturedLogBytes
    ? next
    : next.slice(next.length - maximumCapturedLogBytes)
}

function commandDescription(command, arguments_) {
  return [command, ...arguments_].map((value) => JSON.stringify(value)).join(' ')
}

async function runCommand(command, arguments_, options = {}) {
  const child = spawn(command, arguments_, {
    cwd: options.cwd,
    env: options.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  let stdout = ''
  let stderr = ''
  child.stdout.on('data', (chunk) => {
    stdout = appendBounded(stdout, chunk)
  })
  child.stderr.on('data', (chunk) => {
    stderr = appendBounded(stderr, chunk)
  })

  const timeoutMilliseconds = options.timeoutMilliseconds ?? 120_000
  let timedOut = false
  const timeout = setTimeout(() => {
    timedOut = true
    child.kill('SIGTERM')
  }, timeoutMilliseconds)
  timeout.unref()

  const [code, signal] = await once(child, 'exit')
  clearTimeout(timeout)
  if (timedOut || code !== 0) {
    throw new Error([
      `${commandDescription(command, arguments_)} ${timedOut ? 'timed out' : `exited with ${code ?? signal}`}.`,
      stdout.trim() ? `stdout:\n${stdout.trim()}` : '',
      stderr.trim() ? `stderr:\n${stderr.trim()}` : '',
    ].filter(Boolean).join('\n'))
  }
  return { stdout, stderr }
}

function startCapturedProcess(command, arguments_, options) {
  const logs = { stdout: '', stderr: '' }
  const child = spawn(command, arguments_, {
    cwd: options.cwd,
    env: options.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  child.stdout.on('data', (chunk) => {
    logs.stdout = appendBounded(logs.stdout, chunk)
  })
  child.stderr.on('data', (chunk) => {
    logs.stderr = appendBounded(logs.stderr, chunk)
  })
  return { child, logs }
}

async function stopProcess(child, timeoutMilliseconds = 8_000) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return
  child.kill('SIGTERM')
  const exited = once(child, 'exit').then(() => true)
  if (await Promise.race([exited, delay(timeoutMilliseconds).then(() => false)])) return
  child.kill('SIGKILL')
  await Promise.race([once(child, 'exit'), delay(2_000)])
}

async function killIsolatedTmuxServer(socket) {
  if (!socket || socket === productionTmuxSocket || !/^[A-Za-z0-9._-]{1,64}$/.test(socket)) {
    throw new Error(`Refusing to clean up unsafe E2E tmux socket ${JSON.stringify(socket)}`)
  }
  const child = spawn('tmux', ['-L', socket, 'kill-server'], {
    stdio: 'ignore',
  })
  await once(child, 'exit')
}

async function discoverChrome() {
  const candidates = [process.env.KIWI_CODE_CHROME_BIN]
  if (process.platform === 'darwin') {
    candidates.push(
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
      path.join(os.homedir(), 'Applications/Google Chrome.app/Contents/MacOS/Google Chrome'),
    )
  }
  if (process.platform === 'linux') {
    candidates.push(
      '/usr/bin/google-chrome',
      '/usr/bin/google-chrome-stable',
      '/usr/bin/chromium',
      '/usr/bin/chromium-browser',
    )
  }
  if (process.platform === 'win32') {
    candidates.push(
      path.join(process.env.PROGRAMFILES || '', 'Google', 'Chrome', 'Application', 'chrome.exe'),
      path.join(process.env['PROGRAMFILES(X86)'] || '', 'Google', 'Chrome', 'Application', 'chrome.exe'),
    )
  }

  for (const candidate of candidates.filter(Boolean)) {
    try {
      const metadata = await stat(candidate)
      if (metadata.isFile() && (process.platform === 'win32' || (metadata.mode & 0o111))) {
        return candidate
      }
    } catch {
      // Try the next browser-host-compatible candidate.
    }
  }

  const executableNames = process.platform === 'win32'
    ? ['chrome.exe']
    : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser']
  for (const directory of (process.env.PATH || '').split(path.delimiter)) {
    for (const name of executableNames) {
      const candidate = path.join(directory, name)
      try {
        await access(candidate)
        return candidate
      } catch {
        // Try the next PATH entry.
      }
    }
  }
  throw new Error('Chrome is unavailable. Set KIWI_CODE_CHROME_BIN to an executable Chrome or Chromium binary.')
}

async function waitForHealth(baseURL, processState, timeoutMilliseconds = 30_000) {
  const deadline = Date.now() + timeoutMilliseconds
  let latestError
  while (Date.now() < deadline) {
    if (processState.child.exitCode !== null || processState.child.signalCode !== null) {
      throw new Error([
        `Kiwi Code exited before becoming healthy (${processState.child.exitCode ?? processState.child.signalCode}).`,
        processState.logs.stdout.trim() ? `stdout:\n${processState.logs.stdout.trim()}` : '',
        processState.logs.stderr.trim() ? `stderr:\n${processState.logs.stderr.trim()}` : '',
      ].filter(Boolean).join('\n'))
    }
    try {
      const response = await fetch(`${baseURL}/api/health`)
      const body = await response.json()
      if (response.ok && body.status === 'ok' && body.instanceId) return body
      latestError = new Error(`health returned ${response.status}: ${JSON.stringify(body)}`)
    } catch (error) {
      latestError = error
    }
    await delay(100)
  }
  throw new Error(`Kiwi Code did not become healthy at ${baseURL}: ${latestError?.message ?? 'timeout'}`)
}

async function apiRequest(baseURL, pathname, options = {}) {
  const response = await fetch(new URL(pathname, baseURL), {
    ...options,
    headers: {
      accept: 'application/json',
      ...(options.body === undefined ? {} : { 'content-type': 'application/json' }),
      ...options.headers,
    },
  })
  const text = await response.text()
  let body
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = text
    }
  }
  if (!response.ok) {
    throw new Error(`${options.method ?? 'GET'} ${pathname} returned ${response.status}: ${typeof body === 'string' ? body : JSON.stringify(body)}`)
  }
  return body
}

async function prepareFixtureRepository(paths) {
  const extensionDirectory = path.join(paths.fixtureRepository, '.pi', 'extensions')
  await mkdir(extensionDirectory, { recursive: true, mode: 0o700 })
  await mkdir(path.dirname(paths.fixturePath), { recursive: true, mode: 0o700 })
  await copyFile(fixtureProviderSource, path.join(extensionDirectory, 'kiwi-e2e-provider.ts'))
  await copyFile(chatFixtureSource, paths.fixturePath)
  await writeFile(
    path.join(paths.fixtureRepository, 'README.md'),
    [
      '# Kiwi Code',
      '',
      'This isolated repository exists only for the Kiwi Code browser E2E suite.',
      'The real Pi agent must read this file before the fixture provider will answer.',
      '',
    ].join('\n'),
    { mode: 0o600 },
  )

  await runCommand('git', ['init', '--initial-branch=main', '--quiet'], { cwd: paths.fixtureRepository })
  await runCommand('git', ['config', 'user.email', 'kiwi-e2e@example.invalid'], { cwd: paths.fixtureRepository })
  await runCommand('git', ['config', 'user.name', 'Kiwi E2E'], { cwd: paths.fixtureRepository })
  await runCommand('git', ['add', '.'], { cwd: paths.fixtureRepository })
  await runCommand('git', ['commit', '--quiet', '-m', 'Add deterministic Pi E2E fixture'], {
    cwd: paths.fixtureRepository,
  })
}

async function seedDiscoveryOnlyModel(paths, fixture) {
  await mkdir(paths.piAgentDirectory, { recursive: true, mode: 0o700 })
  await writeFile(
    path.join(paths.piAgentDirectory, 'models.json'),
    `${JSON.stringify({
      providers: {
        'kiwi-e2e': {
          baseUrl: 'http://127.0.0.1:1',
          apiKey: 'fixture',
          api: 'openai-completions',
          models: [{
            id: fixture.model.id,
            name: fixture.model.name,
            reasoning: fixture.model.reasoning,
            thinkingLevelMap: fixture.model.thinkingLevelMap,
            input: ['text', 'image'],
            cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
            contextWindow: 128_000,
            maxTokens: 16_384,
          }],
        },
      },
    }, null, 2)}\n`,
    { mode: 0o600 },
  )
}

function collectPageDiagnostics(page, baseURL) {
  const diagnostics = {
    abortedRequests: [],
    consoleErrors: [],
    pageErrors: [],
    failedRequests: [],
    failedResponses: [],
  }
  const appOrigin = new URL(baseURL).origin

  page.on('console', (message) => {
    if (message.type() === 'error') diagnostics.consoleErrors.push(message.text())
  })
  page.on('pageerror', (error) => {
    diagnostics.pageErrors.push(error.stack || error.message)
  })
  page.on('requestfailed', (request) => {
    if (!request.url().startsWith(appOrigin)) return
    const failure = {
      method: request.method(),
      url: request.url(),
      error: request.failure()?.errorText ?? 'request failed',
    }
    if (failure.error === 'net::ERR_ABORTED') {
      // React cleanup deliberately aborts in-flight activity acknowledgements
      // when a workspace unmounts. Preserve those for diagnostics without
      // treating a caller-initiated cancellation as a network failure.
      diagnostics.abortedRequests.push(failure)
      return
    }
    diagnostics.failedRequests.push(failure)
  })
  page.on('response', (response) => {
    if (!response.url().startsWith(appOrigin) || response.status() < 400) return
    diagnostics.failedResponses.push({
      method: response.request().method(),
      status: response.status(),
      url: response.url(),
    })
  })
  return diagnostics
}

function browserDiagnosticsSummary(diagnostics) {
  return JSON.stringify(diagnostics, null, 2)
}

async function readPiReports(reportDirectory) {
  const names = await readdir(reportDirectory).catch(() => [])
  const entries = []
  for (const name of names.filter((candidate) => candidate.endsWith('.jsonl')).sort()) {
    const contents = await readFile(path.join(reportDirectory, name), 'utf8')
    for (const [index, line] of contents.split(/\r?\n/).entries()) {
      if (!line.trim()) continue
      try {
        entries.push(JSON.parse(line))
      } catch (error) {
        throw new Error(`Invalid Pi E2E report ${name}:${index + 1}: ${error.message}`)
      }
    }
  }
  return entries
}

async function writeFailureDiagnostics(paths, processState, diagnostics, error) {
  const payload = {
    error: error instanceof Error ? { message: error.message, stack: error.stack } : String(error),
    browser: diagnostics,
    server: processState?.logs ?? null,
  }
  await writeFile(
    path.join(paths.tempRoot, 'failure-diagnostics.json'),
    `${JSON.stringify(payload, null, 2)}\n`,
    { mode: 0o600 },
  ).catch(() => {})
}

export async function startE2EHarness() {
  await access(fixtureProviderSource)
  await access(chatFixtureSource)
  const fixture = JSON.parse(await readFile(chatFixtureSource, 'utf8'))
  const chromeExecutable = await discoverChrome()

  await runCommand('npm', ['run', 'build'], {
    cwd: webDirectory,
    timeoutMilliseconds: 240_000,
  })

  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'kiwi-code-e2e-'))
  const paths = {
    tempRoot,
    dataDirectory: path.join(tempRoot, 'data'),
    homeDirectory: path.join(tempRoot, 'home'),
    fixtureRepository: path.join(tempRoot, 'fixture-repository'),
    reportDirectory: path.join(tempRoot, 'reports'),
    fixturePath: path.join(tempRoot, 'fixtures', 'real-chat.json'),
    piAgentDirectory: path.join(tempRoot, 'pi-agent'),
    chromeProfile: path.join(tempRoot, 'chrome-profile'),
    binary: path.join(tempRoot, 'kiwi-code-e2e'),
  }
  const tmuxSocket = isolatedTmuxSocket()
  const port = await reserveLoopbackPort()
  const baseURL = `http://127.0.0.1:${port}`
  let processState
  let browser
  let page
  let diagnostics = {
    abortedRequests: [],
    consoleErrors: [],
    pageErrors: [],
    failedRequests: [],
    failedResponses: [],
  }

  async function cleanup() {
    if (page && !page.isClosed()) await page.close().catch(() => {})
    if (browser) await browser.close().catch(() => {})
    if (processState?.child && processState.child.exitCode === null && processState.child.signalCode === null) {
      await fetch(`${baseURL}/api/restart`, { method: 'POST' }).catch(() => {})
      await Promise.race([
        once(processState.child, 'exit'),
        delay(5_000),
      ]).catch(() => {})
    }
    await stopProcess(processState?.child)
    await killIsolatedTmuxServer(tmuxSocket).catch(() => {})
    await rm(tempRoot, { recursive: true, force: true })
  }

  try {
    await Promise.all([
      mkdir(paths.dataDirectory, { recursive: true, mode: 0o700 }),
      mkdir(paths.homeDirectory, { recursive: true, mode: 0o700 }),
      mkdir(paths.fixtureRepository, { recursive: true, mode: 0o700 }),
      mkdir(paths.reportDirectory, { recursive: true, mode: 0o700 }),
      mkdir(paths.chromeProfile, { recursive: true, mode: 0o700 }),
    ])
    await prepareFixtureRepository(paths)
    await seedDiscoveryOnlyModel(paths, fixture)
    await runCommand('go', ['build', '-o', paths.binary, '.'], {
      cwd: repositoryRoot,
      timeoutMilliseconds: 240_000,
    })

    const appEnvironment = {
      ...process.env,
      HOME: paths.homeDirectory,
      PI_CODING_AGENT_DIR: paths.piAgentDirectory,
      PI_OFFLINE: '1',
      PI_SKIP_VERSION_CHECK: '1',
      KIWI_CODE_E2E_PI_FIXTURE: paths.fixturePath,
      KIWI_CODE_E2E_PI_REPORT_DIR: paths.reportDirectory,
      KIWI_CODE_CHROME_BIN: chromeExecutable,
    }
    processState = startCapturedProcess(paths.binary, [
      '-mode', 'development',
      '-addr', `127.0.0.1:${port}`,
      '-data-dir', paths.dataDirectory,
      '-tmux-socket', tmuxSocket,
      '-chrome-binary', chromeExecutable,
      '-add-current-directory',
    ], {
      cwd: repositoryRoot,
      env: appEnvironment,
    })
    await waitForHealth(baseURL, processState)

    const project = await apiRequest(baseURL, '/api/projects', {
      method: 'POST',
      body: JSON.stringify({
        name: 'E2E Fixture',
        path: paths.fixtureRepository,
        profileId: 'personal',
      }),
    })
    const workspaceThread = await apiRequest(
      baseURL,
      `/api/projects/${encodeURIComponent(project.id)}/threads/${encodeURIComponent(project.threads[0].id)}`,
      {
        method: 'PATCH',
        body: JSON.stringify({ title: 'E2E Workspace' }),
      },
    )
    project.threads[0] = workspaceThread
    const emptyProfile = await apiRequest(baseURL, '/api/profiles', {
      method: 'POST',
      body: JSON.stringify({ name: 'Empty E2E' }),
    })

    const codingAgents = await apiRequest(
      baseURL,
      `/api/coding-agents?projectId=${encodeURIComponent(project.id)}`,
    )
    const piConfig = codingAgents.find((candidate) => candidate.id === 'pi')
    const fixtureModelID = `kiwi-e2e/${fixture.model.id}`
    if (!piConfig?.models?.some((model) => model.id === fixtureModelID)) {
      throw new Error(`Pi discovery did not expose ${fixtureModelID}: ${JSON.stringify(codingAgents)}`)
    }

    const chromeArguments = [
      '--disable-background-networking',
      '--disable-breakpad',
      '--disable-component-update',
      '--disable-crash-reporter',
      '--disable-crashpad-for-testing',
      '--disable-default-apps',
      '--disable-features=Translate,MediaRouter',
      '--disable-sync',
      '--metrics-recording-only',
      '--no-default-browser-check',
      '--no-first-run',
      `--crash-dumps-dir=${paths.chromeProfile}`,
    ]
    if (typeof process.getuid === 'function' && process.getuid() === 0) {
      chromeArguments.push('--no-sandbox')
    }
    browser = await puppeteer.launch({
      executablePath: chromeExecutable,
      headless: true,
      pipe: true,
      userDataDir: paths.chromeProfile,
      defaultViewport: { width: 1440, height: 1000, deviceScaleFactor: 1 },
      env: appEnvironment,
      args: chromeArguments,
    })
    page = await browser.newPage()
    page.setDefaultTimeout(20_000)
    page.setDefaultNavigationTimeout(30_000)
    diagnostics = collectPageDiagnostics(page, baseURL)

    return {
      api: (pathname, options) => apiRequest(baseURL, pathname, options),
      assertNoBrowserFailures() {
        const failures = {
          consoleErrors: diagnostics.consoleErrors,
          pageErrors: diagnostics.pageErrors,
          failedRequests: diagnostics.failedRequests,
          failedResponses: diagnostics.failedResponses,
        }
        if (Object.values(failures).some((items) => items.length > 0)) {
          throw new Error(`Browser diagnostics contained failures:\n${browserDiagnosticsSummary(failures)}`)
        }
      },
      baseURL,
      cleanup,
      diagnostics,
      emptyProfile,
      fixture,
      fixtureModelID,
      page,
      paths,
      project,
      readPiReports: () => readPiReports(paths.reportDirectory),
      serverLogs: processState.logs,
      tmuxSocket,
      workspaceThread,
    }
  } catch (error) {
    await writeFailureDiagnostics(paths, processState, diagnostics, error)
    const serverOutput = processState
      ? [
          processState.logs.stdout.trim() ? `server stdout:\n${processState.logs.stdout.trim()}` : '',
          processState.logs.stderr.trim() ? `server stderr:\n${processState.logs.stderr.trim()}` : '',
        ].filter(Boolean).join('\n')
      : ''
    await cleanup().catch(() => {})
    if (serverOutput && error instanceof Error) error.message += `\n${serverOutput}`
    throw error
  }
}

export async function withE2EHarness(callback) {
  const harness = await startE2EHarness()
  try {
    return await callback(harness)
  } catch (error) {
    await writeFailureDiagnostics(
      harness.paths,
      { logs: harness.serverLogs },
      harness.diagnostics,
      error,
    )
    const details = [
      error instanceof Error ? error.message : String(error),
      `Browser diagnostics:\n${browserDiagnosticsSummary(harness.diagnostics)}`,
      harness.serverLogs.stdout.trim() ? `Server stdout:\n${harness.serverLogs.stdout.trim()}` : '',
      harness.serverLogs.stderr.trim() ? `Server stderr:\n${harness.serverLogs.stderr.trim()}` : '',
    ].filter(Boolean).join('\n')
    if (error instanceof Error) {
      error.message = details
      throw error
    }
    throw new Error(details)
  } finally {
    await harness.cleanup()
  }
}
