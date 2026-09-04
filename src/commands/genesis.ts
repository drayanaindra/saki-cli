import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { RunEngineSelection } from '../engines.js'
import { resolveEngineSelection } from '../engine-selection.js'

// `saki genesis "<product idea>" [--restart] [--engine <engine|auto>] [--profile <dir>]` — spawns
// the /saki-builder:genesis skill.
//
// Same shape as cmdRoadmapAdd: genesis produces its own preconditions (roadmap, PRD, stack) by
// running a skill; there is no REST resource for "start a product from scratch" to POST to.
export interface GenesisSpawnFlags {
  profile?: string
  engine?: RunEngineSelection
}

export async function cmdGenesis(
  ctx: Ctx,
  idea: string,
  flags: Record<string, string | boolean>,
  spawnFlags: GenesisSpawnFlags = {},
): Promise<ExitCode> {
  const text = idea.trim()
  if (!text) {
    fail(
      'genesis needs a one-line product idea',
      EXIT.USAGE,
      'usage: saki genesis "<product idea>" [--restart] [--engine <e>] [--profile <dir>]',
    )
  }

  const restart = flags.restart === true ? ' --restart' : ''
  const engine = await resolveEngineSelection(ctx.client, spawnFlags.engine, spawnFlags.profile)
  const body: Record<string, unknown> = {
    prompt: `/saki-builder:genesis "${text}"${restart}`,
    cwd: ctx.cwd,
  }
  if (spawnFlags.profile) body.configDir = spawnFlags.profile
  if (engine) body.engine = engine
  const res = await ctx.client.post<{ runId?: string }>('/api/run', body)
  const runId = res?.runId
  if (!runId) fail('the studio accepted genesis but returned no runId', EXIT.ERROR)

  emit({ runId }, { json: ctx.json, human: `started ${runId} — running genesis` }, ctx.write)
  return EXIT.OK
}
