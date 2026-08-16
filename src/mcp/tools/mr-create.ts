import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdMrCreate } from '../../commands/repo.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// The only tool in this MCP surface with openWorldHint:true. Every other tool's OWN network call stays
// within the local, loopback-only backend (closed world) — this one's does not: it pushes the branch and
// opens a REAL merge request on a remote host via glab. (saki_run_start's SPAWNED agent can separately
// reach a remote model API, but that is a downstream effect of the process it starts, not a network call
// this tool itself makes — the same distinction destructiveHint already draws for that tool.)
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
