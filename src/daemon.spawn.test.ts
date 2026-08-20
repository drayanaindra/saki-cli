import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import { existsSync } from 'node:fs'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { daemonStatePath, ensureDaemon, readDaemonState } from './daemon.js'

// Slice 2 (PID tracking + stale-state cleanup) drives the SPAWN path, so `node:child_process` is
// mocked file-wide to count spawns. It lives apart from daemon.test.ts on purpose: that suite proves
// the real-binary error paths (ENOENT / EACCES) and a module mock would hollow them out.
const childProcess = vi.hoisted(() => ({ spawn: vi.fn() }))
vi.mock('node:child_process', () => childProcess)

const DEFAULT_GO_URL = 'http://127.0.0.1:8788'
const STALE_PID = 999_999

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  childProcess.spawn.mockReset()
})

async function stateDir(): Promise<string> {
  return mkdtemp(join(tmpdir(), 'saki-daemon-spawn-'))
}

// A backend that is only reachable once `up` flips — the same shape as a real Go process, which owns
// a PID well before it owns a listening socket.
function stubBackend(up = false): { up: boolean } {
  const gate = { up }
  vi.stubGlobal('fetch', vi.fn(async () => {
    if (!gate.up) throw new Error('connect ECONNREFUSED')
    return { ok: true, status: 200, json: async () => ({ ok: true }) } as unknown as Response
  }))
  return gate
}

// The fake child reports THIS process's pid so the post-liveness `isAlive(child.pid)` guard sees a
// live process, and brings the gate up after `readyMs` to model backend boot latency.
function stubSpawn(gate: { up: boolean }, readyMs = 20): void {
  childProcess.spawn.mockImplementation(() => {
    const child = new EventEmitter() as EventEmitter & { pid: number; unref: () => void }
    child.pid = process.pid
    child.unref = () => undefined
    setTimeout(() => child.emit('spawn'), 0)
    setTimeout(() => { gate.up = true }, readyMs)
    return child
  })
}

function killThrows(pid: number, code: string): void {
  vi.spyOn(process, 'kill').mockImplementation(((target: number) => {
    if (target === pid) {
      const err = new Error(code) as NodeJS.ErrnoException
      err.code = code
      throw err
    }
    return true
  }) as typeof process.kill)
}

// Every stale-state criterion ends the same way: the bad file is gone and exactly one fresh daemon
// replaced it. Seeded state is written raw so non-numeric PIDs (AC 2.4) can be expressed.
async function expectStaleStateReplaced(dir: string, seed: string): Promise<void> {
  const env = { SAKI_DAEMON_STATE_DIR: dir }
  await writeFile(daemonStatePath(env), seed)
  const gate = stubBackend()
  stubSpawn(gate)

  await expect(ensureDaemon(env)).resolves.toEqual({
    pid: process.pid,
    socketPath: null,
    goUrl: DEFAULT_GO_URL,
  })
  expect(childProcess.spawn).toHaveBeenCalledTimes(1)
}

describe('ensureDaemon PID tracking and stale-state cleanup', () => {
  // AC 2.1
  it('suppresses a second spawn when the tracked PID is alive and healthy', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const state = { pid: process.pid, socketPath: null, goUrl: 'http://go.test' }
    await writeFile(daemonStatePath(env), JSON.stringify(state))
    const before = await readFile(daemonStatePath(env), 'utf8')
    stubBackend(true)

    await expect(ensureDaemon(env)).resolves.toEqual(state)
    expect(childProcess.spawn).not.toHaveBeenCalled()
    await expect(readFile(daemonStatePath(env), 'utf8')).resolves.toBe(before)
  })

  // AC 2.2
  it('cleans ESRCH stale state and spawns a fresh daemon', async () => {
    const dir = await stateDir()
    killThrows(STALE_PID, 'ESRCH')
    await expectStaleStateReplaced(
      dir,
      JSON.stringify({ pid: STALE_PID, socketPath: null, goUrl: 'http://go.test' }),
    )
  })

  // AC 2.4 — a PID owned by another user is never reusable, and a non-numeric PID is not a PID.
  it('cleans EPERM and non-numeric PID state', async () => {
    const eperm = await stateDir()
    killThrows(STALE_PID, 'EPERM')
    await expectStaleStateReplaced(
      eperm,
      JSON.stringify({ pid: STALE_PID, socketPath: null, goUrl: 'http://go.test' }),
    )

    childProcess.spawn.mockReset()
    const nan = await stateDir()
    await expectStaleStateReplaced(
      nan,
      '{"pid":"not-a-pid","socketPath":null,"goUrl":"http://go.test"}',
    )
  })

  // AC 2.5 — the OS recycled the PID onto an unrelated process.
  it('rejects an alive PID whose backend does not answer', async () => {
    const dir = await stateDir()
    await expectStaleStateReplaced(
      dir,
      JSON.stringify({ pid: process.pid, socketPath: null, goUrl: 'http://go.test' }),
    )
  })

  // AC 2.3 — 🔒 INVARIANT 2: at most one saki-backend per UID.
  it('routes a concurrent caller into the poll path instead of a second spawn', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const gate = stubBackend()
    // Boot slower than the loser's first poll, so the loser MUST tolerate an unhealthy-but-booting
    // winner rather than reclaiming the lock.
    stubSpawn(gate, 150)

    const [first, second] = await Promise.all([ensureDaemon(env), ensureDaemon(env)])

    expect(childProcess.spawn).toHaveBeenCalledTimes(1)
    expect(first.pid).toBe(process.pid)
    expect(second.pid).toBe(process.pid)
    await expect(readDaemonState(env)).resolves.toEqual({
      pid: process.pid,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
    })
  })

  // AC 2.3 (reclaim half) — a winner that fails must not strand the lock for the next invocation.
  it('removes its own claimed lock when the spawn fails', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    stubBackend()
    childProcess.spawn.mockImplementation(() => {
      const child = new EventEmitter() as EventEmitter & { pid: number; unref: () => void }
      child.pid = process.pid
      child.unref = () => undefined
      const err = new Error('permission denied') as NodeJS.ErrnoException
      err.code = 'EACCES'
      setTimeout(() => child.emit('error', err), 0)
      return child
    })

    await expect(ensureDaemon(env)).rejects.toMatchObject({ code: 3 })
    await expect(readDaemonState(env)).resolves.toBeNull()
    expect(existsSync(daemonStatePath(env))).toBe(false)
  })

  // PRD §8 Slice 2, loser path: "If PID never appears (winner crashed pre-write): delete state file,
  // restart from step 1." The bounded poll is what makes that recoverable instead of permanent.
  it('reclaims a stranded spawn lock after the bounded poll', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    // A sentinel with no winner behind it — the file a crashed spawner leaves on disk.
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: -1, socketPath: null, goUrl: DEFAULT_GO_URL }))
    const gate = stubBackend()
    stubSpawn(gate)

    await expect(ensureDaemon(env)).resolves.toEqual({
      pid: process.pid,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
    })
    expect(childProcess.spawn).toHaveBeenCalledTimes(1)
  })
})
