import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunStop } from '../../commands/runs.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// Destructive — stops a running process. Not idempotent: a second stop on an already-finished run gets
// a distinct NOT_FOUND-shaped failure (cmdRunStop, runs.ts), not the same success.
const RUN_STOP_ANNOTATIONS = {
  readOnlyHint: false,
  destructiveHint: true,
  idempotentHint: false,
  openWorldHint: false,
} as const

export function registerRunStopTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_stop',
    {
      title: 'saki run stop',
      description: 'stop a running run',
      inputSchema: { runId: z.string() },
      annotations: RUN_STOP_ANNOTATIONS,
    },
    async (args: { runId: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRunStop(ctx, args.runId), captured)
    },
  )
}
