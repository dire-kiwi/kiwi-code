import { spawn } from 'node:child_process'
import { promises as fs } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const heartbeatIntervalMs = 5_000
const requestTimeoutMs = 3_500
// The finished transition drives the sidebar's completed dot, so it is worth
// waiting out a slow server rather than leaving the thread stuck on working
// until the stale-working timeout silently clears the indicator.
const transitionTimeoutMs = 2_500
const transitionAttempts = 3
const titleTimeoutMs = 20_000
const maxTitleOutputBytes = 64 * 1024
const titleModel = 'openai-codex/gpt-5.6-luna'
const titleThinking = 'low'

function safeSegment(value) {
  return String(value || 'unknown').replace(/[^a-zA-Z0-9_-]/g, '-').slice(0, 160)
}

async function readInput() {
  const chunks = []
  for await (const chunk of process.stdin) chunks.push(chunk)
  const text = Buffer.concat(chunks).toString('utf8').trim()
  return text ? JSON.parse(text) : {}
}

function threadEndpoint() {
  return (process.env.KIWI_CODE_THREAD_ENDPOINT || '').replace(/\/+$/, '')
}

function codingAgent() {
  return process.env.KIWI_CODE_CODING_AGENT === 'codex'
    ? 'codex'
    : (process.env.KIWI_CODE_CODING_AGENT || 'claude')
}

function activityEndpoint() {
  return `agents/${codingAgent() === 'codex' ? 'codex' : 'claude'}`
}

function stateDirectory() {
  if (codingAgent() === 'codex' && process.env.KIWI_CODE_CODEX_STATE_DIR) {
    return process.env.KIWI_CODE_CODEX_STATE_DIR
  }
  if (codingAgent() !== 'codex' && process.env.KIWI_CODE_CLAUDE_STATE_DIR) {
    return process.env.KIWI_CODE_CLAUDE_STATE_DIR
  }
  const uid = typeof process.getuid === 'function' ? process.getuid() : 'user'
  return path.join(os.tmpdir(), `kiwi-code-${codingAgent()}-${uid}`)
}

function sessionKey(input) {
  return [
    process.env.KIWI_CODE_PROJECT_ID,
    process.env.KIWI_CODE_THREAD_ID,
    input.session_id,
  ].map(safeSegment).join('-')
}

function activitySession(input) {
  return input.session_id ? safeSegment(input.session_id) : ''
}

function statePath(input) {
  return path.join(stateDirectory(), `${sessionKey(input)}.activity.json`)
}

function titleMarkerPath(input) {
  return path.join(stateDirectory(), `${sessionKey(input)}.title-attempted`)
}

async function ensureStateDirectory() {
  await fs.mkdir(stateDirectory(), { recursive: true, mode: 0o700 })
}

async function readState(input) {
  try {
    return JSON.parse(await fs.readFile(statePath(input), 'utf8'))
  } catch {
    return null
  }
}

async function writeState(input, state) {
  await ensureStateDirectory()
  const destination = statePath(input)
  const temporary = `${destination}.${process.pid}.${Date.now()}.tmp`
  await fs.writeFile(temporary, JSON.stringify(state), { mode: 0o600 })
  await fs.rename(temporary, destination)
}

async function request(url, init = {}, timeoutMs = requestTimeoutMs) {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetch(url, { ...init, signal: controller.signal })
    if (!response.ok) throw new Error(`Kiwi Code returned ${response.status}`)
    return response
  } finally {
    clearTimeout(timeout)
  }
}

async function sendActivity(state, token, timeoutMs = requestTimeoutMs, promptStartedAt = '', session = '') {
  const endpoint = threadEndpoint()
  if (!endpoint) return
  await request(`${endpoint}/${activityEndpoint()}/activity`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      state,
      agent: codingAgent(),
      // The heartbeat and the stop hook are separate processes, so their updates
      // can arrive out of order. The token lets Kiwi Code discard a working
      // heartbeat that belongs to a turn it already saw finish.
      ...(token ? { token: String(token).slice(0, 200) } : {}),
      // Child sessions report against the same thread. Scoping by session keeps
      // one of them finishing from clearing the spinner while another still works.
      ...(session ? { session: String(session).slice(0, 200) } : {}),
      ...(promptStartedAt ? { promptStartedAt } : {}),
    }),
  }, timeoutMs)
}

async function sendActivityWithRetry(state, token, promptStartedAt, session, timeoutMs, attempts) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      await sendActivity(state, token, timeoutMs, promptStartedAt, session)
      return true
    } catch {
      if (attempt < attempts - 1) await sleep(150)
    }
  }
  return false
}

function processExists(pid) {
  if (!Number.isInteger(pid) || pid <= 1) return true
  try {
    process.kill(pid, 0)
    return true
  } catch (error) {
    return error?.code !== 'ESRCH'
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function heartbeat(input) {
  if (!threadEndpoint()) return
  const pinnedToken = input.kiwi_activity_token ? String(input.kiwi_activity_token) : ''
  const token = pinnedToken || String(input.prompt_id || `${Date.now()}-${process.pid}`)
  const current = await readState(input)
  if (pinnedToken && current && current.token !== token) return
  if (current?.token === token && current.state !== 'working') return
  const promptStartedAt = String(
    input.kiwi_prompt_started_at ||
    (current?.token === token ? current.promptStartedAt : '') ||
    new Date().toISOString(),
  )
  const parentPid = Number(input.kiwi_parent_pid) || process.ppid
  if (!current || current.token !== token || current.state !== 'working' || current.promptStartedAt !== promptStartedAt) {
    await writeState(input, { token, state: 'working', promptStartedAt })
  }

  while (true) {
    if (!processExists(parentPid)) {
      await transitionActivity(input, 'idle')
      return
    }

    const current = await readState(input)
    if (!current || current.token !== token || current.state !== 'working') return

    const startedAt = Date.now()
    await sendActivity('working', token, requestTimeoutMs, promptStartedAt, activitySession(input)).catch(() => {})
    // Pace from the start of the request so a slow round trip does not stretch
    // the interval past the server's stale-working timeout, which would drop
    // the sidebar indicator mid-turn.
    await sleep(Math.max(500, heartbeatIntervalMs - (Date.now() - startedAt)))
  }
}

async function startActivity(input) {
  if (!threadEndpoint()) return
  const token = String(input.prompt_id || `${Date.now()}-${process.pid}`)
  const promptStartedAt = new Date().toISOString()
  // The managed lifecycle hooks do not provide a reliable per-prompt ID.
  // This start hook is synchronous, so persist our generation before the agent
  // begins the turn; the synchronous stop hook can then report that generation
  // even if the detached heartbeat's first request is still in flight.
  await writeState(input, { token, state: 'working', promptStartedAt })
  const heartbeatInput = Buffer.from(JSON.stringify({
    session_id: input.session_id,
    kiwi_activity_token: token,
    kiwi_prompt_started_at: promptStartedAt,
    kiwi_parent_pid: process.ppid,
  })).toString('base64url')
  const child = spawn(process.execPath, [process.argv[1], 'heartbeat', heartbeatInput], {
    detached: true,
    env: process.env,
    stdio: 'ignore',
  })
  child.on('error', () => {})
  child.unref()
}

async function transitionActivity(input, state) {
  if (!threadEndpoint()) return
  const current = await readState(input)
  const inputToken = input.prompt_id ? String(input.prompt_id) : ''
  const token = inputToken || String(current?.token || `${Date.now()}-${process.pid}`)
  const promptStartedAt = String(
    input.kiwi_prompt_started_at ||
    (current?.token === token ? current.promptStartedAt : '') ||
    new Date().toISOString(),
  )
  // Writing the state first stops the heartbeat process on its next tick. The
  // token and generation travel with the update so an already in-flight
  // heartbeat cannot resurrect the working indicator on the server. Prefer an
  // explicit input token over stale file state for test and forward-compatible
  // hook protocols.
  await writeState(input, { token, state, promptStartedAt })
  await sendActivityWithRetry(
    state,
    token,
    promptStartedAt,
    activitySession(input),
    transitionTimeoutMs,
    transitionAttempts,
  )
}

async function endSession(input) {
  const current = await readState(input)
  if (current?.state === 'working') await transitionActivity(input, 'idle')
}

function cleanTitle(value) {
  let title = String(value || '')
    .split('\n')
    .map((line) => line.trim())
    .find(Boolean) || ''
  title = title
    .replace(/^#+\s*/, '')
    .replace(/^title\s*:\s*/i, '')
    .replace(/^[`"'“”‘’]+|[`"'“”‘’]+$/g, '')
    .replace(/\s+/g, ' ')
    .replace(/[.:;,!]+$/, '')
    .trim()
  return Array.from(title).slice(0, 80).join('').trim()
}

function titlePrompt(firstMessage) {
  return [
    "Create a concise title for a software-development work thread from the user's first message.",
    'Return only the title: no quotes, markdown, label, explanation, or trailing punctuation.',
    'Use 3 to 7 words and at most 60 characters. Describe the concrete task, not the conversation.',
    '',
    '<first-message>',
    String(firstMessage || '').trim() || 'The user sent an image without accompanying text.',
    '</first-message>',
  ].join('\n')
}

async function generateTitle(prompt) {
  const executable = process.env.KIWI_CODE_PI_PATH || 'pi'
  const environment = { ...process.env, PI_SKIP_VERSION_CHECK: '1' }
  delete environment.CLAUDECODE
  delete environment.CLAUDE_CODE_CHILD_SESSION
  delete environment.CLAUDE_CODE_SESSION_ID
  delete environment.CODEX_THREAD_ID

  // Match Pi's thread-title extension while keeping this one-shot process
  // isolated from project context and globally installed resources.
  const args = [
    '--print',
    '--no-session',
    '--no-tools',
    '--no-extensions',
    '--no-skills',
    '--no-prompt-templates',
    '--no-themes',
    '--no-context-files',
    '--model', titleModel,
    '--thinking', titleThinking,
    '--system-prompt', 'Generate only the requested concise title. Do not use tools.',
    titlePrompt(prompt),
  ]

  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {
      env: environment,
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    const output = []
    let outputBytes = 0
    let settled = false
    const finish = (error, value) => {
      if (settled) return
      settled = true
      clearTimeout(timeout)
      if (error) reject(error)
      else resolve(value)
    }
    const timeout = setTimeout(() => {
      child.kill('SIGTERM')
      finish(new Error('Pi title generation timed out'))
    }, titleTimeoutMs)

    child.stdout.on('data', (chunk) => {
      outputBytes += chunk.length
      if (outputBytes <= maxTitleOutputBytes) output.push(chunk)
      else child.kill('SIGTERM')
    })
    child.on('error', (error) => finish(error))
    child.on('close', (code) => {
      if (outputBytes > maxTitleOutputBytes) {
        finish(new Error('Pi title output was too large'))
      } else if (code !== 0) {
        finish(new Error(`Pi title generation exited with status ${code}`))
      } else {
        finish(null, Buffer.concat(output).toString('utf8'))
      }
    })
  })
}

function emitSessionTitle(title) {
  // Claude accepts a sessionTitle extension on UserPromptSubmit. Codex does not;
  // its own session naming remains independent from Kiwi Code's thread title.
  if (codingAgent() === 'codex') return
  process.stdout.write(JSON.stringify({
    suppressOutput: true,
    hookSpecificOutput: {
      hookEventName: 'UserPromptSubmit',
      sessionTitle: title,
    },
  }))
}

async function nameThread(input) {
  const endpoint = threadEndpoint()
  if (!endpoint || typeof input.prompt !== 'string') return
  await ensureStateDirectory()
  try {
    const marker = await fs.open(titleMarkerPath(input), 'wx', 0o600)
    await marker.close()
  } catch (error) {
    if (error?.code === 'EEXIST') return
    throw error
  }

  const threadResponse = await request(endpoint, {}, 2_000)
  const thread = await threadResponse.json()
  if (thread?.autoNamed && typeof thread.title === 'string' && thread.title.trim()) {
    emitSessionTitle(cleanTitle(thread.title))
    return
  }

  const title = cleanTitle(await generateTitle(input.prompt))
  if (!title) throw new Error('Pi returned an empty title')
  const updateResponse = await request(endpoint, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, autoGenerated: true }),
  }, 4_000)
  const updated = await updateResponse.json()
  emitSessionTitle(cleanTitle(updated?.title) || title)
}

async function main() {
  const action = process.argv[2]
  const encodedHeartbeatInput = action === 'heartbeat' ? process.argv[3] : ''
  const input = encodedHeartbeatInput
    ? JSON.parse(Buffer.from(encodedHeartbeatInput, 'base64url').toString('utf8'))
    : await readInput()
  switch (action) {
    case 'start':
      await startActivity(input)
      break
    case 'heartbeat':
      await heartbeat(input)
      break
    case 'finished':
      await transitionActivity(input, 'finished')
      break
    case 'session-end':
      await endSession(input)
      break
    case 'title':
      await nameThread(input)
      break
  }
}

await main().catch((error) => {
  // Kiwi Code integration must never block or fail the user's coding-agent turn.
  if (process.argv[2] !== 'title') return
  const message = error instanceof Error ? error.message : String(error)
  process.stdout.write(JSON.stringify({
    systemMessage: `Could not name Kiwi Code thread: ${message}`,
  }))
})
