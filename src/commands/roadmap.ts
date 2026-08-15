import { emit, renderTable, type Column } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { RoadmapItem, RoadmapResult, WorkItemType } from '../types.js'

// MIRRORS frontend/src/lib/addCommand.ts:9. Each type forces its flag, which is what skips the
// skill's interactive "Confirm?" step so `/add` can be driven headlessly.
export const ADD_FLAG: Record<WorkItemType, string> = {
  Epic: '--epic',
  Feature: '--feature',
  Improvement: '--improvement',
  Bug: '--bug',
}

const FLAG_TO_TYPE: Array<[string, WorkItemType]> = [
  ['epic', 'Epic'],
  ['feature', 'Feature'],
  ['improvement', 'Improvement'],
  ['bug', 'Bug'],
]

const ROADMAP_COLUMNS: Column<RoadmapItem>[] = [
  { header: 'ID', value: (i) => i.id },
  { header: 'TYPE', value: (i) => i.type },
  { header: 'TRACK', value: (i) => i.track },
  { header: 'STATUS', value: (i) => i.status },
  { header: 'TITLE', value: (i) => i.title, max: 60 },
]

// Exactly one type flag must be present: `/add` categorises interactively without one, which would
// hang a headless run, and two would be ambiguous.
export function resolveAddType(flags: Record<string, string | boolean>): WorkItemType {
  const picked = FLAG_TO_TYPE.filter(([flag]) => flags[flag] === true).map(([, type]) => type)
  if (picked.length === 1) return picked[0]
  const list = Object.values(ADD_FLAG).join(', ')
  if (picked.length === 0) {
    fail(`one of ${list} is required`, EXIT.USAGE, 'the flag is what keeps /add non-interactive')
  }
  fail(`exactly one of ${list} may be given (got ${picked.length})`, EXIT.USAGE)
}

// `saki roadmap list` — read-only view of tasks/roadmap.md (index.ts:759).
export async function cmdRoadmapList(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.get<RoadmapResult>('/api/roadmap', { cwd: ctx.cwd })
  if (!res?.found) {
    fail(
      `no tasks/roadmap.md found in ${ctx.cwd}`,
      EXIT.NOT_FOUND,
      'scaffold one with `/saki-builder:roadmap init` in that repo',
    )
  }
  const items = res.epics ?? []
  emit(
    res,
    {
      json: ctx.json,
      human: items.length ? renderTable(items, ROADMAP_COLUMNS) : 'roadmap found, but it has no work items yet',
    },
    ctx.write,
  )
  return EXIT.OK
}

// `saki roadmap add "<intent>" --feature` — spawns the /add skill.
//
// There is deliberately no REST path for this: GET /api/roadmap is read-only and the server
// comment at index.ts:757 states every status transition is produced by spawning a skill, never by
// the board writing the file. Adding via a run keeps the CLI on that same rule.
export async function cmdRoadmapAdd(
  ctx: Ctx,
  intent: string,
  flags: Record<string, string | boolean>,
): Promise<ExitCode> {
  // Validate BOTH inputs before any network call, so a malformed invocation can never spawn a run.
  const type = resolveAddType(flags)
  const text = intent.trim()
  if (!text) {
    fail('roadmap add needs an intent', EXIT.USAGE, 'usage: saki roadmap add "<intent>" --feature')
  }

  const res = await ctx.client.post<{ runId?: string }>('/api/run', {
    prompt: `/saki-builder:add ${ADD_FLAG[type]} ${text}`,
    cwd: ctx.cwd,
  })
  const runId = res?.runId
  if (!runId) fail('the studio accepted the add but returned no runId', EXIT.ERROR)

  emit(
    { runId, type },
    { json: ctx.json, human: `started ${runId} — adding a ${type} to the roadmap` },
    ctx.write,
  )
  return EXIT.OK
}
