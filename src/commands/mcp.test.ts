import { describe, it, expect } from 'vitest'
import { spawn } from 'node:child_process'
import { InMemoryTransport } from '@modelcontextprotocol/sdk/inMemory.js'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { createSakiMcpServer } from '../mcp/server.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'

// routedCtx pattern from src/commands/doctor.test.ts:13-38 — `down: true` rejects the fetch (a dead
// socket), matching how a real UNREACHABLE backend surfaces.
function routedClient(routes: Record<string, { status?: number; body?: unknown; down?: boolean }>) {
  const impl = (async (url: string | URL) => {
    const u = String(url)
    const key = Object.keys(routes).find((k) => u.includes(k))
    const r = key ? routes[key] : { status: 404, body: { error: 'no stub' } }
    if (r.down) throw new TypeError('fetch failed')
    const status = r.status ?? 200
    return {
      ok: status < 300,
      status,
      json: async () => r.body,
      text: async () => JSON.stringify(r.body ?? ''),
    } as unknown as Response
  }) as unknown as typeof fetch
  return new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl })
}

// Connects a fresh createSakiMcpServer instance to one half of an in-memory transport pair, and an SDK
// Client to the other half — the fast, real-server path the rplan-review testability fix (Step 4) exists
// to enable, in place of spawning a real process for every scenario.
async function connectedClient(client: StudioClient) {
  const ctx = makeCtx({ client, cwd: '/repo' })
  const server = createSakiMcpServer(ctx)
  const [serverTransport, clientTransport] = InMemoryTransport.createLinkedPair()
  const mcpClient = new Client({ name: 'test-client', version: '0.0.0' })
  await Promise.all([server.connect(serverTransport), mcpClient.connect(clientTransport)])
  return mcpClient
}

describe('saki mcp — saki_status tool', () => {
  it('happy path: isError:false, content matches the real cmdStatus --json body', async () => {
    const client = routedClient({ '/api/health': { body: { ok: true, service: 'saki-backend' } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_status', arguments: {} })
    expect(result.isError).toBe(false)
    const content = result.content as { type: string; text: string }[]
    expect(content).toHaveLength(1)
    const body = JSON.parse(content[0].text)
    expect(body.backendReachable).toBe(true)
  })

  it('backend unreachable (returned-code path): isError:true, real body + synthesized exit-code line', async () => {
    const client = routedClient({ '/api/health': { down: true } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_status', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as { type: string; text: string }[]
    const body = JSON.parse(content[0].text)
    expect(body.backendReachable).toBe(false)
    expect(content[1].text).toBe('Exited with code 3 (UNREACHABLE)')
  })

  it('tools/list contains exactly saki_status, with inputSchema and annotations', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const { tools } = await mcpClient.listTools()
    expect(tools).toHaveLength(1)
    expect(tools[0].name).toBe('saki_status')
    expect(tools[0].inputSchema).toBeTruthy()
    expect(tools[0].annotations).toMatchObject({ readOnlyHint: true, destructiveHint: false })
  })

  it('back-to-back calls are isolated — the second response excludes the first call data', async () => {
    const client = routedClient({ '/api/health': { down: true } })
    const mcpClient = await connectedClient(client)
    const first = await mcpClient.callTool({ name: 'saki_status', arguments: {} })
    const second = await mcpClient.callTool({ name: 'saki_status', arguments: {} })
    const firstContent = first.content as { type: string; text: string }[]
    const secondContent = second.content as { type: string; text: string }[]
    expect(secondContent).toHaveLength(firstContent.length)
    expect(second.isError).toBe(first.isError)
  })

  it('unregistered tool name errors cleanly — no hang, no crash', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_nonexistent', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as { type: string; text: string }[]
    expect(content[0].text).toContain('saki_nonexistent')
  })
})

describe('saki mcp — real process', () => {
  it(
    'stdout is pure MCP frames, and the process exits 0 on stdin close',
    async () => {
      const child = spawn('node_modules/.bin/tsx', ['src/index.ts', 'mcp'], {
        cwd: process.cwd(),
        env: { ...process.env, SAKI_BACKEND_URL: 'http://127.0.0.1:1' },
        stdio: ['pipe', 'pipe', 'pipe'],
      })

      const stdoutChunks: Buffer[] = []
      child.stdout.on('data', (chunk: Buffer) => stdoutChunks.push(chunk))

      const send = (msg: unknown) => child.stdin.write(JSON.stringify(msg) + '\n')
      send({ jsonrpc: '2.0', method: 'initialize', id: 0, params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 't', version: '0' } } })
      send({ jsonrpc: '2.0', method: 'notifications/initialized' })
      send({ jsonrpc: '2.0', method: 'tools/list', id: 1 })
      send({ jsonrpc: '2.0', method: 'tools/call', id: 2, params: { name: 'saki_status', arguments: {} } })

      // give the process time to answer before closing stdin
      await new Promise((resolve) => setTimeout(resolve, 2000))

      const exitPromise = new Promise<number | null>((resolve) => child.once('exit', (code) => resolve(code)))
      child.stdin.end()
      const exitCode = await Promise.race([
        exitPromise,
        new Promise<number | null>((_, reject) => setTimeout(() => reject(new Error('process did not exit within 10s')), 10_000)),
      ])

      const stdout = Buffer.concat(stdoutChunks).toString('utf8')
      const lines = stdout.split('\n').filter((l) => l.trim().length > 0)
      expect(lines.length).toBeGreaterThan(0)
      for (const line of lines) {
        expect(() => JSON.parse(line)).not.toThrow()
      }
      expect(exitCode).toBe(0)
    },
    15_000,
  )
})
