import { createRequire } from 'node:module'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import type { Ctx } from '../ctx.js'
import type { ToolCtxFactory } from './tool-ctx.js'
import { registerStatusTool } from './tools/status.js'
import { registerDoctorTool } from './tools/doctor.js'
import { registerRoadmapListTool } from './tools/roadmap-list.js'
import { registerRunsTool } from './tools/runs.js'
import { registerPrdShowTool } from './tools/prd-show.js'

// package.json sits outside tsconfig's rootDir ("src"), so a static import would fail tsc regardless of
// resolveJsonModule — read it at runtime instead.
const packageVersion: string = createRequire(import.meta.url)('../../package.json').version

// Builds a saki MCP server with every tool registered, but UNCONNECTED — no transport attached. The
// caller (cmdMcp) owns connecting it, which is what makes this factory reusable both for the real stdio
// process and for fast in-memory integration tests.
export function createSakiMcpServer(ctx: Ctx): McpServer {
  const server = new McpServer({ name: 'saki', version: packageVersion })
  const makeToolCtx: ToolCtxFactory = () => ({ client: ctx.client, cwd: ctx.cwd })
  registerStatusTool(server, makeToolCtx)
  registerDoctorTool(server, makeToolCtx)
  registerRoadmapListTool(server, makeToolCtx)
  registerRunsTool(server, makeToolCtx)
  registerPrdShowTool(server, makeToolCtx)
  return server
}
