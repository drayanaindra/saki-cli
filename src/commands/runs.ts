import { emit, renderTable, type Column } from '../output.js'
import { EXIT, CliError, fail, type ExitCode } from '../exit.js'
import { streamRun } from '../sse.js'
import type { Ctx } from '../ctx.js'
import { RUN_STATUS_DONE, type RunSummary } from '../types.js'

const RUN_COLUMNS: Column<RunSummary>[] = [
  { header: 'ID', value: (r) => r.id, max: 36 },
  { header: 'STATUS', value: (r) => r.status },
  { header: 'KIND', value: (r) => r.meta?.kind ?? '—' },
  { header: 'STARTED', value: (r) => formatStarted(r.startedAt) },
  { header: 'PROMPT', value: (r) => r.prompt.replace(/\s+/g, ' ').trim(), max: 60 },
]

function formatStarted(ms: number): string {
  if (!ms) return '—'
  return new Date(ms).toISOString().replace('T', ' ').slice(0, 19)
}

// `saki runs` — what the studio still holds in memory, newest first (index.ts:1283).
export async function cmdRuns(ctx: Ctx): Promise<ExitCode> {
  const runs = await ctx.client.get<RunSummary[]>('/api/runs', { cwd: ctx.cwd })
  const list = Array.isArray(runs) ? runs : []
  emit(
    list,
    {
      json: ctx.json,
      human: list.length ? renderTable(list, RUN_COLUMNS) : `no runs held for ${ctx.cwd}`,
    },
    ctx.write,
  )
  return EXIT.OK
}

// `saki run stop <id>` — 404 means unknown or already finished (index.ts:1285).
export async function cmdRunStop(ctx: Ctx, runId: string): Promise<ExitCode> {
  if (!runId) fail('run stop needs a run id', EXIT.USAGE, 'usage: saki run stop <runId>')
  try {
    await ctx.client.post(`/api/run/${encodeURIComponent(runId)}/stop`, {})
  } catch (err) {
    if (err instanceof CliError && err.code === EXIT.NOT_FOUND) {
      throw new CliError(
        `no running run with id ${runId}`,
        EXIT.NOT_FOUND,
        'it may have already finished — check `saki runs`',
      )
    }
    throw err
  }
  emit({ stopped: true, runId }, { json: ctx.json, human: `stopped ${runId}` }, ctx.write)
  return EXIT.OK
}

// `saki run tail <id>` — stream to the terminal, then exit with the RUN's verdict rather than the
// CLI's. An agent can therefore branch on `saki run tail` alone.
export async function cmdRunTail(ctx: Ctx, runId: string): Promise<ExitCode> {
  if (!runId) fail('run tail needs a run id', EXIT.USAGE, 'usage: saki run tail <runId>')
  const end = await streamRun(ctx.client, runId, (line) => ctx.write(line))

  // Success is `status === 'done'` — the server's vocabulary is 'running' | 'done' | 'error'
  // (runManager.ts:50) and the end frame carries it verbatim (runManager.ts:953). It never emits
  // 'completed'; checking for that made EVERY green build exit 1.
  const ok = end.status === RUN_STATUS_DONE

  if (ctx.json) {
    emit({ runId, ...end }, { json: true }, ctx.write)
  } else {
    // A stopped/killed run ends with exitCode null (runManager.ts:1165) — print the status alone
    // rather than an invented "exit 0" next to an error.
    const suffix = end.exitCode == null ? '' : ` (exit ${end.exitCode})`
    ctx.write(`run ${end.status}${suffix}`)
  }
  return ok ? EXIT.OK : EXIT.ERROR
}
