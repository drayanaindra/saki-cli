import { describe, expect, it } from 'vitest'
import { cmdInitEnv } from './init-env.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import type { InitEnvResult } from '../types.js'

function ctxFor(result?: InitEnvResult) {
  const posts: Array<{ url: string; body: unknown }> = []
  const out: string[] = []
  const err: string[] = []
  const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
    posts.push({ url: String(url), body: JSON.parse(String(init?.body)) })
    return { ok: true, status: 200, json: async () => result, text: async () => '' } as unknown as Response
  }) as typeof fetch
  return {
    ctx: makeCtx({
      client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl }),
      cwd: '/repo',
      write: (s) => out.push(s),
      writeErr: (s) => err.push(s),
      json: true,
    }),
    posts,
    out,
    err,
  }
}

const ok: InitEnvResult = { engine: 'codex', profile: 'default', changed: true, status: 'ok', reason: '', fix: '' }

describe('cmdInitEnv', () => {
  it('validates engine before making a request', async () => {
    const { ctx, posts } = ctxFor(ok)
    await expect(cmdInitEnv(ctx, [], { engine: 'typo' })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('posts cwd, engine, and profile and returns success', async () => {
    const { ctx, posts, out } = ctxFor(ok)
    const code = await cmdInitEnv(ctx, [], { engine: 'codex', profile: '/tmp/profile' })
    expect(code).toBe(EXIT.OK)
    expect(posts[0].body).toEqual({ cwd: '/repo', engine: 'codex', profile: '/tmp/profile' })
    expect(JSON.parse(out[0])).toEqual(ok)
  })

  it('returns error for a failed verification and prints its fix', async () => {
    const failed: InitEnvResult = { ...ok, changed: false, status: 'failed', reason: 'not provisioned', fix: 'fix it' }
    const { ctx, err } = ctxFor(failed)
    const code = await cmdInitEnv(ctx, [], { engine: 'codex' })
    expect(code).toBe(EXIT.ERROR)
    expect(err).toEqual(['error: not provisioned', 'fix (codex): fix it'])
  })

  it('rejects positional arguments', async () => {
    const { ctx, posts } = ctxFor(ok)
    await expect(cmdInitEnv(ctx, ['extra'], { engine: 'codex' })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('rejects a relative profile that escapes cwd before making a request', async () => {
    const { ctx, posts } = ctxFor(ok)
    await expect(cmdInitEnv(ctx, [], { engine: 'codex', profile: '../outside' })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })
})
