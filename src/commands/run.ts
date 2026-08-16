import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import { cmdRunTail } from './runs.js'
import { fetchPrd, resolveTargetPrdPath } from './prd.js'
import { findItem, resolvePlanPath } from '../resolve.js'
import type { Ctx } from '../ctx.js'
import type { RoadmapResult, RunSummary } from '../types.js'

// The saki-builder skills the CLI can launch headlessly — the full manual chain
// (rplan → rplan-review → approved → qa → reviewer → wrap) plus the PRD-track entry points
// (pickup → proto → build) and prd-review. All are non-interactive, which is what makes them
// drivable without a terminal.
export const RUN_VERBS = [
  'build',
  'pickup',
  'proto',
  'rplan',
  'prd-review',
  'rplan-review',
  'approved',
  'qa',
  'reviewer',
  'wrap',
] as const
export type RunVerb = (typeof RUN_VERBS)[number]

export function isRunVerb(v: string): v is RunVerb {
  return (RUN_VERBS as readonly string[]).includes(v)
}

// Shared by the CLI's `['run']` handler (index.ts) and the `saki_run_start` MCP tool, so an unknown verb
// produces the byte-identical message/hint on both surfaces — one function, not a hand-copied string.
export function assertRunVerb(v: string): RunVerb {
  if (isRunVerb(v)) return v
  fail(
    `unknown run verb: ${v || '(none)'}`,
    EXIT.USAGE,
    `expected one of ${RUN_VERBS.join(', ')} — or \`run tail\` / \`run stop\``,
  )
}

// Verbs that take NO target: `reviewer` reviews the git diff, `wrap` gates the whole repo. Handing
// either an id or a path is a mistake worth rejecting rather than forwarding as a bogus argument.
const NO_TARGET_VERBS = new Set<RunVerb>(['reviewer', 'wrap'])

// Verbs whose target is OPTIONAL — each falls back to the newest matching file in `tasks/` when
// given nothing (prd-review:66, rplan-review:19, approved:28-30, qa:16-18). Requiring an argument
// would block that documented default.
const OPTIONAL_TARGET_VERBS = new Set<RunVerb>([
  'prd-review',
  'rplan-review',
  'approved',
  'qa',
  ...NO_TARGET_VERBS,
])

// Verbs whose id resolves through `**Child plan:**` rather than `**Child PRD:**` — they operate on
// a PLAN. Separate roadmap fields (roadmap.ts:36,63-67); an item can carry both.
const PLAN_TARGET_VERBS = new Set<RunVerb>(['rplan-review', 'approved', 'qa'])

// `wrap --heal` is a MODE the skill reads from its own invocation text (wrap/SKILL.md:45), so it
// belongs in the prompt — in the HTTP body the studio would simply ignore it.
const HEAL_VERBS = new Set<RunVerb>(['wrap'])

export function targetIsOptional(verb: RunVerb): boolean {
  return OPTIONAL_TARGET_VERBS.has(verb)
}

export function takesNoTarget(verb: RunVerb): boolean {
  return NO_TARGET_VERBS.has(verb)
}

export function supportsHeal(verb: RunVerb): boolean {
  return HEAL_VERBS.has(verb)
}

// Always emit the CANONICAL plugin namespace.
//
// The studio rewrites it per profile at spawn time — resolveCmdNs() probes the profile's skill dir
// and normalizeCmdNs() rewrites the leading token (apps/server/src/index.ts:1270, cmdNs.ts:26,41) —
// so a bare/symlink profile receives `/build` and a plugin profile `/saki-builder:build`. The CLI
// must not try to detect this itself: guessing per-profile from the client side is precisely the
// bug cmdNs.ts:20-24 was written to fix.
export function buildRunPrompt(verb: string, arg: string): string {
  const a = arg.trim()
  return `/saki-builder:${verb}${a ? ` ${a}` : ''}`
}

// E26 — the agent runtime a run executes on. Validated by the Go backend at the boundary
// (backend/adapter/http.go parseRunEngine): only these, or empty for the claude default.
export const RUN_ENGINES = ['claude', 'opencode', 'codex'] as const
export type RunEngine = (typeof RUN_ENGINES)[number]

export function isRunEngine(v: string): v is RunEngine {
  return (RUN_ENGINES as readonly string[]).includes(v)
}

// Shared by index.ts's `startRun` helper and the `saki_run_start` MCP tool — same byte-identical
// message/hint reasoning as `assertRunVerb` above.
export function assertRunEngine(v: string): RunEngine {
  if (isRunEngine(v)) return v
  fail(`unknown engine: ${v}`, EXIT.USAGE, `expected one of ${RUN_ENGINES.join(', ')}`)
}

export interface RunStartFlags {
  profile?: string
  follow?: boolean
  // Only the Go backend understands this; sending it to Express would be silently ignored, which is
  // why the CLI routes /api/run to Go (routes.ts).
  engine?: RunEngine
  // `wrap --heal` only — autonomous mode: auto-fix and re-run a failing DoD gate instead of
  // stopping. Ignored by every other verb.
  heal?: boolean
}

// Turn the user's argument into the exact target the skill should receive.
//
// Each verb points at a different artifact, and getting this wrong reviews or builds the wrong
// thing rather than failing loudly:
//   build         -> the absolute PRD path (also the studio's dedupe lane key — see below)
//   prd-review    -> the item's Child PRD
//   rplan-review  -> the item's Child PLAN (a DIFFERENT roadmap field — roadmap.ts:36)
//   pickup/proto/rplan -> the raw argument; these skills take an item id themselves
async function resolveTarget(ctx: Ctx, verb: RunVerb, arg: string): Promise<string> {
  const a = arg.trim()
  if (!a) return '' // optional-target verb with nothing given — let the skill pick

  // An explicit path is already the answer for the review verbs; only an id needs a lookup.
  const looksLikePath = /\.md$/i.test(a)

  if (verb === 'build') {
    // A build's lane identity is the ABSOLUTE PRD path — not the argument the user typed. The
    // server says so outright (index.ts:236: "the lane key IS the PRD path") and the UI sends
    // exactly that (App.tsx:1447). Two things key off it, and both break on any other value:
    //   1. dedupe — activeBuild() matches on laneKey ALONE (runManager.ts:659), so a relative path
    //      or a bare id never matches the UI's key and the CLI spawns a SECOND concurrent /build on
    //      a branch the studio is already building.
    //   2. the auto-resume progress fingerprint — buildProgressFingerprint(cwd, laneKey) is read by
    //      workitems.ts AS a PRD path; a non-path yields a constant fingerprint, so the circuit
    //      breaker sees "no progress" and parks a build that is advancing fine.
    // fetchPrd (not just path resolution) because resolving alone accepts ANY `.md` string, which
    // let a typo'd path spawn a real run, and let a subdirectory cwd fork the lane. Confirming
    // existence turns both into a loud exit 4, and adopts the studio's canonical path.
    return (await fetchPrd(ctx, await resolveTargetPrdPath(ctx, a))).path
  }
  if (verb === 'prd-review' && !looksLikePath) {
    return resolveTargetPrdPath(ctx, a)
  }
  if (PLAN_TARGET_VERBS.has(verb) && !looksLikePath) {
    const roadmap = await ctx.client.get<RoadmapResult>('/api/roadmap', { cwd: ctx.cwd })
    if (!roadmap?.found) {
      fail(`no tasks/roadmap.md found in ${ctx.cwd}`, EXIT.NOT_FOUND, 'or pass the plan path directly')
    }
    return resolvePlanPath(findItem(roadmap.epics ?? [], a), ctx.cwd)
  }
  return a
}

// Ids of builds already running for this lane. Used only to report re-adoption honestly — the
// server remains authoritative for whether a second build is actually spawned.
async function activeBuildIds(ctx: Ctx, laneKey: string): Promise<Set<string>> {
  try {
    const runs = await ctx.client.get<RunSummary[]>('/api/runs', { cwd: ctx.cwd })
    return new Set(
      (Array.isArray(runs) ? runs : [])
        .filter((r) => r.status === 'running' && r.meta?.kind === 'build' && r.meta?.laneKey === laneKey)
        .map((r) => r.id),
    )
  } catch {
    // Reporting nicety only — never let it block a spawn.
    return new Set()
  }
}

interface RunStartResponse {
  runId?: string
  deduped?: boolean
}

// `saki run <verb> <arg>` — spawn a headless skill run (index.ts:1249).
export async function cmdRunStart(
  ctx: Ctx,
  verb: RunVerb,
  arg: string,
  flags: RunStartFlags,
): Promise<ExitCode> {
  if (!arg.trim() && !targetIsOptional(verb)) {
    fail(`run ${verb} needs an argument`, EXIT.USAGE, `usage: saki run ${verb} <roadmap-id|path>`)
  }
  if (arg.trim() && takesNoTarget(verb)) {
    fail(
      `run ${verb} takes no argument (got "${arg.trim()}")`,
      EXIT.USAGE,
      verb === 'reviewer'
        ? 'reviewer works on the git diff, not a named file'
        : 'wrap gates the whole repo, not a named file',
    )
  }

  const target = await resolveTarget(ctx, verb, arg)

  // `--heal` is part of the COMMAND, not the request: the skill parses it from its own invocation
  // text (wrap/SKILL.md:45). Putting it in the body would silently do nothing.
  const heal = flags.heal === true && supportsHeal(verb) ? ' --heal' : ''

  const body: Record<string, unknown> = {
    prompt: `${buildRunPrompt(verb, target)}${heal}`,
    cwd: ctx.cwd,
  }
  if (flags.profile) body.configDir = flags.profile
  if (flags.engine) body.engine = flags.engine
  if (verb === 'build') body.meta = { kind: 'build', laneKey: target }

  // Snapshot the lane's live builds BEFORE spawning, so a re-adoption can be reported honestly.
  //
  // The two backends answer differently: Express returns `{runId, deduped:true}` when it re-adopts,
  // but the Go build path (backend/adapter/http.go:458 → writeSpawnResult) returns only `{runId}`.
  // Go still dedupes — it just doesn't say so. Without this the CLI prints "started <id>" for a run
  // it did not start. Comparing against the pre-spawn set covers both: if the id we get back was
  // already running, it was re-adopted.
  const before = verb === 'build' ? await activeBuildIds(ctx, target) : new Set<string>()

  const res = await ctx.client.post<RunStartResponse>('/api/run', body)
  const runId = res?.runId
  if (!runId) fail('the studio accepted the run but returned no runId', EXIT.ERROR)

  const deduped = res.deduped === true || before.has(runId)
  emit(
    { runId, deduped },
    {
      json: ctx.json,
      human: deduped
        ? `${runId} already running for this lane — reusing it (no second build spawned)`
        : `started ${runId}`,
    },
    ctx.write,
  )

  // --follow makes the command block until the run settles and adopt the RUN's verdict, so an
  // agent can do `saki run build x --follow && next-step`.
  if (flags.follow) return cmdRunTail(ctx, runId)
  return EXIT.OK
}
