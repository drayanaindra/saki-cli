import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunTail } from '../../commands/runs.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// Read-only — this tool only streams and reports a run's state, it never mutates anything. Blocks for
// as long as the run takes (PRD §12 lean: mirror `saki run tail`'s own untimed behavior, no MCP-specific
// timeout or cancellation).
const RUN_TAIL_ANNOTATIONS = {
  readOnlyHint: true,
  destructiveHint: false,
  idempotentHint: true,
  openWorldHint: false,
} as const

export function registerRunTailTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_tail',
    {
      title: 'saki run tail',
      description:
        "stream a run to its terminal state and return the verdict — blocks for as long as the run takes, mirroring `saki run tail`'s own untimed behavior",
      inputSchema: { runId: z.string() },
      annotations: RUN_TAIL_ANNOTATIONS,
    },
    async (args: { runId: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRunTail(ctx, args.runId), captured)
    },
  )
}
