import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdBranchSwitch } from '../../commands/repo.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, DESTRUCTIVE_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

// Destructive — changes working-tree state (matches saki_run_stop's reasoning, slice 3). `branch` needs
// NO new injection guard here: the server-side git-argument-injection protections (ValidateBranchName on
// the create path, the `switch -- <branch>` argv separator on the existing-branch path) predate this PRD
// entirely and were independently re-verified by a security review before this tool was written — see
// tasks/mcp-surface-saki-mcp-slice4-plan.md's Research section.

export function registerBranchSwitchTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_branch_switch',
    {
      title: 'saki branch switch',
      description: 'switch (or create-and-switch) the current git branch',
      inputSchema: { branch: z.string(), create: z.boolean().optional() },
      annotations: DESTRUCTIVE_ANNOTATIONS,
    },
    async (args: { branch: string; create?: boolean }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return exitCodeToToolResult(() => cmdBranchSwitch(ctx, args.branch, { create: args.create }), captured)
    },
  )
}
