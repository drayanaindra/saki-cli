import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdPrdLock, looksLikePath } from '../../commands/prd.js'
import { EXIT, fail } from '../../exit.js'
import { pathEscapesCwd } from '../path-guard.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// Idempotent — locking an already-locked PRD reports alreadyLocked, never errors (prd.ts:61).
const PRD_LOCK_ANNOTATIONS = {
  readOnlyHint: false,
  destructiveHint: false,
  idempotentHint: true,
  openWorldHint: false,
} as const

export function registerPrdLockTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_prd_lock',
    {
      title: 'saki prd lock',
      description:
        "freeze a PRD's requirements by roadmap id or a .md path — an escaping path (../, absolute) is refused, same as saki_prd_show",
      inputSchema: { target: z.string() },
      annotations: PRD_LOCK_ANNOTATIONS,
    },
    async (args: { target: string }) => {
      const base = makeToolCtx()
      const { ctx, captured } = buildToolCtx(base)
      // Same trim-once-reuse-everywhere discipline as prd-show.ts / run-start.ts.
      const target = args.target.trim()
      if (looksLikePath(target) && pathEscapesCwd(target, base.cwd)) {
        return exitCodeToToolResult(
          async () => fail(`target "${target}" resolves outside the repo (${base.cwd}) — refusing to lock it`, EXIT.USAGE),
          captured,
        )
      }
      return exitCodeToToolResult(() => cmdPrdLock(ctx, target), captured)
    },
  )
}
