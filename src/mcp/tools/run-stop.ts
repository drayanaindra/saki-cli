import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunStop } from '../../commands/runs.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, DESTRUCTIVE_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

// Destructive — stops a running process. Not idempotent: a second stop on an already-finished run gets
// a distinct NOT_FOUND-shaped failure (cmdRunStop, runs.ts), not the same success.

export function registerRunStopTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_stop',
    {
      title: 'saki run stop',
      description: 'stop a running run',
      inputSchema: { runId: z.string() },
      annotations: DESTRUCTIVE_ANNOTATIONS,
    },
    async (args: { runId: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRunStop(ctx, args.runId), captured)
    },
  )
}
