import { spawn } from 'node:child_process'
import { promises as fs } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const heartbeatIntervalMs = 5_000
const requestTimeoutMs = 3_500
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
  return (process.env.DIRE_MUX_THREAD_ENDPOINT || '').replace(/\/+$/, '')
}

function stateDirectory() {
  if (process.env.DIRE_MUX_CLAUDE_STATE_DIR) return process.env.DIRE_MUX_CLAUDE_STATE_DIR
  const uid = typeof process.getuid === 'function' ? process.getuid() : 'user'
  return path.join(os.tmpdir(), `dire-mux-claude-${uid}`)
}

function sessionKey(input) {
  return [
    process.env.DIRE_MUX_PROJECT_ID,
    process.env.DIRE_MUX_THREAD_ID,
    input.session_id,
  ].map(safeSegment).join('-')
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
    if (!response.ok) throw new Error(`Dire Mux returned ${response.status}`)
    return response
  } finally {
    clearTimeout(timeout)
  }
}

async function agentCapability() {
  const pluginRoot = process.env.CLAUDE_PLUGIN_ROOT
  if (!pluginRoot) return ''
  try {
    return (await fs.readFile(path.join(pluginRoot, '..', 'agent-token'), 'utf8')).trim()
  } catch {
    return ''
  }
}

async function updateWorkflowActivation(input) {
  const endpoint = threadEndpoint()
  const token = await agentCapability()
  if (!endpoint || !token || typeof input.prompt !== 'string') return
  const effort = String(input?.effort?.level || process.env.CLAUDE_EFFORT || '').trim().toLowerCase()
  const response = await request(`${endpoint}/workflows/activation`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Dire-Mux-Agent-Token': token,
    },
    body: JSON.stringify({
      prompt: input.prompt,
      source: 'claude-hook',
      mode: effort === 'ultracode' ? 'ultracode' : 'prompt',
    }),
  }, 2_500)
  const activation = await response.json()
  const sizeInstructions = {
    small: ' Aim for fewer than 5 agents unless the human prompt clearly requires a different scale.',
    medium: ' Aim for fewer than 15 agents unless the human prompt clearly requires a different scale.',
    large: ' Aim for fewer than 50 agents unless the human prompt clearly requires a different scale.',
  }
  const size = sizeInstructions[activation?.sizeGuideline] || ''
  const additionalContext = activation?.activated
    ? `[Dire Mux workflow activation] This human turn activated Dire Mux workflows (${activation.mode || 'prompt'}). You may use the dire_mux_run_workflow or dire_mux_run_saved_workflow MCP tool when useful.${size}`
    : '[Dire Mux workflow activation] This human turn did not activate Dire Mux workflows. Do not call dire_mux_run_workflow or dire_mux_run_saved_workflow; broad work alone is not activation.'
  process.stdout.write(JSON.stringify({
    suppressOutput: true,
    hookSpecificOutput: {
      hookEventName: 'UserPromptSubmit',
      additionalContext,
    },
  }))
}

async function sendActivity(state, timeoutMs = requestTimeoutMs, promptStartedAt = '') {
  const endpoint = threadEndpoint()
  if (!endpoint) return
  await request(`${endpoint}/claude/activity`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      state,
      agent: process.env.DIRE_MUX_CODING_AGENT || 'claude',
      ...(promptStartedAt ? { promptStartedAt } : {}),
    }),
  }, timeoutMs)
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
  const token = String(input.prompt_id || `${Date.now()}-${process.pid}`)
  const promptStartedAt = new Date().toISOString()
  const parentPid = process.ppid
  await writeState(input, { token, state: 'working' })

  while (true) {
    if (!processExists(parentPid)) {
      await transitionActivity(input, 'idle')
      return
    }

    const current = await readState(input)
    if (!current || current.token !== token || current.state !== 'working') return

    await sendActivity('working', requestTimeoutMs, promptStartedAt).catch(() => {})
    await sleep(heartbeatIntervalMs)
  }
}

async function transitionActivity(input, state, onlyIfActive = false) {
  if (!threadEndpoint()) return
  const current = await readState(input)
  if (onlyIfActive && current?.state !== 'working') return
  const token = String(current?.token || input.prompt_id || `${Date.now()}-${process.pid}`)
  await writeState(input, { token, state })
  const timeoutMs = 700
  await sendActivity(state, timeoutMs).catch(() => {})
  // Send a second local transition after any in-flight heartbeat has drained.
  // Both updates happen before the hook returns, so an acknowledgement cannot
  // be followed by a late heartbeat that resurrects the finished indicator.
  await sleep(100)
  const latest = await readState(input)
  if (latest?.token === token && latest.state === state) {
    await sendActivity(state, timeoutMs).catch(() => {})
  }
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
  const executable = process.env.DIRE_MUX_PI_PATH || 'pi'
  const environment = { ...process.env, PI_SKIP_VERSION_CHECK: '1' }
  delete environment.CLAUDECODE
  delete environment.CLAUDE_CODE_CHILD_SESSION
  delete environment.CLAUDE_CODE_SESSION_ID

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
  const input = await readInput()
  switch (action) {
    case 'workflow-activation':
      await updateWorkflowActivation(input)
      break
    case 'heartbeat':
      await heartbeat(input)
      break
    case 'finished':
      await transitionActivity(input, 'finished')
      break
    case 'finished-if-active':
      await transitionActivity(input, 'finished', true)
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
  // Dire Mux integration must never block or fail the user's Claude turn.
  if (process.argv[2] !== 'title') return
  const message = error instanceof Error ? error.message : String(error)
  process.stdout.write(JSON.stringify({
    systemMessage: `Could not name Dire Mux thread: ${message}`,
  }))
})
