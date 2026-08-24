import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'

// `saki genesis "<product idea>" [--restart]` — spawns the /saki-builder:genesis skill.
//
// Same shape as cmdRoadmapAdd: genesis produces its own preconditions (roadmap, PRD, stack) by
// running a skill, there is no REST resource for "start a product from scratch" to POST to.
export async function cmdGenesis(ctx: Ctx, idea: string, flags: Record<string, string | boolean>): Promise<ExitCode> {
  const text = idea.trim()
  if (!text) {
    fail('genesis needs a one-line product idea', EXIT.USAGE, 'usage: saki genesis "<product idea>" [--restart]')
  }

  const restart = flags.restart === true ? ' --restart' : ''
  const res = await ctx.client.post<{ runId?: string }>('/api/run', {
    prompt: `/saki-builder:genesis "${text}"${restart}`,
    cwd: ctx.cwd,
  })
  const runId = res?.runId
  if (!runId) fail('the studio accepted genesis but returned no runId', EXIT.ERROR)

  emit({ runId }, { json: ctx.json, human: `started ${runId} — running genesis` }, ctx.write)
  return EXIT.OK
}
