import { describe, it, expect, vi } from 'vitest'

// Isolated in its own file — vi.mock is hoisted file-wide, and stubbing the transport here must not
// affect mcp.test.ts's real-SDK tests or mcp-transport-init-failure.test.ts's throwing stub.
// `lastTransport` lets a test reach into the instance McpServer.connect() wired up (it overwrites the
// transport's onclose/onerror/onmessage), so a test can simulate the transport itself reporting an error.
let lastTransport: { onclose?: () => void; onerror?: (err: Error) => void } | undefined
vi.mock('@modelcontextprotocol/sdk/server/stdio.js', () => ({
  StdioServerTransport: class {
    onclose?: () => void
    onerror?: (err: Error) => void
    constructor() {
      lastTransport = this
    }
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

  it('resolves EXIT.OK and logs to stderr when the transport reports an error mid-session', async () => {
    const { cmdMcp } = await import('./mcp.js')
    const { StudioClient } = await import('../client.js')
    const { makeCtx } = await import('../ctx.js')
    const { EXIT } = await import('../exit.js')

    const errOut: string[] = []
    const ctx = makeCtx({
      client: new StudioClient({ baseUrl: 'http://s.test' }),
      cwd: '/repo',
      writeErr: (s) => errOut.push(s),
    })

    const runPromise = cmdMcp(ctx)
    await new Promise((resolve) => setTimeout(resolve, 10))
    lastTransport?.onerror?.(new Error('pipe broke'))

    await expect(runPromise).resolves.toBe(EXIT.OK)
    expect(errOut.some((l) => l.includes('pipe broke'))).toBe(true)
  })
})
