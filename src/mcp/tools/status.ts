import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdStatus } from '../../commands/status.js'
import { exitCodeToToolResult, type CapturedIO } from '../result.js'
import { makeCtx, type Ctx } from '../../ctx.js'

// Registers `saki_status` on `server`. `makeToolCtx` builds the base Ctx (client + cwd) shared across
// every tool this run; the handler layers a FRESH capture/write pair onto it on every invocation — never
// hoisted — so back-to-back tool calls never leak one call's output into another's response.
export function registerStatusTool(server: McpServer, makeToolCtx: () => Pick<Ctx, 'client' | 'cwd'>): void {
  server.registerTool(
    'saki_status',
    {
      title: 'saki status',
      description: 'are both studio servers up, and will they let me in',
      inputSchema: {},
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    },
    async (_args: Record<string, never>) => {
      const captured: CapturedIO = { out: [], err: [] }
      const base = makeToolCtx()
      const ctx = makeCtx({
        client: base.client,
        cwd: base.cwd,
        json: true,
        write: (s) => captured.out.push(s),
        writeErr: (s) => captured.err.push(s),
      })
      return exitCodeToToolResult(() => cmdStatus(ctx), captured)
    },
  )
}
