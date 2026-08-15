import { describe, it, expect } from 'vitest'
import { cmdWorkitems, cmdBranch, cmdBranchList, cmdBranchSwitch, cmdMrCreate, cmdScreenshots } from './repo.js'
import { cmdArtifacts } from './artifacts.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'

function ctxFor(routes: Record<string, { status?: number; body?: unknown }>, json = false) {
  const posts: Array<{ url: string; body: unknown }> = []
  const impl = (async (url: string | URL, init?: RequestInit) => {
    const u = String(url)
    if (init?.method === 'POST') posts.push({ url: u, body: JSON.parse(String(init.body)) })
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
  return { ctx, out, posts }
}

describe('cmdWorkitems', () => {
  it('reports the prd and plan queues', async () => {
    const { ctx, out } = ctxFor({ '/api/workitems': { body: { prds: [{ id: 'a' }], plans: [] } } }, true)
    expect(await cmdWorkitems(ctx)).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ prds: [{ id: 'a' }], plans: [] })
  })

  it('says so when the queue is empty', async () => {
    const { ctx, out } = ctxFor({ '/api/workitems': { body: { prds: [], plans: [] } } })
    await cmdWorkitems(ctx)
    expect(out[0]).toContain('no open work items')
  })
})

describe('cmdBranch', () => {
  it('prints the current branch', async () => {
    const { ctx, out } = ctxFor({ '/api/branch': { body: { branch: 'feature/x' } } })
    expect(await cmdBranch(ctx)).toBe(EXIT.OK)
    expect(out[0]).toBe('feature/x')
  })

  it('reports a detached HEAD rather than printing null', async () => {
    const { ctx, out } = ctxFor({ '/api/branch': { body: { branch: null } } })
    expect(await cmdBranch(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('detached')
  })

  it('lists local branches', async () => {
    const { ctx, out } = ctxFor({ '/api/branches': { body: { branches: ['main', 'feature/x'] } } })
    expect(await cmdBranchList(ctx)).toBe(EXIT.OK)
    expect(out.join('\n')).toContain('feature/x')
  })
})

describe('cmdBranchSwitch — the HTTP-200-failure trap', () => {
  it('switches on success', async () => {
    const { ctx, out, posts } = ctxFor({ '/api/switch-branch': { body: { ok: true, branch: 'feature/x' } } })
    expect(await cmdBranchSwitch(ctx, 'feature/x', {})).toBe(EXIT.OK)
    expect(posts[0].body).toMatchObject({ cwd: '/repo', branch: 'feature/x' })
    expect(out[0]).toContain('feature/x')
  })

  it('passes create through for -c/--create', async () => {
    const { ctx, posts } = ctxFor({ '/api/switch-branch': { body: { ok: true, branch: 'new' } } })
    await cmdBranchSwitch(ctx, 'new', { create: true })
    expect(posts[0].body).toMatchObject({ create: true })
  })

  it('maps a dirty-tree refusal (HTTP 200 + ok:false) to REMOTE_FAILED with git stderr', async () => {
    // index.ts:811 returns HTTP 200 on git failure. Exiting 0 here would tell an agent the switch
    // succeeded when the tree never moved.
    const { ctx } = ctxFor({
      '/api/switch-branch': {
        status: 200,
        body: { ok: false, error: 'error: Your local changes would be overwritten' },
      },
    })
    await expect(cmdBranchSwitch(ctx, 'feature/x', {})).rejects.toMatchObject({
      code: EXIT.REMOTE_FAILED,
      message: expect.stringContaining('local changes'),
    })
  })

  it('rejects a missing branch name as USAGE', async () => {
    const { ctx } = ctxFor({})
    await expect(cmdBranchSwitch(ctx, '', {})).rejects.toMatchObject({ code: EXIT.USAGE })
  })
})

describe('cmdMrCreate — the HTTP-200-failure trap', () => {
  it('prints the MR url on success', async () => {
    const { ctx, out } = ctxFor({ '/api/create-mr': { body: { ok: true, url: 'https://gl/mr/1' } } })
    expect(await cmdMrCreate(ctx)).toBe(EXIT.OK)
    expect(out[0]).toBe('https://gl/mr/1')
  })

  it('maps a no-remote refusal (HTTP 200 + ok:false) to REMOTE_FAILED', async () => {
    const { ctx } = ctxFor({
      '/api/create-mr': { status: 200, body: { ok: false, error: 'no git remote configured' } },
    })
    await expect(cmdMrCreate(ctx)).rejects.toMatchObject({
      code: EXIT.REMOTE_FAILED,
      message: expect.stringContaining('no git remote'),
    })
  })

  it('succeeds even when the studio returns no url', async () => {
    const { ctx, out } = ctxFor({ '/api/create-mr': { body: { ok: true } } })
    expect(await cmdMrCreate(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('merge request created')
  })
})

describe('cmdScreenshots', () => {
  it('lists shots with their urls', async () => {
    const { ctx, out } = ctxFor({ '/api/screenshots': { body: { shots: ['tasks/qa/a.png'] } } })
    expect(await cmdScreenshots(ctx)).toBe(EXIT.OK)
    expect(out.join('\n')).toContain('tasks/qa/a.png')
  })

  it('addresses a shot via /api/screenshot?cwd=&rel= — NOT the path-encoded proto route', async () => {
    // index.ts:925 serves screenshots by query; only the proto gallery (index.ts:938) is
    // path-encoded. Using the proto builder here would produce a 404 for every screenshot.
    const { ctx, out } = ctxFor({ '/api/screenshots': { body: { shots: ['tasks/qa/a.png'] } } }, true)
    await cmdScreenshots(ctx)
    const { urls } = JSON.parse(out[0])
    expect(urls[0]).toBe('http://s.test/api/screenshot?cwd=%2Frepo&rel=tasks%2Fqa%2Fa.png')
    expect(urls[0]).not.toContain('/api/proto/')
  })

  it('is an empty success, not an error, when there are no shots', async () => {
    const { ctx, out } = ctxFor({ '/api/screenshots': { body: { shots: [] } } }, true)
    expect(await cmdScreenshots(ctx)).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ shots: [], urls: [] })
  })
})

describe('cmdArtifacts — documented session limitation', () => {
  it('lists artifacts when a session happens to exist', async () => {
    const { ctx, out } = ctxFor({ '/artifacts': { body: { artifacts: [{ id: 'a1' }] } } }, true)
    expect(await cmdArtifacts(ctx, 'r1')).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ artifacts: [{ id: 'a1' }] })
  })

  it('explains the 401 instead of leaking a bare auth error', async () => {
    // index.ts:2195 calls getSession() directly; DEV_MODE only short-circuits the middleware
    // (authGate.ts:71), so a cookieless CLI is 401'd here by design. We do NOT weaken that guard.
    const { ctx } = ctxFor({ '/artifacts': { status: 401, body: { error: 'unauthenticated' } } })
    await expect(cmdArtifacts(ctx, 'r1')).rejects.toMatchObject({
      code: EXIT.AUTH_REQUIRED,
      message: expect.stringContaining('requires a browser session'),
      hint: expect.stringContaining('DEV_MODE does not seed one'),
    })
  })

  it('rejects a missing run id as USAGE', async () => {
    const { ctx } = ctxFor({})
    await expect(cmdArtifacts(ctx, '')).rejects.toMatchObject({ code: EXIT.USAGE })
  })
})
