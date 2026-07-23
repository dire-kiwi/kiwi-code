'use strict'

const { randomBytes, timingSafeEqual } = require('node:crypto')
const fs = require('node:fs')
const fsp = require('node:fs/promises')
const http = require('node:http')
const path = require('node:path')
const { pipeline } = require('node:stream/promises')
const { BrowserProviderError, validateActionBody } = require('./browser-helpers.cjs')

const MAX_REQUEST_BYTES = 2 * 1024 * 1024
const MAX_RESPONSE_BYTES = 24 * 1024 * 1024
const RECORDER_ASSETS = new Map([
  ['index.html', { file: 'browser-recorder.html', contentType: 'text/html; charset=utf-8' }],
  ['browser-recorder-renderer.js', { file: 'browser-recorder-renderer.js', contentType: 'text/javascript; charset=utf-8' }],
])

function configPathFor(app, environment = process.env) {
  if (environment.KIWI_CODE_BROWSER_PROVIDER_CONFIG) {
    return path.resolve(environment.KIWI_CODE_BROWSER_PROVIDER_CONFIG)
  }
  const dataDirectory = environment.KIWI_CODE_DATA_DIR || app.getPath('userData')
  return path.join(dataDirectory, 'browser-provider.json')
}

async function atomicWriteConfig(configPath, config) {
  await fsp.mkdir(path.dirname(configPath), { recursive: true })
  const temporaryPath = `${configPath}.${process.pid}.${randomBytes(8).toString('hex')}.tmp`
  let file
  try {
    file = await fsp.open(temporaryPath, 'wx', 0o600)
    await file.writeFile(`${JSON.stringify(config)}\n`, 'utf8')
    await file.sync()
    await file.close()
    file = null
    await fsp.chmod(temporaryPath, 0o600)
    await fsp.rename(temporaryPath, configPath)
    await fsp.chmod(configPath, 0o600)
  } finally {
    await file?.close().catch(() => {})
    await fsp.unlink(temporaryPath).catch(() => {})
  }
}

function matchesConfig(value, expected) {
  return value?.version === expected.version &&
    value?.pid === expected.pid &&
    value?.port === expected.port &&
    value?.token === expected.token
}

async function removeMatchingConfig(configPath, expected) {
  try {
    const current = JSON.parse(await fsp.readFile(configPath, 'utf8'))
    if (matchesConfig(current, expected)) await fsp.unlink(configPath)
  } catch (error) {
    if (error?.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error
  }
}

function removeMatchingConfigSync(configPath, expected) {
  try {
    const current = JSON.parse(fs.readFileSync(configPath, 'utf8'))
    if (matchesConfig(current, expected)) fs.unlinkSync(configPath)
  } catch (error) {
    if (error?.code !== 'ENOENT' && !(error instanceof SyntaxError)) {
      console.error('Could not remove browser provider config:', error)
    }
  }
}

function matchesSecret(supplied, expected) {
  if (typeof supplied !== 'string' || typeof expected !== 'string') return false
  const left = Buffer.from(supplied)
  const right = Buffer.from(expected)
  return left.length === right.length && timingSafeEqual(left, right)
}

function authorized(request, token) {
  return matchesSecret(request.headers.authorization, `Bearer ${token}`)
}

function sendJson(response, status, body) {
  const encoded = Buffer.from(JSON.stringify(body))
  if (encoded.length > MAX_RESPONSE_BYTES && status < 400) {
    sendJson(response, 413, { ok: false, error: { code: 'output_too_large' } })
    return
  }
  response.writeHead(status, {
    'Cache-Control': 'no-store',
    'Content-Length': encoded.length,
    'Content-Type': 'application/json; charset=utf-8',
    'X-Content-Type-Options': 'nosniff',
  })
  response.end(encoded)
}

function validateRecordingBody(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new BrowserProviderError('invalid_request', 'Recording request must be an object.')
  }
  const allowed = new Set(['projectId', 'threadId', 'recordingId'])
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new BrowserProviderError('invalid_request', `Unknown recording request field ${key}.`)
  }
  for (const key of allowed) {
    if (typeof value[key] !== 'string' || value[key].length < 1 || value[key].length > 256 || value[key].includes('\u0000')) {
      throw new BrowserProviderError('invalid_request', `${key} must be a nonempty string.`)
    }
  }
  return value
}

async function readJsonBody(request) {
  const contentType = request.headers['content-type'] || ''
  if (!/^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(contentType)) {
    throw new BrowserProviderError('invalid_request', 'Content-Type must be application/json.', 415)
  }
  if (request.headers['content-encoding'] && request.headers['content-encoding'] !== 'identity') {
    throw new BrowserProviderError('invalid_request', 'Compressed request bodies are not accepted.', 415)
  }
  const declared = Number(request.headers['content-length'])
  if (Number.isFinite(declared) && declared > MAX_REQUEST_BYTES) {
    throw new BrowserProviderError('request_too_large', 'Request body exceeds the size limit.', 413)
  }
  const chunks = []
  let bytes = 0
  for await (const chunk of request) {
    bytes += chunk.length
    if (bytes > MAX_REQUEST_BYTES) {
      throw new BrowserProviderError('request_too_large', 'Request body exceeds the size limit.', 413)
    }
    chunks.push(chunk)
  }
  if (bytes === 0) throw new BrowserProviderError('invalid_json', 'Request body must contain JSON.')
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'))
  } catch {
    throw new BrowserProviderError('invalid_json', 'Request body is not valid JSON.')
  }
}

class BrowserProviderServer {
  constructor({ app, workspace, environment = process.env }) {
    this.app = app
    this.workspace = workspace
    this.environment = environment
    this.server = null
    this.config = null
    this.configPath = configPathFor(app, environment)
    this.stopping = null
    this.exitCleanup = null
  }

  async start() {
    if (this.server) return this.config
    const token = randomBytes(32).toString('hex')
    this.server = http.createServer((request, response) => {
      void this.handle(request, response).catch((error) => {
        if (!response.headersSent) this.sendError(response, error)
        else response.destroy()
      })
    })
    this.server.keepAliveTimeout = 5_000
    this.server.headersTimeout = 10_000
    await new Promise((resolve, reject) => {
      const onError = (error) => { this.server.off('listening', onListening); reject(error) }
      const onListening = () => { this.server.off('error', onError); resolve() }
      this.server.once('error', onError)
      this.server.once('listening', onListening)
      this.server.listen({ host: '127.0.0.1', port: 0, exclusive: true })
    })
    const address = this.server.address()
    if (!address || typeof address === 'string') throw new Error('Browser provider did not acquire a loopback port.')
    this.config = { version: 1, pid: process.pid, port: address.port, token }
    this.workspace.addProtectedOrigin(`http://127.0.0.1:${address.port}`)
    try {
      await atomicWriteConfig(this.configPath, this.config)
    } catch (error) {
      await new Promise((resolve) => this.server.close(resolve))
      this.server = null
      this.config = null
      throw error
    }
    this.exitCleanup = () => removeMatchingConfigSync(this.configPath, this.config)
    process.once('exit', this.exitCleanup)
    return this.config
  }

  recorderPageURL() {
    if (!this.config) throw new Error('Browser provider is not running.')
    return `http://127.0.0.1:${this.config.port}/v1/recorder/${this.config.token}/index.html`
  }

  async serveRecorderAsset(request, response, requestUrl) {
    if (!requestUrl.pathname.startsWith('/v1/recorder/')) return false
    const parts = requestUrl.pathname.split('/')
    const token = parts.length === 5 ? parts[3] : ''
    // Expected shape: /v1/recorder/<token>/<asset>. Keep malformed and bad-token
    // responses indistinguishable and never reflect the secret.
    const expectedAsset = parts.length === 5 ? RECORDER_ASSETS.get(parts[4]) : undefined
    if (
      request.method !== 'GET' || requestUrl.search || !this.config ||
      !matchesSecret(token, this.config.token) || !expectedAsset
    ) {
      sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
      return true
    }
    const contents = await fsp.readFile(path.join(__dirname, expectedAsset.file))
    response.writeHead(200, {
      'Cache-Control': 'no-store',
      'Content-Length': contents.length,
      'Content-Security-Policy': "default-src 'none'; script-src 'self'; connect-src 'none'; img-src 'none'; media-src 'none'; style-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
      'Content-Type': expectedAsset.contentType,
      'Cross-Origin-Resource-Policy': 'same-origin',
      'Referrer-Policy': 'no-referrer',
      'X-Content-Type-Options': 'nosniff',
    })
    response.end(contents)
    return true
  }

  async handle(request, response) {
    let requestUrl
    try { requestUrl = new URL(request.url, 'http://127.0.0.1') } catch {
      sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
      return
    }
    if (await this.serveRecorderAsset(request, response, requestUrl)) return
    if (!this.config || !authorized(request, this.config.token)) {
      sendJson(response, 401, { ok: false, error: { code: 'unauthorized' } })
      return
    }
    if (request.method !== 'POST' || requestUrl.search) {
      sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
      return
    }
    if (requestUrl.pathname === '/v1/action') {
      const action = validateActionBody(await readJsonBody(request))
      try {
        const result = await this.workspace.perform(action)
        sendJson(response, 200, { ok: true, result })
      } catch (error) {
        this.sendError(response, error)
      }
      return
    }
    if (requestUrl.pathname === '/v1/recording') {
      const input = validateRecordingBody(await readJsonBody(request))
      try {
        const opened = await this.workspace.openRecording(
          input.projectId,
          input.threadId,
          input.recordingId,
          request.headers.range,
        )
        const headers = {
          'Accept-Ranges': 'bytes',
          'Cache-Control': 'no-store',
          'Content-Disposition': `attachment; filename="${opened.recording.filename}"`,
          'Content-Length': opened.range?.length ?? opened.recording.bytes,
          'Content-Type': opened.recording.mimeType,
          'X-Content-Type-Options': 'nosniff',
          'X-Kiwi-Code-Recording-Title': encodeURIComponent(opened.recording.title),
        }
        if (opened.range) {
          headers['Content-Range'] = `bytes ${opened.range.start}-${opened.range.end}/${opened.range.totalBytes}`
        }
        response.writeHead(opened.range ? 206 : 200, headers)
        await pipeline(opened.stream, response)
      } catch (error) {
        if (response.headersSent) throw error
        this.sendError(response, error)
      }
      return
    }
    sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
  }

  sendError(response, error) {
    if (error instanceof BrowserProviderError) {
      if (
        error.code === 'recording_range_not_satisfiable' &&
        Number.isSafeInteger(error.totalBytes) && error.totalBytes > 0
      ) {
        response.setHeader('Accept-Ranges', 'bytes')
        response.setHeader('Content-Range', `bytes */${error.totalBytes}`)
      }
      sendJson(response, error.status, { ok: false, error: { code: error.code } })
      return
    }
    console.error('Electron browser provider operation failed:', error)
    sendJson(response, 500, { ok: false, error: { code: 'operation_failed' } })
  }

  stop() {
    if (this.stopping) return this.stopping
    this.stopping = (async () => {
      const server = this.server
      this.server = null
      if (server) {
        await new Promise((resolve) => {
          let settled = false
          let forceTimer
          const finish = () => {
            if (settled) return
            settled = true
            clearTimeout(forceTimer)
            resolve()
          }
          server.close(finish)
          forceTimer = setTimeout(() => {
            server.closeAllConnections?.()
            finish()
          }, 3_000)
          forceTimer.unref?.()
        })
      }
      if (this.config) await removeMatchingConfig(this.configPath, this.config)
      if (this.exitCleanup) process.off('exit', this.exitCleanup)
      this.exitCleanup = null
    })()
    return this.stopping
  }
}

module.exports = {
  BrowserProviderServer,
  atomicWriteConfig,
  configPathFor,
  matchesConfig,
  removeMatchingConfig,
  validateRecordingBody,
}
