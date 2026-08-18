import { isAbsolute, relative, resolve } from 'node:path'
import { emit } from '../output.js'
import { EXIT, CliError, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { InitEnvResult } from '../types.js'
import { RUN_ENGINES, assertRunEngine } from '../engines.js'

// `saki init-env` — provision ONE engine profile, then prove it. The mutating twin of `saki doctor`,
// which stays strictly read-only.
//
// Exit codes: 0 iff the engine's SHARED PROOF passed (never merely because an installer exited 0);
// 1 when setup or the proof failed — deliberately the same code `saki doctor` returns for a failed
// engine report, NOT REMOTE_FAILED, which is reserved for the `{ok:false}` refusal envelope
// (src/client.ts postOk); 2 for bad arguments, decided here before any HTTP request is made.
export async function cmdInitEnv(
  ctx: Ctx,
  positionals: string[],
  flags: Record<string, string | boolean>,
): Promise<ExitCode> {
  if (positionals.length) throw new CliError('init-env takes no positional arguments', EXIT.USAGE)
  const raw = typeof flags.engine === 'string' ? flags.engine : ''
  if (!raw) throw new CliError(`--engine is required (${RUN_ENGINES.join('|')})`, EXIT.USAGE)
  const engine = assertRunEngine(raw)
  const profile = resolveProfileFlag(ctx, flags.profile)

  const result = await ctx.client.post<InitEnvResult>('/api/init-env', { cwd: ctx.cwd, engine, profile })
  // Defend against a malformed body the way cmdDoctor does: a 200 with an empty body would otherwise
  // become an uncaught TypeError on `.status`, reported as a nonsense message at exit 1.
  if (!result || typeof result.status !== 'string') {
    throw new CliError('the backend returned no init-env result', EXIT.ERROR)
  }

  emit(result, { json: ctx.json, human: humanLine(result) }, ctx.write)

  if (result.status !== 'ok') {
    if (result.reason) ctx.writeErr(`error: ${result.reason}`)
    if (result.fix) ctx.writeErr(`fix (${result.engine}): ${result.fix}`)
    return EXIT.ERROR
  }
  return EXIT.OK
}

// A RELATIVE profile is resolved against the repo cwd and may not escape it. An ABSOLUTE one is
// taken as given: PRD §6 states a legitimate default lives outside the repository (`~/.codex`), so
// containment binds repo-relative input, not an operator's explicitly chosen profile.
function resolveProfileFlag(ctx: Ctx, flag: string | boolean | undefined): string | undefined {
  if (typeof flag !== 'string' || !flag) return undefined
  if (isAbsolute(flag)) return resolve(flag)
  const resolved = resolve(ctx.cwd, flag)
  const rel = relative(ctx.cwd, resolved)
  if (rel === '..' || rel.startsWith('../')) {
    throw new CliError('profile path escapes the repository cwd', EXIT.USAGE)
  }
  return resolved
}

// Names the profile, because "which profile did I just provision" is the entire question this
// command answers — `saki doctor` renders a PROFILE column for the same reason.
function humanLine(result: InitEnvResult): string {
  const changed = result.changed ? 'changed' : 'unchanged'
  return `${result.engine} ${result.profile}: ${result.status} (${changed})`
}
