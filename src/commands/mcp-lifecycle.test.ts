import { describe, it, expect, vi } from 'vitest'

// Isolated in its own file — vi.mock is hoisted file-wide, and stubbing the transport here must not
// affect mcp.test.ts's real-SDK tests or mcp-transport-init-failure.test.ts's throwing stub.
vi.mock('@modelcontextprotocol/sdk/server/stdio.js', () => ({
  StdioServerTransport: class {
    async start() {}
    async close() {}
    async send() {}
  },
}))

describe('cmdMcp — lifecycle', () => {
  it('resolves EXIT.OK once stdin emits "end" (the explicit close listener)', async () => {
    const { cmdMcp } = await import('./mcp.js')
    const { StudioClient } = await import('../client.js')
    const { makeCtx } = await import('../ctx.js')
    const { EXIT } = await import('../exit.js')

    const ctx = makeCtx({ client: new StudioClient({ baseUrl: 'http://s.test' }), cwd: '/repo' })

    const runPromise = cmdMcp(ctx)
    // give cmdMcp a tick to reach the connect() call and register the stdin listener
    await new Promise((resolve) => setTimeout(resolve, 10))
    process.stdin.emit('end')

    await expect(runPromise).resolves.toBe(EXIT.OK)
  })
})
