import { open, readFile, lstat, stat, unlink, mkdir } from 'node:fs/promises'
import { constants as FS_CONST, existsSync, mkdirSync } from 'node:fs'
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
// Ceiling on a single health probe. Loopback either answers fast or is not answering.
const HEALTH_PROBE_MS = 2_000
// §16 wire format: pid -1 marks the state file as claimed but not yet backed by a spawned process.
const LOCK_SENTINEL_PID = -1
// Authority carried by unix-socket requests. It addresses nothing — it exists so OriginGuard
// (backend/adapter/originguard.go) sees a loopback Host on socket traffic.
const SOCKET_HOST = 'localhost'
// Truncating write that refuses a symlink at the final component. The state file lives in a shared
// temp root, so a plain 'w' open would follow a planted link and overwrite its target as this user.
const STATE_WRITE_FLAGS = FS_CONST.O_WRONLY | FS_CONST.O_CREAT | FS_CONST.O_TRUNC | FS_CONST.O_NOFOLLOW

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

// Fetch over the daemon's unix socket, for Go-origin requests only.
//
// The URL authority is rewritten rather than a `Host` header being set: fetch treats `Host` as a
// forbidden header name and drops it silently, so the header route leaves whatever authority `goUrl`
// happens to carry to satisfy OriginGuard by luck. The dispatcher pins every connection to the
// socket, so the authority no longer addresses anything — it only decides the Host header, and
// pinning it to loopback is what makes OriginGuard's accept deterministic (§12.3 / AC 4.6).
export function socketFetch(socketPath: string): typeof fetch {
  const dispatcher = new Agent({ connect: { socketPath } })
  return (async (input: string | URL, init?: RequestInit) => {
    // Deliberately not accepting a Request: rewriting the URL means only `init` carries the method,
    // headers and body, so a Request would silently downgrade a POST to a GET. The one caller
    // (client.ts requestOn) passes a URL string plus init; fail loudly if that ever changes.
    if (typeof input !== 'string' && !(input instanceof URL)) {
      throw new CliError('socket transport takes a url, not a Request', EXIT.ERROR)
    }
    const url = new URL(String(input))
    url.protocol = 'http:'
    url.hostname = SOCKET_HOST
    url.port = ''
    return (undiciFetch as unknown as (input: unknown, init: unknown) => Promise<unknown>)(
      url.toString(),
      { ...init, dispatcher },
    ) as unknown as Response
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

// The state directory lives in a SHARED temp root, so its existence proves nothing about who owns it:
// mkdir(recursive) succeeds silently on a directory somebody else created and never re-applies the
// mode. Everything security-relevant about the daemon comes out of a file in here — the socketPath
// the CLI dials, the goUrl it fetches, the PID `saki backend stop` signals — so a directory this user
// does not exclusively own is a full hijack of the CLI↔backend channel, and on a host where TMPDIR is
// the shared /tmp another local user can win the race by simply creating it first.
async function ensurePrivateStateDir(env: DaemonEnv): Promise<void> {
  const dir = daemonStateDir(env)
  await mkdir(dir, { recursive: true, mode: 0o700 })
  // POSIX-only: uid scoping and file modes are meaningless on Windows, where the socket is a
  // declared Non-Goal and the CLI runs over TCP anyway.
  const uid = process.getuid?.()
  if (uid === undefined) return
  const info = await lstat(dir)
  // isDirectory() is false for a symlink under lstat, which is the point: a planted symlink would
  // otherwise satisfy mkdir and redirect every write below it.
  //
  // Group/other WRITE is the hijack bit — planting a state file or socket needs write on the
  // directory. Read/traverse is not the threat: the records inside are 0600, so a 0755 directory
  // (what an inherited umask commonly produces) is safe and must not be rejected.
  if (!info.isDirectory() || info.uid !== uid || (info.mode & 0o022) !== 0) {
    throw new CliError(
      `daemon state directory is not private: ${dir}`,
      EXIT.UNREACHABLE,
      'remove it, or point SAKI_DAEMON_STATE_DIR at a directory you own with mode 0700',
    )
  }
}

async function writeState(state: DaemonState, env: DaemonEnv): Promise<void> {
  await ensurePrivateStateDir(env)
  // O_NOFOLLOW, not writeFile: the plain 'w' flag is O_CREAT|O_TRUNC and FOLLOWS symlinks, so a
  // symlink planted at this path would truncate and overwrite whatever it points at, as this user.
  const handle = await open(daemonStatePath(env), STATE_WRITE_FLAGS, 0o600)
  try {
    await handle.writeFile(JSON.stringify(state))
  } finally {
    await handle.close()
  }
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
  await ensurePrivateStateDir(env)
  try {
    // 'wx' is O_CREAT|O_EXCL, which already refuses an existing symlink — the exclusivity that makes
    // this the lock ALSO makes it symlink-safe.
    const handle = await open(path, 'wx', 0o600)
    await handle.writeFile(JSON.stringify({ pid: LOCK_SENTINEL_PID, socketPath: null, goUrl: DEFAULT_GO_URL }))
    await handle.close()
    return true
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'EEXIST') return false
    throw err
  }
}

// Every health probe is BOUNDED. A peer that accepts the connection and then never answers — a
// SIGSTOPped backend, a swapping one, an unrelated listener squatting the port — leaves an un-aborted
// fetch pending for undici's 300 s header timeout. That single call would blow the wall-clock budget
// outcome 5.5 rests on, which is why waitForLiveness already aborts and why these must too.
export async function probeBackendHealth(
  goUrl: string,
  timeoutMs = HEALTH_PROBE_MS,
  fetchImpl?: typeof fetch,
): Promise<boolean> {
  if (timeoutMs <= 0) return false
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await (fetchImpl ?? fetch)(`${goUrl}/api/health`, { signal: controller.signal })
    const body = await response.json() as { ok?: boolean }
    return response.ok && body.ok === true
  } catch {
    return false
  } finally {
    clearTimeout(timer)
  }
}

// Clamp a probe to whatever is left of the caller's budget, so the last probe before a deadline
// cannot overshoot it.
function probeBudget(deadline?: number): number {
  if (deadline === undefined) return HEALTH_PROBE_MS
  return Math.min(HEALTH_PROBE_MS, deadline - Date.now())
}

async function healthy(state: DaemonState, deadline?: number): Promise<boolean> {
  if (!isAlive(state.pid)) return false
  return probeBackendHealth(state.goUrl, probeBudget(deadline))
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
  if (await healthy(state, deadline)) return state
  const bootDeadline = Math.min(deadline, Date.now() + Math.max(0, DEFAULT_TIMEOUT_MS - await stateAgeMs(env)))
  while (Date.now() < bootDeadline && isAlive(state.pid)) {
    await delay(LOCK_POLL_MS)
    if (await healthy(state, deadline)) return (await readDaemonState(env)) ?? state
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
  if (await probeBackendHealth(goUrl, probeBudget(deadline))) return { pid: 0, socketPath: null, goUrl }
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
    // readDaemonState also returns null for the pid:-1 sentinel, i.e. a lock ANOTHER invocation is
    // currently holding while it spawns. Deleting that would re-open the very double-spawn the
    // O_CREAT|O_EXCL protocol exists to close, so only an unowned, non-lock record is cleaned up.
    if (!(await stateIsLock(env))) await releaseState(env, state?.pid ?? (await readRawPid(env)) ?? 0)
    return 'not-running'
  }
  try {
    process.kill(state.pid, 'SIGTERM')
  } catch {
    await releaseState(env, state.pid)
    return 'not-running'
  }
  const deadline = Date.now() + (options.graceMs ?? STOP_GRACE_MS)
  while (Date.now() < deadline && isAlive(state.pid)) await delay(LOCK_POLL_MS)
  if (isAlive(state.pid)) {
    try { process.kill(state.pid, 'SIGKILL') } catch { /* exited between the probe and the signal */ }
  }
  // Compare-and-delete: while this call was waiting out the grace period, another invocation can have
  // auto-started a replacement and written ITS record. Unlinking that one would orphan a live daemon.
  await releaseState(env, state.pid)
  return 'stopped'
}

export function ensureStateDirForTests(env: DaemonEnv = process.env): void {
  mkdirSync(daemonStateDir(env), { recursive: true, mode: 0o700 })
}
