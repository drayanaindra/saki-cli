import { isAbsolute, relative, resolve as resolvePath, sep } from 'node:path'
import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunStart, assertRunVerb, assertRunEngine, type RunStartFlags } from '../../commands/run.js'
import { looksLikePath } from '../../commands/prd.js'
import { EXIT, fail } from '../../exit.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// `saki_run_start` is the first STATE-CHANGING tool this MCP surface exposes — it spawns a new,
// unsandboxed coding-agent process (CLAUDE.md rule 3). `--follow` is deliberately NOT exposed: the
// intended MCP flow is always two calls (this tool, then saki_run_tail), never one call blocking for an
// entire build's duration.
const RUN_START_ANNOTATIONS = {
  readOnlyHint: false,
  destructiveHint: false,
  idempotentHint: false,
  openWorldHint: false,
} as const

// Same shape as src/mcp/tools/prd-show.ts's containment check — reused here because `build`'s target,
// when .md-shaped, resolves through resolveTargetPrdPath (run.ts -> prd.ts) with NO cwd-containment of
// its own, and the result both fetches a PRD AND becomes part of the prompt sent to a NEW spawned
// process. An MCP tool's `target` argument can be steered by the calling agent from content already in
// its context, unlike a human-typed CLI argument — so this runs BEFORE cmdRunStart ever does.
function pathEscapesCwd(target: string, cwd: string): boolean {
  const abs = isAbsolute(target) ? target : resolvePath(cwd, target)
  const rel = relative(cwd, abs)
  return rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel)
}

export function registerRunStartTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_start',
    {
      title: 'saki run start',
      description:
        'start a headless saki-builder skill run (build/pickup/proto/rplan/...); returns immediately with a runId — call saki_run_tail separately to block for its result',
      inputSchema: {
        verb: z.string(),
        target: z.string().optional(),
        profile: z.string().optional(),
        engine: z.string().optional(),
        heal: z.boolean().optional(),
      },
      annotations: RUN_START_ANNOTATIONS,
    },
    async (args: { verb: string; target?: string; profile?: string; engine?: string; heal?: boolean }) => {
      const base = makeToolCtx()
      const { ctx, captured } = buildToolCtx(base)
      // Trimmed exactly once, then reused for both the guard and cmdRunStart — the same discipline
      // slice 2's reviewer caught a regression on (a guard checking an untrimmed string can diverge
      // from what the wrapped command actually resolves once IT trims).
      const target = (args.target ?? '').trim()
      return exitCodeToToolResult(async () => {
        const verb = assertRunVerb(args.verb)
        if (verb === 'build' && looksLikePath(target) && pathEscapesCwd(target, base.cwd)) {
          fail(
            `target "${target}" resolves outside the repo (${base.cwd}) — refusing to start a build against it`,
            EXIT.USAGE,
          )
        }
        const flags: RunStartFlags = { profile: args.profile, heal: args.heal }
        if (args.engine !== undefined) flags.engine = assertRunEngine(args.engine)
        return cmdRunStart(ctx, verb, target, flags)
      }, captured)
    },
  )
}
