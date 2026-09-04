import { describe, it, expect } from 'vitest'
import { cmdGenesis } from './genesis.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'

function ctxFor(res: { status?: number; body?: unknown }, json = false, doctor?: unknown) {
  const posts: unknown[] = []
  const impl = (async (url: string | URL, init?: RequestInit) => {
    const u = String(url)
    if (u.includes('/api/doctor')) {
      return {
        ok: true,
        status: 200,
        json: async () => doctor,
        text: async () => JSON.stringify(doctor ?? ''),
      } as unknown as Response
    }
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
  it('forwards every supported engine and its profile', async () => {
    for (const engine of ['claude', 'codex', 'opencode', 'omp'] as const) {
      const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
      await cmdGenesis(ctx, 'idea', {}, { engine, profile: `/profiles/${engine}` })
      expect(posts[0]).toMatchObject({ engine, configDir: `/profiles/${engine}` })
    }
  })

  it('resolves auto to the first usable engine before spawning', async () => {
    const { ctx, posts } = ctxFor(
      { status: 201, body: { runId: 'r1' } },
      false,
      {
        engines: [
          { engine: 'claude', profile: '/p', status: 'failed', reason: 'missing plugin', fix: 'init' },
          { engine: 'omp', profile: '/p', status: 'ok', reason: '', fix: '' },
        ],
      },
    )

    await expect(cmdGenesis(ctx, 'idea', {}, { engine: 'auto', profile: '/p' })).resolves.toBe(EXIT.OK)
    expect(posts[0]).toMatchObject({ engine: 'omp', configDir: '/p' })
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
