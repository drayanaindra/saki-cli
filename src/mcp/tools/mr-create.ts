import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdMrCreate } from '../../commands/repo.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// The only tool in this MCP surface with openWorldHint:true — every other tool talks only to the local,
// loopback-only backend (closed world); this one pushes the branch and opens a REAL merge request on a
// remote host via glab, an externally-visible side effect the rest of the annotation set doesn't have.
const MR_CREATE_ANNOTATIONS = {
  readOnlyHint: false,
  destructiveHint: false,
  idempotentHint: false,
  openWorldHint: true,
} as const

export function registerMrCreateTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_mr_create',
    {
      title: 'saki mr create',
      description: 'push the current branch and open a merge request via glab',
      inputSchema: {},
      annotations: MR_CREATE_ANNOTATIONS,
    },
    async (_args: Record<string, never>) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdMrCreate(ctx), captured)
    },
  )
}
