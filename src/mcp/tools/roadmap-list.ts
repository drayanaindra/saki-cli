import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRoadmapList } from '../../commands/roadmap.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerRoadmapListTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_roadmap_list',
    {
      title: 'saki roadmap list',
      description: 'work items in this repo',
      inputSchema: {},
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRoadmapList(ctx), captured)
    },
  )
}
