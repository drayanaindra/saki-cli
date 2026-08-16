import { describe, it, expect } from 'vitest'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { createSakiMcpServer } from './server.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'

function testCtx() {
  return makeCtx({ client: new StudioClient({ baseUrl: 'http://s.test' }), cwd: '/repo' })
}

describe('createSakiMcpServer', () => {
  it('returns an McpServer instance, unconnected', () => {
    const server = createSakiMcpServer(testCtx())
    expect(server).toBeInstanceOf(McpServer)
    expect(server.isConnected()).toBe(false)
  })

  it('two calls return two independent instances (no shared module-level state)', () => {
    const a = createSakiMcpServer(testCtx())
    const b = createSakiMcpServer(testCtx())
    expect(a).not.toBe(b)
  })
})
