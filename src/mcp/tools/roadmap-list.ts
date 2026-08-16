import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRoadmapList } from '../../commands/roadmap.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx } from '../tool-ctx.js'
import type { Ctx } from '../../ctx.js'

export function registerRoadmapListTool(server: McpServer, makeToolCtx: () => Pick<Ctx, 'client' | 'cwd'>): void {
  server.registerTool(
    'saki_roadmap_list',
    {
      title: 'saki roadmap list',
      description: 'work items in this repo',
      inputSchema: {},
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdRoadmapList(ctx), captured)
    },
  )
}
