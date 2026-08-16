import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdBranch } from '../../commands/repo.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerBranchTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_branch',
    {
      title: 'saki branch',
      description: 'current branch',
      inputSchema: {},
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdBranch(ctx), captured)
    },
  )
}
