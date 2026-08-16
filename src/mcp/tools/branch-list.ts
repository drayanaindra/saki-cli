import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdBranchList } from '../../commands/repo.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerBranchListTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_branch_list',
    {
      title: 'saki branch list',
      description: 'local branches',
      inputSchema: {},
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdBranchList(ctx), captured)
    },
  )
}
