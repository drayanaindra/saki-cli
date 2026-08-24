import { afterEach, describe, expect, it, vi } from 'vitest'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import { cmdBackend } from './backend.js'

const daemon = vi.hoisted(() => ({
  ensureDaemon: vi.fn(),
  readDaemonState: vi.fn(),
  stopDaemon: vi.fn(),
}))

vi.mock('../daemon.js', () => daemon)

afterEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

function context(json = true) {
  const out: string[] = []
  const errors: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://s.test' }),
    cwd: '/repo',
    json,
    write: (value) => out.push(value),
    writeErr: (value) => errors.push(value),
    env: { SAKI_DAEMON_STATE_DIR: '/tmp/saki-test' },
  })
  return { ctx, out, errors }
}

const state = { pid: 42, socketPath: null, goUrl: 'http://127.0.0.1:8788' }

describe('cmdBackend', () => {
  it('rejects missing, unknown, and extra actions', async () => {
    const { ctx, errors } = context()
    await expect(cmdBackend(ctx, [], {})).resolves.toBe(EXIT.USAGE)
    await expect(cmdBackend(ctx, ['restart'], {})).resolves.toBe(EXIT.USAGE)
    await expect(cmdBackend(ctx, ['start', 'extra'], {})).resolves.toBe(EXIT.USAGE)
    expect(errors).toEqual([
      'error: usage: saki backend start|stop|status',
      'error: usage: saki backend start|stop|status',
      'error: usage: saki backend start|stop|status',
    ])
  })

  it('stops a running backend and renders the stopped result', async () => {
    daemon.stopDaemon.mockResolvedValue('stopped')
    const { ctx, out } = context()
    await expect(cmdBackend(ctx, ['stop'], {})).resolves.toBe(EXIT.OK)
    expect(daemon.stopDaemon).toHaveBeenCalledWith(ctx.env)
    expect(JSON.parse(out[0])).toEqual({ status: 'stopped' })
  })

  it('reports an already stopped backend in human output', async () => {
    daemon.stopDaemon.mockResolvedValue('not-running')
    const { ctx, out } = context(false)
    await expect(cmdBackend(ctx, ['stop'], {})).resolves.toBe(EXIT.OK)
    expect(out).toEqual(['backend not running'])
  })

  it('starts a new backend and reports its PID', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.ensureDaemon.mockResolvedValue(state)
    const { ctx, out } = context(false)
    await expect(cmdBackend(ctx, ['start'], {})).resolves.toBe(EXIT.OK)
    expect(out).toEqual(['backend started (pid 42)'])
  })

  it('reports an already running backend', async () => {
    daemon.readDaemonState.mockResolvedValue(state)
    daemon.ensureDaemon.mockResolvedValue(state)
    const { ctx, out } = context()
    await expect(cmdBackend(ctx, ['start'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual(state)
  })

  it('reports healthy and unhealthy status states', async () => {
    daemon.readDaemonState.mockResolvedValue(state)
    const fetchMock = vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) }))
    vi.stubGlobal('fetch', fetchMock)
    const healthy = context(false)
    await expect(cmdBackend(healthy.ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(healthy.out).toEqual(['backend healthy (pid 42)'])
    expect(fetchMock).toHaveBeenCalledWith('http://127.0.0.1:8788/api/health', expect.anything())

    daemon.readDaemonState.mockResolvedValue(null)
    const rejectMock = vi.fn(async () => { throw new Error('ECONNREFUSED') })
    vi.stubGlobal('fetch', rejectMock)
    const unavailable = context()
    await expect(cmdBackend(unavailable.ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(unavailable.out[0])).toEqual({
      pid: null,
      healthy: false,
      goUrl: unavailable.ctx.client.goUrl,
      socketPath: null,
    })
    expect(rejectMock).toHaveBeenCalledWith(`${unavailable.ctx.client.goUrl}/api/health`, expect.anything())
  })

  it('reports healthy via reachability probe when no state file exists (service-managed backend)', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    const fetchMock = vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) }))
    vi.stubGlobal('fetch', fetchMock)
    const { ctx, out } = context()
    await expect(cmdBackend(ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({
      pid: null,
      healthy: true,
      goUrl: ctx.client.goUrl,
      socketPath: null,
    })
    expect(fetchMock).toHaveBeenCalledWith(`${ctx.client.goUrl}/api/health`, expect.anything())
  })

  it('renders a distinct human message for a service-managed backend with no state file', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ ok: true }) })))
    const { ctx, out } = context(false)
    await expect(cmdBackend(ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(out).toEqual(['backend healthy (service-managed, no local state file)'])
  })
})
