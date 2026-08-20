import { open, readFile, unlink, writeFile, mkdir } from 'node:fs/promises'
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
  SAKI_DAEMON_STATE_PATH?: string
  TMPDIR?: string
}

const DEFAULT_GO_URL = 'http://127.0.0.1:8788'
const DEFAULT_TIMEOUT_MS = 10_000
const STATE_NAME = 'backend.state.json'

export function daemonStateDir(env: DaemonEnv = process.env): string {
  const uid = userInfo().uid ?? process.getuid?.() ?? 0
  return env.SAKI_DAEMON_STATE_DIR ?? join(env.TMPDIR ?? process.env.TMPDIR ?? '/tmp', `saki-${uid}`)
}

export function daemonStatePath(env: DaemonEnv = process.env): string {
  return env.SAKI_DAEMON_STATE_PATH ?? join(daemonStateDir(env), STATE_NAME)
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
    return raw.pid === -1
  } catch {
    return false
  }
}

async function acquireStateLock(env: DaemonEnv): Promise<boolean> {
  const path = daemonStatePath(env)
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  try {
    const handle = await open(path, 'wx', 0o600)
    await handle.writeFile(JSON.stringify({ pid: -1, socketPath: null, goUrl: DEFAULT_GO_URL }))
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

export async function ensureDaemon(env: DaemonEnv = process.env): Promise<DaemonState> {
  const existing = await readDaemonState(env)
  if (existing && await healthy(existing)) return existing
  if (existing) await removeDaemonState(env)
  else if (existsSync(daemonStatePath(env)) && !(await stateIsLock(env))) await removeDaemonState(env)
  const goUrl = env.SAKI_BACKEND_URL ?? DEFAULT_GO_URL
  // A manually launched or older daemon may already own the TCP port without a state file. Reuse it
  // instead of spawning a second process that fails to bind while a superficial health probe goes green.
  if (await goHealthy(goUrl)) return { pid: 0, socketPath: null, goUrl }

  const winner = await acquireStateLock(env)
  if (!winner) {
    for (let i = 0; i < 10; i++) {
      await new Promise((resolve) => setTimeout(resolve, 100))
      const state = await readDaemonState(env)
      if (state && state.pid > 0) {
        if (await healthy(state)) return state
        await removeDaemonState(env)
        break
      }
    }
    await removeDaemonState(env)
    return ensureDaemon(env)
  }

  try {
    const stateEnv = { ...env, SAKI_DAEMON_STATE_PATH: daemonStatePath(env) }
    const child = await spawnDaemon(stateEnv)
    if (!child.pid) throw new CliError('saki-backend did not report a PID', EXIT.UNREACHABLE)
    await writeState({ pid: child.pid, socketPath: null, goUrl }, stateEnv)
    await waitForLiveness(goUrl)
    if (!isAlive(child.pid)) throw new CliError('saki-backend exited before startup completed', EXIT.UNREACHABLE)
    const deadline = Date.now() + DEFAULT_TIMEOUT_MS
    while (Date.now() < deadline) {
      const state = await readDaemonState(stateEnv)
      if (state?.pid === child.pid) return state
      await new Promise((resolve) => setTimeout(resolve, 100))
    }
    throw new CliError('saki-backend startup was not recorded', EXIT.UNREACHABLE)
  } catch (err) {
    await removeDaemonState(env)
    throw err
  }
}

export async function stopDaemon(env: DaemonEnv = process.env): Promise<'not-running' | 'stopped'> {
  const state = await readDaemonState(env)
  if (!state || !(await healthy(state))) {
    await removeDaemonState(env)
    return 'not-running'
  }
  try { process.kill(state.pid, 'SIGTERM') } catch { await removeDaemonState(env); return 'not-running' }
  const deadline = Date.now() + 5_000
  while (Date.now() < deadline && isAlive(state.pid)) await new Promise((resolve) => setTimeout(resolve, 100))
  if (isAlive(state.pid)) { try { process.kill(state.pid, 'SIGKILL') } catch { /* already gone */ } }
  await removeDaemonState(env)
  return 'stopped'
}

export function ensureStateDirForTests(env: DaemonEnv = process.env): void {
  mkdirSync(daemonStateDir(env), { recursive: true, mode: 0o700 })
}
