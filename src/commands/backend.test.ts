import { afterEach, describe, expect, it, vi } from 'vitest'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import { cmdBackend } from './backend.js'

const daemon = vi.hoisted(() => ({
  ensureDaemon: vi.fn(),
  readDaemonState: vi.fn(),
  stopDaemon: vi.fn(),
  probeBackendHealth: vi.fn(),
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

  // AC 3.3 — idempotent start names the existing PID rather than claiming a fresh spawn.
  it('reports an already running backend', async () => {
    daemon.readDaemonState.mockResolvedValue(state)
    daemon.ensureDaemon.mockResolvedValue(state)
    const { ctx, out } = context()
    await expect(cmdBackend(ctx, ['start'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual(state)

    const human = context(false)
    await expect(cmdBackend(human.ctx, ['start'], {})).resolves.toBe(EXIT.OK)
    expect(human.out).toEqual(['backend already running (pid 42)'])
  })

  // AC 3.4 — the --json contract: {pid, healthy, goUrl, socketPath}, socketPath a path once Slice 4
  // has provisioned one.
  it('emits the documented status --json shape for a running daemon', async () => {
    const socketed = { ...state, socketPath: '/tmp/saki-501/backend.sock' }
    daemon.readDaemonState.mockResolvedValue(socketed)
    daemon.probeBackendHealth.mockResolvedValue(true)
    const { ctx, out } = context()

    await expect(cmdBackend(ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({
      pid: 42,
      healthy: true,
      goUrl: 'http://127.0.0.1:8788',
      socketPath: '/tmp/saki-501/backend.sock',
    })
  })

  // AC 3.4 — an alive PID whose backend does not answer reports healthy:false, not an error exit.
  it('reports an unhealthy daemon without failing the command', async () => {
    daemon.readDaemonState.mockResolvedValue(state)
    daemon.probeBackendHealth.mockResolvedValue(false)
    const { ctx, out } = context()

    await expect(cmdBackend(ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toMatchObject({ pid: 42, healthy: false })
  })

  it('reports healthy and unhealthy status states', async () => {
    daemon.readDaemonState.mockResolvedValue(state)
    daemon.probeBackendHealth.mockResolvedValue(true)
    const healthy = context(false)
    await expect(cmdBackend(healthy.ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(healthy.out).toEqual(['backend healthy (pid 42)'])

    daemon.readDaemonState.mockResolvedValue(null)
    const unavailable = context()
    await expect(cmdBackend(unavailable.ctx, ['status'], {})).resolves.toBe(EXIT.OK)
    expect(JSON.parse(unavailable.out[0])).toEqual({
      pid: null,
      healthy: false,
      goUrl: unavailable.ctx.client.goUrl,
      socketPath: null,
    })
  })
})
