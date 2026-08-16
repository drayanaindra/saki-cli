import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdPrdShow, looksLikePath } from '../../commands/prd.js'
import { EXIT, fail } from '../../exit.js'
import { pathEscapesCwd } from '../path-guard.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerPrdShowTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_prd_show',
    {
      title: 'saki prd show',
      description:
        'print a PRD by roadmap id or a .md path; a path is read relative to the repo — an escaping path (../, absolute) is refused',
      inputSchema: { target: z.string() },
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (args: { target: string }) => {
      const base = makeToolCtx()
      const { ctx, captured } = buildToolCtx(base)
      // Trimmed exactly once, then reused for BOTH the guard and cmdPrdShow — resolveTargetPrdPath
      // (prd.ts:18) trims independently, so a guard checking the untrimmed string could pass a
      // leading-space payload (" ../x.md") that resolves differently once cmdPrdShow trims it itself.
      const target = args.target.trim()
      if (looksLikePath(target) && pathEscapesCwd(target, base.cwd)) {
        return exitCodeToToolResult(
          async () => fail(`target "${target}" resolves outside the repo (${base.cwd}) — refusing to read it`, EXIT.USAGE),
          captured,
        )
      }
      return exitCodeToToolResult(() => cmdPrdShow(ctx, target), captured)
    },
  )
}
