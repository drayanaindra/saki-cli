import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRuns } from '../../commands/runs.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerRunsTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_runs',
    {
      title: 'saki runs',
      description: 'runs the studio still holds',
      inputSchema: {},
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRuns(ctx), captured)
    },
  )
}
