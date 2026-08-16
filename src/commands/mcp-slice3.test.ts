import { describe, it, expect } from 'vitest'
import { InMemoryTransport } from '@modelcontextprotocol/sdk/inMemory.js'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { createSakiMcpServer } from '../mcp/server.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { RUN_VERBS, RUN_ENGINES } from './run.js'
import type { RunEvent } from '../types.js'

type Content = { type: string; text: string }[]

// SSE-stream-capable route stub, ported from src/commands/runs.test.ts's routed() — `saki_run_tail`
// hits /events/:id, a streaming endpoint, not a plain JSON one. Route key order matters: '/api/run' is a
// PREFIX of both '/api/runs' and '/api/run/<id>/stop', so more specific keys must be listed first —
// Object.keys(routes).find() returns the FIRST match in insertion order.
function routedClient(
  routes: Record<string, { status?: number; body?: unknown; stream?: string[]; down?: boolean }>,
  urls: string[] = [],
) {
  const impl = (async (url: string | URL) => {
    const u = String(url)
    urls.push(u)
    const key = Object.keys(routes).find((k) => u.includes(k))
    const r = key ? routes[key] : { status: 404, body: { error: 'no stub' } }
    if (r.down) throw new TypeError('fetch failed')
    const status = r.status ?? 200
    if (r.stream) {
      const enc = new TextEncoder()
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (const s of r.stream!) c.enqueue(enc.encode(s))
          c.close()
        },
      })
      return { ok: status < 300, status, body } as unknown as Response
    }
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

const evFrame = (text: string) => {
  const ev: RunEvent = { seq: 1, ts: 0, line: { kind: 'raw', text } }
  return `data: ${JSON.stringify(ev)}\n\n`
}
const endFrame = (status: string, exitCode: number | null) =>
  `event: end\ndata: ${JSON.stringify({ status, exitCode })}\n\n`

// Finds the content block whose text parses as JSON carrying a `status` field — NOT the last block: on
// a non-OK exit, exitCodeToToolResult appends a synthesized "Exited with code N" text block AFTER the
// terminal JSON (result.ts:36-38), so the JSON is second-to-last on a failed run, not last.
function findStatusBlock(content: Content): { status: string; exitCode: number | null } | undefined {
  for (const c of content) {
    try {
      const parsed = JSON.parse(c.text)
      if (parsed && typeof parsed === 'object' && 'status' in parsed) return parsed
    } catch {
      // not JSON — keep scanning
    }
  }
  return undefined
}

const ROADMAP_ITEM = {
  id: 'F5',
  n: 5,
  type: 'Feature',
  track: 'PRD',
  title: 'some feature',
  status: 'In-progress',
  goal: 'g',
  childPrd: 'prd-f5.md',
  childPlan: null,
  phaseChain: null,
}
const ROADMAP = { found: true, epics: [ROADMAP_ITEM] }
const PRD = { found: true, path: '/repo/tasks/prd-f5.md', content: '# p' }

// Order matters — see routedClient's comment above.
const BUILD_STUB_CHAIN = {
  '/api/roadmap': { body: ROADMAP },
  '/api/prd': { body: PRD },
  '/api/runs': { body: [] },
  '/api/run/': { status: 404, body: { error: 'not found' } },
  '/api/run': { body: { runId: 'r1', deduped: false } },
}

describe('saki mcp — slice 3 run lifecycle tools', () => {
  it('mcp3: run_start happy', async () => {
    const client = routedClient(BUILD_STUB_CHAIN)
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_run_start', arguments: { verb: 'build', target: 'F5' } })
    expect(result.isError).toBe(false)
    const content = result.content as Content
    expect(JSON.parse(content[0].text)).toEqual({ runId: 'r1', deduped: false })
  })

  it('mcp3: run_start unknown verb', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({
      name: 'saki_run_start',
      arguments: { verb: 'launch-the-missiles', target: 'F5' },
    })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('unknown run verb: launch-the-missiles'))).toBe(true)
    expect(content.some((c) => c.text.includes(RUN_VERBS.join(', ')))).toBe(true)
  })

  it('mcp3: run_start unknown engine', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({
      name: 'saki_run_start',
      arguments: { verb: 'reviewer', engine: 'gpt' },
    })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('unknown engine: gpt'))).toBe(true)
    expect(content.some((c) => c.text.includes(RUN_ENGINES.join(', ')))).toBe(true)
  })

  it('mcp3: run_tail failure verdict', async () => {
    const client = routedClient({ '/events/': { stream: [endFrame('error', 1)] } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_run_tail', arguments: { runId: 'r1' } })
    expect(result.isError).toBe(true)
    const statusBlock = findStatusBlock(result.content as Content)
    expect(statusBlock?.status).toBe('error')
  })

  it('mcp3: run_start then run_tail success loop', async () => {
    const client = routedClient({
      ...BUILD_STUB_CHAIN,
      '/events/': { stream: [evFrame('building...'), endFrame('done', 0)] },
    })
    const mcpClient = await connectedClient(client)

    const started = await mcpClient.callTool({ name: 'saki_run_start', arguments: { verb: 'build', target: 'F5' } })
    expect(started.isError).toBe(false)
    const { runId } = JSON.parse((started.content as Content)[0].text)
    expect(runId).toBe('r1') // sanity: matches the stub, so the threading assertion below is meaningful

    const tailed = await mcpClient.callTool({ name: 'saki_run_tail', arguments: { runId } })
    expect(tailed.isError).toBe(false)
    const statusBlock = findStatusBlock(tailed.content as Content)
    expect(statusBlock?.status).toBe('done')
  })

  it('mcp3: run_stop happy then runs shows stopped', async () => {
    const client = routedClient({
      '/api/run/': { body: {} },
      '/api/runs': { body: [{ id: 'r1', status: 'error', prompt: '/saki-builder:build x', cwd: '/repo', startedAt: 0 }] },
    })
    const mcpClient = await connectedClient(client)

    const stopped = await mcpClient.callTool({ name: 'saki_run_stop', arguments: { runId: 'r1' } })
    expect(stopped.isError).toBe(false)
    expect(JSON.parse((stopped.content as Content)[0].text)).toEqual({ stopped: true, runId: 'r1' })

    const runs = await mcpClient.callTool({ name: 'saki_runs', arguments: {} })
    expect(runs.isError).toBe(false)
    const list = JSON.parse((runs.content as Content)[0].text)
    expect(list[0]).toMatchObject({ id: 'r1', status: 'error' })
  })

  it('mcp3: run_tail empty runId', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_run_tail', arguments: { runId: '' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp3: run_stop unknown run', async () => {
    const client = routedClient({ '/api/run/': { status: 404, body: { error: 'no such run' } } })
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_run_stop', arguments: { runId: 'ghost' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 4 (NOT_FOUND)'))).toBe(true)
  })

  it('mcp3: run_start build target escape rejected', async () => {
    const urls: string[] = []
    const client = routedClient(BUILD_STUB_CHAIN, urls)
    const mcpClient = await connectedClient(client)
    const relative = await mcpClient.callTool({
      name: 'saki_run_start',
      arguments: { verb: 'build', target: '../../../etc/passwd.md' },
    })
    expect(relative.isError).toBe(true)
    expect((relative.content as Content).some((c) => c.text.includes('resolves outside the repo'))).toBe(true)

    const absolute = await mcpClient.callTool({
      name: 'saki_run_start',
      arguments: { verb: 'build', target: '/etc/passwd.md' },
    })
    expect(absolute.isError).toBe(true)
    expect((absolute.content as Content).some((c) => c.text.includes('resolves outside the repo'))).toBe(true)

    // cmdRunStart must never have run — no /api/prd or /api/run request for either rejected call
    expect(urls.some((u) => u.includes('/api/prd') || u.includes('/api/run'))).toBe(false)
  })

  it('mcp3: run_start empty target for build', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({ name: 'saki_run_start', arguments: { verb: 'build', target: '' } })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp3: run_start target given to reviewer', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const result = await mcpClient.callTool({
      name: 'saki_run_start',
      arguments: { verb: 'reviewer', target: 'F5' },
    })
    expect(result.isError).toBe(true)
    const content = result.content as Content
    expect(content.some((c) => c.text.includes('Exited with code 2 (USAGE)'))).toBe(true)
  })

  it('mcp3: cross-tool calls are isolated', async () => {
    const client = routedClient({ '/api/runs': { body: [] } })
    const mcpClient = await connectedClient(client)
    const first = await mcpClient.callTool({ name: 'saki_run_start', arguments: { verb: 'nope' } })
    expect(first.isError).toBe(true)
    const second = await mcpClient.callTool({ name: 'saki_runs', arguments: {} })
    expect(second.isError).toBe(false)
    const secondContent = second.content as Content
    expect(secondContent.some((c) => c.text.includes('unknown run verb'))).toBe(false)
  })

  it('mcp3: tools/list has 8 tools with correct annotations', async () => {
    const client = routedClient({})
    const mcpClient = await connectedClient(client)
    const { tools } = await mcpClient.listTools()
    expect(tools).toHaveLength(8)
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
      ].sort(),
    )
    expect(byName.saki_run_start?.annotations).toMatchObject({ readOnlyHint: false, destructiveHint: false })
    expect(byName.saki_run_tail?.annotations).toMatchObject({ readOnlyHint: true, destructiveHint: false })
    expect(byName.saki_run_stop?.annotations).toMatchObject({ readOnlyHint: false, destructiveHint: true })
  })
})
