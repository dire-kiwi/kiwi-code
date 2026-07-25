'use strict'

const fsp = require('node:fs/promises')
const path = require('node:path')
const { randomBytes } = require('node:crypto')

const MAX_ACTIVE_RECORDINGS = 2
const MAX_RECORDING_BYTES = 1024 * 1024 * 1024
const MAX_RECORDING_DURATION_MS = 60 * 60 * 1000
const MIN_IDLE_TIMEOUT_MS = 1_000
const MAX_IDLE_TIMEOUT_MS = MAX_RECORDING_DURATION_MS
const RECORDING_RETENTION_MS = 24 * 60 * 60 * 1000
const MAX_RETAINED_RECORDINGS = 20
const MAX_RETAINED_BYTES = 4 * 1024 * 1024 * 1024
const FRAME_INTERVAL_MS = 100
const TRANSITION_TIMEOUT_MS = 15_000
const ID_PATTERN = /^rec-[a-f0-9]{32}$/
const MIME_TYPES = new Set(['video/webm', 'video/webm;codecs=vp8', 'video/webm;codecs=vp9'])

function key(projectId, threadId) { return `${projectId}\0${threadId}` }
function wait(milliseconds) { return new Promise((resolve) => setTimeout(resolve, milliseconds)) }
function deferred() {
  let resolve, reject
  const promise = new Promise((onResolve, onReject) => { resolve = onResolve; reject = onReject })
  promise.catch(() => {})
  return { promise, resolve, reject }
}
function withTimeout(promise, milliseconds, error) {
  let timer
  const timeout = new Promise((_resolve, reject) => { timer = setTimeout(() => reject(error), milliseconds); timer.unref?.() })
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer))
}
function recordingTitle(value, error) {
  if (typeof value !== 'string' || value.includes('\0')) throw error('invalid_params')
  const title = value.replace(/\s+/g, ' ').trim()
  const words = title.split(' ').filter(Boolean)
  if (title.length < 3 || title.length > 80 || words.length < 2 || words.length > 12 || /[\x00-\x1f\x7f]/.test(title)) {
    throw error('invalid_params')
  }
  return title
}
function publicCompleted(recording) {
  return {
    id: recording.id, state: 'completed', targetId: recording.targetId, title: recording.title,
    startedAt: recording.startedAt, finishedAt: recording.finishedAt, durationMs: recording.durationMs,
    bytes: recording.bytes, mimeType: recording.mimeType, filename: recording.filename,
  }
}
function publicActive(recording) {
  return {
    id: recording.id, state: recording.state, targetId: recording.targetId, title: recording.title,
    startedAt: recording.startedAt, mimeType: recording.mimeType || undefined,
    ...(recording.idleTimeoutMs ? { idleTimeoutMs: recording.idleTimeoutMs, idleDeadlineAt: recording.idleDeadlineAt || undefined } : {}),
  }
}
function validMetadata(value) {
  return value && value.version === 1 && ID_PATTERN.test(value.id) && value.filename === `${value.id}.webm` &&
    typeof value.projectId === 'string' && typeof value.threadId === 'string' && typeof value.targetId === 'string' &&
    typeof value.title === 'string' && value.title.length >= 3 && value.title.length <= 80 &&
    Number.isInteger(value.durationMs) && value.durationMs >= 0 &&
    Number.isInteger(value.bytes) && value.bytes > 0 && value.bytes <= MAX_RECORDING_BYTES && MIME_TYPES.has(value.mimeType) &&
    Number.isFinite(Date.parse(value.startedAt)) && Number.isFinite(Date.parse(value.finishedAt))
}

class HeadlessRecordingManager {
  constructor(directory, errorFactory) {
    this.directory = directory
    this.error = errorFactory
    this.active = new Map()
    this.completed = new Map()
    this.initialized = false
  }

  metadataPath(id) { return path.join(this.directory, `${id}.json`) }
  videoPath(id) { return path.join(this.directory, `${id}.webm`) }
  partialPath(id) { return path.join(this.directory, `${id}.webm.part`) }

  async initialize() {
    if (this.initialized) return
    if (!this.directory) throw this.error('recording_failed')
    await fsp.mkdir(this.directory, { recursive: true, mode: 0o700 })
    await fsp.chmod(this.directory, 0o700).catch(() => {})
    const entries = await fsp.readdir(this.directory, { withFileTypes: true })
    for (const entry of entries) {
      if (entry.isFile() && (entry.name.endsWith('.part') || entry.name.includes('.tmp'))) {
        await fsp.rm(path.join(this.directory, entry.name), { force: true }).catch(() => {})
      }
    }
    for (const entry of entries) {
      if (!entry.isFile() || !entry.name.endsWith('.json')) continue
      const metadataPath = path.join(this.directory, entry.name)
      try {
        const stat = await fsp.lstat(metadataPath)
        if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 16 * 1024) throw new Error('metadata')
        const recording = JSON.parse(await fsp.readFile(metadataPath, 'utf8'))
        if (!validMetadata(recording) || entry.name !== `${recording.id}.json`) throw new Error('metadata')
        const video = await fsp.lstat(this.videoPath(recording.id))
        if (!video.isFile() || video.isSymbolicLink() || video.size !== recording.bytes) throw new Error('video')
        this.completed.set(recording.id, recording)
      } catch {
        const id = entry.name.slice(0, -5)
        await fsp.rm(metadataPath, { force: true }).catch(() => {})
        if (ID_PATTERN.test(id)) await fsp.rm(this.videoPath(id), { force: true }).catch(() => {})
      }
    }
    this.initialized = true
    await this.prune()
  }

  snapshot(projectId, threadId) {
    const active = this.active.get(key(projectId, threadId))
    return {
      recording: active ? publicActive(active) : null,
      recordings: [...this.completed.values()]
        .filter((recording) => recording.projectId === projectId && recording.threadId === threadId)
        .sort((left, right) => right.finishedAt.localeCompare(left.finishedAt))
        .map(publicCompleted),
    }
  }

  activeFor(projectId, threadId) { return this.active.get(key(projectId, threadId)) || null }
  isTarget(projectId, threadId, targetId) { return this.activeFor(projectId, threadId)?.targetId === targetId }

  touch(projectId, threadId) {
    const active = this.activeFor(projectId, threadId)
    if (active?.state === 'recording' && active.idleTimeoutMs) this.armIdle(active)
    return this.snapshot(projectId, threadId)
  }

  armIdle(active) {
    clearTimeout(active.idleTimer)
    if (!active.idleTimeoutMs || active.state !== 'recording' || this.active.get(active.key) !== active) return
    active.idleDeadlineAt = new Date(Date.now() + active.idleTimeoutMs).toISOString()
    active.idleTimer = setTimeout(() => { void this.stop(active.projectId, active.threadId, active.id).catch(() => this.abort(active)) }, active.idleTimeoutMs)
    active.idleTimer.unref?.()
  }

  async start({ browser, projectId, threadId, targetId, title, sourcePage, capturePage, idleTimeoutMs }) {
    await this.initialize()
    const sessionKey = key(projectId, threadId)
    if (this.active.has(sessionKey)) throw this.error('recording_active')
    if (this.active.size >= MAX_ACTIVE_RECORDINGS) throw this.error('recording_limit_reached')
    if (!sourcePage || sourcePage.isClosed()) throw this.error('page_not_found')
    title = recordingTitle(title, this.error)
    if (idleTimeoutMs !== undefined && (!Number.isInteger(idleTimeoutMs) || idleTimeoutMs < MIN_IDLE_TIMEOUT_MS || idleTimeoutMs > MAX_IDLE_TIMEOUT_MS)) {
      throw this.error('invalid_params')
    }
    const id = `rec-${randomBytes(16).toString('hex')}`
    const file = await fsp.open(this.partialPath(id), 'wx', 0o600)
    const context = await browser.createBrowserContext()
    const recorderPage = await context.newPage()
    await recorderPage.setViewport({ width: 1280, height: 720, deviceScaleFactor: 1 })
    const active = {
      id, key: sessionKey, projectId, threadId, targetId, title, sourcePage, capturePage, context, recorderPage, file,
      state: 'starting', startedAt: new Date().toISOString(), startedMs: Date.now(), mimeType: '', bytes: 0,
      sequence: 0, writes: Promise.resolve(), done: deferred(), captureActive: false, captureLoop: null,
      idleTimeoutMs: idleTimeoutMs || null, idleDeadlineAt: null, idleTimer: null, durationTimer: null, closing: false,
    }
    this.active.set(sessionKey, active)
    try {
      await recorderPage.exposeFunction('__kiwiRecordingEvent', async (event) => this.handleEvent(active, event))
      active.mimeType = await recorderPage.evaluate(async ({ id }) => {
        document.documentElement.innerHTML = '<body style="margin:0;background:#000"><canvas width="1280" height="720"></canvas></body>'
        const canvas = document.querySelector('canvas')
        const context = canvas.getContext('2d', { alpha: false, desynchronized: true })
        context.fillStyle = '#000'; context.fillRect(0, 0, canvas.width, canvas.height)
        const stream = canvas.captureStream(10)
        const candidates = ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm']
        const mimeType = candidates.find((candidate) => MediaRecorder.isTypeSupported(candidate))
        if (!mimeType) throw new Error('No WebM encoder')
        const recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: 2_000_000 })
        const state = { id, canvas, context, stream, recorder, sequence: 0, writes: Promise.resolve() }
        globalThis.__kiwiRecorder = state
        function blobBase64(blob) {
          return blob.arrayBuffer().then((buffer) => {
            const bytes = new Uint8Array(buffer); let binary = ''
            for (let index = 0; index < bytes.length; index += 8192) binary += String.fromCharCode(...bytes.subarray(index, index + 8192))
            return btoa(binary)
          })
        }
        recorder.addEventListener('dataavailable', (event) => {
          if (!event.data?.size) return
          const sequence = state.sequence++
          state.writes = state.writes.then(async () => globalThis.__kiwiRecordingEvent({ type: 'chunk', id, sequence, data: await blobBase64(event.data) }))
        })
        recorder.addEventListener('error', (event) => { void globalThis.__kiwiRecordingEvent({ type: 'error', id, message: String(event.error?.message || 'encoder') }) })
        recorder.addEventListener('stop', () => { void state.writes.then(() => globalThis.__kiwiRecordingEvent({ type: 'stopped', id })) })
        recorder.start(1000)
        return recorder.mimeType
      }, { id })
      if (!MIME_TYPES.has(active.mimeType)) throw this.error('recording_failed')
      active.captureActive = true
      active.firstFrame = deferred()
      active.captureLoop = this.captureFrames(active)
      await withTimeout(active.firstFrame.promise, 5_000, this.error('recording_failed'))
      active.state = 'recording'
      active.startedAt = new Date().toISOString(); active.startedMs = Date.now()
      active.durationTimer = setTimeout(() => { void this.stop(projectId, threadId, id).catch(() => this.abort(active)) }, MAX_RECORDING_DURATION_MS)
      active.durationTimer.unref?.()
      this.armIdle(active)
      return publicActive(active)
    } catch (error) {
      await this.abort(active)
      throw error?.code ? error : this.error('recording_failed')
    }
  }

  async captureFrames(active) {
    let failures = 0
    while (active.captureActive && !active.closing && this.active.get(active.key) === active) {
      const started = Date.now()
      try {
        if (active.sourcePage.isClosed()) throw this.error('page_not_found')
        const data = await active.capturePage()
        if (typeof data !== 'string' || data.length < 4 || data.length > 12 * 1024 * 1024) throw new Error('frame')
        await active.recorderPage.evaluate(async (jpeg) => {
          const state = globalThis.__kiwiRecorder
          const response = await fetch(`data:image/jpeg;base64,${jpeg}`)
          const bitmap = await createImageBitmap(await response.blob())
          try {
            const scale = Math.min(state.canvas.width / bitmap.width, state.canvas.height / bitmap.height)
            const width = Math.max(1, Math.round(bitmap.width * scale)), height = Math.max(1, Math.round(bitmap.height * scale))
            const x = Math.floor((state.canvas.width - width) / 2), y = Math.floor((state.canvas.height - height) / 2)
            state.context.fillStyle = '#000'; state.context.fillRect(0, 0, state.canvas.width, state.canvas.height)
            state.context.drawImage(bitmap, x, y, width, height)
          } finally { bitmap.close() }
        }, data)
        failures = 0
        if (active.firstFrame) { active.firstFrame.resolve(); active.firstFrame = null }
      } catch (error) {
        failures += 1
        if (failures > 3) { if (active.firstFrame) active.firstFrame.reject(error); void this.abort(active); return }
      }
      const remaining = FRAME_INTERVAL_MS - (Date.now() - started)
      if (remaining > 0) await wait(remaining)
    }
  }

  async handleEvent(active, event) {
    if (!event || event.id !== active.id || this.active.get(active.key) !== active) throw this.error('recording_failed')
    if (event.type === 'chunk') {
      if (!Number.isInteger(event.sequence) || event.sequence !== active.sequence || typeof event.data !== 'string' || event.data.length > 24 * 1024 * 1024) throw this.error('recording_failed')
      const chunk = Buffer.from(event.data, 'base64')
      if (!chunk.length || active.bytes + chunk.length > MAX_RECORDING_BYTES) throw this.error('recording_failed')
      await active.file.write(chunk)
      active.sequence += 1; active.bytes += chunk.length
      return true
    }
    if (event.type === 'stopped') { await this.finalize(active); return true }
    if (event.type === 'error') { await this.abort(active); throw this.error('recording_failed') }
    throw this.error('recording_failed')
  }

  async stop(projectId, threadId, recordingId) {
    const active = this.activeFor(projectId, threadId)
    if (!active || (recordingId && recordingId !== active.id)) throw this.error('recording_not_active')
    if (active.state === 'recording' || active.state === 'starting') {
      active.state = 'finalizing'; clearTimeout(active.durationTimer); clearTimeout(active.idleTimer); active.idleDeadlineAt = null
      active.captureActive = false
      await active.captureLoop?.catch(() => {})
      await active.recorderPage.evaluate(() => { const recorder = globalThis.__kiwiRecorder?.recorder; if (recorder?.state !== 'inactive') recorder.stop() })
    }
    return publicCompleted(await withTimeout(active.done.promise, TRANSITION_TIMEOUT_MS, this.error('recording_failed')))
  }

  async finalize(active) {
    if (active.closing || this.active.get(active.key) !== active) return
    if (!active.bytes || !MIME_TYPES.has(active.mimeType)) { await this.abort(active); return }
    active.closing = true
    await active.file.sync(); await active.file.close(); active.file = null
    await fsp.rename(this.partialPath(active.id), this.videoPath(active.id)); await fsp.chmod(this.videoPath(active.id), 0o600)
    const recording = {
      version: 1, id: active.id, projectId: active.projectId, threadId: active.threadId, targetId: active.targetId,
      title: active.title, startedAt: active.startedAt, finishedAt: new Date().toISOString(),
      durationMs: Math.max(0, Date.now() - active.startedMs), bytes: active.bytes, mimeType: active.mimeType, filename: `${active.id}.webm`,
    }
    const temporary = `${this.metadataPath(active.id)}.${randomBytes(8).toString('hex')}.tmp`
    await fsp.writeFile(temporary, `${JSON.stringify(recording)}\n`, { mode: 0o600, flag: 'wx' })
    await fsp.rename(temporary, this.metadataPath(active.id)); await fsp.chmod(this.metadataPath(active.id), 0o600)
    this.completed.set(recording.id, recording); this.active.delete(active.key)
    await active.context.close().catch(() => {})
    active.done.resolve(recording)
    await this.prune()
  }

  async abort(active) {
    if (!active || active.closing) return
    active.closing = true; active.captureActive = false
    clearTimeout(active.durationTimer); clearTimeout(active.idleTimer)
    if (this.active.get(active.key) === active) this.active.delete(active.key)
    active.done.reject(this.error('recording_failed'))
    await active.file?.close().catch(() => {}); active.file = null
    await active.context?.close().catch(() => {})
    await Promise.all([this.partialPath(active.id), this.videoPath(active.id), this.metadataPath(active.id)].map((item) => fsp.rm(item, { force: true }).catch(() => {})))
  }

  async delete(projectId, threadId, recordingId) {
    if (!ID_PATTERN.test(recordingId)) throw this.error('recording_not_found')
    const recording = this.completed.get(recordingId)
    if (!recording || recording.projectId !== projectId || recording.threadId !== threadId) throw this.error('recording_not_found')
    await Promise.all([fsp.rm(this.videoPath(recordingId), { force: true }), fsp.rm(this.metadataPath(recordingId), { force: true })])
    this.completed.delete(recordingId)
    return { deleted: true, recordingId }
  }

  async prune() {
    const ordered = [...this.completed.values()].sort((left, right) => right.finishedAt.localeCompare(left.finishedAt))
    const now = Date.now()
    let retainedBytes = 0
    for (let index = 0; index < ordered.length; index += 1) {
      const recording = ordered[index]
      retainedBytes += recording.bytes
      if (index < MAX_RETAINED_RECORDINGS && retainedBytes <= MAX_RETAINED_BYTES && now - Date.parse(recording.finishedAt) <= RECORDING_RETENTION_MS) continue
      this.completed.delete(recording.id)
      await Promise.all([fsp.rm(this.videoPath(recording.id), { force: true }).catch(() => {}), fsp.rm(this.metadataPath(recording.id), { force: true }).catch(() => {})])
    }
  }

  async dispose() {
    for (const active of [...this.active.values()]) {
      try { await this.stop(active.projectId, active.threadId, active.id) } catch { await this.abort(active) }
    }
  }
}

module.exports = { HeadlessRecordingManager, recordingTitle }
