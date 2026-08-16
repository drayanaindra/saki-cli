import { describe, it, expect, vi } from 'vitest'

// Isolated in its own file: vi.mock is hoisted file-wide, and mocking the transport here must not
// affect mcp.test.ts's real-SDK integration + real-process tests.
vi.mock('@modelcontextprotocol/sdk/server/stdio.js', () => ({
  StdioServerTransport: class {
    constructor() {
      throw new Error('handshake setup failed')
    }
  },
}))

describe('cmdMcp — transport init failure', () => {
  it('exits non-zero with a message, not a stack trace — no hang', async () => {
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

    const code = await cmdMcp(ctx)

    expect(code).toBe(EXIT.ERROR)
    expect(errOut.some((l) => l.includes('handshake setup failed'))).toBe(true)
    expect(errOut.some((l) => l.includes(' at '))).toBe(false)
  })
})
