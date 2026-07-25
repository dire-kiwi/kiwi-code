'use strict'

const { randomBytes } = require('node:crypto')
const fs = require('node:fs')
const fsp = require('node:fs/promises')
const path = require('node:path')
const { BrowserProviderError, isRecord } = require('./browser-helpers.cjs')

const BROWSER_RECORDER_COMMAND_CHANNEL = 'kiwi-code-browser-recorder:command'
const BROWSER_RECORDER_EVENT_CHANNEL = 'kiwi-code-browser-recorder:event'
const RECORDING_VERSION = 1
const RECORDING_DIRECTORY = 'browser-recordings'
const RECORDING_MIME_TYPES = new Set([
  'video/webm',
  'video/webm;codecs=vp8',
  'video/webm;codecs=vp9',
])
const RECORDING_ID_PATTERN = /^rec-[a-f0-9]{32}$/
const MAX_ACTIVE_RECORDINGS = 2
const MAX_RECORDING_BYTES = 1024 * 1024 * 1024
const MAX_RECORDING_DURATION_MS = 60 * 60 * 1000
const MIN_RECORDING_IDLE_TIMEOUT_MS = 1_000
const MAX_RECORDING_IDLE_TIMEOUT_MS = MAX_RECORDING_DURATION_MS
const MAX_RECORDING_CHUNK_BYTES = 16 * 1024 * 1024
const MAX_RETAINED_RECORDINGS = 20
const MAX_RETAINED_RECORDING_BYTES = 4 * 1024 * 1024 * 1024
const MIN_RECORDING_FREE_BYTES = 256 * 1024 * 1024
const MAX_RECORDING_METADATA_BYTES = 16 * 1024
const RECORDING_RETENTION_MS = 24 * 60 * 60 * 1000
const RECORDING_PRUNE_INTERVAL_MS = 60 * 60 * 1000
const RECORDER_START_TIMEOUT_MS = 10_000
const RECORDER_STOP_TIMEOUT_MS = 15_000
const FIRST_RECORDING_FRAME_TIMEOUT_MS = 5_000
const RECORDING_FRAME_INTERVAL_MS = Math.round(1000 / 15)
const MAX_CONSECUTIVE_FRAME_FAILURES = 3
const MAX_RECORDING_FRAME_BYTES = 8 * 1024 * 1024
const RECORDER_VIEWPORT = { x: 0, y: 0, width: 320, height: 180 }

function sessionKey(projectId, threadId) {
  return `${projectId}\u0000${threadId}`
}

function deferred() {
  let resolve
  let reject
  const promise = new Promise((onResolve, onReject) => {
    resolve = onResolve
    reject = onReject
  })
  // A renderer can fail while no operation is awaiting this transition.
  promise.catch(() => {})
  return { promise, resolve, reject }
}

function boundedText(value, maximum = 256) {
  return typeof value === 'string' && value.length > 0 && value.length <= maximum && !value.includes('\u0000')
}

function validTimestamp(value) {
  return typeof value === 'string' && Number.isFinite(Date.parse(value))
}

function recordingTitle(value) {
  if (typeof value !== 'string' || value.includes('\u0000')) throw recordingError('invalid_params', 'A short recording title is required.', 400)
  const title = value.replace(/\s+/g, ' ').trim()
  const words = title.split(' ').filter(Boolean)
  if (title.length < 3 || title.length > 80 || words.length < 2 || words.length > 12 || /[\x00-\x1f\x7f]/.test(title)) {
    throw recordingError('invalid_params', 'Recording titles must be 2 to 12 words and at most 80 characters.', 400)
  }
  return title
}

function publicRecording(recording) {
  return {
    id: recording.id,
    state: 'completed',
    targetId: recording.targetId,
    title: recording.title,
    startedAt: recording.startedAt,
    finishedAt: recording.finishedAt,
    durationMs: recording.durationMs,
    bytes: recording.bytes,
    mimeType: recording.mimeType,
    filename: recording.filename,
  }
}

function publicActiveRecording(recording) {
  return {
    id: recording.id,
    state: recording.state,
    targetId: recording.targetId,
    title: recording.title,
    startedAt: recording.startedAt,
    mimeType: recording.mimeType || undefined,
    ...(recording.idleTimeoutMs
      ? { idleTimeoutMs: recording.idleTimeoutMs, idleDeadlineAt: recording.idleDeadlineAt || undefined }
      : {}),
  }
}

function validPersistedRecording(value) {
  const allowed = new Set([
    'version', 'id', 'projectId', 'threadId', 'targetId', 'title', 'startedAt',
    'finishedAt', 'durationMs', 'bytes', 'mimeType', 'filename',
  ])
  return isRecord(value) &&
    Object.keys(value).every((key) => allowed.has(key)) && Object.keys(value).length === allowed.size &&
    value.version === RECORDING_VERSION &&
    typeof value.id === 'string' && RECORDING_ID_PATTERN.test(value.id) &&
    boundedText(value.projectId) && boundedText(value.threadId) && boundedText(value.targetId, 128) &&
    boundedText(value.title, 80) && value.title.trim().split(/\s+/).length >= 2 && value.title.trim().split(/\s+/).length <= 12 &&
    validTimestamp(value.startedAt) && validTimestamp(value.finishedAt) &&
    Number.isInteger(value.durationMs) && value.durationMs >= 0 && value.durationMs <= MAX_RECORDING_DURATION_MS + RECORDER_STOP_TIMEOUT_MS &&
    Number.isInteger(value.bytes) && value.bytes > 0 && value.bytes <= MAX_RECORDING_BYTES &&
    typeof value.mimeType === 'string' && RECORDING_MIME_TYPES.has(value.mimeType) &&
    typeof value.filename === 'string' && value.filename === `${value.id}.webm`
}

function recordingError(code, message, status = 422) {
  return new BrowserProviderError(code, message, status, false)
}

function recordingIdleTimeout(value) {
  if (value === undefined) return null
  if (!Number.isInteger(value) || value < MIN_RECORDING_IDLE_TIMEOUT_MS || value > MAX_RECORDING_IDLE_TIMEOUT_MS) {
    throw recordingError(
      'invalid_params',
      `idleTimeoutMs must be an integer from ${MIN_RECORDING_IDLE_TIMEOUT_MS} to ${MAX_RECORDING_IDLE_TIMEOUT_MS}.`,
      400,
    )
  }
  return value
}

function recordingRangeError(totalBytes) {
  const error = recordingError(
    'recording_range_not_satisfiable',
    'The requested recording byte range is not available.',
    416,
  )
  error.totalBytes = totalBytes
  return error
}

function parseRecordingRange(value, totalBytes) {
  if (value === undefined) return null
  if (
    typeof value !== 'string' || value.length < 1 || value.length > 128 ||
    !Number.isSafeInteger(totalBytes) || totalBytes < 1
  ) {
    throw recordingRangeError(totalBytes)
  }
  const match = /^bytes=(\d*)-(\d*)$/i.exec(value)
  if (!match || (!match[1] && !match[2])) throw recordingRangeError(totalBytes)

  const parsePart = (part) => {
    if (!part) return null
    const parsed = Number(part)
    if (!Number.isSafeInteger(parsed) || parsed < 0) throw recordingRangeError(totalBytes)
    return parsed
  }
  const first = parsePart(match[1])
  const last = parsePart(match[2])
  let start
  let end
  if (first === null) {
    if (!last) throw recordingRangeError(totalBytes)
    start = Math.max(0, totalBytes - last)
    end = totalBytes - 1
  } else {
    if (first >= totalBytes) throw recordingRangeError(totalBytes)
    start = first
    end = last === null ? totalBytes - 1 : Math.min(last, totalBytes - 1)
    if (end < start) throw recordingRangeError(totalBytes)
  }
  return { start, end, length: end - start + 1, totalBytes }
}

function safeErrorText(value, maximum = 500) {
  const text = value instanceof Error ? value.message : String(value)
  return text
    .replace(/https?:\/\/\S+/gi, '[url]')
    .replace(/\b[a-f0-9]{64}\b/gi, '[secret]')
    .replace(/[\r\n]/g, ' ')
    .slice(0, maximum)
}

function recordingElapsedMs(active) {
  if (typeof active.startedMonotonic === 'bigint') {
    return Number((process.hrtime.bigint() - active.startedMonotonic) / 1_000_000n)
  }
  return Math.max(0, Date.now() - Date.parse(active.startedAt))
}

function wait(milliseconds) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, milliseconds)
    timer.unref?.()
  })
}

function timeoutAfter(milliseconds, error) {
  let timer
  const promise = new Promise((_resolve, reject) => {
    timer = setTimeout(() => reject(error), milliseconds)
    timer.unref?.()
  })
  return { promise, cancel: () => clearTimeout(timer) }
}

async function atomicWritePrivate(filePath, contents) {
  const temporaryPath = `${filePath}.${process.pid}.${randomBytes(8).toString('hex')}.tmp`
  let handle
  try {
    handle = await fsp.open(temporaryPath, 'wx', 0o600)
    await handle.writeFile(contents)
    await handle.sync()
    await handle.close()
    handle = null
    await fsp.chmod(temporaryPath, 0o600)
    await fsp.rename(temporaryPath, filePath)
    await fsp.chmod(filePath, 0o600)
  } finally {
    await handle?.close().catch(() => {})
    await fsp.rm(temporaryPath, { force: true }).catch(() => {})
  }
}

function bufferFromChunk(value) {
  if (value instanceof ArrayBuffer) return Buffer.from(value)
  if (ArrayBuffer.isView(value)) return Buffer.from(value.buffer, value.byteOffset, value.byteLength)
  return null
}

class BrowserRecordingManager {
  constructor({ app, BaseWindow, WebContentsView }) {
    this.app = app
    this.BaseWindow = BaseWindow
    this.WebContentsView = WebContentsView
    this.recorderPageURL = ''
    this.directory = path.join(app.getPath('userData'), RECORDING_DIRECTORY)
    this.active = new Map()
    this.completed = new Map()
    this.initialized = false
    this.disposed = false
    this.disposePromise = null
    this.pruneTimer = null
  }

  async initialize() {
    if (this.initialized) return
    await fsp.mkdir(this.directory, { recursive: true, mode: 0o700 })
    await fsp.chmod(this.directory, 0o700).catch(() => {})
    const entries = await fsp.readdir(this.directory, { withFileTypes: true })
    const metadataNames = new Set(entries.filter((entry) => entry.isFile() && entry.name.endsWith('.json')).map((entry) => entry.name))

    for (const entry of entries) {
      if (!entry.isFile()) continue
      if (entry.name.endsWith('.part') || entry.name.includes('.tmp')) {
        await fsp.rm(path.join(this.directory, entry.name), { force: true }).catch(() => {})
      }
    }

    for (const name of metadataNames) {
      const metadataPath = path.join(this.directory, name)
      try {
        const metadataStat = await fsp.lstat(metadataPath)
        if (!metadataStat.isFile() || metadataStat.isSymbolicLink() || metadataStat.size > MAX_RECORDING_METADATA_BYTES) {
          throw new Error('invalid metadata file')
        }
        const raw = await fsp.readFile(metadataPath, 'utf8')
        const recording = JSON.parse(raw)
        if (!validPersistedRecording(recording) || name !== `${recording.id}.json`) throw new Error('invalid metadata')
        const videoPath = this.videoPath(recording.id)
        const stat = await fsp.lstat(videoPath)
        if (!stat.isFile() || stat.isSymbolicLink() || stat.size !== recording.bytes) throw new Error('invalid recording file')
        this.completed.set(recording.id, recording)
      } catch {
        const id = name.slice(0, -'.json'.length)
        await Promise.all([
          fsp.rm(metadataPath, { force: true }).catch(() => {}),
          RECORDING_ID_PATTERN.test(id) ? fsp.rm(this.videoPath(id), { force: true }).catch(() => {}) : Promise.resolve(),
        ])
      }
    }

    for (const entry of entries) {
      if (!entry.isFile() || !entry.name.endsWith('.webm')) continue
      const id = entry.name.slice(0, -'.webm'.length)
      if (!this.completed.has(id)) await fsp.rm(path.join(this.directory, entry.name), { force: true }).catch(() => {})
    }
    this.initialized = true
    await this.pruneCompleted()
    this.pruneTimer = setInterval(() => {
      void this.pruneCompleted().catch((error) => console.error('Could not prune browser recordings:', error))
    }, RECORDING_PRUNE_INTERVAL_MS)
    this.pruneTimer.unref?.()
  }

  setRecorderPageURL(url) {
    const parsed = new URL(url)
    if (parsed.protocol !== 'http:' || parsed.hostname !== '127.0.0.1' || parsed.username || parsed.password || parsed.search || parsed.hash) {
      throw new Error('Recorder page URL must use an uncredentialed loopback HTTP URL.')
    }
    this.recorderPageURL = parsed.toString()
  }

  metadataPath(id) { return path.join(this.directory, `${id}.json`) }
  videoPath(id) { return path.join(this.directory, `${id}.webm`) }
  partialPath(id) { return path.join(this.directory, `${id}.webm.part`) }

  snapshot(projectId, threadId) {
    const active = this.active.get(sessionKey(projectId, threadId))
    const recordings = [...this.completed.values()]
      .filter((recording) => recording.projectId === projectId && recording.threadId === threadId)
      .sort((left, right) => right.finishedAt.localeCompare(left.finishedAt))
      .map(publicRecording)
    return {
      recording: active ? publicActiveRecording(active) : null,
      recordings,
    }
  }

  isRecordingTarget(projectId, threadId, targetId) {
    return this.active.get(sessionKey(projectId, threadId))?.targetId === targetId
  }

  clearIdleTimer(active) {
    clearTimeout(active?.idleTimer)
    if (active) {
      active.idleTimer = null
      active.idleDeadlineAt = null
    }
  }

  armIdleTimer(active) {
    this.clearIdleTimer(active)
    if (!active?.idleTimeoutMs || active.state !== 'recording' || this.active.get(active.key) !== active) return
    active.idleDeadlineAt = new Date(Date.now() + active.idleTimeoutMs).toISOString()
    active.idleTimer = setTimeout(() => {
      active.idleTimer = null
      active.idleDeadlineAt = null
      if (this.active.get(active.key) !== active || active.state !== 'recording') return
      void this.stop(active.projectId, active.threadId, active.id).catch(() => this.abortActive(active))
    }, active.idleTimeoutMs)
    active.idleTimer.unref?.()
  }

  touch(projectId, threadId) {
    const active = this.active.get(sessionKey(projectId, threadId))
    if (active?.idleTimeoutMs && active.state === 'recording') this.armIdleTimer(active)
    return this.snapshot(projectId, threadId)
  }

  async start({ projectId, threadId, targetId, title, sourceWebContents, capturePage, releaseSourceView, idleTimeoutMs }) {
    if (!this.initialized || this.disposed || !this.recorderPageURL) {
      throw recordingError('recording_failed', 'Browser recording is unavailable.', 503)
    }
    const key = sessionKey(projectId, threadId)
    const validatedTitle = recordingTitle(title)
    const validatedIdleTimeoutMs = recordingIdleTimeout(idleTimeoutMs)
    if (this.active.has(key)) throw recordingError('recording_active', 'This browser session is already recording.', 409)
    if (this.active.size >= MAX_ACTIVE_RECORDINGS) throw recordingError('recording_limit_reached', 'The browser recording limit has been reached.', 409)
    if (!sourceWebContents || sourceWebContents.isDestroyed() || typeof sourceWebContents.capturePage !== 'function') {
      throw recordingError('page_not_found', 'The selected browser page is closed.', 404)
    }

    if (typeof fsp.statfs === 'function') {
      try {
        const fileSystem = await fsp.statfs(this.directory)
        const available = Number(fileSystem.bavail) * Number(fileSystem.bsize)
        if (Number.isFinite(available) && available < MIN_RECORDING_FREE_BYTES) {
          throw recordingError('recording_failed', 'There is not enough free disk space to start recording.')
        }
      } catch (error) {
        if (error instanceof BrowserProviderError) throw error
        // Size and write limits still fail closed if this platform cannot report free space.
      }
    }

    const id = `rec-${randomBytes(16).toString('hex')}`
    const file = await fsp.open(this.partialPath(id), 'wx', 0o600)
    const ready = deferred()
    const done = deferred()
    const active = {
      id,
      key,
      projectId,
      threadId,
      targetId,
      title: validatedTitle,
      state: 'starting',
      startedAt: new Date().toISOString(),
      startedMonotonic: null,
      mimeType: '',
      bytes: 0,
      sequence: 0,
      file,
      window: null,
      view: null,
      ready,
      started: deferred(),
      done,
      durationTimer: null,
      idleTimeoutMs: validatedIdleTimeoutMs,
      idleDeadlineAt: null,
      idleTimer: null,
      sourceWebContents,
      capturePage: typeof capturePage === 'function'
        ? capturePage
        : () => sourceWebContents.capturePage(undefined, { stayAwake: true, stayHidden: true }),
      releaseSourceView: typeof releaseSourceView === 'function' ? releaseSourceView : () => {},
      captureActive: false,
      captureLoop: null,
      frameSequence: 0,
      pendingFrameSequence: null,
      frameAcknowledged: null,
      firstFrame: deferred(),
      closing: false,
    }
    this.active.set(key, active)

    try {
      this.createRecorderSurface(active)
      await active.view.webContents.loadURL(this.recorderPageURL)
      if (sourceWebContents.isDestroyed()) throw recordingError('page_not_found', 'The selected browser page closed before recording started.', 404)
      active.view.webContents.send(BROWSER_RECORDER_COMMAND_CHANNEL, {
        type: 'start',
        id,
        mode: 'frames',
        width: 1280,
        height: 720,
        maxBytes: MAX_RECORDING_BYTES,
        maxDurationMs: MAX_RECORDING_DURATION_MS,
      })
      const timeout = timeoutAfter(RECORDER_START_TIMEOUT_MS, recordingError('recording_failed', 'Browser recording did not start in time.'))
      try {
        await Promise.race([ready.promise, timeout.promise])
      } finally {
        timeout.cancel()
      }
      await wait(100)
      this.startFrameCapture(active)
      const frameTimeout = timeoutAfter(FIRST_RECORDING_FRAME_TIMEOUT_MS, recordingError('recording_failed', 'The browser did not produce a recording frame.'))
      try {
        await Promise.race([active.firstFrame.promise, frameTimeout.promise])
      } finally {
        frameTimeout.cancel()
      }
      active.state = 'recording'
      const remainingDuration = Math.max(1, MAX_RECORDING_DURATION_MS - recordingElapsedMs(active))
      active.durationTimer = setTimeout(() => {
        void this.stop(projectId, threadId).catch(() => this.abortActive(active))
      }, remainingDuration)
      active.durationTimer.unref?.()
      this.armIdleTimer(active)
      active.started.resolve()
      return publicActiveRecording(active)
    } catch (error) {
      console.error(`Could not start browser recording: ${safeErrorText(error)}`)
      await this.abortActive(active, error)
      if (error instanceof BrowserProviderError) throw error
      throw recordingError('recording_failed', 'Browser recording could not start.')
    }
  }

  startFrameCapture(active) {
    active.captureActive = true
    active.captureLoop = this.captureFrames(active)
    active.captureLoop.catch((error) => {
      console.error(`Browser frame capture failed: ${safeErrorText(error)}`)
      active.firstFrame.reject(error)
      if (!active.closing) void this.abortActive(active, error)
    })
  }

  async captureFrames(active) {
    let consecutiveCaptureFailures = 0
    while (active.captureActive && this.active.get(active.key) === active && !active.closing) {
      const started = Date.now()
      let frame
      try {
        const captureTimeout = timeoutAfter(
          FIRST_RECORDING_FRAME_TIMEOUT_MS,
          recordingError('recording_failed', 'The browser did not return a recording frame in time.'),
        )
        let image
        try {
          image = await Promise.race([
            active.capturePage(),
            captureTimeout.promise,
          ])
        } finally {
          captureTimeout.cancel()
        }
        if (!image || image.isEmpty()) throw new Error('The browser returned an empty recording frame.')
        const size = image.getSize()
        if (!Number.isFinite(size.width) || size.width < 1 || !Number.isFinite(size.height) || size.height < 1) {
          throw new Error('The browser returned an invalid recording frame.')
        }
        const scale = Math.min(1, 1280 / size.width, 720 / size.height)
        const encodedImage = scale < 1
          ? image.resize({
            width: Math.max(1, Math.round(size.width * scale)),
            height: Math.max(1, Math.round(size.height * scale)),
            quality: 'good',
          })
          : image
        frame = encodedImage.toJPEG(80)
        if (!frame || frame.length < 1 || frame.length > MAX_RECORDING_FRAME_BYTES) {
          throw new Error('The browser recording frame exceeded its size limit.')
        }
        consecutiveCaptureFailures = 0
      } catch (error) {
        consecutiveCaptureFailures += 1
        if (consecutiveCaptureFailures <= MAX_CONSECUTIVE_FRAME_FAILURES && active.captureActive && !active.closing) {
          await wait(100)
          continue
        }
        throw error
      }
      if (!active.captureActive || active.closing || this.active.get(active.key) !== active) break
      const sequence = active.frameSequence
      active.frameSequence += 1
      active.pendingFrameSequence = sequence
      active.frameAcknowledged = deferred()
      active.view.webContents.send(BROWSER_RECORDER_COMMAND_CHANNEL, {
        type: 'frame',
        id: active.id,
        sessionId: sequence,
        data: frame.buffer.slice(frame.byteOffset, frame.byteOffset + frame.byteLength),
      })
      const acknowledgementTimeout = timeoutAfter(
        FIRST_RECORDING_FRAME_TIMEOUT_MS,
        recordingError('recording_failed', 'The browser recorder did not accept a frame.'),
      )
      try {
        await Promise.race([active.frameAcknowledged.promise, acknowledgementTimeout.promise])
      } finally {
        acknowledgementTimeout.cancel()
      }
      active.firstFrame.resolve()
      const remaining = RECORDING_FRAME_INTERVAL_MS - (Date.now() - started)
      if (remaining > 0 && active.captureActive) await wait(remaining)
    }
  }

  async stopFrameCapture(active) {
    active.captureActive = false
    active.frameAcknowledged?.resolve()
    if (active.captureLoop) await active.captureLoop.catch(() => {})
    active.captureLoop = null
    active.pendingFrameSequence = null
    active.frameAcknowledged = null
  }

  createRecorderSurface(active) {
    const recorderWindow = new this.BaseWindow({
      width: RECORDER_VIEWPORT.width,
      height: RECORDER_VIEWPORT.height,
      show: false,
    })
    const recorderView = new this.WebContentsView({
      webPreferences: {
        backgroundThrottling: false,
        contextIsolation: true,
        disableDialogs: true,
        nodeIntegration: false,
        partition: `kiwi-code-recorder-${active.id}`,
        preload: path.join(__dirname, 'browser-recorder-preload.cjs'),
        sandbox: true,
      },
    })
    recorderWindow.contentView.addChildView(recorderView)
    recorderView.setBounds(RECORDER_VIEWPORT)
    active.window = recorderWindow
    active.view = recorderView

    const recorderSession = recorderView.webContents.session
    // Canvas-backed frame recording requires no camera, microphone, or display
    // permission. Deny every permission in this private partition.
    recorderSession.setPermissionCheckHandler(() => false)
    recorderSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false))
    recorderSession.on('will-download', (event) => event.preventDefault())
    recorderView.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
    const recorderPageUrl = this.recorderPageURL
    const blockUnexpectedNavigation = (event, url) => {
      if (url !== recorderPageUrl) event.preventDefault()
    }
    recorderView.webContents.on('will-navigate', blockUnexpectedNavigation)
    recorderView.webContents.on('will-redirect', blockUnexpectedNavigation)
    recorderView.webContents.on('render-process-gone', () => {
      void this.abortActive(active, recordingError('recording_failed', 'The browser recorder process stopped.'))
    })
    recorderView.webContents.on('destroyed', () => {
      if (!active.closing) void this.abortActive(active, recordingError('recording_failed', 'The browser recorder closed.'))
    })
  }

  async handleRendererEvent(event, value) {
    if (!isRecord(value) || typeof value.id !== 'string' || typeof value.type !== 'string') {
      throw recordingError('recording_failed', 'Invalid recorder event.')
    }
    const active = [...this.active.values()].find((candidate) => candidate.id === value.id)
    if (
      !active || !active.view || active.view.webContents.isDestroyed() ||
      event.sender !== active.view.webContents || event.senderFrame !== active.view.webContents.mainFrame
    ) {
      throw recordingError('recording_failed', 'Untrusted recorder event.', 403)
    }

    if (value.type === 'ready') {
      if (active.state !== 'starting' || typeof value.mimeType !== 'string' || !RECORDING_MIME_TYPES.has(value.mimeType)) {
        throw recordingError('recording_failed', 'Invalid recorder readiness event.')
      }
      active.mimeType = value.mimeType
      active.startedAt = new Date().toISOString()
      active.startedMonotonic = process.hrtime.bigint()
      active.ready.resolve()
      return { accepted: true }
    }

    if (value.type === 'frame.ack') {
      if (active.state === 'finalizing' && active.pendingFrameSequence === null) {
        return { accepted: true, sessionId: value.sessionId }
      }
      if (active.pendingFrameSequence === null || value.sessionId !== active.pendingFrameSequence) {
        throw recordingError('recording_failed', 'Invalid recording frame acknowledgement.')
      }
      active.pendingFrameSequence = null
      active.frameAcknowledged?.resolve()
      return { accepted: true, sessionId: value.sessionId }
    }

    if (value.type === 'chunk') {
      if (!['starting', 'recording', 'finalizing'].includes(active.state) || value.sequence !== active.sequence) {
        throw recordingError('recording_failed', 'Invalid recorder chunk sequence.')
      }
      const chunk = bufferFromChunk(value.bytes)
      if (!chunk || chunk.length < 1 || chunk.length > MAX_RECORDING_CHUNK_BYTES) {
        throw recordingError('recording_failed', 'Invalid recorder chunk.')
      }
      if (active.bytes + chunk.length > MAX_RECORDING_BYTES) {
        const error = recordingError('recording_failed', 'The browser recording exceeded its size limit.', 413)
        await this.abortActive(active, error)
        throw error
      }
      try {
        await active.file.writeFile(chunk)
      } catch {
        const error = recordingError('recording_failed', 'The browser recording could not be written to disk.')
        await this.abortActive(active, error)
        throw error
      }
      active.bytes += chunk.length
      active.sequence += 1
      return { accepted: true, sequence: value.sequence }
    }

    if (value.type === 'stopped') {
      if (active.state !== 'finalizing') throw recordingError('recording_failed', 'Unexpected recorder stop event.')
      const recording = await this.finalizeActive(active)
      return { accepted: true, recording: publicRecording(recording) }
    }

    if (value.type === 'error') {
      const errorName = typeof value.errorName === 'string' ? safeErrorText(value.errorName, 80) : 'Error'
      const errorMessage = typeof value.errorMessage === 'string' ? safeErrorText(value.errorMessage) : ''
      console.error(`Browser recorder renderer error: ${errorName}${errorMessage ? `: ${errorMessage}` : ''}`)
      const error = recordingError('recording_failed', 'The browser recorder reported an error.')
      await this.abortActive(active, error)
      throw error
    }

    throw recordingError('recording_failed', 'Unknown recorder event.')
  }

  async stop(projectId, threadId, recordingId) {
    const active = this.active.get(sessionKey(projectId, threadId))
    if (!active || (recordingId !== undefined && recordingId !== active.id)) {
      throw recordingError('recording_not_active', 'This browser session is not recording that recording.', 409)
    }
    if (active.state === 'starting') {
      const timeout = timeoutAfter(
        RECORDER_START_TIMEOUT_MS + FIRST_RECORDING_FRAME_TIMEOUT_MS + 1_000,
        recordingError('recording_failed', 'Browser recording did not start in time.'),
      )
      try { await Promise.race([active.started.promise, timeout.promise]) } finally { timeout.cancel() }
    }
    if (active.state === 'recording') {
      active.state = 'finalizing'
      clearTimeout(active.durationTimer)
      this.clearIdleTimer(active)
      await this.stopFrameCapture(active)
      if (!active.view || active.view.webContents.isDestroyed()) {
        const error = recordingError('recording_failed', 'The browser recorder is unavailable.')
        await this.abortActive(active, error)
        throw error
      }
      active.view.webContents.send(BROWSER_RECORDER_COMMAND_CHANNEL, { type: 'stop', id: active.id })
    }
    const timeout = timeoutAfter(RECORDER_STOP_TIMEOUT_MS, recordingError('recording_failed', 'Browser recording did not stop in time.'))
    try {
      return publicRecording(await Promise.race([active.done.promise, timeout.promise]))
    } catch (error) {
      await this.abortActive(active, error)
      if (error instanceof BrowserProviderError) throw error
      throw recordingError('recording_failed', 'Browser recording could not be finalized.')
    } finally {
      timeout.cancel()
    }
  }

  async stopIfActive(projectId, threadId) {
    if (!this.active.has(sessionKey(projectId, threadId))) return null
    return this.stop(projectId, threadId)
  }

  async cancelTarget(projectId, threadId, targetId) {
    const active = this.active.get(sessionKey(projectId, threadId))
    if (!active || active.targetId !== targetId) return
    await this.abortActive(active, recordingError('recording_failed', 'The recorded browser tab closed.'))
  }

  async finalizeActive(active) {
    if (this.active.get(active.key) !== active) throw recordingError('recording_failed', 'Browser recording is no longer active.')
    if (active.bytes <= 0 || !active.mimeType) {
      const error = recordingError('recording_failed', 'Browser recording produced no video data.')
      await this.abortActive(active, error)
      throw error
    }
    clearTimeout(active.durationTimer)
    this.clearIdleTimer(active)
    await active.file.sync()
    await active.file.close()
    active.file = null
    await fsp.rename(this.partialPath(active.id), this.videoPath(active.id))
    await fsp.chmod(this.videoPath(active.id), 0o600)
    const finishedAt = new Date().toISOString()
    const recording = {
      version: RECORDING_VERSION,
      id: active.id,
      projectId: active.projectId,
      threadId: active.threadId,
      targetId: active.targetId,
      title: active.title,
      startedAt: active.startedAt,
      finishedAt,
      durationMs: Math.max(0, Math.min(MAX_RECORDING_DURATION_MS + RECORDER_STOP_TIMEOUT_MS, recordingElapsedMs(active))),
      bytes: active.bytes,
      mimeType: active.mimeType,
      filename: `${active.id}.webm`,
    }
    await atomicWritePrivate(this.metadataPath(active.id), `${JSON.stringify(recording)}\n`)
    this.completed.set(recording.id, recording)
    this.active.delete(active.key)
    this.closeSurface(active)
    try { active.releaseSourceView() } catch {}
    active.done.resolve(recording)
    await this.pruneCompleted()
    return recording
  }

  async abortActive(active, reason = recordingError('recording_failed', 'Browser recording was cancelled.')) {
    if (!active || active.closing) return
    active.closing = true
    clearTimeout(active.durationTimer)
    this.clearIdleTimer(active)
    if (this.active.get(active.key) === active) this.active.delete(active.key)
    active.ready.reject(reason)
    active.started.reject(reason)
    active.done.reject(reason)
    active.firstFrame.reject(reason)
    try {
      if (active.view && !active.view.webContents.isDestroyed()) {
        active.view.webContents.send(BROWSER_RECORDER_COMMAND_CHANNEL, { type: 'cancel', id: active.id })
      }
    } catch {
      // The renderer may already be gone.
    }
    active.captureActive = false
    active.frameAcknowledged?.resolve()
    await active.file?.close().catch(() => {})
    active.file = null
    await Promise.all([
      fsp.rm(this.partialPath(active.id), { force: true }).catch(() => {}),
      fsp.rm(this.videoPath(active.id), { force: true }).catch(() => {}),
      fsp.rm(this.metadataPath(active.id), { force: true }).catch(() => {}),
    ])
    this.completed.delete(active.id)
    this.closeSurface(active)
    try { active.releaseSourceView() } catch {}
  }

  closeSurface(active) {
    if (!active || active.surfaceClosed) return
    active.surfaceClosed = true
    active.closing = true
    const recorderWindow = active.window
    const recorderView = active.view
    active.window = null
    active.view = null
    try { recorderWindow?.contentView.removeChildView(recorderView) } catch {}
    try {
      if (recorderView && !recorderView.webContents.isDestroyed()) {
        recorderView.webContents.close({ waitForBeforeUnload: false })
      }
    } catch {}
    try { if (recorderWindow && !recorderWindow.isDestroyed()) recorderWindow.close() } catch {}
  }

  async delete(projectId, threadId, recordingId) {
    if (typeof recordingId !== 'string' || !RECORDING_ID_PATTERN.test(recordingId)) {
      throw recordingError('recording_not_found', 'Browser recording not found.', 404)
    }
    const active = this.active.get(sessionKey(projectId, threadId))
    if (active?.id === recordingId) throw recordingError('recording_active', 'Stop the browser recording before deleting it.', 409)
    const recording = this.completed.get(recordingId)
    if (!recording || recording.projectId !== projectId || recording.threadId !== threadId) {
      throw recordingError('recording_not_found', 'Browser recording not found.', 404)
    }
    await Promise.all([
      fsp.rm(this.videoPath(recordingId), { force: true }),
      fsp.rm(this.metadataPath(recordingId), { force: true }),
    ])
    this.completed.delete(recordingId)
    return { deleted: true, recordingId }
  }

  async open(projectId, threadId, recordingId, rangeHeader) {
    if (typeof recordingId !== 'string' || !RECORDING_ID_PATTERN.test(recordingId)) {
      throw recordingError('recording_not_found', 'Browser recording not found.', 404)
    }
    const recording = this.completed.get(recordingId)
    if (!recording || recording.projectId !== projectId || recording.threadId !== threadId) {
      throw recordingError('recording_not_found', 'Browser recording not found.', 404)
    }
    const flags = fs.constants.O_RDONLY | (fs.constants.O_NOFOLLOW || 0)
    let handle
    try {
      handle = await fsp.open(this.videoPath(recordingId), flags)
      const stat = await handle.stat()
      if (!stat.isFile() || stat.size !== recording.bytes || stat.size <= 0 || stat.size > MAX_RECORDING_BYTES) {
        throw new Error('invalid recording file')
      }
      const range = parseRecordingRange(rangeHeader, recording.bytes)
      const streamOptions = range
        ? { autoClose: true, start: range.start, end: range.end }
        : { autoClose: true }
      return {
        recording: publicRecording(recording),
        stream: handle.createReadStream(streamOptions),
        range,
      }
    } catch (error) {
      await handle?.close().catch(() => {})
      if (error instanceof BrowserProviderError) throw error
      throw recordingError('recording_not_found', 'Browser recording not found.', 404)
    }
  }

  async pruneCompleted() {
    const now = Date.now()
    const ordered = [...this.completed.values()].sort((left, right) => right.finishedAt.localeCompare(left.finishedAt))
    let retainedBytes = 0
    const removals = []
    for (let index = 0; index < ordered.length; index += 1) {
      const recording = ordered[index]
      retainedBytes += recording.bytes
      if (
        now - Date.parse(recording.finishedAt) > RECORDING_RETENTION_MS ||
        index >= MAX_RETAINED_RECORDINGS ||
        retainedBytes > MAX_RETAINED_RECORDING_BYTES
      ) {
        removals.push(recording)
      }
    }
    await Promise.all(removals.map(async (recording) => {
      this.completed.delete(recording.id)
      await Promise.all([
        fsp.rm(this.videoPath(recording.id), { force: true }).catch(() => {}),
        fsp.rm(this.metadataPath(recording.id), { force: true }).catch(() => {}),
      ])
    }))
  }

  dispose() {
    if (this.disposePromise) return this.disposePromise
    this.disposed = true
    clearInterval(this.pruneTimer)
    this.pruneTimer = null
    this.disposePromise = (async () => {
      const active = [...this.active.values()]
      await Promise.all(active.map(async (recording) => {
        try { await this.stop(recording.projectId, recording.threadId) } catch { await this.abortActive(recording) }
      }))
    })()
    return this.disposePromise
  }
}

module.exports = {
  BROWSER_RECORDER_COMMAND_CHANNEL,
  BROWSER_RECORDER_EVENT_CHANNEL,
  BrowserRecordingManager,
  MAX_RECORDING_BYTES,
  MAX_RECORDING_IDLE_TIMEOUT_MS,
  MIN_RECORDING_IDLE_TIMEOUT_MS,
  RECORDING_ID_PATTERN,
  RECORDING_MIME_TYPES,
  parseRecordingRange,
  validPersistedRecording,
}
