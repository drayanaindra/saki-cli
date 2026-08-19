import { describe, expect, it } from 'vitest'
import { mkdirSync, symlinkSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
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

  // Criterion 1.1's other half: the typo case was covered, an ABSENT --engine was not — and it is
  // the one an agent hits when it forgets the flag entirely.
  it('rejects an empty --engine before making a request', async () => {
    const { ctx, posts } = ctxFor(ok)
    await expect(cmdInitEnv(ctx, [], {})).rejects.toMatchObject({ code: EXIT.USAGE })
    await expect(cmdInitEnv(ctx, [], { engine: '' })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  // PRD §6: a default profile legitimately lives outside the repo (`~/.codex`), so an ABSOLUTE path
  // is passed through — containment binds repo-relative input only.
  it('passes an absolute profile through without a containment check', async () => {
    const { ctx, posts } = ctxFor(ok)
    const code = await cmdInitEnv(ctx, [], { engine: 'codex', profile: '/somewhere/else/.codex' })
    expect(code).toBe(EXIT.OK)
    expect(posts[0].body).toMatchObject({ profile: '/somewhere/else/.codex' })
  })

  it('rejects a repository-relative symlink that resolves outside cwd', async () => {
    const root = mkdtempSync(join(tmpdir(), 'saki-init-env-'))
    const outside = mkdtempSync(join(tmpdir(), 'saki-init-env-outside-'))
    const link = join(root, 'profile')
    mkdirSync(root, { recursive: true })
    symlinkSync(outside, link)
    const { ctx, posts } = ctxFor(ok)
    const scopedCtx = { ...ctx, cwd: root }
    await expect(cmdInitEnv(scopedCtx, [], { engine: 'codex', profile: 'profile' })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })


  // An unsupported engine is a real verdict, not a transport failure: EXIT.ERROR with the reason
  // printed, so an agent can tell "stop, this needs another feature" from "retry".
  it('maps an unsupported-engine verdict to EXIT.ERROR and prints its reason', async () => {
    const notVerified: InitEnvResult = {
      ...ok,
      engine: 'claude',
      changed: false,
      status: 'not_verified',
      reason: 'engine provisioning is not verified for this engine (claude requires F4)',
      fix: '',
    }
    const { ctx, err } = ctxFor(notVerified)
    const code = await cmdInitEnv(ctx, [], { engine: 'claude' })
    expect(code).toBe(EXIT.ERROR)
    expect(err).toEqual([`error: ${notVerified.reason}`])
  })

  // A 200 with an empty body must produce a clean diagnosis, not an uncaught TypeError reported as
  // "Cannot read properties of undefined". Same defence cmdDoctor already carries.
  it('diagnoses a malformed backend body instead of throwing a TypeError', async () => {
    const { ctx } = ctxFor(undefined)
    await expect(cmdInitEnv(ctx, [], { engine: 'codex' })).rejects.toMatchObject({
      code: EXIT.ERROR,
      message: 'the backend returned no init-env result',
    })
  })

  // The human line must name the profile — "which profile did I just provision" is the question the
  // command exists to answer, and --json is not the default.
  it('names the engine, profile and change state in human output', async () => {
    const { ctx, out } = ctxFor(ok)
    const humanCtx = { ...ctx, json: false }
    const code = await cmdInitEnv(humanCtx, [], { engine: 'codex' })
    expect(code).toBe(EXIT.OK)
    expect(out[0]).toContain('codex')
    expect(out[0]).toContain(ok.profile)
    expect(out[0]).toContain('changed')
  })
})
