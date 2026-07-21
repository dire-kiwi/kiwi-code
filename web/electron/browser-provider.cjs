'use strict'

const { randomBytes, timingSafeEqual } = require('node:crypto')
const fs = require('node:fs')
const fsp = require('node:fs/promises')
const http = require('node:http')
const path = require('node:path')
const { BrowserProviderError, validateActionBody } = require('./browser-helpers.cjs')

const MAX_REQUEST_BYTES = 2 * 1024 * 1024
const MAX_RESPONSE_BYTES = 24 * 1024 * 1024

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

function authorized(request, token) {
  const supplied = request.headers.authorization
  if (typeof supplied !== 'string') return false
  const expected = `Bearer ${token}`
  const left = Buffer.from(supplied)
  const right = Buffer.from(expected)
  return left.length === right.length && timingSafeEqual(left, right)
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

  async handle(request, response) {
    if (!this.config || !authorized(request, this.config.token)) {
      sendJson(response, 401, { ok: false, error: { code: 'unauthorized' } })
      return
    }
    let requestUrl
    try { requestUrl = new URL(request.url, 'http://127.0.0.1') } catch {
      sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
      return
    }
    if (request.method !== 'POST' || requestUrl.pathname !== '/v1/action' || requestUrl.search) {
      sendJson(response, 404, { ok: false, error: { code: 'not_found' } })
      return
    }
    const action = validateActionBody(await readJsonBody(request))
    try {
      const result = await this.workspace.perform(action)
      sendJson(response, 200, { ok: true, result })
    } catch (error) {
      this.sendError(response, error)
    }
  }

  sendError(response, error) {
    if (error instanceof BrowserProviderError) {
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
}
