import { makeCtx, type Ctx } from '../ctx.js'
import type { CapturedIO } from './result.js'

// Shared by every register*Tool(server, makeToolCtx) function — one name instead of repeating the
// Pick<Ctx, 'client' | 'cwd'> => ... signature per tool file.
export type ToolCtxFactory = () => Pick<Ctx, 'client' | 'cwd'>

// Every tool registered so far is read-only (slices 1-2) — the identical annotations literal was
// copied into 5 files; naming it once means a 6th tool reuses it instead of adding a 6th copy.
export const READ_ONLY_ANNOTATIONS = {
  readOnlyHint: true,
  destructiveHint: false,
  idempotentHint: true,
  openWorldHint: false,
} as const

// State-changing AND not repeatable-with-the-same-effect: starting a run, stopping a run, and switching
// branches (run-start.ts, run-stop.ts, branch-switch.ts) share this exact shape — extracted at the third
// copy (reviewer finding, slice 4), same reasoning as READ_ONLY_ANNOTATIONS above. Each tool keeps its
// own comment explaining WHY it's destructive — this only names the shared shape.
export const DESTRUCTIVE_ANNOTATIONS = {
  readOnlyHint: false,
  destructiveHint: true,
  idempotentHint: false,
  openWorldHint: false,
} as const

// Builds a FRESH {ctx, captured} pair for one tool invocation. Call this INSIDE every tool handler body,
// never hoisted to module or registration scope — a hoisted pair would leak one call's output into the
// next tool call's response (the exact regression slice 1's review caught once already).
export function buildToolCtx(base: Pick<Ctx, 'client' | 'cwd'>): { ctx: Ctx; captured: CapturedIO } {
  const captured: CapturedIO = { out: [] }
  const ctx = makeCtx({
    client: base.client,
    cwd: base.cwd,
    json: true,
    write: (s) => captured.out.push(s),
    // Real stderr, not captured — outside the MCP protocol channel (stdout), safe for local debugging.
    writeErr: (s) => process.stderr.write(`${s}\n`),
  })
  return { ctx, captured }
}
