import { afterEach, describe, expect, it, vi } from 'vitest'
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { daemonStateDir, daemonStatePath, readDaemonState, waitForLiveness, binaryPath, removeDaemonState, isAlive, ensureDaemon, stopDaemon } from './daemon.js'
import { EXIT } from './exit.js'


afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('daemon state and liveness', () => {
  it('uses a caller-provided UID-scoped state directory and reads the wire format', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    expect(daemonStateDir(env)).toBe(dir)
    expect(daemonStatePath(env)).toBe(join(dir, 'backend.state.json'))
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: 123, socketPath: '/tmp/s.sock', goUrl: 'http://127.0.0.1:8788' }))
    await expect(readDaemonState(env)).resolves.toEqual({ pid: 123, socketPath: '/tmp/s.sock', goUrl: 'http://127.0.0.1:8788' })
  })

  it('rejects malformed and lock-sentinel state', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), '{bad')
    await expect(readDaemonState(env)).resolves.toBeNull()
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: -1, socketPath: null, goUrl: 'http://127.0.0.1:8788' }))
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  it('waits for a healthy response and times out with UNREACHABLE', async () => {
    let calls = 0
    const fetchImpl = (async () => {
      calls++
      return { ok: calls > 1, status: calls > 1 ? 200 : 503, json: async () => ({ ok: calls > 1 }) } as unknown as Response
    }) as typeof fetch
    await expect(waitForLiveness('http://go.test', { fetchImpl, timeoutMs: 100, intervalMs: 1 })).resolves.toBeUndefined()
    await expect(waitForLiveness('http://go.test', { fetchImpl: (async () => { throw new Error('down') }) as typeof fetch, timeoutMs: 2, intervalMs: 1 })).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
  })

  it('honors an explicit backend binary path', () => {
    expect(binaryPath({ SAKI_BACKEND_BIN: '/custom/saki-backend' })).toBe('/custom/saki-backend')
  })

  it('maps a missing backend binary to UNREACHABLE without waiting for liveness timeout', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_BIN: join(dir, 'missing-backend') }
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('connection refused') }))
    await expect(ensureDaemon(env)).rejects.toMatchObject({ code: EXIT.UNREACHABLE, message: 'saki-backend binary not found' })
  })

  it('maps a non-executable backend binary to UNREACHABLE without hanging', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const binary = join(dir, 'backend')
    await writeFile(binary, '#!/bin/sh\n')
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_BIN: binary }
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('connection refused') }))
    await expect(ensureDaemon(env)).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
  })

  it('does not auto-start when an explicit backend URL is configured', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_URL: 'http://go.test' }
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) })))
    await expect(ensureDaemon(env)).resolves.toEqual({ pid: 0, socketPath: null, goUrl: 'http://go.test' })
  })

  it('removes missing state files and reports process liveness', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await expect(removeDaemonState(env)).resolves.toBeUndefined()
    expect(isAlive(process.pid)).toBe(true)
    expect(isAlive(-1)).toBe(false)
  })

  it('reuses an already healthy backend without spawning', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_URL: 'http://go.test' }
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) })))
    await expect(ensureDaemon(env)).resolves.toEqual({ pid: 0, socketPath: null, goUrl: 'http://go.test' })
  })

  it('reports a stale state as not running and removes it', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: 123, socketPath: null, goUrl: 'http://go.test' }))
    vi.spyOn(process, 'kill').mockImplementation(() => { throw new Error('gone') })
    await expect(stopDaemon(env)).resolves.toBe('not-running')
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

})
