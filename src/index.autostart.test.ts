import { afterEach, describe, expect, it, vi } from 'vitest'
import { main } from './index.js'
import { EXIT } from './exit.js'

// The pre-flight auto-start block in main() is skipped whenever deps.fetchImpl is set, which every
// other index test does — so §10 rule 4 and AC 1.1 are only observable from a suite that mocks the
// daemon module instead of injecting a transport.
const daemon = vi.hoisted(() => ({
  ensureDaemon: vi.fn(),
  readDaemonState: vi.fn(),
  stopDaemon: vi.fn(),
  socketFetch: vi.fn(),
}))

vi.mock('./daemon.js', () => daemon)

const RUNNING = { pid: 4242, socketPath: null, goUrl: 'http://127.0.0.1:8788' }

afterEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

function run(argv: string[]) {
  const out: string[] = []
  const errors: string[] = []
  return {
    out,
    errors,
    exit: main(argv, { write: (s) => out.push(s), writeErr: (s) => errors.push(s), env: {} }),
  }
}

describe('pre-flight auto-start', () => {
  // §10 rule 4 — status and stop must report the state they FIND. Recovering first would make
  // `saki backend status` incapable of ever reporting a stopped daemon.
  it.each(['status', 'stop'])('does not auto-start for `saki backend %s`', async (action) => {
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.stopDaemon.mockResolvedValue('not-running')
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('connect ECONNREFUSED') }))

    await run(['backend', action]).exit

    expect(daemon.ensureDaemon).not.toHaveBeenCalled()
  })

  // §10 rule 4 for `start`: the command spawns deliberately, so ensureDaemon must run EXACTLY once —
  // the command's own call. A pre-flight call as well would mean the command reported on a daemon it
  // had already silently started.
  it('reaches the daemon exactly once for `saki backend start`', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.ensureDaemon.mockResolvedValue(RUNNING)

    await run(['backend', 'start']).exit

    expect(daemon.ensureDaemon).toHaveBeenCalledTimes(1)
  })

  // AC 1.1 — an ordinary command auto-starts and says so on stderr.
  it('auto-starts for an ordinary command and logs the event to stderr', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.ensureDaemon.mockResolvedValue(RUNNING)
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ ok: true, service: 'saki-backend' }),
    })))

    const { errors, exit } = run(['status'])
    await exit

    expect(daemon.ensureDaemon).toHaveBeenCalled()
    expect(errors.some((line) => line.startsWith('daemon:autostart'))).toBe(true)
  })

  // The event goes to stderr, never stdout — `--json` output has to stay parseable.
  it('keeps the autostart event off stdout', async () => {
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.ensureDaemon.mockResolvedValue(RUNNING)
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ ok: true, service: 'saki-backend' }),
    })))

    const { out, exit } = run(['status', '--json'])
    await exit

    expect(out.some((line) => line.includes('daemon:autostart'))).toBe(false)
  })

  // Reusing an already-tracked daemon is silent — the event marks a spawn, not every command.
  it('stays silent when the tracked daemon is reused', async () => {
    daemon.readDaemonState.mockResolvedValue(RUNNING)
    daemon.ensureDaemon.mockResolvedValue(RUNNING)
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ ok: true, service: 'saki-backend' }),
    })))

    const { errors, exit } = run(['status'])
    await exit

    expect(errors.some((line) => line.startsWith('daemon:autostart'))).toBe(false)
  })

  // AC 1.3 — a daemon that cannot start surfaces as UNREACHABLE (3), never ERROR (1): agents branch
  // on the number (§10 rule 3).
  it('surfaces an unstartable daemon as UNREACHABLE', async () => {
    const { CliError } = await import('./exit.js')
    daemon.readDaemonState.mockResolvedValue(null)
    daemon.ensureDaemon.mockRejectedValue(
      new CliError('saki-backend binary not found', EXIT.UNREACHABLE, 'run npm run backend:build'),
    )

    const { exit, errors } = run(['status'])

    await expect(exit).resolves.toBe(EXIT.UNREACHABLE)
    expect(errors.join('\n')).toContain('saki-backend binary not found')
  })

  // `--help` must never spawn anything: it is the one command that has to work on a broken install.
  it('does not auto-start for a help request', async () => {
    const { exit } = run(['backend', '--help'])

    await expect(exit).resolves.toBe(EXIT.OK)
    expect(daemon.ensureDaemon).not.toHaveBeenCalled()
  })
})
