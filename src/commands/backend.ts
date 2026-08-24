import { emit } from '../output.js'
import { EXIT, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import { ensureDaemon, readDaemonState, stopDaemon } from '../daemon.js'

export async function cmdBackend(ctx: Ctx, positionals: string[], flags: Record<string, string | boolean>): Promise<ExitCode> {
  const action = positionals[0]
  if (!action || positionals.length > 1 || !['start', 'stop', 'status'].includes(action)) {
    return fail(ctx, 'usage: saki backend start|stop|status')
  }
  if (action === 'stop') {
    const result = await stopDaemon(ctx.env)
    emit({ status: result }, { json: ctx.json, human: result === 'stopped' ? 'backend stopped' : 'backend not running' }, ctx.write)
    return EXIT.OK
  }
  if (action === 'start') {
    const before = await readDaemonState(ctx.env)
    const state = await ensureDaemon(ctx.env)
    const human = before && before.pid === state.pid ? `backend already running (pid ${state.pid})` : `backend started (pid ${state.pid})`
    emit(state, { json: ctx.json, human }, ctx.write)
    return EXIT.OK
  }
  const state = await readDaemonState(ctx.env)
  const answer = state
    ? { pid: state.pid, healthy: Boolean(await ensureHealthy(state)), goUrl: state.goUrl, socketPath: state.socketPath }
    // No state file doesn't always mean no backend. backend/cmd/server/main.go writes its own state
    // file unconditionally on startup (same daemonStatePath() logic, keyed on uid + TMPDIR), so a
    // service-managed backend under the SAME user/TMPDIR as this shell is already covered by the
    // `state` branch above. This arm only matters when they diverge — a different uid (e.g. `sudo
    // brew services`) or a launchd/systemd environment with its own TMPDIR — where readDaemonState
    // can't find the file the service wrote. Probe reachability directly rather than assume "down".
    : { pid: null, healthy: Boolean(await ensureHealthy({ goUrl: ctx.client.goUrl })), goUrl: ctx.client.goUrl, socketPath: null }
  const human = answer.healthy
    ? answer.pid === null
      ? 'backend healthy (service-managed, no local state file)'
      : `backend healthy (pid ${answer.pid})`
    : 'backend not running'
  emit(answer, { json: ctx.json, human }, ctx.write)
  return EXIT.OK
}

async function ensureHealthy(state: { goUrl: string }): Promise<boolean> {
  try {
    const res = await fetch(`${state.goUrl}/api/health`, { signal: AbortSignal.timeout(3_000) })
    return res.ok && (await res.json() as { ok?: boolean }).ok === true
  } catch {
    return false
  }
}

function fail(ctx: Ctx, message: string): ExitCode {
  ctx.writeErr(`error: ${message}`)
  return EXIT.USAGE
}
