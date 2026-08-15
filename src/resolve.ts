import { isAbsolute, resolve as resolvePath } from 'node:path'
import { EXIT, CliError } from './exit.js'
import type { RoadmapItem } from './types.js'

// Pure resolvers turning CLI arguments into the absolute paths and ids the studio API expects.

// Every studio route is repo-scoped by a `cwd` query/body field, so the CLI resolves one up front
// and threads it through. Default: wherever the agent is standing.
export function resolveCwd(flag: string | undefined, base: string = process.cwd()): string {
  if (!flag) return base
  // Normalize through resolvePath even when already absolute: `GET /api/runs` compares cwd EXACTLY
  // (index.ts:1280 `r.cwd === cwd`), so a trailing slash — `--cwd /repo/` — silently returned an
  // empty run list. resolvePath strips it and collapses `.`/`..`.
  return resolvePath(isAbsolute(flag) ? flag : resolvePath(base, flag))
}

// Look an item up by roadmap id. Case-insensitive so `e12` works as well as `E12` — an agent
// composing ids from prose shouldn't fail on capitalisation.
export function findItem(items: RoadmapItem[], id: string): RoadmapItem {
  const want = id.trim().toLowerCase()
  const hit = items.find((i) => i.id.toLowerCase() === want)
  if (!hit) {
    throw new CliError(
      `no roadmap item with id ${id}`,
      EXIT.NOT_FOUND,
      'run `saki roadmap list` to see the ids in this repo',
    )
  }
  return hit
}

// Absolute path of an item's Child PRD.
//
// `**Child PRD:**` is captured as raw field text (apps/server/src/roadmap.ts:63), and the sibling
// `childPlan` is documented as a "tasks/-relative bare filename" (roadmap.ts:36) — so the field may
// arrive bare (`prd-x.md`) or already prefixed (`tasks/prd-x.md`). Rather than assume one shape,
// normalise both (and pass an absolute value straight through).
export function resolvePrdPath(item: RoadmapItem, cwd: string): string {
  const raw = item.childPrd?.trim()
  if (!raw) {
    throw new CliError(
      `${item.id} has no Child PRD yet`,
      EXIT.NOT_FOUND,
      `run \`saki run pickup ${item.id}\` to write one`,
    )
  }
  if (isAbsolute(raw)) return raw
  return resolvePath(cwd, raw.startsWith('tasks/') ? raw : `tasks/${raw}`)
}

// Absolute path of an item's Child PLAN — the Plan-track counterpart of resolvePrdPath.
//
// `**Child plan:**` is a SEPARATE roadmap field from `**Child PRD:**` (roadmap.ts:36,63-67): an
// Improvement/Bug carries a plan, an Epic/Feature carries a PRD, and an item can carry both.
// Reviewing a plan must therefore read childPlan — using childPrd would review the wrong artifact,
// or claim "no PRD" for an item whose plan exists.
export function resolvePlanPath(item: RoadmapItem, cwd: string): string {
  const raw = item.childPlan?.trim()
  if (!raw) {
    throw new CliError(
      `${item.id} has no plan yet`,
      EXIT.NOT_FOUND,
      `run \`saki rplan ${item.id}\` to write one`,
    )
  }
  if (isAbsolute(raw)) return raw
  return resolvePath(cwd, raw.startsWith('tasks/') ? raw : `tasks/${raw}`)
}

// Encode a repo path for `/api/proto/:dir/*` (apps/server/src/index.ts:938), which decodes with
// `Buffer.from(dir.replace(/-/g,'+').replace(/_/g,'/'), 'base64')`. Node tolerates absent padding
// on decode, so stripping `=` keeps the URL clean and still round-trips.
export function encodeProtoDir(cwd: string): string {
  return Buffer.from(cwd, 'utf8')
    .toString('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

// The full URL of a proto gallery asset. Path-based (not a query string) because the gallery's own
// relative <img src> tags must resolve to sibling requests on this same route (index.ts:934-937).
export function protoUrl(baseUrl: string, cwd: string, rel: string): string {
  return `${baseUrl}/api/proto/${encodeProtoDir(cwd)}/${rel.replace(/^\/+/, '')}`
}
