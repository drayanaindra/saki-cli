import { isAbsolute, resolve as resolvePath, sep } from 'node:path'
import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdPrdShow, looksLikePath } from '../../commands/prd.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx } from '../tool-ctx.js'
import type { Ctx } from '../../ctx.js'

// A .md-path target that resolves outside cwd. Mirrors backend/domain/lock.go:108-115's containment
// check for the identical shape. Unlike the CLI (a human deliberately types the path), an MCP tool's
// `target` argument can be steered by the calling agent from content already in its context — so this
// check runs at the MCP boundary, BEFORE cmdPrdShow/resolveTargetPrdPath (which stay unchanged) ever run.
function pathEscapesCwd(target: string, cwd: string): boolean {
  const abs = isAbsolute(target) ? target : resolvePath(cwd, target)
  return abs !== cwd && !abs.startsWith(cwd + sep)
}

export function registerPrdShowTool(server: McpServer, makeToolCtx: () => Pick<Ctx, 'client' | 'cwd'>): void {
  server.registerTool(
    'saki_prd_show',
    {
      title: 'saki prd show',
      description:
        'print a PRD by roadmap id or a .md path; a path is read relative to the repo — an escaping path (../, absolute) is refused',
      inputSchema: { target: z.string() },
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    },
    async (args: { target: string }) => {
      const base = makeToolCtx()
      if (looksLikePath(args.target) && pathEscapesCwd(args.target, base.cwd)) {
        return {
          content: [
            {
              type: 'text' as const,
              text: `target "${args.target}" resolves outside the repo (${base.cwd}) — refusing to read it`,
            },
          ],
          isError: true,
        }
      }
      const { ctx, captured } = buildToolCtx(base)
      return exitCodeToToolResult(() => cmdPrdShow(ctx, args.target), captured)
    },
  )
}
