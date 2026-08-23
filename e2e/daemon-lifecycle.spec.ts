import { test, expect } from '@playwright/test'
import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// F1 — daemon lifecycle + unix-socket transport, driven end to end through the REAL
// dist/saki-backend binary and the REAL dist/index.js CLI (CLAUDE.md project rule 2: a
// fake-binary test cannot prove an engine/process invocation). The webServer in
// playwright.config.ts already started a real saki-backend on 127.0.0.1:8788 before this
// file runs — that instance itself wrote the daemon state file (main.go writeDaemonState
// runs unconditionally), so `saki backend stop` here stops a real, already-running daemon
// and `saki status` afterwards proves a real re-spawn, not a mock.
const CLI = path.join(process.cwd(), 'dist', 'index.js')
const uid = typeof process.getuid === 'function' ? process.getuid() : 0
const statePath = path.join(os.tmpdir(), `saki-${uid}`, 'backend.state.json')
const EXIT_UNREACHABLE = 3 // src/exit.ts EXIT.UNREACHABLE — an agent branches on this number

function readState(): { pid: number; socketPath: string | null; goUrl: string } {
  const raw = JSON.parse(fs.readFileSync(statePath, 'utf8'))
  expect(raw, `state file at ${statePath} did not parse to a daemon state`).toMatchObject({ pid: expect.any(Number) })
  return raw
}

// No SAKI_BACKEND_URL — that env var disables the daemon gate entirely (src/index.ts), so
// omitting it is what exercises the real auto-start / lifecycle path under test here. Also
// strip SAKI_STUDIO_URL (an operator's shell may export it for the companion express studio) so
// `saki status` reports on the Go backend alone, matching AC 1.1's "no manual pre-launch" scenario.
function saki(args: string[]) {
  const env = { ...process.env }
  delete env.SAKI_BACKEND_URL
  delete env.SAKI_STUDIO_URL
  return spawnSync(process.execPath, [CLI, ...args], { encoding: 'utf8', timeout: 60_000, env })
}

function parseJson(stdout: string): Record<string, unknown> {
  return JSON.parse(stdout.trim().split('\n').filter(Boolean).pop() ?? '')
}

async function healthOk(url: string): Promise<boolean> {
  try {
    const res = await fetch(`${url}/api/health`)
    const body = (await res.json()) as { ok?: boolean }
    return res.ok && body.ok === true
  } catch {
    return false
  }
}

test.describe('daemon lifecycle + unix socket — real saki-backend binary', () => {
  test.setTimeout(120_000)

  // Safety net: if a step throws mid-spec after `backend stop` but before the final respawn, this
  // guarantees a healthy backend is left at 127.0.0.1:8788 regardless — otherwise one failure here
  // cascades into UNREACHABLE failures across every later spec file (opencode-*, alphabetically after
  // this one) that assumes a live backend via SAKI_BACKEND_URL.
  test.afterAll(() => {
    saki(['backend', 'start'])
  })

  test('stop, auto-start, no double-spawn, socket transport', async () => {
    expect(fs.existsSync(CLI), `${CLI} is missing — the webServer step must build the CLI`).toBe(true)
    expect(await healthOk('http://127.0.0.1:8788'), 'webServer must have a live backend before this spec').toBe(true)

    await test.step('AC 3.2 / 5.4 — backend stop terminates the real daemon and removes state', () => {
      const stop = saki(['backend', 'stop', '--json'])
      expect(stop.status, stop.stderr).toBe(0)
      expect(parseJson(stop.stdout)).toMatchObject({ status: 'stopped' })
      expect(fs.existsSync(statePath)).toBe(false)
    })

    await test.step('confirms the real process is actually gone', async () => {
      expect(await healthOk('http://127.0.0.1:8788')).toBe(false)
    })

    let firstPid = -1
    await test.step('AC 1.1 / 1.2 / 5.1 / 5.2 — saki status auto-starts within 10s and logs the event', () => {
      const start = Date.now()
      const status = saki(['status'])
      const elapsedMs = Date.now() - start
      expect(status.status, status.stderr).toBe(0)
      expect(status.stderr).toMatch(/daemon:autostart \{result:"success"/)
      expect(elapsedMs).toBeLessThanOrEqual(10_000)
      firstPid = readState().pid
      expect(firstPid).toBeGreaterThan(0)
    })

    await test.step('the auto-started backend is really answering on the loopback TCP port', async () => {
      expect(await healthOk('http://127.0.0.1:8788')).toBe(true)
    })

    await test.step('AC 2.1 — a second command reuses the daemon, no second spawn, no autostart log', () => {
      const status = saki(['status'])
      expect(status.status, status.stderr).toBe(0)
      expect(status.stderr).not.toMatch(/daemon:autostart/)
      expect(readState().pid).toBe(firstPid)
    })

    await test.step('AC 3.3 — backend start on an already-running daemon is idempotent', () => {
      const start = saki(['backend', 'start'])
      expect(start.status, start.stderr).toBe(0)
      expect(start.stdout).toContain('already running')

      const startJson = saki(['backend', 'start', '--json'])
      expect(startJson.status, startJson.stderr).toBe(0)
      expect(parseJson(startJson.stdout)).toMatchObject({ pid: firstPid })
    })

    let socketPath = ''
    await test.step('AC 3.4 / 4.1 — backend status --json reports the live socket path', () => {
      const status = saki(['backend', 'status', '--json'])
      expect(status.status, status.stderr).toBe(0)
      const parsed = parseJson(status.stdout) as { pid: number; healthy: boolean; goUrl: string; socketPath: string | null }
      expect(parsed).toMatchObject({ pid: firstPid, healthy: true, goUrl: 'http://127.0.0.1:8788' })
      expect(typeof parsed.socketPath).toBe('string')
      socketPath = parsed.socketPath as string
    })

    await test.step('AC 4.4 — the unix socket file is 0600 (owner read-write only)', () => {
      expect(fs.existsSync(socketPath)).toBe(true)
      const mode = fs.statSync(socketPath).mode & 0o777
      expect(mode).toBe(0o600)
    })

    await test.step('AC 4.5 / security — health via the unix socket answers within 3s and passes OriginGuard', () => {
      const start = Date.now()
      const curl = spawnSync('curl', [
        '--unix-socket', socketPath,
        '-sS', '-o', '/dev/null', '-w', '%{http_code}',
        '-H', 'Host: localhost',
        'http://localhost/api/health',
      ], { encoding: 'utf8', timeout: 5_000 })
      const elapsedMs = Date.now() - start
      expect(curl.stdout.trim(), curl.stderr).toBe('200')
      expect(elapsedMs).toBeLessThanOrEqual(3_000)
    })

    await test.step('AC 4.2 — SAKI_BACKEND_URL forces TCP regardless of socket presence', () => {
      // Socket is live and healthy at this point (prior steps proved it). Pointing SAKI_BACKEND_URL
      // at a port nothing listens on proves the CLI actually took the TCP/env path and never fell
      // back to the working socket — a real proof, not a tautology (would go green on socket or TCP).
      const env = { ...process.env, SAKI_BACKEND_URL: 'http://127.0.0.1:8799' }
      delete env.SAKI_STUDIO_URL
      const status = spawnSync(process.execPath, [CLI, 'status'], { encoding: 'utf8', timeout: 30_000, env })
      expect(status.status, status.stderr).toBe(EXIT_UNREACHABLE)
    })

    await test.step('AC 4.6 — a stale socket from a prior crash is removed before the next bind', async () => {
      // SIGKILL bypasses the graceful SIGTERM handler (main.go) that would otherwise remove the
      // socket itself — this is what actually leaves a stale socket file behind, unlike `backend stop`.
      // Re-read the state file first: firstPid was captured several steps ago, so confirm it is
      // still the live daemon's PID before signalling (guards against signalling a recycled PID).
      expect(readState().pid).toBe(firstPid)
      process.kill(firstPid, 'SIGKILL')
      const deadline = Date.now() + 5_000
      while (Date.now() < deadline && (await healthOk('http://127.0.0.1:8788'))) {
        await new Promise((resolve) => setTimeout(resolve, 100))
      }
      expect(await healthOk('http://127.0.0.1:8788')).toBe(false)
      expect(fs.existsSync(socketPath), 'the crash must leave the socket file behind, unremoved').toBe(true)

      const status = saki(['status'])
      expect(status.status, status.stderr).toBe(0)
      expect(await healthOk('http://127.0.0.1:8788')).toBe(true)

      const curl = spawnSync('curl', [
        '--unix-socket', socketPath,
        '-sS', '-o', '/dev/null', '-w', '%{http_code}',
        '-H', 'Host: localhost',
        'http://localhost/api/health',
      ], { encoding: 'utf8', timeout: 5_000 })
      expect(curl.stdout.trim(), curl.stderr).toBe('200')
    })

    // Leave a healthy daemon running at 127.0.0.1:8788 — later spec files (opencode-*, in
    // alphabetical run order) expect a live backend there via SAKI_BACKEND_URL.
    await test.step('leaves a healthy daemon running for subsequent specs', async () => {
      expect(await healthOk('http://127.0.0.1:8788')).toBe(true)
    })
  })
})
