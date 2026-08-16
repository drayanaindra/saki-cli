import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js'
import { EXIT, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import { createSakiMcpServer } from '../mcp/server.js'

// `saki mcp` — starts a stdio MCP server exposing saki's journey commands as typed tools. Long-lived: it
// blocks until the client closes stdin, unlike every other command's bounded request/response shape.
export async function cmdMcp(ctx: Ctx): Promise<ExitCode> {
  const server = createSakiMcpServer(ctx)
  try {
    await server.connect(new StdioServerTransport())
  } catch (err) {
    ctx.writeErr(`error: failed to start the MCP transport: ${err instanceof Error ? err.message : String(err)}`)
    return EXIT.ERROR
  }
  // The SDK registers no 'end' listener on stdin itself, so nothing closes the process when the client
  // disconnects unless we listen ourselves.
  await new Promise<void>((resolve) => {
    process.stdin.on('end', () => resolve())
  })
  return EXIT.OK
}
