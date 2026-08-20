import { open, readFile, stat, unlink, writeFile, mkdir } from 'node:fs/promises'
import { existsSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { userInfo } from 'node:os'
import { spawn, type ChildProcess } from 'node:child_process'
import { Agent, fetch as undiciFetch } from 'undici'
import { EXIT, CliError } from './exit.js'

export interface DaemonState {
  pid: number
  socketPath: string | null
  goUrl: string
}

export interface DaemonEnv {
  SAKI_BACKEND_BIN?: string
  SAKI_BACKEND_URL?: string
  SAKI_DAEMON_STATE_DIR?: string
  TMPDIR?: string
}

const DEFAULT_GO_URL = 'http://127.0.0.1:8788'
const DEFAULT_TIMEOUT_MS = 10_000
const STATE_NAME = 'backend.state.json'
// Spawn-lock timings. The loser waits LOCK_POLL_TRIES × LOCK_POLL_MS (1 s) for the winner's real PID
// to replace the sentinel, then follows that PID for the liveness budget. MAX_LOCK_ATTEMPTS bounds the
// reclaim loop so a contended lock can never turn into an unbounded respawn.
const LOCK_POLL_MS = 100
const LOCK_POLL_TRIES = 10
const MAX_LOCK_ATTEMPTS = 3
// §10 rule 5: how long a stopping daemon gets to honour SIGTERM before SIGKILL.
const STOP_GRACE_MS = 5_000
// §16 wire format: pid -1 marks the state file as claimed but not yet backed by a spawned process.
const LOCK_SENTINEL_PID = -1

export function daemonStateDir(env: DaemonEnv = process.env): string {
  const uid = userInfo().uid ?? process.getuid?.() ?? 0
  return env.SAKI_DAEMON_STATE_DIR ?? join(env.TMPDIR ?? process.env.TMPDIR ?? '/tmp', `saki-${uid}`)
}

export function daemonStatePath(env: DaemonEnv = process.env): string {
  return join(daemonStateDir(env), STATE_NAME)
}

export async function readDaemonState(env: DaemonEnv = process.env): Promise<DaemonState | null> {
  try {
    const parsed = JSON.parse(await readFile(daemonStatePath(env), 'utf8')) as Partial<DaemonState>
    const pid = parsed.pid
    if (!Number.isInteger(pid) || (pid as number) <= 0 || typeof parsed.goUrl !== 'string') return null
    if (parsed.socketPath !== null && typeof parsed.socketPath !== 'string') return null
    return { pid: pid as number, socketPath: parsed.socketPath ?? null, goUrl: parsed.goUrl }
  } catch {
    return null
  }
}

export async function removeDaemonState(env: DaemonEnv = process.env): Promise<void> {
  await unlink(daemonStatePath(env)).catch(() => undefined)
}

export function binaryPath(env: DaemonEnv = process.env): string | null {
  if (env.SAKI_BACKEND_BIN) return env.SAKI_BACKEND_BIN
  const adjacent = join(dirname(fileURLToPath(import.meta.url)), 'saki-backend')
  if (existsSync(adjacent)) return adjacent
  return 'saki-backend'
}

export function isAlive(pid: number): boolean {
  if (!Number.isInteger(pid) || pid <= 0) return false
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

export async function waitForLiveness(
  goUrl = DEFAULT_GO_URL,
  options: { timeoutMs?: number; intervalMs?: number; fetchImpl?: typeof fetch } = {},
): Promise<void> {
  const deadline = Date.now() + (options.timeoutMs ?? DEFAULT_TIMEOUT_MS)
  const fetchImpl = options.fetchImpl ?? fetch
  let lastError = 'backend did not become healthy'
  while (Date.now() < deadline) {
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      const controller = new AbortController()
      const remaining = deadline - Date.now()
      const request = fetchImpl(`${goUrl}/api/health`, { signal: controller.signal })
        .then(async (response) => ({ response, body: await response.json() as { ok?: boolean } }))
      const timeout = new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          controller.abort()
          reject(new Error('health check timed out'))
        }, remaining)
      })
      const { response, body } = await Promise.race([request, timeout])
      if (response.ok && body.ok === true) return
      lastError = `health returned HTTP ${response.status}`
    } catch (err) {
      lastError = err instanceof Error ? err.message : String(err)
    } finally {
      if (timer) clearTimeout(timer)
    }
    if (Date.now() >= deadline) break
    await new Promise((resolve) => setTimeout(resolve, Math.min(options.intervalMs ?? 100, deadline - Date.now())))
  }
  throw new CliError(`saki-backend liveness timeout: ${lastError}`, EXIT.UNREACHABLE)
}

export function socketFetch(socketPath: string): typeof fetch {
  const dispatcher = new Agent({ connect: { socketPath } })
  return (async (input: string | URL | Request, init?: RequestInit) => {
    const headers = new Headers(init?.headers)
    headers.set('Host', 'localhost')
    return (undiciFetch as unknown as (input: unknown, init: unknown) => Promise<unknown>)(input, { ...init, headers: Object.fromEntries(headers.entries()), dispatcher }) as unknown as Response
  }) as unknown as typeof fetch
}

async function spawnDaemon(env: DaemonEnv = process.env): Promise<ChildProcess> {
  const binary = binaryPath(env)
  if (!binary) throw new CliError('saki-backend binary not found', EXIT.UNREACHABLE, 'run npm run backend:build')

  const child = spawn(binary, [], {
    detached: true,
    stdio: 'ignore',
    env: { ...process.env, ...env, SAKI_DAEMON_STATE_PATH: daemonStatePath(env) },
  })
  child.unref()

  await new Promise<void>((resolve, reject) => {
    child.once('spawn', resolve)
    child.once('error', reject)
  }).catch((err) => {
    const code = (err as NodeJS.ErrnoException).code
    if (code === 'ENOENT') {
      throw new CliError('saki-backend binary not found', EXIT.UNREACHABLE, 'run npm run backend:build')
    }
    throw new CliError(`failed to start saki-backend: ${err instanceof Error ? err.message : String(err)}`, EXIT.UNREACHABLE)
  })
  return child
}

async function writeState(state: DaemonState, env: DaemonEnv): Promise<void> {
  const path = daemonStatePath(env)
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  await writeFile(path, JSON.stringify(state), { mode: 0o600 })
}

async function stateIsLock(env: DaemonEnv): Promise<boolean> {
  try {
    const raw = JSON.parse(await readFile(daemonStatePath(env), 'utf8')) as { pid?: unknown }
    return raw.pid === LOCK_SENTINEL_PID
  } catch {
    return false
  }
}

async function acquireStateLock(env: DaemonEnv): Promise<boolean> {
  const path = daemonStatePath(env)
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  try {
    const handle = await open(path, 'wx', 0o600)
    await handle.writeFile(JSON.stringify({ pid: LOCK_SENTINEL_PID, socketPath: null, goUrl: DEFAULT_GO_URL }))
    await handle.close()
    return true
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'EEXIST') return false
    throw err
  }
}

async function healthy(state: DaemonState, fetchImpl?: typeof fetch): Promise<boolean> {
  if (!isAlive(state.pid)) return false
  try {
    const response = await (fetchImpl ?? fetch)(`${state.goUrl}/api/health`)
    const body = await response.json() as { ok?: boolean }
    return response.ok && body.ok === true
  } catch {
    return false
  }
}

async function goHealthy(goUrl: string): Promise<boolean> {
  try {
    const response = await fetch(`${goUrl}/api/health`)
    const body = await response.json() as { ok?: boolean }
    return response.ok && body.ok === true
  } catch {
    return false
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

// Age of the state record. A booting winner's file was written moments ago; a record whose PID the OS
// later recycled onto an unrelated process is old. That gap is the only honest way to tell the two
// apart — on disk they are byte-identical.
async function stateAgeMs(env: DaemonEnv): Promise<number> {
  try {
    return Date.now() - (await stat(daemonStatePath(env))).mtimeMs
  } catch {
    return Number.POSITIVE_INFINITY
  }
}

// Compare-and-delete. An invocation may only drop the state file while it still holds the record it
// is responsible for: between the read and the unlink another daemon can have written its own state,
// and deleting THAT record orphans a healthy daemon (nothing left tracks its PID).
async function releaseState(env: DaemonEnv, ownedPid: number): Promise<void> {
  const current = await readRawPid(env)
  if (current === null || current === ownedPid) await removeDaemonState(env)
}

async function readRawPid(env: DaemonEnv): Promise<number | null> {
  try {
    const raw = JSON.parse(await readFile(daemonStatePath(env), 'utf8')) as { pid?: unknown }
    return typeof raw.pid === 'number' ? raw.pid : null
  } catch {
    return null
  }
}

// Wait out a daemon that holds a live PID but is not answering yet.
//
// 🔒 INVARIANT 2 turns on this: the CLI records the child PID immediately after spawn while the Go
// process still has to bind its listeners, so a second invocation arriving mid-boot sees a valid,
// alive, UNHEALTHY record. Treating that as stale deletes the winner's state, frees the lock, and
// spawns a second saki-backend against the same port.
//
// Returns null once the record cannot become healthy — the PID is gone, or the boot budget it was
// written under has already elapsed (AC 2.5: a recycled PID never goes healthy, and its record is old).
async function awaitBackendReady(state: DaemonState, env: DaemonEnv, deadline: number): Promise<DaemonState | null> {
  if (await healthy(state)) return state
  const bootDeadline = Math.min(deadline, Date.now() + Math.max(0, DEFAULT_TIMEOUT_MS - await stateAgeMs(env)))
  while (Date.now() < bootDeadline && isAlive(state.pid)) {
    await delay(LOCK_POLL_MS)
    if (await healthy(state)) return (await readDaemonState(env)) ?? state
  }
  return null
}

// Reuse a daemon that is already serving, or clear the state that proves one is not.
//
// Returns `pid: 0` for a backend that owns the TCP port without a state file — a manually launched or
// older daemon. Spawning a second process there would lose the port bind while a superficial health
// probe went green against the FIRST one.
async function reuseRunningDaemon(env: DaemonEnv, deadline: number): Promise<DaemonState | null> {
  const existing = await readDaemonState(env)
  if (existing) {
    const ready = await awaitBackendReady(existing, env, deadline)
    if (ready) return ready
    await releaseState(env, existing.pid)
  } else if (existsSync(daemonStatePath(env)) && !(await stateIsLock(env))) {
    await removeDaemonState(env)
  }
  const goUrl = env.SAKI_BACKEND_URL ?? DEFAULT_GO_URL
  if (await goHealthy(goUrl)) return { pid: 0, socketPath: null, goUrl }
  return null
}

// Loser path of the spawn lock: wait on the invocation that won it instead of racing a second daemon
// into the same port. Resolves to the winner's state, or the PID to compare-and-delete on reclaim.
async function followWinner(
  env: DaemonEnv,
  deadline: number,
): Promise<{ state: DaemonState | null; followedPid: number | null }> {
  let state: DaemonState | null = null
  for (let i = 0; i < LOCK_POLL_TRIES && !state && Date.now() < deadline; i++) {
    await delay(LOCK_POLL_MS)
    state = await readDaemonState(env)
  }
  if (!state) return { state: null, followedPid: null }
  return { state: await awaitBackendReady(state, env, deadline), followedPid: state.pid }
}

// Winner path of the spawn lock. Every failure releases the claim this call owns — the sentinel until
// the child PID is recorded, that PID afterwards — so the lock is never stranded by an exiting CLI.
async function spawnAndRecord(env: DaemonEnv, deadline: number): Promise<DaemonState> {
  const goUrl = env.SAKI_BACKEND_URL ?? DEFAULT_GO_URL
  let owned = LOCK_SENTINEL_PID
  let child: ChildProcess | undefined
  try {
    child = await spawnDaemon(env)
    if (!child.pid) throw new CliError('saki-backend did not report a PID', EXIT.UNREACHABLE)
    owned = child.pid
    await writeState({ pid: child.pid, socketPath: null, goUrl }, env)
    await waitForLiveness(goUrl, { timeoutMs: Math.max(0, deadline - Date.now()) })
    if (!isAlive(child.pid)) throw new CliError('saki-backend exited before startup completed', EXIT.UNREACHABLE)
    // Re-read rather than reuse the write above: the Go process rewrites the same file once its
    // listeners are up, and that copy is the one carrying socketPath.
    const state = await readDaemonState(env)
    if (!state || state.pid !== child.pid) throw new CliError('saki-backend startup was not recorded', EXIT.UNREACHABLE)
    return state
  } catch (err) {
    // Reap the child we spawned. Dropping the state file while the process lives would leave an
    // orphan holding the port with nothing tracking its PID (outcome 5.3) — one per failed command.
    if (child?.pid && isAlive(child.pid)) {
      try { process.kill(child.pid, 'SIGTERM') } catch { /* exited between the probe and the signal */ }
    }
    await releaseState(env, owned)
    throw err
  }
}

export async function ensureDaemon(
  env: DaemonEnv = process.env,
  options: { budgetMs?: number } = {},
): Promise<DaemonState> {
  // ONE hard wall-clock budget covers every wait below — probe, follow, spawn and liveness. Outcome
  // 5.5 requires the CLI to exit within it on every timeout path, so a per-attempt budget would not
  // do: three attempts of a 10 s wait is a 30 s hang. Attempts are bounded too, because an unbounded
  // retry is the runaway-spawn failure mode this lock exists to prevent.
  const deadline = Date.now() + (options.budgetMs ?? DEFAULT_TIMEOUT_MS)
  for (let attempt = 0; attempt < MAX_LOCK_ATTEMPTS && Date.now() < deadline; attempt++) {
    const running = await reuseRunningDaemon(env, deadline)
    if (running) return running
    if (await acquireStateLock(env)) return await spawnAndRecord(env, deadline)
    const { state, followedPid } = await followWinner(env, deadline)
    if (state) return state
    if (followedPid !== null) await releaseState(env, followedPid)
    else await releaseState(env, LOCK_SENTINEL_PID)
  }
  throw new CliError(
    'saki-backend could not be started: spawn lock stayed contended',
    EXIT.UNREACHABLE,
    'check the daemon with `saki backend status --json`, then `saki backend start`',
  )
}

// §10 rule 5: SIGTERM first, escalate to SIGKILL after the grace period, and always remove the state
// file afterwards regardless of which signal did it.
export async function stopDaemon(
  env: DaemonEnv = process.env,
  options: { graceMs?: number } = {},
): Promise<'not-running' | 'stopped'> {
  const state = await readDaemonState(env)
  // Liveness, NOT health, decides whether there is anything to stop. A daemon that has stopped
  // answering /api/health is still our process holding the port — calling that "not running" and
  // dropping its state file would orphan it (outcome 5.3) with nothing left tracking its PID.
  if (!state || !isAlive(state.pid)) {
    await removeDaemonState(env)
    return 'not-running'
  }
  try {
    process.kill(state.pid, 'SIGTERM')
  } catch {
    await removeDaemonState(env)
    return 'not-running'
  }
  const deadline = Date.now() + (options.graceMs ?? STOP_GRACE_MS)
  while (Date.now() < deadline && isAlive(state.pid)) await delay(LOCK_POLL_MS)
  if (isAlive(state.pid)) {
    try { process.kill(state.pid, 'SIGKILL') } catch { /* exited between the probe and the signal */ }
  }
  await removeDaemonState(env)
  return 'stopped'
}

export function ensureStateDirForTests(env: DaemonEnv = process.env): void {
  mkdirSync(daemonStateDir(env), { recursive: true, mode: 0o700 })
}
