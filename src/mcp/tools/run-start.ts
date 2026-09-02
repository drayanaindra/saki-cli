import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunStart, assertRunVerb, assertRunEngine, type RunStartFlags } from '../../commands/run.js'
import { looksLikePath } from '../../commands/prd.js'
import { EXIT, fail } from '../../exit.js'
import { pathEscapesCwd } from '../path-guard.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, DESTRUCTIVE_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

// `saki_run_start` is the first STATE-CHANGING tool this MCP surface exposes — it spawns a new,
// unsandboxed coding-agent process (CLAUDE.md rule 3) that can edit, delete, commit, and (for `wrap`)
// push. `destructiveHint:true` reflects that downstream capability, not just this tool's own direct
// effect (reviewer finding, slice 3: labeling it non-destructive under-informs a client's auto-approval
// policy, especially next to saki_run_stop, which merely kills a process). `--follow` is deliberately
// NOT exposed: the intended MCP flow is always two calls (this tool, then saki_run_tail), never one call
// blocking for an entire build's duration.

export function registerRunStartTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_start',
    {
      title: 'saki run start',
      description:
        'start a headless saki-builder step or build workflow; returns immediately with a runId or workflowId — use the CLI workflow follow/continue contract for hands-off builds',
      inputSchema: {
        // `verb`/`engine` are z.string(), not z.enum(RUN_VERBS)/z.enum(RUN_ENGINES): an SDK enum
        // rejects a bad value at the PROTOCOL layer (a non-isError shape), which would fail criterion
        // 3.2's "same validation error the CLI emits, as isError:true" — assertRunVerb/assertRunEngine
        // (called inside the wrapped closure below) produce that instead, at the cost of the schema not
        // advertising the valid values itself (they're listed in this description).
        verb: z.string(),
        target: z.string().optional(),
        profile: z.string().optional(),
        engine: z.string().optional(),
        heal: z.boolean().optional(),
      },
      annotations: DESTRUCTIVE_ANNOTATIONS,
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
        // Applies to EVERY target-taking verb, not just `build`: a reviewer pass (slice 3) found the
        // original build-only gate inconsistent with its own rationale — a `.md`-shaped escaping target
        // reaches the SAME spawned-process prompt for pickup/proto/rplan/qa/reviewer/etc as it does for
        // build, just without the extra fetchPrd validation step build alone has.
        if (looksLikePath(target) && pathEscapesCwd(target, base.cwd)) {
          fail(
            `target "${target}" resolves outside the repo (${base.cwd}) — refusing to start a run against it`,
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
