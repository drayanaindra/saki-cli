import { describe, it, expect } from 'vitest'
import { InMemoryTransport } from '@modelcontextprotocol/sdk/inMemory.js'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { createSakiMcpServer } from '../mcp/server.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'

type Content = { type: string; text: string }[]

// routedCtx pattern from src/commands/doctor.test.ts:13-38 — `down: true` rejects the fetch (a dead
// socket). `urls` records every request so a case can assert a call was (or was NOT) made.
function routedClient(
  routes: Record<string, { status?: number; body?: unknown; down?: boolean }>,
  urls: string[] = [],
) {
  const impl = (async (url: string | URL) => {
    const u = String(url)
    urls.push(u)
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

async function connectedClient(client: StudioClient, cwd = '/repo') {
  const ctx = makeCtx({ client, cwd })
  const server = createSakiMcpServer(ctx)
  const [serverTransport, clientTransport] = InMemoryTransport.createLinkedPair()
  const mcpClient = new Client({ name: 'test-client', version: '0.0.0' })
  await Promise.all([server.connect(serverTransport), mcpClient.connect(clientTransport)])
  return mcpClient
}

const engine = (over: Partial<{ engine: string; profile: string; status: 'ok' | 'failed' | 'unknown'; reason: string; fix: string }> = {}) => ({
  engine: 'codex',
  profile: 'default',
  status: 'ok' as const,
  reason: '',
  fix: '',
  ...over,
})

const ROADMAP_ITEM = {
  id: 'F3',
  n: 3,
  type: 'Feature',
  track: 'PRD',
  title: 'MCP surface',
  status: 'In-progress',
  goal: 'g',
  childPrd: 'prd-mcp-surface-saki-mcp.md',
  childPlan: null,
  phaseChain: null,
}
const ROADMAP = { found: true, epics: [ROADMAP_ITEM] }
const PRD = { found: true, path: '/repo/tasks/prd-mcp-surface-saki-mcp.md', content: '# p' }

describe('saki mcp — slice 2 read-only tools', () => {
  it('mcp2: doctor happy', async () => {
    const client = routedClient({ '/api/doctor': { body: { engines: [engine()] } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_doctor', arguments: {} })
    expect(result.isError).toBe(false)
  })

  it('mcp2: doctor engine failure', async () => {
    const client = routedClient({
      '/api/doctor': { body: { engines: [engine({ status: 'failed', reason: 'not on PATH', fix: 'install codex' })] } },
    })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_doctor', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    const body = JSON.parse(content[0].text)
    expect(body.engines.find((e: { status: string }) => e.status !== 'ok').fix).toBe('install codex')
  })

  it('mcp2: doctor profile param', async () => {
    const urls: string[] = []
    const client = routedClient({ '/api/doctor': { body: { engines: [engine()] } } }, urls)
    const mcpClient = await connectedClient(client)
    await mcpClient.callTool({ name: 'saki_doctor', arguments: { profile: 'custom-profile' } })
    expect(urls.some((u) => u.includes('/api/doctor') && u.includes('profile=custom-profile'))).toBe(true)
  })

  it('mcp2: roadmap_list happy', async () => {
    const client = routedClient({ '/api/roadmap': { body: ROADMAP } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_roadmap_list', arguments: {} })
    expect(result.isError).toBe(false)
    const content = result.content as Content
    expect(JSON.parse(content[0].text).epics[0].id).toBe('F3')
  })

  it('mcp2: roadmap_list no roadmap file', async () => {
    const client = routedClient({ '/api/roadmap': { body: { found: false } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_roadmap_list', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 4 (NOT_FOUND)'))).toBe(true)
  })

  it('mcp2: runs happy', async () => {
    const client = routedClient({ '/api/runs': { body: [] } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_runs', arguments: {} })
    expect(result.isError).toBe(false)
    const content = result.content as Content
    expect(JSON.parse(content[0].text)).toEqual([])
  })

  it('mcp2: runs backend unreachable', async () => {
    const client = routedClient({ '/api/runs': { down: true } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_runs', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 3 (UNREACHABLE)'))).toBe(true)
  })

  it('mcp2: prd_show happy id', async () => {
    const client = routedClient({ '/api/roadmap': { body: ROADMAP }, '/api/prd': { body: PRD } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: 'F3' } })
    expect(result.isError).toBe(false)
    const content = result.content as Content
    expect(JSON.parse(content[0].text).path).toBe(PRD.path)
  })

  it('mcp2: prd_show happy path', async () => {
    const client = routedClient({ '/api/prd': { body: PRD } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: 'tasks/prd-mcp-surface-saki-mcp.md' } })
    expect(result.isError).toBe(false)
  })

  it('mcp2: prd_show path escape rejected', async () => {
    const urls: string[] = []
    const client = routedClient({ '/api/prd': { body: PRD }, '/api/roadmap': { body: ROADMAP } }, urls)
    const mcpClient = await connectedClient(client)
    const relative = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: '../outside.md' } })
    expect(relative.isError).toBe(true)
    const relativeContent = relative.content as Content
    expect(relativeContent[0].text).toContain('resolves outside the repo')
    // the refusal carries an exit code like every other failure path (exitCodeToToolResult), not a
    // bespoke isError:true with no code line
    expect(relativeContent[0].text).toContain('Exited with code 2 (USAGE)')

    const absolute = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: '/etc/some.md' } })
    expect(absolute.isError).toBe(true)

    // a leading-space payload must not slip past the guard: resolveTargetPrdPath trims before
    // resolving, so a guard checking the UNtrimmed string could resolve a different (safe-looking)
    // path than the one cmdPrdShow actually reads — reviewer-caught regression
    const spacePrefixed = await mcpClient.callTool({
      name: 'saki_prd_show',
      arguments: { target: ' ../outside.md' },
    })
    expect(spacePrefixed.isError).toBe(true)
    expect((spacePrefixed.content as Content)[0].text).toContain('resolves outside the repo')

    // cmdPrdShow must never have run — no /api/prd or /api/roadmap request for any rejected call
    expect(urls.some((u) => u.includes('/api/prd') || u.includes('/api/roadmap'))).toBe(false)
  })

  it('mcp2: prd_show empty target', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: '' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp2: prd_show unknown id', async () => {
    const client = routedClient({ '/api/roadmap': { body: ROADMAP } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: 'E999' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 4 (NOT_FOUND)'))).toBe(true)
  })

  it('mcp2: cross-tool calls are isolated', async () => {
    const client = routedClient({
      '/api/doctor': { body: { engines: [engine({ status: 'failed', fix: 'do X' })] } },
      '/api/roadmap': { body: ROADMAP },
    })
    const mcpClient = await connectedClient(client)
    const first = await mcpClient.callTool({ name: 'saki_doctor', arguments: {} })
    expect(first.isError).toBe(true)
    const second = await mcpClient.callTool({ name: 'saki_roadmap_list', arguments: {} })
    expect(second.isError).toBe(false)
    const secondContent = second.content as Content
    expect(secondContent.some((c) => c.text.includes('do X') || c.text.includes('Exited with code 1'))).toBe(false)
  })

  it('mcp2: tools/list has 5 tools', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const { tools } = await mcpClient.listTools()
    expect(tools).toHaveLength(5)
    const names = tools.map((t) => t.name).sort()
    expect(names).toEqual(['saki_doctor', 'saki_prd_show', 'saki_roadmap_list', 'saki_runs', 'saki_status'].sort())
    for (const t of tools) {
      expect(t.annotations).toMatchObject({ readOnlyHint: true, destructiveHint: false })
    }
  })
})
