import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { WorkItemsResult } from '../types.js'

// Repo-level commands. Every git action goes through the studio's routes rather than shelling out
// locally, so the CLI and the web UI can never disagree about what happened.

// `saki workitems` — PRDs + plans that are not-started or in-progress (index.ts:905).
export async function cmdWorkitems(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.get<WorkItemsResult>('/api/workitems', { cwd: ctx.cwd })
  const prds = res?.prds ?? []
  const plans = res?.plans ?? []
  const human =
    prds.length + plans.length === 0
      ? `no open work items in ${ctx.cwd}`
      : [
          `PRDs  (${prds.length})`,
          ...prds.map((p) => `  ${JSON.stringify(p)}`),
          `plans (${plans.length})`,
          ...plans.map((p) => `  ${JSON.stringify(p)}`),
        ].join('\n')
  emit(res, { json: ctx.json, human }, ctx.write)
  return EXIT.OK
}

// `saki branch` — current branch (index.ts:786).
export async function cmdBranch(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.get<{ branch: string | null }>('/api/branch', { cwd: ctx.cwd })
  const branch = res?.branch ?? null
  emit(
    { branch },
    { json: ctx.json, human: branch ?? 'detached HEAD (no current branch)' },
    ctx.write,
  )
  return EXIT.OK
}

// `saki branch list` — local branches (index.ts:798).
export async function cmdBranchList(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.get<{ branches: string[] }>('/api/branches', { cwd: ctx.cwd })
  const branches = res?.branches ?? []
  emit(
    { branches },
    { json: ctx.json, human: branches.length ? branches.join('\n') : 'no local branches' },
    ctx.write,
  )
  return EXIT.OK
}

export interface BranchSwitchFlags {
  create?: boolean
}

// `saki branch switch <name> [--create]` (index.ts:811).
//
// Uses postOk: a git failure comes back as HTTP 200 with ok:false, so status alone would report a
// refused switch as success.
export async function cmdBranchSwitch(
  ctx: Ctx,
  branch: string,
  flags: BranchSwitchFlags,
): Promise<ExitCode> {
  if (!branch.trim()) {
    fail('branch switch needs a branch name', EXIT.USAGE, 'usage: saki branch switch <name> [--create]')
  }
  const res = await ctx.client.postOk<{ ok?: boolean; branch?: string }>('/api/switch-branch', {
    cwd: ctx.cwd,
    branch: branch.trim(),
    create: flags.create === true,
  })
  emit(
    { branch: res.branch ?? branch, created: flags.create === true },
    { json: ctx.json, human: `switched to ${res.branch ?? branch}` },
    ctx.write,
  )
  return EXIT.OK
}

// `saki mr create` — push the current branch and open an MR via glab (index.ts:831). Same
// HTTP-200-failure shape as switch-branch, so same postOk treatment.
export async function cmdMrCreate(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.postOk<{ ok?: boolean; url?: string }>('/api/create-mr', {
    cwd: ctx.cwd,
  })
  emit(
    { url: res.url ?? null },
    { json: ctx.json, human: res.url ?? 'merge request created (the studio returned no url)' },
    ctx.write,
  )
  return EXIT.OK
}

// `saki screenshots` — the repo's /qa screenshots (index.ts:917), each with the URL that serves it.
//
// NOTE the route difference: a screenshot is served by `/api/screenshot?cwd=&rel=` (index.ts:925),
// NOT by the path-encoded `/api/proto/:dir/*` route. Only the proto gallery needs path encoding
// (so its relative <img src> tags resolve to siblings); a screenshot is addressed by query.
export async function cmdScreenshots(ctx: Ctx): Promise<ExitCode> {
  const res = await ctx.client.get<{ shots?: string[] }>('/api/screenshots', { cwd: ctx.cwd })
  const shots = res?.shots ?? []
  const urlFor = (rel: string): string => ctx.client.url('/api/screenshot', { cwd: ctx.cwd, rel })
  emit(
    { shots, urls: shots.map(urlFor) },
    {
      json: ctx.json,
      human: shots.length
        ? shots.map((rel) => `${rel}\n  ${urlFor(rel)}`).join('\n')
        : `no /qa screenshots in ${ctx.cwd}`,
    },
    ctx.write,
  )
  return EXIT.OK
}
