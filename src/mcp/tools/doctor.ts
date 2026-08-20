import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdDoctor } from '../../commands/doctor.js'
import { exitCodeToToolResult } from '../result.js'
import { buildToolCtx, READ_ONLY_ANNOTATIONS, type ToolCtxFactory } from '../tool-ctx.js'

export function registerDoctorTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_doctor',
    {
      title: 'saki doctor',
      description: 'can each engine actually run a saki-builder command, before you dispatch a run',
      inputSchema: { profile: z.string().optional() },
      annotations: READ_ONLY_ANNOTATIONS,
    },
    async (args: { profile?: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      const flags: Record<string, string | boolean> = {}
      if (args.profile) flags.profile = args.profile
      return exitCodeToToolResult(() => cmdDoctor(ctx, [], flags), captured)
    },
  )
}
