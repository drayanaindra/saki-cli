import { isAbsolute, resolve as resolvePath } from 'node:path'
import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import { findItem, resolvePrdPath } from '../resolve.js'
import type { Ctx } from '../ctx.js'
import type { PrdResult, RoadmapResult } from '../types.js'

// A PRD can be addressed two ways: by roadmap id (`E12` — resolved through the item's Child PRD)
// or by an explicit .md path. Anything ending in .md is treated as a path.
function looksLikePath(target: string): boolean {
  return /\.md$/i.test(target.trim())
}

// Resolve `target` to an absolute PRD path, going through the roadmap when it is an id.
export async function resolveTargetPrdPath(ctx: Ctx, target: string): Promise<string> {
  const t = target.trim()
  if (!t) fail('a roadmap id or PRD path is required', EXIT.USAGE)

  if (looksLikePath(t)) return isAbsolute(t) ? t : resolvePath(ctx.cwd, t)

  const roadmap = await ctx.client.get<RoadmapResult>('/api/roadmap', { cwd: ctx.cwd })
  if (!roadmap?.found) {
    fail(`no tasks/roadmap.md found in ${ctx.cwd}`, EXIT.NOT_FOUND, 'or pass the PRD path directly')
  }
  return resolvePrdPath(findItem(roadmap.epics ?? [], t), ctx.cwd)
}

// Fetch a PRD by resolved path (index.ts:738).
export async function fetchPrd(ctx: Ctx, path: string): Promise<PrdResult & { path: string }> {
  const prd = await ctx.client.get<PrdResult>('/api/prd', { cwd: ctx.cwd, path })
  if (!prd?.found) {
    // The hint must fit how the path was ADDRESSED. A relative path resolves against --cwd, so the
    // usual cause of this error is running from a subdirectory — saying "the roadmap points at it"
    // there sends the operator to inspect a roadmap field they never used.
    fail(
      `no PRD file at ${path}`,
      EXIT.NOT_FOUND,
      `it is not on disk — a relative path resolves against ${ctx.cwd}, so pass --cwd <repo-root> if you ran this from a subdirectory, or check the item's Child PRD field`,
    )
  }
  return { ...prd, path: prd.path ?? path }
}

// `saki prd show <id|path>`
export async function cmdPrdShow(ctx: Ctx, target: string): Promise<ExitCode> {
  const prd = await fetchPrd(ctx, await resolveTargetPrdPath(ctx, target))
  emit(prd, { json: ctx.json, human: prd.content ?? `(empty PRD at ${prd.path})` }, ctx.write)
  return EXIT.OK
}

// `saki prd lock <id|path>` — freeze requirements (index.ts:1050). Idempotent by design: the
// server answers alreadyLocked rather than erroring, and so do we.
export async function cmdPrdLock(ctx: Ctx, target: string): Promise<ExitCode> {
  const path = (await fetchPrd(ctx, await resolveTargetPrdPath(ctx, target))).path
  const res = await ctx.client.postOk<{ ok?: boolean; locked?: boolean; alreadyLocked?: boolean; path?: string }>(
    '/api/lock-prd',
    { path, cwd: ctx.cwd },
  )
  const already = res.alreadyLocked === true
  emit(
    { path, locked: true, alreadyLocked: already },
    { json: ctx.json, human: `${already ? 'already locked' : 'locked'}  ${path}` },
    ctx.write,
  )
  return EXIT.OK
}
