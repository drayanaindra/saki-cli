import { isAbsolute, relative, resolve as resolvePath, sep } from 'node:path'
import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdPrdShow, looksLikePath } from '../../commands/prd.js'
import { EXIT, fail } from '../../exit.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

// A .md-path `target` that resolves outside cwd. Mirrors backend/domain/lock.go:108-115's containment
// check for the identical shape. Unlike the CLI (a human deliberately types the path), an MCP tool's
// `target` argument can be steered by the calling agent from content already in its context — so this
// check runs at the MCP boundary, BEFORE cmdPrdShow/resolveTargetPrdPath (which stay unchanged) ever run.
//
// `path.relative` (not a `startsWith(cwd + sep)` prefix test) so a resolved path that merely SHARES
// cwd's prefix as a substring — or a directory literally named ".." inside cwd — can't produce a false
// verdict either way; relative() itself does the path-boundary-aware comparison.
function pathEscapesCwd(target: string, cwd: string): boolean {
  const abs = isAbsolute(target) ? target : resolvePath(cwd, target)
  const rel = relative(cwd, abs)
  return rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel)
}

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
