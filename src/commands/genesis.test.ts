import { describe, it, expect } from 'vitest'
import { cmdGenesis } from './genesis.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'

function ctxFor(res: { status?: number; body?: unknown }, json = false) {
  const posts: unknown[] = []
  const impl = (async (_u: string | URL, init?: RequestInit) => {
    if (init?.method === 'POST') posts.push(JSON.parse(String(init.body)))
    const status = res.status ?? 200
    return {
      ok: status < 300,
      status,
      json: async () => res.body,
      text: async () => JSON.stringify(res.body ?? ''),
    } as unknown as Response
  }) as unknown as typeof fetch
  const out: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl }),
    cwd: '/repo',
    json,
    write: (s) => out.push(s),
  })
  return { ctx, out, posts }
}

describe('cmdGenesis', () => {
  it('spawns /saki-builder:genesis with the quoted idea', async () => {
    const { ctx, posts, out } = ctxFor({ status: 201, body: { runId: 'r1' } })
    expect(await cmdGenesis(ctx, 'a recipe sharing app', {})).toBe(EXIT.OK)
    expect(posts[0]).toMatchObject({
      prompt: '/saki-builder:genesis "a recipe sharing app"',
      cwd: '/repo',
    })
    expect(out[0]).toContain('r1')
  })

  it('appends --restart when the flag is set', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await cmdGenesis(ctx, 'idea', { restart: true })
    expect(posts[0]).toMatchObject({ prompt: '/saki-builder:genesis "idea" --restart' })
  })

  it('rejects an empty idea WITHOUT posting anything', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await expect(cmdGenesis(ctx, '   ', {})).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('fails when the studio returns no runId', async () => {
    const { ctx } = ctxFor({ status: 201, body: {} })
    await expect(cmdGenesis(ctx, 'idea', {})).rejects.toMatchObject({ code: EXIT.ERROR })
  })
})
