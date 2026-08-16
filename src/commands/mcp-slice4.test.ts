import { describe, it, expect } from 'vitest'
import { InMemoryTransport } from '@modelcontextprotocol/sdk/inMemory.js'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { createSakiMcpServer } from '../mcp/server.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'

type Content = { type: string; text: string }[]

// routedClient pattern from src/commands/mcp-slice3.test.ts — ordered keys (most specific first) so
// '/api/switch-branch' etc never collide with a shorter prefix, plus a `posted` capture for POST bodies.
function routedClient(
  routes: Record<string, { status?: number; body?: unknown; down?: boolean }>,
  urls: string[] = [],
  posted: { url: string; body: unknown }[] = [],
) {
  const impl = (async (url: string | URL, init?: RequestInit) => {
    const u = String(url)
    urls.push(u)
    if (init?.method === 'POST' && typeof init.body === 'string') {
      posted.push({ url: u, body: JSON.parse(init.body) })
    }
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

describe('saki mcp — slice 4 repo/git + PRD lock tools', () => {
  it('mcp4: branch happy', async () => {
    const client = routedClient({ '/api/branch': { body: { branch: 'feature/x' } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_branch', arguments: {} })
    expect(result.isError).toBe(false)
    expect(JSON.parse((result.content as Content)[0].text)).toEqual({ branch: 'feature/x' })
  })

  it('mcp4: branch detached head', async () => {
    const client = routedClient({ '/api/branch': { body: { branch: null } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_branch', arguments: {} })
    expect(result.isError).toBe(false)
    expect(JSON.parse((result.content as Content)[0].text)).toEqual({ branch: null })
  })

  it('mcp4: branch backend unreachable', async () => {
    const client = routedClient({ '/api/branch': { down: true } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_branch', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 3 (UNREACHABLE)'))).toBe(true)
  })

  it('mcp4: branch_switch dirty tree false success', async () => {
    const client = routedClient({
      '/api/switch-branch': { body: { ok: false, error: 'cannot switch: dirty working tree' } },
    })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({
      name: 'saki_branch_switch',
      arguments: { branch: 'main' },
    })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 5 (REMOTE_FAILED)'))).toBe(true)
  })

  it('mcp4: mr_create false success', async () => {
    const client = routedClient({
      '/api/create-mr': { body: { ok: false, error: 'no git remote configured' } },
    })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_mr_create', arguments: {} })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 5 (REMOTE_FAILED)'))).toBe(true)
  })

  it('mcp4: branch_list then branch_switch happy', async () => {
    const client = routedClient({
      '/api/branches': { body: { branches: ['main', 'feature/x'] } },
      '/api/switch-branch': { body: { branch: 'feature/x' } },
    })
    const mcpClient = await connectedClient(client)
    const list = await mcpClient.callTool({ name: 'saki_branch_list', arguments: {} })
    expect(list.isError).toBe(false)
    expect(JSON.parse((list.content as Content)[0].text)).toEqual({ branches: ['main', 'feature/x'] })

    const switched = await mcpClient.callTool({
      name: 'saki_branch_switch',
      arguments: { branch: 'feature/x' },
    })
    expect(switched.isError).toBe(false)
    expect(JSON.parse((switched.content as Content)[0].text)).toEqual({ branch: 'feature/x', created: false })
  })

  it('mcp4: mr_create happy', async () => {
    const client = routedClient({ '/api/create-mr': { body: { url: 'https://example.com/mr/1' } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_mr_create', arguments: {} })
    expect(result.isError).toBe(false)
    expect(JSON.parse((result.content as Content)[0].text)).toEqual({ url: 'https://example.com/mr/1' })
  })

  it('mcp4: prd_lock then prd_show reflects lock', async () => {
    // A bespoke STATEFUL stub (not the shared stateless routedClient) — proves the second call actually
    // reflects the first's effect, not two independently-passing canned bodies (QA-flagged gap, slice 4
    // plan review). PrdResult has no `locked` field, so the lock's visible effect is the PRD's own
    // content gaining a `<!-- prd-locked -->`-shaped marker, mirroring this project's real workflow.
    let locked = false
    const impl = (async (url: string | URL) => {
      const u = String(url)
      if (u.includes('/api/lock-prd')) {
        locked = true
        return { ok: true, status: 200, json: async () => ({ ok: true, alreadyLocked: false }), text: async () => '' } as unknown as Response
      }
      if (u.includes('/api/prd')) {
        const content = locked ? '# PRD\n\n<!-- prd-locked: 2026-08-16 -->' : '# PRD\n\nStatus: Draft'
        return {
          ok: true,
          status: 200,
          json: async () => ({ found: true, path: '/repo/tasks/prd-x.md', content }),
          text: async () => '',
        } as unknown as Response
      }
      return { ok: false, status: 404, json: async () => ({ error: 'no stub' }), text: async () => '' } as unknown as Response
    }) as unknown as typeof fetch
    const client = new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl })
    const mcpClient = await connectedClient(client)

    const before = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: 'tasks/prd-x.md' } })
    expect(before.isError).toBe(false)
    expect(JSON.parse((before.content as Content)[0].text).content).not.toContain('prd-locked')

    const lockResult = await mcpClient.callTool({ name: 'saki_prd_lock', arguments: { target: 'tasks/prd-x.md' } })
    expect(lockResult.isError).toBe(false)
    expect(JSON.parse((lockResult.content as Content)[0].text)).toEqual({
      path: '/repo/tasks/prd-x.md',
      locked: true,
      alreadyLocked: false,
    })

    const after = await mcpClient.callTool({ name: 'saki_prd_show', arguments: { target: 'tasks/prd-x.md' } })
    expect(after.isError).toBe(false)
    expect(JSON.parse((after.content as Content)[0].text).content).toContain('prd-locked')
  })

  it('mcp4: prd_lock target escape rejected', async () => {
    const urls: string[] = []
    const client = routedClient({ '/api/prd': { body: { found: true, path: '/repo/x.md' } } }, urls)
    const mcpClient = await connectedClient(client)
    const relative = await mcpClient.callTool({
      name: 'saki_prd_lock',
      arguments: { target: '../../../etc/passwd.md' },
    })
    expect(relative.isError).toBe(true)
    expect((relative.content as Content).some((c) => c.text.includes('resolves outside the repo'))).toBe(true)

    const absolute = await mcpClient.callTool({ name: 'saki_prd_lock', arguments: { target: '/etc/passwd.md' } })
    expect(absolute.isError).toBe(true)
    expect((absolute.content as Content).some((c) => c.text.includes('resolves outside the repo'))).toBe(true)

    // cmdPrdLock must never have run — no /api/prd, /api/roadmap, or /api/lock-prd request
    expect(urls.some((u) => u.includes('/api/prd') || u.includes('/api/roadmap') || u.includes('/api/lock-prd'))).toBe(
      false,
    )
  })

  it('mcp4: prd_lock empty target', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_prd_lock', arguments: { target: '' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp4: branch_switch empty branch', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_branch_switch', arguments: { branch: '' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp4: cross-tool calls are isolated', async () => {
    const client = routedClient({
      '/api/switch-branch': { body: { ok: false, error: 'dirty tree' } },
      '/api/branch': { body: { branch: 'main' } },
    })
    const mcpClient = await connectedClient(client)
    const first = await mcpClient.callTool({ name: 'saki_branch_switch', arguments: { branch: 'x' } })
    expect(first.isError).toBe(true)
    const second = await mcpClient.callTool({ name: 'saki_branch', arguments: {} })
    expect(second.isError).toBe(false)
    const secondContent = second.content as Content
    expect(secondContent.some((c) => c.text.includes('dirty tree') || c.text.includes('REMOTE_FAILED'))).toBe(false)
  })

  it('mcp4: tools/list has 13 tools with correct annotations', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const { tools } = await mcpClient.listTools()
    expect(tools).toHaveLength(13)
    const byName = Object.fromEntries(tools.map((t) => [t.name, t]))
    expect(Object.keys(byName).sort()).toEqual(
      [
        'saki_status',
        'saki_doctor',
        'saki_roadmap_list',
        'saki_runs',
        'saki_prd_show',
        'saki_run_start',
        'saki_run_tail',
        'saki_run_stop',
        'saki_branch',
        'saki_branch_list',
        'saki_branch_switch',
        'saki_mr_create',
        'saki_prd_lock',
      ].sort(),
    )
    expect(byName.saki_mr_create?.annotations).toMatchObject({ openWorldHint: true })
    for (const name of Object.keys(byName)) {
      if (name !== 'saki_mr_create') {
        expect(byName[name]?.annotations).toMatchObject({ openWorldHint: false })
      }
    }
    expect(byName.saki_branch_switch?.annotations).toMatchObject({ readOnlyHint: false, destructiveHint: true })
    expect(byName.saki_prd_lock?.annotations).toMatchObject({ readOnlyHint: false, idempotentHint: true })
  })
})
