import { describe, expect, it } from 'vitest'
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { daemonStateDir, daemonStatePath, readDaemonState, waitForLiveness, binaryPath } from './daemon.js'
import { EXIT } from './exit.js'

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
})
