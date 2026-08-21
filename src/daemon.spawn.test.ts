import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import { existsSync } from 'node:fs'
import { chmod, lstat, mkdtemp, readFile, symlink, utimes, writeFile } from 'node:fs/promises'
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
// replaced it. Seeded state is written raw so non-numeric PIDs (AC 2.4) can be expressed, and aged
// past the boot budget so it can only be read as stale — a record written moments ago is
// indistinguishable from a winner still booting, which is the case the aging separates.
async function expectStaleStateReplaced(dir: string, seed: string): Promise<void> {
  const env = { SAKI_DAEMON_STATE_DIR: dir }
  await writeFile(daemonStatePath(env), seed)
  const aged = new Date(Date.now() - 60_000)
  await utimes(daemonStatePath(env), aged, aged)
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

  // AC 2.4 — a PID owned by another user is never reusable.
  it('cleans EPERM state owned by another user', async () => {
    const dir = await stateDir()
    killThrows(STALE_PID, 'EPERM')
    await expectStaleStateReplaced(
      dir,
      JSON.stringify({ pid: STALE_PID, socketPath: null, goUrl: 'http://go.test' }),
    )
  })

  // AC 2.4 — a non-numeric PID is not a PID.
  it('cleans non-numeric PID state', async () => {
    const dir = await stateDir()
    await expectStaleStateReplaced(
      dir,
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
  it('reclaims a tokenized winner record after the winner dies', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), JSON.stringify({
      pid: STALE_PID,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
      claimToken: 'winner-token',
    }))
    killThrows(STALE_PID, 'ESRCH')
    const gate = stubBackend()
    stubSpawn(gate)

    await expect(ensureDaemon(env, { budgetMs: 2_000 })).resolves.toEqual({
      pid: process.pid,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
    })
    expect(childProcess.spawn).toHaveBeenCalledTimes(1)
  })

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

  // AC 2.3, cross-process half — 🔒 INVARIANT 2. A SECOND CLI arriving mid-boot reads the winner's
  // already-written state: valid, PID alive, health still refusing. That is byte-identical to the
  // recycled-PID state of AC 2.5, and reading it as stale is what frees the lock for a second spawn.
  it('waits out a winner that is still booting instead of spawning a second daemon', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const winner = { pid: process.pid, socketPath: null, goUrl: 'http://go.test' }
    await writeFile(daemonStatePath(env), JSON.stringify(winner))
    const gate = stubBackend()
    stubSpawn(gate)
    // The winner's Go process finishes binding shortly after this invocation starts.
    setTimeout(() => { gate.up = true }, 250)

    await expect(ensureDaemon(env)).resolves.toEqual(winner)
    expect(childProcess.spawn).not.toHaveBeenCalled()
  })

  it('publishes the child PID before a delayed backend becomes healthy', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const gate = stubBackend()
    stubSpawn(gate, 1_500)

    const [first, second] = await Promise.all([ensureDaemon(env), ensureDaemon(env)])

    expect(childProcess.spawn).toHaveBeenCalledTimes(1)
    expect(first.pid).toBe(process.pid)
    expect(second.pid).toBe(process.pid)
  })

  it('releases the PID record when startup fails after spawn', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    stubBackend()
    stubSpawn({ up: false })

    await expect(ensureDaemon(env, { budgetMs: 300 })).rejects.toMatchObject({ code: 3 })
    await expect(readDaemonState(env)).resolves.toBeNull()
  })

  it('preserves the socket record written by the backend during startup', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const socketPath = join(dir, 'backend.sock')
    let healthCalls = 0
    vi.stubGlobal('fetch', vi.fn(async () => {
      healthCalls++
      if (healthCalls === 1) throw new Error('connect ECONNREFUSED')
      if (healthCalls < 4) throw new Error('backend still binding')
      await writeFile(daemonStatePath(env), JSON.stringify({
        pid: process.pid,
        socketPath,
        goUrl: DEFAULT_GO_URL,
      }))
      return { ok: true, status: 200, json: async () => ({ ok: true }) } as unknown as Response
    }))
    stubSpawn({ up: true }, 0)

    await expect(ensureDaemon(env)).resolves.toEqual({
      pid: process.pid,
      socketPath,
      goUrl: DEFAULT_GO_URL,
    })
  })

  // AC 4.3 — a crashed daemon leaves state naming a socket that is gone. Auto-start must replace it
  // promptly rather than stalling on the dead path, and the record it leaves carries no stale socket.
  it('replaces a dead daemon whose state names a removed socket', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), JSON.stringify({
      pid: STALE_PID,
      socketPath: join(dir, 'backend.sock'),
      goUrl: DEFAULT_GO_URL,
    }))
    killThrows(STALE_PID, 'ESRCH')
    const gate = stubBackend()
    stubSpawn(gate)

    const started = Date.now()
    await expect(ensureDaemon(env)).resolves.toEqual({
      pid: process.pid,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
    })
    expect(Date.now() - started).toBeLessThan(10_000)
    expect(childProcess.spawn).toHaveBeenCalledTimes(1)
  })

  // Security: the state dir sits in a shared temp root. mkdir(recursive) succeeds on a directory
  // someone else already owns and never re-applies the mode, so ownership has to be proven, not
  // assumed — everything the CLI trusts (socketPath to dial, PID to signal) is a file inside it.
  it('refuses a state directory this user does not exclusively own', async () => {
    const dir = await stateDir()
    await chmod(dir, 0o777)
    stubBackend()

    await expect(ensureDaemon({ SAKI_DAEMON_STATE_DIR: dir })).rejects.toMatchObject({ code: 3 })
    expect(childProcess.spawn).not.toHaveBeenCalled()
  })

  // Security, READ side: validating only on write leaves the hijack open, because the reuse path
  // returns a record before anything writes. A planted, healthy-looking record in a world-writable
  // directory would otherwise hand over the socketPath the CLI dials and the PID `stop` signals.
  it('does not trust a healthy-looking record from a world-writable directory', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    await writeFile(daemonStatePath(env), JSON.stringify({
      pid: process.pid,
      socketPath: '/tmp/attacker.sock',
      goUrl: 'http://127.0.0.1:19999',
    }))
    await chmod(dir, 0o777)
    // Everything the attacker controls answers healthy — only the directory's mode betrays it.
    stubBackend(true)

    await expect(readDaemonState(env)).resolves.toBeNull()
    // Not trusted: no attacker-controlled socketPath to dial, and no attacker-controlled goUrl or
    // PID carried forward. What comes back is derived from the environment, never from the file.
    await expect(ensureDaemon(env)).resolves.toEqual({
      pid: 0,
      socketPath: null,
      goUrl: DEFAULT_GO_URL,
    })
    expect(childProcess.spawn).not.toHaveBeenCalled()
  })

  // The same untrusted directory must fail LOUDLY the moment a write is attempted, rather than
  // silently degrading — a read can fall back, a write cannot.
  it('fails loudly when it has to write into a directory it does not own', async () => {
    const dir = await stateDir()
    await chmod(dir, 0o777)
    stubBackend()

    await expect(ensureDaemon({ SAKI_DAEMON_STATE_DIR: dir })).rejects.toMatchObject({ code: 3 })
    expect(childProcess.spawn).not.toHaveBeenCalled()
  })

  // Security: a symlink planted at the state path must be UNLINKED, never written through — a plain
  // 'w' open follows it and would truncate whatever it points at, as this user.
  it('replaces a symlink planted at the state path without writing through it', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    const victim = join(dir, 'victim.txt')
    await writeFile(victim, 'precious')
    await symlink(victim, daemonStatePath(env))
    const gate = stubBackend()
    stubSpawn(gate)

    await expect(ensureDaemon(env)).resolves.toMatchObject({ pid: process.pid })
    await expect(readFile(victim, 'utf8')).resolves.toBe('precious')
    await expect(lstat(daemonStatePath(env)).then((s) => s.isSymbolicLink())).resolves.toBe(false)
  })

  // Outcome 5.5 — the whole call honours ONE wall-clock budget, so a contended lock cannot turn
  // three bounded attempts into a multiple-of-the-budget hang.
  it('gives up inside its budget when the lock stays contended', async () => {
    const dir = await stateDir()
    const env = { SAKI_DAEMON_STATE_DIR: dir }
    // A sentinel nobody ever completes: every attempt loses the lock and finds no real PID.
    await writeFile(daemonStatePath(env), JSON.stringify({ pid: -1, socketPath: null, goUrl: DEFAULT_GO_URL }))
    stubBackend()
    childProcess.spawn.mockImplementation(() => {
      throw new Error('spawn must not be reached while the lock is held')
    })
    // Re-seed the sentinel as fast as the loop can clear it, standing in for a peer that keeps winning.
    const contend = setInterval(() => {
      void writeFile(daemonStatePath(env), JSON.stringify({ pid: -1, socketPath: null, goUrl: DEFAULT_GO_URL }))
    }, 10)

    const started = Date.now()
    try {
      await expect(ensureDaemon(env, { budgetMs: 400 })).rejects.toMatchObject({ code: 3 })
    } finally {
      clearInterval(contend)
    }
    // Just above the injected budget: a regression to a PER-ATTEMPT budget would be
    // MAX_LOCK_ATTEMPTS x 400ms = 1200ms and must fail here, so the bound has to be tighter than that.
    expect(Date.now() - started).toBeLessThan(1_100)
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
