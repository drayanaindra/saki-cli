import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRuns } from '../../commands/runs.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx } from '../tool-ctx.js'
import type { Ctx } from '../../ctx.js'

export function registerRunsTool(server: McpServer, makeToolCtx: () => Pick<Ctx, 'client' | 'cwd'>): void {
  server.registerTool(
    'saki_runs',
    {
      title: 'saki runs',
      description: 'runs the studio still holds',
      inputSchema: {},
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRuns(ctx), captured)
    },
  )
}
