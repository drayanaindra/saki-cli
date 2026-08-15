import { describe, it, expect } from 'vitest'
import { cmdPrdShow, cmdPrdLock } from './prd.js'
import { cmdProto } from './proto.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import { encodeProtoDir } from '../resolve.js'
import type { RoadmapItem } from '../types.js'

const item = (over: Partial<RoadmapItem> = {}): RoadmapItem => ({
  id: 'E12',
  n: 12,
  type: 'Epic',
  track: 'PRD',
  title: 'Checkout redesign',
  status: 'Planned',
  goal: 'g',
  childPrd: 'prd-checkout.md',
  childPlan: null,
  phaseChain: null,
  ...over,
})

function ctxFor(routes: Record<string, { status?: number; body?: unknown }>, json = false) {
  const posts: Array<{ url: string; body: unknown }> = []
  const gets: string[] = []
  const impl = (async (url: string | URL, init?: RequestInit) => {
    const u = String(url)
    if (init?.method === 'POST') posts.push({ url: u, body: JSON.parse(String(init.body)) })
    else gets.push(u)
    const key = Object.keys(routes).find((k) => u.includes(k))
    const r = key ? routes[key] : { status: 404, body: { error: 'no stub' } }
    const status = r.status ?? 200
    return {
      ok: status < 300,
      status,
      json: async () => r.body,
      text: async () => JSON.stringify(r.body ?? ''),
    } as unknown as Response
  }) as unknown as typeof fetch
  const out: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl }),
    cwd: '/repo',
    json,
    write: (s) => out.push(s),
  })
  return { ctx, out, posts, gets }
}

describe('cmdPrdShow', () => {
  it('resolves the id through the roadmap and fetches the PRD by path', async () => {
    const { ctx, out, gets } = ctxFor(
      {
        '/api/roadmap': { body: { found: true, epics: [item()] } },
        '/api/prd': { body: { found: true, path: '/repo/tasks/prd-checkout.md', content: '# PRD', score: 96 } },
      },
      true,
    )
    expect(await cmdPrdShow(ctx, 'E12')).toBe(EXIT.OK)
    expect(gets.some((u) => u.includes('path=%2Frepo%2Ftasks%2Fprd-checkout.md'))).toBe(true)
    expect(JSON.parse(out[0]).path).toBe('/repo/tasks/prd-checkout.md')
  })

  it('prints the PRD body in human mode', async () => {
    const { ctx, out } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/p.md', content: '# Real PRD body' } },
    })
    await cmdPrdShow(ctx, 'E12')
    expect(out.join('\n')).toContain('# Real PRD body')
  })

  it('exits NOT_FOUND with a pickup hint when the item has no Child PRD', async () => {
    const { ctx } = ctxFor({ '/api/roadmap': { body: { found: true, epics: [item({ childPrd: null })] } } })
    await expect(cmdPrdShow(ctx, 'E12')).rejects.toMatchObject({
      code: EXIT.NOT_FOUND,
      hint: expect.stringContaining('saki run pickup E12'),
    })
  })

  it('exits NOT_FOUND when the roadmap has no such id', async () => {
    const { ctx } = ctxFor({ '/api/roadmap': { body: { found: true, epics: [item()] } } })
    await expect(cmdPrdShow(ctx, 'B99')).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
  })

  it('exits NOT_FOUND when the PRD file itself is missing on disk', async () => {
    const { ctx } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: false } },
    })
    await expect(cmdPrdShow(ctx, 'E12')).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
  })

  it('accepts a direct .md path instead of an id', async () => {
    const { ctx, gets } = ctxFor({
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md', content: 'x' } },
    })
    expect(await cmdPrdShow(ctx, 'tasks/prd-x.md')).toBe(EXIT.OK)
    // No roadmap lookup needed for an explicit path.
    expect(gets.some((u) => u.includes('/api/roadmap'))).toBe(false)
  })
})

describe('cmdPrdLock', () => {
  it('posts the resolved path and cwd', async () => {
    const { ctx, out, posts } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-checkout.md' } },
      '/api/lock-prd': { body: { ok: true, locked: true, path: '/repo/tasks/prd-checkout.md' } },
    })
    expect(await cmdPrdLock(ctx, 'E12')).toBe(EXIT.OK)
    expect(posts[0].body).toMatchObject({ path: '/repo/tasks/prd-checkout.md', cwd: '/repo' })
    expect(out[0]).toContain('locked')
  })

  it('treats an already-locked PRD as success, not an error', async () => {
    const { ctx, out } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/p.md' } },
      '/api/lock-prd': { body: { ok: true, alreadyLocked: true, path: '/p.md' } },
    })
    expect(await cmdPrdLock(ctx, 'E12')).toBe(EXIT.OK)
    expect(out[0]).toContain('already locked')
  })

  it('maps an ok:false lock refusal to REMOTE_FAILED', async () => {
    const { ctx } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/p.md' } },
      '/api/lock-prd': { body: { ok: false, error: 'permission denied' } },
    })
    await expect(cmdPrdLock(ctx, 'E12')).rejects.toMatchObject({ code: EXIT.REMOTE_FAILED })
  })
})

describe('cmdProto', () => {
  it('prints the gallery url built from the PRD preview file', async () => {
    const { ctx, out } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': {
        body: { found: true, path: '/repo/tasks/prd-checkout.md', protoPreviewFile: 'tasks/proto-checkout/index.html' },
      },
    })
    expect(await cmdProto(ctx, 'E12', {})).toBe(EXIT.OK)
    expect(out[0]).toBe(
      `http://s.test/api/proto/${encodeProtoDir('/repo')}/tasks/proto-checkout/index.html`,
    )
  })

  it('exits NOT_FOUND with a proto hint when no preview has been rendered', async () => {
    const { ctx } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/p.md', protoPreviewFile: null } },
    })
    await expect(cmdProto(ctx, 'E12', {})).rejects.toMatchObject({
      code: EXIT.NOT_FOUND,
      hint: expect.stringContaining('saki run proto'),
    })
  })

  it('emits a json record with the url under --json', async () => {
    const { ctx, out } = ctxFor(
      {
        '/api/roadmap': { body: { found: true, epics: [item()] } },
        '/api/prd': { body: { found: true, path: '/p.md', protoPreviewFile: 'tasks/proto-x/index.html' } },
      },
      true,
    )
    await cmdProto(ctx, 'E12', {})
    expect(JSON.parse(out[0]).url).toContain('/api/proto/')
  })

  it('invokes the opener exactly once with the url when --open is set', async () => {
    const { ctx, out } = ctxFor({
      '/api/roadmap': { body: { found: true, epics: [item()] } },
      '/api/prd': { body: { found: true, path: '/p.md', protoPreviewFile: 'tasks/proto-x/index.html' } },
    })
    const opened: string[] = []
    expect(await cmdProto(ctx, 'E12', { open: true }, (u) => opened.push(u))).toBe(EXIT.OK)
    expect(opened).toHaveLength(1)
    expect(opened[0]).toBe(out[0])
  })
})
