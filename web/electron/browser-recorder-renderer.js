'use strict'

const MAX_PENDING_BYTES = 64 * 1024 * 1024
const MAX_FRAME_BYTES = 8 * 1024 * 1024
const CHUNK_INTERVAL_MS = 1_000
const MIME_TYPES = [
  'video/webm;codecs=vp9',
  'video/webm;codecs=vp8',
  'video/webm',
]

let active = null

function stopTracks(recording) {
  for (const track of recording?.stream?.getTracks?.() || []) track.stop()
}

async function reportFailure(recording, reason) {
  if (!recording || recording.failed || recording.cancelled) return
  recording.failed = true
  try {
    if (recording.recorder.state !== 'inactive') recording.recorder.stop()
  } catch {}
  stopTracks(recording)
  try {
    await window.kiwiCodeBrowserRecorder.event({
      type: 'error',
      id: recording.id,
      errorName: typeof reason?.name === 'string' ? reason.name.slice(0, 80) : 'Error',
      errorMessage: typeof reason?.message === 'string' ? reason.message.slice(0, 500) : '',
    })
  } catch {
    // The main process may already have destroyed the private recorder view.
  }
}

function queueChunk(recording, blob) {
  if (!blob || blob.size <= 0 || recording.cancelled || recording.failed) return
  recording.pendingBytes += blob.size
  if (recording.pendingBytes > MAX_PENDING_BYTES || recording.totalBytes + recording.pendingBytes > recording.maxBytes) {
    void reportFailure(recording, new Error('Recorder chunks exceeded the pending data limit.'))
    return
  }
  const sequence = recording.sequence
  recording.sequence += 1
  recording.writes = recording.writes.then(async () => {
    const bytes = await blob.arrayBuffer()
    await window.kiwiCodeBrowserRecorder.event({
      type: 'chunk',
      id: recording.id,
      sequence,
      bytes,
    })
    recording.pendingBytes -= blob.size
    recording.totalBytes += blob.size
  }).catch((error) => reportFailure(recording, error))
}

async function start(command) {
  if (active || !command || typeof command.id !== 'string') return
  let stream
  try {
    if (command.mode !== 'frames') throw new Error('Unknown browser recording mode.')
    const width = Number.isInteger(command.width) && command.width >= 320 && command.width <= 3840 ? command.width : 1280
    const height = Number.isInteger(command.height) && command.height >= 180 && command.height <= 2160 ? command.height : 720
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const canvasContext = canvas.getContext('2d', { alpha: false, desynchronized: true })
    if (!canvasContext || typeof canvas.captureStream !== 'function') {
      throw new Error('Canvas video capture is unavailable.')
    }
    canvasContext.fillStyle = '#000'
    canvasContext.fillRect(0, 0, width, height)
    document.body.appendChild(canvas)
    stream = canvas.captureStream(15)
    const mimeType = MIME_TYPES.find((candidate) => MediaRecorder.isTypeSupported(candidate))
    if (!mimeType) throw new Error('No supported WebM MediaRecorder encoder is available.')
    const recorder = new MediaRecorder(stream, {
      mimeType,
      videoBitsPerSecond: 2_000_000,
    })
    const recording = {
      id: command.id,
      stream,
      recorder,
      mode: command.mode,
      canvas,
      canvasContext,
      frames: Promise.resolve(),
      maxBytes: Number.isSafeInteger(command.maxBytes) && command.maxBytes > 0 ? command.maxBytes : Number.MAX_SAFE_INTEGER,
      sequence: 0,
      pendingBytes: 0,
      totalBytes: 0,
      writes: Promise.resolve(),
      cancelled: false,
      failed: false,
    }
    active = recording
    recorder.addEventListener('dataavailable', (event) => queueChunk(recording, event.data))
    recorder.addEventListener('error', (event) => { void reportFailure(recording, event.error) })
    recorder.addEventListener('stop', () => {
      void (async () => {
        try {
          await recording.writes
          stopTracks(recording)
          if (!recording.cancelled && !recording.failed) {
            await window.kiwiCodeBrowserRecorder.event({ type: 'stopped', id: recording.id })
          }
        } catch {
          await reportFailure(recording, new Error('Could not finish recorder chunk writes.'))
        } finally {
          if (active === recording) active = null
        }
      })()
    })
    for (const track of stream.getVideoTracks()) {
      track.addEventListener('ended', () => {
        if (recorder.state !== 'inactive' && !recording.cancelled) {
          void reportFailure(recording, new Error('The captured tab stream ended.'))
        }
      })
    }
    recorder.start(CHUNK_INTERVAL_MS)
    await window.kiwiCodeBrowserRecorder.event({
      type: 'ready',
      id: recording.id,
      mimeType: recorder.mimeType,
    })
  } catch (error) {
    stopTracks({ stream })
    if (active?.id === command.id) {
      await reportFailure(active, error)
    } else {
      try {
        await window.kiwiCodeBrowserRecorder.event({
          type: 'error',
          id: command.id,
          errorName: typeof error?.name === 'string' ? error.name.slice(0, 80) : 'Error',
          errorMessage: typeof error?.message === 'string' ? error.message.slice(0, 500) : '',
        })
      } catch {}
    }
  }
}

function drawFrame(command) {
  const recording = active
  const bytes = command?.data instanceof ArrayBuffer
    ? new Uint8Array(command.data)
    : ArrayBuffer.isView(command?.data)
      ? new Uint8Array(command.data.buffer, command.data.byteOffset, command.data.byteLength)
      : null
  if (
    !recording || recording.mode !== 'frames' || command?.id !== recording.id ||
    !Number.isInteger(command.sessionId) || !bytes || bytes.byteLength < 1 || bytes.byteLength > MAX_FRAME_BYTES ||
    recording.cancelled || recording.failed
  ) return
  recording.frames = recording.frames.then(async () => {
    const bitmap = await createImageBitmap(new Blob([bytes], { type: 'image/jpeg' }))
    try {
      const canvas = recording.canvas
      const context = recording.canvasContext
      const scale = Math.min(canvas.width / bitmap.width, canvas.height / bitmap.height)
      const width = Math.max(1, Math.round(bitmap.width * scale))
      const height = Math.max(1, Math.round(bitmap.height * scale))
      const x = Math.floor((canvas.width - width) / 2)
      const y = Math.floor((canvas.height - height) / 2)
      context.fillStyle = '#000'
      context.fillRect(0, 0, canvas.width, canvas.height)
      context.drawImage(bitmap, x, y, width, height)
    } finally {
      bitmap.close()
    }
    await window.kiwiCodeBrowserRecorder.event({
      type: 'frame.ack', id: recording.id, sessionId: command.sessionId,
    })
  }).catch((error) => reportFailure(recording, error))
}

function stop(command) {
  const recording = active
  if (!recording || command?.id !== recording.id || recording.cancelled || recording.failed) return
  void recording.frames.then(() => {
    if (recording.recorder.state !== 'inactive') recording.recorder.stop()
  }).catch((error) => reportFailure(recording, error))
}

function cancel(command) {
  const recording = active
  if (!recording || command?.id !== recording.id) return
  recording.cancelled = true
  try {
    if (recording.recorder.state !== 'inactive') recording.recorder.stop()
  } catch {}
  stopTracks(recording)
}

window.kiwiCodeBrowserRecorder.onCommand((command) => {
  if (!command || typeof command.type !== 'string') return
  if (command.type === 'start') void start(command)
  else if (command.type === 'frame') drawFrame(command)
  else if (command.type === 'stop') stop(command)
  else if (command.type === 'cancel') cancel(command)
})
