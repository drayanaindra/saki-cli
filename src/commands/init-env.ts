import { isAbsolute, relative, resolve } from 'node:path'
import { emit } from '../output.js'
import { EXIT, CliError, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { InitEnvResult } from '../types.js'
import { RUN_ENGINES, assertRunEngine } from './run.js'

export async function cmdInitEnv(
  ctx: Ctx,
  positionals: string[],
  flags: Record<string, string | boolean>,
): Promise<ExitCode> {
  if (positionals.length) throw new CliError('init-env takes no positional arguments', EXIT.USAGE)
  const raw = typeof flags.engine === 'string' ? flags.engine : ''
  if (!raw) throw new CliError(`--engine is required (${RUN_ENGINES.join('|')})`, EXIT.USAGE)
  const engine = assertRunEngine(raw)
  const rawProfile = typeof flags.profile === 'string' ? flags.profile : undefined
  let profile: string | undefined
  if (rawProfile) {
    const absolute = isAbsolute(rawProfile)
    const resolved = absolute ? resolve(rawProfile) : resolve(ctx.cwd, rawProfile)
    if (!absolute) {
      const rel = relative(ctx.cwd, resolved)
      if (rel === '..' || rel.startsWith('../')) {
        throw new CliError('profile path escapes the repository cwd', EXIT.USAGE)
      }
    }
    profile = resolved
  }
  const result = await ctx.client.post<InitEnvResult>('/api/init-env', { cwd: ctx.cwd, engine, profile })
  emit(
    result,
    { json: ctx.json, human: `${result.engine}: ${result.status}${result.changed ? ' (changed)' : ' (unchanged)'}` },
    ctx.write,
  )
  if (result.status !== 'ok') {
    if (result.reason) ctx.writeErr(`error: ${result.reason}`)
    if (result.fix) ctx.writeErr(`fix (${result.engine}): ${result.fix}`)
    return EXIT.ERROR
  }
  return EXIT.OK
}
