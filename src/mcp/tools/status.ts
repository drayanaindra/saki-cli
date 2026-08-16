import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdStatus } from '../../commands/status.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

// Registers `saki_status` on `server`. `makeToolCtx` builds the base Ctx (client + cwd) shared across
// every tool this run; `buildToolCtx` layers a FRESH capture/write pair onto it on every invocation.
export function registerStatusTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_status',
    {
      title: 'saki status',
      description: 'are both studio servers up, and will they let me in',
      inputSchema: {},
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdStatus(ctx), captured)
    },
  )
}
