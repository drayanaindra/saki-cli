import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdDoctor } from '../../commands/doctor.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx } from '../tool-ctx.js'
import type { Ctx } from '../../ctx.js'

export function registerDoctorTool(server: McpServer, makeToolCtx: () => Pick<Ctx, 'client' | 'cwd'>): void {
  server.registerTool(
    'saki_doctor',
    {
      title: 'saki doctor',
      description: 'can codex/opencode actually run a saki-builder command, before you dispatch a run',
      inputSchema: { profile: z.string().optional() },
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    },
    async (args: { profile?: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      const flags: Record<string, string | boolean> = {}
      if (args.profile) flags.profile = args.profile
      return exitCodeToToolResult(() => cmdDoctor(ctx, [], flags), captured)
    },
  )
}
