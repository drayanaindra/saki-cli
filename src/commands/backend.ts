import { emit } from '../output.js'
import { EXIT, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import { ensureDaemon, probeBackendHealth, readDaemonState, stopDaemon } from '../daemon.js'

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
  return status(ctx)
}

// §10 rule 4: status reports the state it FINDS, which means probing even when no state file exists.
// A manually launched or pre-daemon backend owns the port without a record, and ensureDaemon already
// treats that as running (pid 0) — reporting "not running" for a backend that is answering would be
// the exact misreport this rule exists to prevent.
async function status(ctx: Ctx): Promise<ExitCode> {
  const state = await readDaemonState(ctx.env)
  const goUrl = state?.goUrl ?? ctx.client.goUrl
  const healthy = await probeBackendHealth(goUrl)
  const answer = {
    pid: state?.pid ?? null,
    healthy,
    goUrl,
    socketPath: state?.socketPath ?? null,
  }
  emit(answer, { json: ctx.json, human: humanStatus(answer) }, ctx.write)
  return EXIT.OK
}

function humanStatus(answer: { pid: number | null; healthy: boolean }): string {
  if (!answer.healthy) return 'backend not running'
  // An untracked-but-healthy backend is a real, reportable state: it is serving, we just did not
  // start it, so there is no PID to name.
  return answer.pid === null ? 'backend healthy (not daemon-tracked)' : `backend healthy (pid ${answer.pid})`
}

function fail(ctx: Ctx, message: string): ExitCode {
  ctx.writeErr(`error: ${message}`)
  return EXIT.USAGE
}
