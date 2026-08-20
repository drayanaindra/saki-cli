import { afterEach, describe, expect, it, vi } from 'vitest'
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { daemonStateDir, daemonStatePath, readDaemonState, waitForLiveness, binaryPath, removeDaemonState, isAlive, ensureDaemon, stopDaemon, socketFetch, ensureStateDirForTests } from './daemon.js'
import { EXIT } from './exit.js'

const spawnMock = vi.hoisted(() => vi.fn())
const undiciFetchMock = vi.hoisted(() => vi.fn())
const undiciAgentMock = vi.hoisted(() => vi.fn(function AgentMock() { return {} }))
vi.mock('node:child_process', () => ({ spawn: spawnMock }))
vi.mock('undici', () => ({ fetch: undiciFetchMock, Agent: undiciAgentMock }))

afterEach(() => {
  vi.clearAllMocks()
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

  it('resolves the default backend binary and creates test state directories', async () => {
    expect(binaryPath({})).toBe('saki-backend')
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: join(dir, 'nested') }
    ensureStateDirForTests(env)
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  it('honors an explicit backend binary path', () => {
    expect(binaryPath({ SAKI_BACKEND_BIN: '/custom/saki-backend' })).toBe('/custom/saki-backend')
  })

  it('adds localhost host headers for unix socket fetches', async () => {
    const response = { ok: true } as Response
    undiciFetchMock.mockResolvedValue(response)
    const result = await socketFetch('/tmp/saki.sock')('http://localhost/api/health', { headers: { 'X-Test': 'yes' } })
    expect(result).toBe(response)
    expect(undiciAgentMock).toHaveBeenCalledWith({ connect: { socketPath: '/tmp/saki.sock' } })
    expect(undiciFetchMock).toHaveBeenCalledWith('http://localhost/api/health', expect.objectContaining({
      headers: { host: 'localhost', 'x-test': 'yes' },
      dispatcher: expect.anything(),
    }))
  })

  it('removes missing state files and reports process liveness', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await expect(removeDaemonState(env)).resolves.toBeUndefined()
    expect(isAlive(process.pid)).toBe(true)
    expect(isAlive(999999999)).toBe(false)
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

  it('starts a child and records state after liveness succeeds', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_BIN: '/bin/saki-backend', SAKI_BACKEND_URL: 'http://go.test' }
    const child = { pid: 77, unref: vi.fn() }
    spawnMock.mockReturnValue(child)
    let healthCalls = 0
    vi.stubGlobal('fetch', vi.fn(async () => {
      healthCalls++
      return { ok: healthCalls > 1, status: healthCalls > 1 ? 200 : 503, json: async () => ({ ok: healthCalls > 1 }) }
    }))
    vi.spyOn(process, 'kill').mockImplementation(() => undefined)
    await expect(ensureDaemon(env)).resolves.toEqual({ pid: 77, socketPath: null, goUrl: 'http://go.test' })
    expect(spawnMock).toHaveBeenCalledWith('/bin/saki-backend', [], expect.objectContaining({ detached: true }))
    expect(child.unref).toHaveBeenCalledOnce()
  })

  it('clears the lock and reports startup failure', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_BIN: '/bin/saki-backend', SAKI_BACKEND_URL: 'http://go.test' }
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 503, json: async () => ({ ok: false }) })))
    spawnMock.mockImplementation(() => { throw Object.assign(new Error('missing'), { code: 'ENOENT' }) })
    await expect(ensureDaemon(env)).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  it('stops a healthy daemon and removes its state', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: process.pid, socketPath: null, goUrl: 'http://go.test' }))
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) })))
    const kill = vi.spyOn(process, 'kill').mockImplementation((pid, signal) => {
      if (pid === process.pid && signal === 'SIGTERM') vi.spyOn(process, 'kill').mockImplementation(() => { throw new Error('gone') })
      return true
    })
    await expect(stopDaemon(env)).resolves.toBe('stopped')
    expect(kill).toHaveBeenCalledWith(process.pid, 'SIGTERM')
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  it('waits for a concurrent daemon winner', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-test-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir, SAKI_BACKEND_URL: 'http://go.test' }
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: -1, socketPath: null, goUrl: 'http://go.test' }))
    let healthCalls = 0
    vi.stubGlobal('fetch', vi.fn(async () => {
      healthCalls++
      return { ok: healthCalls > 1, json: async () => ({ ok: healthCalls > 1 }) }
    }))
    const state = { pid: process.pid, socketPath: null, goUrl: 'http://go.test' }
    setTimeout(() => writeFile(daemonStatePath(env), JSON.stringify(state)), 1)
    await expect(ensureDaemon(env)).resolves.toEqual(state)
  })
})
