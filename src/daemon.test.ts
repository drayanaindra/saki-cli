import { afterEach, describe, expect, it, vi } from 'vitest'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { daemonStateDir, daemonStatePath, readDaemonState, waitForLiveness, binaryPath, removeDaemonState, isAlive, ensureDaemon, probeBackendHealth, stopDaemon } from './daemon.js'
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

  it('bounds a health request that never resolves', async () => {
    await expect(waitForLiveness('http://go.test', {
      fetchImpl: (() => new Promise<Response>(() => undefined)) as typeof fetch,
      timeoutMs: 10,
      intervalMs: 1,
    })).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
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

// A daemon under stop: `signals` records what it was sent, and it stays alive until it honours one.
function stoppableDaemon(pid: number, honours: NodeJS.Signals[]): { signals: string[] } {
  const signals: string[] = []
  let alive = true
  vi.spyOn(process, 'kill').mockImplementation(((target: number, signal: NodeJS.Signals | 0) => {
    if (target !== pid) return true
    if (signal === 0) {
      if (!alive) {
        const err = new Error('ESRCH') as NodeJS.ErrnoException
        err.code = 'ESRCH'
        throw err
      }
      return true
    }
    signals.push(signal)
    if (honours.includes(signal)) alive = false
    return true
  }) as typeof process.kill)
  return { signals }
}

async function seedRunningState(pid: number): Promise<{ SAKI_DAEMON_STATE_DIR: string }> {
  const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-stop-'))
  const env = { SAKI_DAEMON_STATE_DIR: dir }
  await writeFile(daemonStatePath(env), JSON.stringify({ pid, socketPath: null, goUrl: 'http://go.test' }))
  return env
}

// A peer that ACCEPTS the connection and then says nothing. Modelled the way a real fetch behaves:
// it settles only when the caller's AbortSignal fires, so a probe that forgot to abort hangs here
// exactly as it would in production.
function silentPeer(seen: AbortSignal[]): typeof fetch {
  return ((_url: string, init?: RequestInit) => {
    if (init?.signal) seen.push(init.signal)
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new Error('aborted')))
    })
  }) as typeof fetch
}

describe('bounded health probes', () => {
  // Outcome 5.5 / AC 1.4. A peer that ACCEPTS the connection and never answers is the case an
  // un-aborted fetch waits ~300s (undici's default header timeout) for — one such probe would blow
  // the whole wall-clock budget the CLI's 10s ceiling rests on.
  it('gives up on a peer that accepts and never answers', async () => {
    const started = Date.now()
    const seen: AbortSignal[] = []

    await expect(probeBackendHealth('http://go.test', 50, silentPeer(seen))).resolves.toBe(false)

    expect(Date.now() - started).toBeLessThan(2_000)
    // Aborted, not merely abandoned: an un-aborted request keeps the socket and the event loop alive
    // until undici's own 300s header timeout.
    expect(seen[0]?.aborted).toBe(true)
  })

  it('reports a healthy backend and rejects a non-ok body', async () => {
    const ok = (async () => ({ ok: true, status: 200, json: async () => ({ ok: true }) })) as unknown as typeof fetch
    const degraded = (async () => ({ ok: true, status: 200, json: async () => ({ ok: false }) })) as unknown as typeof fetch

    await expect(probeBackendHealth('http://go.test', 500, ok)).resolves.toBe(true)
    await expect(probeBackendHealth('http://go.test', 500, degraded)).resolves.toBe(false)
  })

  it('treats an exhausted budget as unhealthy without issuing a request', async () => {
    const never = vi.fn(async () => { throw new Error('should not be called') })

    await expect(probeBackendHealth('http://go.test', 0, never as unknown as typeof fetch)).resolves.toBe(false)
    expect(never).not.toHaveBeenCalled()
  })
})

describe('stopDaemon signal escalation', () => {
  // AC 3.2 / 5.4
  it('terminates on SIGTERM and removes the state file', async () => {
    const env = await seedRunningState(4242)
    const daemon = stoppableDaemon(4242, ['SIGTERM'])

    await expect(stopDaemon(env)).resolves.toBe('stopped')
    expect(daemon.signals).toEqual(['SIGTERM'])
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  // §10 rule 5 — escalation, and the state file goes regardless of which signal did it.
  it('escalates to SIGKILL when SIGTERM is ignored', async () => {
    const env = await seedRunningState(4243)
    const daemon = stoppableDaemon(4243, ['SIGKILL'])

    await expect(stopDaemon(env, { graceMs: 50 })).resolves.toBe('stopped')
    expect(daemon.signals).toEqual(['SIGTERM', 'SIGKILL'])
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  // Outcome 5.3 — a hung daemon must still be killed, never reported away as "not running".
  it('stops a live daemon whose backend has stopped answering health', async () => {
    const env = await seedRunningState(4244)
    const daemon = stoppableDaemon(4244, ['SIGTERM'])
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('connect ECONNREFUSED') }))

    await expect(stopDaemon(env)).resolves.toBe('stopped')
    expect(daemon.signals).toEqual(['SIGTERM'])
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  // 🔒 INVARIANT 2 — a spawn lock another invocation is HOLDING is not this call's to clean up.
  // readDaemonState returns null for the pid:-1 sentinel, so a naive "no state → tidy up" would
  // delete the lock mid-spawn and re-open the double-spawn O_CREAT|O_EXCL exists to close.
  it('leaves a spawn lock held by another invocation alone', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'saki-daemon-stop-'))
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const held = JSON.stringify({ pid: -1, socketPath: null, goUrl: 'http://go.test' })
    await writeFile(daemonStatePath(env), held)

    await expect(stopDaemon(env)).resolves.toBe('not-running')
    await expect(readFile(daemonStatePath(env), 'utf8')).resolves.toBe(held)
  })

  // Outcome 5.3 — while this call waited out the grace period another invocation auto-started a
  // replacement and wrote ITS record. Deleting that orphans a live daemon.
  it('does not delete a replacement daemon written while it was stopping', async () => {
    const env = await seedRunningState(4245)
    const replacement = { pid: 4246, socketPath: null, goUrl: 'http://go.test' }
    let alive = true
    vi.spyOn(process, 'kill').mockImplementation(((target: number, signal: NodeJS.Signals | 0) => {
      if (target !== 4245) return true
      if (signal === 0) {
        if (alive) return true
        const err = new Error('ESRCH') as NodeJS.ErrnoException
        err.code = 'ESRCH'
        throw err
      }
      // The daemon exits — and a concurrent invocation auto-starts a replacement that claims the file
      // before this call finishes waiting.
      alive = false
      writeFileSync(daemonStatePath(env), JSON.stringify(replacement))
      return true
    }) as typeof process.kill)

    await expect(stopDaemon(env, { graceMs: 50 })).resolves.toBe('stopped')
    await expect(readDaemonState(env)).resolves.toEqual(replacement)
  })
})
