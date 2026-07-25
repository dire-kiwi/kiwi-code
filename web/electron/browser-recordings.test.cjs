'use strict'

const assert = require('node:assert/strict')
const { EventEmitter } = require('node:events')
const fs = require('node:fs/promises')
const path = require('node:path')
const test = require('node:test')
const {
  BROWSER_RECORDER_COMMAND_CHANNEL,
  BrowserRecordingManager,
  parseRecordingRange,
  validPersistedRecording,
} = require('./browser-recordings.cjs')

function fakeElectron(directory) {
  let manager
  let rendererError

  class FakeSession extends EventEmitter {
    setPermissionCheckHandler(handler) { this.permissionCheckHandler = handler }
    setPermissionRequestHandler(handler) { this.permissionRequestHandler = handler }
  }

  class FakeWebContents extends EventEmitter {
    constructor() {
      super()
      this.destroyed = false
      this.mainFrame = {}
      this.session = new FakeSession()
    }

    async loadURL(url) { assert.match(url, /^http:\/\/127\.0\.0\.1:\d+\//) }
    isDestroyed() { return this.destroyed }
    setWindowOpenHandler(handler) { this.windowOpenHandler = handler }
    close() { this.destroyed = true }
    send(channel, command) {
      assert.equal(channel, BROWSER_RECORDER_COMMAND_CHANNEL)
      if (command.type === 'start') {
        queueMicrotask(() => {
          void manager.handleRendererEvent(
            { sender: this, senderFrame: this.mainFrame },
            { type: 'ready', id: command.id, mimeType: 'video/webm;codecs=vp9' },
          ).catch((error) => { rendererError = error })
        })
      } else if (command.type === 'frame') {
        queueMicrotask(() => {
          void manager.handleRendererEvent(
            { sender: this, senderFrame: this.mainFrame },
            { type: 'frame.ack', id: command.id, sessionId: command.sessionId },
          ).catch((error) => { rendererError = error })
        })
      } else if (command.type === 'stop') {
        queueMicrotask(() => {
          void (async () => {
            const bytes = Uint8Array.from([26, 69, 223, 163]).buffer
            await manager.handleRendererEvent(
              { sender: this, senderFrame: this.mainFrame },
              { type: 'chunk', id: command.id, sequence: 0, bytes },
            )
            await manager.handleRendererEvent(
              { sender: this, senderFrame: this.mainFrame },
              { type: 'stopped', id: command.id },
            )
          })().catch((error) => { rendererError = error })
        })
      }
    }
  }

  class FakeWebContentsView {
    constructor() { this.webContents = new FakeWebContents() }
    setBounds(bounds) { this.bounds = bounds }
  }

  class FakeBaseWindow {
    constructor() {
      this.destroyed = false
      this.contentView = {
        addChildView: (view) => { this.view = view },
        removeChildView: () => { this.view = null },
      }
    }
    isDestroyed() { return this.destroyed }
    close() { this.destroyed = true }
  }

  manager = new BrowserRecordingManager({
    app: { getPath: () => directory },
    BaseWindow: FakeBaseWindow,
    WebContentsView: FakeWebContentsView,
  })
  manager.setRecorderPageURL('http://127.0.0.1:43210/v1/recorder/secret/index.html')
  return { manager, rendererError: () => rendererError }
}

async function streamContents(stream) {
  const chunks = []
  for await (const chunk of stream) chunks.push(chunk)
  return Buffer.concat(chunks)
}

test('validates persisted recording metadata', () => {
  const valid = {
    version: 1,
    id: `rec-${'a'.repeat(32)}`,
    projectId: 'project',
    threadId: 'thread',
    targetId: '17',
    title: 'Demonstrate checkout flow',
    startedAt: '2026-07-21T10:00:00.000Z',
    finishedAt: '2026-07-21T10:00:01.000Z',
    durationMs: 1000,
    bytes: 4,
    mimeType: 'video/webm;codecs=vp9',
    filename: `rec-${'a'.repeat(32)}.webm`,
  }
  assert.equal(validPersistedRecording(valid), true)
  assert.equal(validPersistedRecording({ ...valid, id: '../secret' }), false)
  assert.equal(validPersistedRecording({ ...valid, bytes: 0 }), false)
  assert.equal(validPersistedRecording({ ...valid, mimeType: 'video/mp4' }), false)
})

test('resolves single recording byte ranges without buffering', () => {
  assert.equal(parseRecordingRange(undefined, 4), null)
  assert.deepEqual(parseRecordingRange('bytes=0-1', 4), { start: 0, end: 1, length: 2, totalBytes: 4 })
  assert.deepEqual(parseRecordingRange('Bytes=0-1', 4), { start: 0, end: 1, length: 2, totalBytes: 4 })
  assert.deepEqual(parseRecordingRange('bytes=2-', 4), { start: 2, end: 3, length: 2, totalBytes: 4 })
  assert.deepEqual(parseRecordingRange('bytes=-2', 4), { start: 2, end: 3, length: 2, totalBytes: 4 })
  assert.deepEqual(parseRecordingRange('bytes=0-99', 4), { start: 0, end: 3, length: 4, totalBytes: 4 })
  for (const value of ['', 'bytes=-0', 'bytes=4-', 'bytes=3-2', 'bytes=0-1,2-3', 'items=0-1']) {
    assert.throws(
      () => parseRecordingRange(value, 4),
      (error) => error.code === 'recording_range_not_satisfiable' && error.totalBytes === 4,
    )
  }
})

test('records bounded chunks, reloads metadata, streams, and deletes by identity', async () => {
  const directory = await fs.mkdtemp(path.join(process.env.TMPDIR || '/tmp', 'kiwi-code-recordings-test-'))
  try {
    const first = fakeElectron(directory)
    await first.manager.initialize()
    let released = false
    let resizeOptions
    const source = {
      isDestroyed: () => false,
      async capturePage() {
        return {
          isEmpty: () => false,
          getSize: () => ({ width: 2560, height: 1600 }),
          resize(options) { resizeOptions = options; return this },
          toJPEG: () => Buffer.from('jpeg'),
        }
      },
    }
    const active = await first.manager.start({
      projectId: 'project', threadId: 'thread', targetId: '17', title: 'Demonstrate checkout flow', sourceWebContents: source,
      releaseSourceView: () => { released = true },
    })
    assert.equal(active.state, 'recording')
    assert.deepEqual(resizeOptions, { width: 1152, height: 720, quality: 'good' })
    assert.equal(first.manager.snapshot('project', 'thread').recording.id, active.id)
    await assert.rejects(
      first.manager.stop('project', 'thread', `rec-${'f'.repeat(32)}`),
      (error) => error.code === 'recording_not_active',
    )

    const completed = await first.manager.stop('project', 'thread', active.id)
    assert.equal(completed.state, 'completed')
    assert.equal(completed.bytes, 4)
    assert.equal(first.rendererError(), undefined)
    assert.equal(first.manager.snapshot('project', 'thread').recording, null)
    assert.equal(first.manager.snapshot('project', 'thread').recordings.length, 1)
    assert.equal(released, true)
    assert.equal((await fs.stat(path.join(directory, 'browser-recordings', `${completed.id}.webm`))).mode & 0o777, 0o600)

    const second = fakeElectron(directory)
    await second.manager.initialize()
    const reloaded = second.manager.snapshot('project', 'thread').recordings
    assert.equal(reloaded.length, 1)
    assert.equal(reloaded[0].id, completed.id)
    assert.equal(second.manager.snapshot('other', 'thread').recordings.length, 0)

    const opened = await second.manager.open('project', 'thread', completed.id)
    assert.equal(opened.range, null)
    assert.deepEqual(await streamContents(opened.stream), Buffer.from([26, 69, 223, 163]))
    const ranged = await second.manager.open('project', 'thread', completed.id, 'bytes=1-2')
    assert.deepEqual(ranged.range, { start: 1, end: 2, length: 2, totalBytes: 4 })
    assert.deepEqual(await streamContents(ranged.stream), Buffer.from([69, 223]))
    await assert.rejects(
      second.manager.open('project', 'thread', completed.id, 'bytes=4-'),
      (error) => error.code === 'recording_range_not_satisfiable' && error.totalBytes === 4,
    )
    await assert.rejects(second.manager.open('other', 'thread', completed.id), (error) => error.code === 'recording_not_found')

    await second.manager.delete('project', 'thread', completed.id)
    assert.equal(second.manager.snapshot('project', 'thread').recordings.length, 0)
    await assert.rejects(fs.stat(path.join(directory, 'browser-recordings', `${completed.id}.webm`)), { code: 'ENOENT' })
    await first.manager.dispose()
    await second.manager.dispose()
  } finally {
    await fs.rm(directory, { recursive: true, force: true })
  }
})

test('auto-finalizes an agent recording after its idle timeout', async () => {
  const directory = await fs.mkdtemp(path.join(process.env.TMPDIR || '/tmp', 'kiwi-code-recordings-idle-'))
  try {
    const { manager, rendererError } = fakeElectron(directory)
    await manager.initialize()
    const source = {
      isDestroyed: () => false,
      async capturePage() {
        return {
          isEmpty: () => false,
          getSize: () => ({ width: 1280, height: 720 }),
          resize() { return this },
          toJPEG: () => Buffer.from('jpeg'),
        }
      },
    }
    const active = await manager.start({
      projectId: 'project', threadId: 'thread', targetId: '17', title: 'Verify inactivity finalization', sourceWebContents: source,
      idleTimeoutMs: 1_000,
    })
    assert.equal(active.idleTimeoutMs, 1_000)
    assert.equal(typeof active.idleDeadlineAt, 'string')
    const firstDeadline = active.idleDeadlineAt
    await new Promise((resolve) => setTimeout(resolve, 20))
    const touched = manager.touch('project', 'thread').recording
    assert.equal(touched.id, active.id)
    assert.ok(Date.parse(touched.idleDeadlineAt) > Date.parse(firstDeadline))

    const deadline = Date.now() + 2_000
    while (manager.snapshot('project', 'thread').recording && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 25))
    }
    assert.equal(manager.snapshot('project', 'thread').recording, null)
    assert.equal(manager.snapshot('project', 'thread').recordings.length, 1)
    assert.equal(rendererError(), undefined)
    await manager.dispose()
  } finally {
    await fs.rm(directory, { recursive: true, force: true })
  }
})

test('removes stale partial and orphaned recording files on startup', async () => {
  const directory = await fs.mkdtemp(path.join(process.env.TMPDIR || '/tmp', 'kiwi-code-recordings-cleanup-'))
  const recordings = path.join(directory, 'browser-recordings')
  try {
    await fs.mkdir(recordings)
    await fs.writeFile(path.join(recordings, `rec-${'b'.repeat(32)}.webm.part`), 'partial')
    await fs.writeFile(path.join(recordings, `rec-${'c'.repeat(32)}.webm`), 'orphan')
    const { manager } = fakeElectron(directory)
    await manager.initialize()
    assert.deepEqual(await fs.readdir(recordings), [])
    await manager.dispose()
  } finally {
    await fs.rm(directory, { recursive: true, force: true })
  }
})
