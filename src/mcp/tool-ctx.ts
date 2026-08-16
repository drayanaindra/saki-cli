import { makeCtx, type Ctx } from '../ctx.js'
import type { CapturedIO } from './result.js'

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
