import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import { cmdRunTail } from './runs.js'
import { streamWorkflow } from '../sse.js'
import { isAbsolute, relative, resolve as resolvePath, sep } from 'node:path'
import { fetchPrd, resolveTargetPrdPath } from './prd.js'
import { findItem, resolvePlanPath } from '../resolve.js'
import type { Ctx } from '../ctx.js'
import type { RoadmapResult, WorkflowStartResult } from '../types.js'

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

// E26 — the agent-runtime vocabulary moved to src/engines.ts (the shared plumbing tier) once
// `saki init-env` became a second consumer: commands never import each other, so a command cannot be
// the home of something two commands need. Re-exported here so existing importers are untouched.
import type { RunEngine } from '../engines.js'
export { RUN_ENGINES, isRunEngine, assertRunEngine, type RunEngine } from '../engines.js'

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

export interface ContinueFlags {
  option?: string
}

// Turn the user's argument into the exact target the skill should receive.
//
// Each verb points at a different artifact, and getting this wrong reviews or builds the wrong
// thing rather than failing loudly:
//   build         -> the raw roadmap id/path (the backend owns workflow lane resolution)
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

  // Build is the hands-off workflow entry point. The backend resolves roadmap ids and absent PRDs;
  // the CLI deliberately sends the user's target unchanged and never performs a prerequisite read.
  if (verb === 'build') return cmdWorkflowStart(ctx, arg, flags)

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
  const res = await ctx.client.post<RunStartResponse>('/api/run', body)
  const runId = res?.runId
  if (!runId) fail('the studio accepted the run but returned no runId', EXIT.ERROR)

  const deduped = res.deduped === true
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

export async function cmdWorkflowStart(ctx: Ctx, target: string, flags: RunStartFlags): Promise<ExitCode> {
  if (!target.trim()) fail('build needs an argument', EXIT.USAGE, 'usage: saki build <roadmap-id|path>')
  const normalized = target.trim()
  validateWorkflowTarget(ctx.cwd, normalized)
  const body: Record<string, unknown> = { cwd: ctx.cwd, target: normalized }
  if (flags.profile) body.configDir = flags.profile
  if (flags.engine) body.engine = flags.engine
  const result = await ctx.client.post<WorkflowStartResult>('/api/workflow', body)
  if (!result?.workflowId) fail('the backend accepted the workflow but returned no workflowId', EXIT.ERROR)
  emit(
    result,
    {
      json: ctx.json,
      human: result.deduped
        ? `${result.workflowId} already running for this lane — reusing it`
        : `started workflow ${result.workflowId} (${result.phase})`,
    },
    ctx.write,
  )
  if (!flags.follow) return workflowExitCode(result.status)
  return cmdWorkflowTail(ctx, result.workflowId)
}

function validateWorkflowTarget(cwd: string, target: string): void {
  if (/^[EFIB]\d+$/i.test(target)) return
  if (!/\.md$/i.test(target) || target.includes('\0')) {
    fail(`invalid build target: ${target}`, EXIT.USAGE, 'use a roadmap id such as F7 or a .md path inside the repo')
  }
  const root = resolvePath(cwd)
  const absolute = resolvePath(root, target)
  const rel = relative(root, absolute)
  if (rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel)) {
    fail(`target "${target}" resolves outside the repo (${root}) — refusing to start a workflow`, EXIT.USAGE)
  }
}

function workflowExitCode(status: WorkflowStartResult['status']): ExitCode {
  return status === 'running' || status === 'done' ? EXIT.OK : EXIT.ERROR
}

export async function cmdWorkflowTail(ctx: Ctx, workflowId: string): Promise<ExitCode> {
  if (!workflowId) fail('workflow follow needs a workflow id', EXIT.USAGE)
  const end = await streamWorkflow(ctx.client, workflowId, (line) => ctx.write(line))
  const ok = end.status === 'done'
  if (ctx.json) {
    emit({ workflowId, ...end }, { json: true }, ctx.write)
  } else {
    const reason = end.reason ? ` — ${end.reason}` : ''
    ctx.write(`workflow ${end.status}${reason}`)
  }
  return ok ? EXIT.OK : EXIT.ERROR
}

export async function cmdRunContinue(ctx: Ctx, workflowId: string, flags: ContinueFlags): Promise<ExitCode> {
  if (!workflowId) fail('run continue needs a workflow id', EXIT.USAGE, 'usage: saki run continue <workflowId> [--option <value>]')
  const result = await ctx.client.post<WorkflowStartResult>(`/api/workflow/${encodeURIComponent(workflowId)}/continue`, {
    option: flags.option ?? '',
  })
  if (!result?.workflowId) fail('the backend returned no workflowId', EXIT.ERROR)
  emit(
    result,
    { json: ctx.json, human: `workflow ${result.workflowId} ${result.status} at ${result.phase}` },
    ctx.write,
  )
  return workflowExitCode(result.status)
}
