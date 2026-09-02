import { describe, it, expect } from 'vitest'
import { cmdRunStart, buildRunPrompt, RUN_VERBS, isRunVerb } from './run.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'

function ctxFor(res: { status?: number; body?: unknown; stream?: string[] }, json = false) {
  const bodies: unknown[] = []
  const impl = (async (url: string | URL, init?: RequestInit) => {
    if (init?.body) bodies.push(JSON.parse(String(init.body)))
    const u = String(url)
    const status = res.status ?? 201
    if (u.includes('/api/workflow')) {
      const body = res.body as Record<string, unknown> | undefined
      return {
        ok: status < 300,
        status,
        json: async () =>
          body?.workflowId
            ? body
            : { workflowId: body?.runId ?? 'w1', phase: 'build', status: 'running', deduped: body?.deduped === true },
        text: async () => JSON.stringify(body ?? ''),
      } as unknown as Response
    }
    if (res.stream) {
      const enc = new TextEncoder()
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (const s of res.stream!) c.enqueue(enc.encode(s))
          c.close()
        },
      })
      return { ok: true, status: 200, body } as unknown as Response
    }
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
  return { ctx, out, bodies }
}

describe('buildRunPrompt', () => {
  it('emits the canonical saki-builder namespace', () => {
    // The server re-namespaces per profile at index.ts:1270 (normalizeCmdNs + resolveCmdNs), so a
    // bare-install profile still resolves this to `/build`. The CLI must NOT try to guess.
    expect(buildRunPrompt('build', 'tasks/prd-x.md')).toBe('/saki-builder:build tasks/prd-x.md')
  })

  it('omits the argument separator when there is no argument', () => {
    expect(buildRunPrompt('roadmap', '')).toBe('/saki-builder:roadmap')
  })

  it('trims surrounding whitespace on the argument', () => {
    expect(buildRunPrompt('pickup', '  E12  ')).toBe('/saki-builder:pickup E12')
  })
})

describe('isRunVerb', () => {
  it('accepts the supported verbs', () => {
    for (const v of RUN_VERBS) expect(isRunVerb(v)).toBe(true)
  })

  it('rejects anything else', () => {
    expect(isRunVerb('deploy')).toBe(false)
  })
})

// A route-aware stub (the flat `ctxFor` above answers every URL with the same body, which cannot
// express roadmap -> prd -> run). Needed to pin the id-resolution path — flagged on re-review as the
// single largest behavioral change with NO test, i.e. exactly the stub blind spot that hid the
// 'completed' blocker.
function routedCtx(routes: Record<string, { status?: number; body?: unknown }>, cwd = '/repo') {
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
    cwd,
    write: (s) => out.push(s),
  })
  return { ctx, out, posts, gets }
}

const ITEM = {
  id: 'E12',
  n: 12,
  type: 'Epic',
  track: 'PRD',
  title: 't',
  status: 'Planned',
  goal: 'g',
  childPrd: 'prd-checkout.md',
  childPlan: null,
  phaseChain: null,
}

describe('cmdRunStart — workflow build contract', () => {
  it('sends the roadmap id to the backend without a client-side PRD lookup', async () => {
    const { ctx, posts } = routedCtx({
      '/api/workflow': { status: 201, body: { workflowId: 'w1', phase: 'pickup', status: 'running', deduped: false } },
    })
    expect(await cmdRunStart(ctx, 'build', 'E12', {})).toBe(EXIT.OK)
    expect(posts[0].url).toContain('/api/workflow')
    expect(posts[0].body).toMatchObject({ cwd: '/repo', target: 'E12' })
  })

  it('rejects an escaping path before making a request', async () => {
    const { ctx, posts } = routedCtx({})
    await expect(cmdRunStart(ctx, 'build', '../outside.md', {})).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('rejects a target that is neither a roadmap id nor a markdown path', async () => {
    const { ctx, posts } = routedCtx({})
    await expect(cmdRunStart(ctx, 'build', 'not-a-target', {})).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('preserves NOT_FOUND for a syntactically valid but missing path', async () => {
    const { ctx, posts } = routedCtx({ '/api/workflow': { status: 404, body: { error: 'target path is missing' } } })
    await expect(cmdRunStart(ctx, 'build', 'tasks/missing.md', {})).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    expect(posts).toHaveLength(1)
  })
})

// Both review skills take an OPTIONAL target and default to the newest file in tasks/ when none is
// given (prd-review/SKILL.md:66, rplan-review/SKILL.md:19). Requiring an argument would block that
// documented default; resolving an id wrongly would review the wrong artifact.
describe('cmdRunStart — review verbs', () => {
  it('prd-review with NO argument sends the bare command (skill picks the newest PRD)', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    expect(await cmdRunStart(ctx, 'prd-review', '', {})).toBe(EXIT.OK)
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:prd-review')
  })

  it('rplan-review with NO argument sends the bare command', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    expect(await cmdRunStart(ctx, 'rplan-review', '', {})).toBe(EXIT.OK)
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:rplan-review')
  })

  it('passes an explicit path through unchanged', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'rplan-review', 'tasks/fix-x-plan.md', {})
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:rplan-review tasks/fix-x-plan.md')
  })

  it('neither review verb sends lane meta — they are not builds', async () => {
    for (const verb of ['prd-review', 'rplan-review'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await cmdRunStart(ctx, verb, '', {})
      expect((bodies[0] as { meta?: unknown }).meta).toBeUndefined()
    }
  })
})

// The tail of the manual chain. Contracts differ per verb and were read from each SKILL.md:
//   approved [plan] / qa [plan]  — optional plan path, else newest *-plan.md (approved:28, qa:16)
//   reviewer                     — takes NO target; it reviews the git diff
//   wrap [--heal]                — takes NO target, but --heal is a MODE that must reach the prompt
describe('cmdRunStart — chain verbs', () => {
  it('approved / qa / reviewer / wrap all work with no argument', async () => {
    for (const verb of ['approved', 'qa', 'reviewer', 'wrap'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      expect(await cmdRunStart(ctx, verb, '', {})).toBe(EXIT.OK)
      expect((bodies[0] as { prompt: string }).prompt).toBe(`/saki-builder:${verb}`)
    }
  })

  it('approved / qa accept an explicit plan path', async () => {
    for (const verb of ['approved', 'qa'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await cmdRunStart(ctx, verb, 'tasks/fix-x-plan.md', {})
      expect((bodies[0] as { prompt: string }).prompt).toBe(`/saki-builder:${verb} tasks/fix-x-plan.md`)
    }
  })

  it('reviewer and wrap REJECT a target — they have nothing to point at', async () => {
    for (const verb of ['reviewer', 'wrap'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await expect(cmdRunStart(ctx, verb, 'E12', {})).rejects.toMatchObject({ code: EXIT.USAGE })
      expect(bodies).toHaveLength(0)
    }
  })

  it('wrap --heal reaches the PROMPT, not the request body', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    expect(await cmdRunStart(ctx, 'wrap', '', { heal: true })).toBe(EXIT.OK)
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:wrap --heal')
    expect((bodies[0] as Record<string, unknown>).heal).toBeUndefined()
  })

  it('--heal is ignored for verbs that have no such mode', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'qa', '', { heal: true })
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:qa')
  })

  // Every plan-consuming verb must resolve an id through **Child plan:**, not pass it through raw.
  // `/saki-builder:approved I1` would hand the skill "I1" as if it were a plan path. This test
  // exists because the first implementation special-cased only rplan-review and nothing caught it.
  it('approved / qa / rplan-review all resolve an id to the Child PLAN path', async () => {
    const PLAN_ITEM = { ...ITEM, id: 'I1', childPrd: null, childPlan: 'i1-fix-plan.md' }
    for (const verb of ['approved', 'qa', 'rplan-review'] as const) {
      const { ctx, posts } = routedCtx({
        '/api/roadmap': { body: { found: true, epics: [PLAN_ITEM] } },
        '/api/run': { status: 201, body: { runId: 'r1' } },
      })
      expect(await cmdRunStart(ctx, verb, 'I1', {})).toBe(EXIT.OK)
      expect((posts[0].body as { prompt: string }).prompt).toBe(
        `/saki-builder:${verb} /repo/tasks/i1-fix-plan.md`,
      )
    }
  })

  it('a plan verb given an id with no Child plan exits 4 and spawns nothing', async () => {
    const { ctx, posts } = routedCtx({
      '/api/roadmap': { body: { found: true, epics: [{ ...ITEM, id: 'I1', childPlan: null }] } },
    })
    await expect(cmdRunStart(ctx, 'approved', 'I1', {})).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    expect(posts).toHaveLength(0)
  })

  it('none of the chain verbs send lane meta', async () => {
    for (const verb of ['approved', 'qa', 'reviewer', 'wrap'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await cmdRunStart(ctx, verb, '', {})
      expect((bodies[0] as { meta?: unknown }).meta).toBeUndefined()
    }
  })
})

describe('cmdRunStart', () => {
  it('posts the workflow target and prints the workflow id', async () => {
    const { ctx, out, bodies } = ctxFor({ body: { runId: 'r1' } }, true)
    expect(await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(bodies[0]).toMatchObject({ target: 'tasks/prd-x.md', cwd: '/repo' })
    expect(JSON.parse(out[0])).toMatchObject({ workflowId: 'r1', deduped: false })
  })

  // REGRESSION (fresh-context review, BLOCKER): laneKey was the raw CLI argument. The studio's
  // build lane is the ABSOLUTE PRD path — the server says so outright (index.ts:236) and the UI
  // sends `laneKey: active.prd.path` (App.tsx:1447). activeBuild() matches on laneKey ALONE
  // (runManager.ts:659), so a relative path never matched the UI's absolute one and the CLI would
  // spawn a SECOND concurrent /build on a branch the studio was already building. The old stub
  // test passed because it only compared the CLI against itself.
  it('sends the path unchanged; the backend owns canonical lane resolution', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})
    expect(bodies[0]).toMatchObject({ target: 'tasks/prd-x.md', cwd: '/repo' })
  })

  it('does not turn the path into a child prompt', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})
    expect((bodies[0] as Record<string, unknown>).prompt).toBeUndefined()
  })

  it('accepts an absolute path inside cwd', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', '/repo/tasks/prd-y.md', {})
    expect((bodies[0] as { target: string }).target).toBe('/repo/tasks/prd-y.md')
  })

  it('sends NO meta for non-build verbs — dedupe is a build-lane concept', async () => {
    for (const verb of ['pickup', 'proto', 'rplan'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await cmdRunStart(ctx, verb, 'E12', {})
      expect((bodies[0] as { meta?: unknown }).meta).toBeUndefined()
    }
  })

  it('reports a deduped in-flight build as success, not as a new run', async () => {
    const { ctx, out } = ctxFor({ status: 200, body: { workflowId: 'existing', phase: 'build', status: 'running', deduped: true } }, true)
    expect(await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toMatchObject({ workflowId: 'existing', deduped: true })
  })

  it('mentions the dedupe in human output so a repeat is not mistaken for a new build', async () => {
    const { ctx, out } = ctxFor({ status: 200, body: { workflowId: 'existing', phase: 'build', status: 'running', deduped: true } })
    await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})
    expect(out[0]).toContain('already running')
  })

  it('passes configDir through when --profile is given', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', 'tasks/p.md', { profile: '/home/me/.claude-work' })
    expect(bodies[0]).toMatchObject({ configDir: '/home/me/.claude-work' })
  })

  it('rejects a missing argument as USAGE', async () => {
    const { ctx } = ctxFor({ body: {} })
    await expect(cmdRunStart(ctx, 'build', '', {})).rejects.toMatchObject({ code: EXIT.USAGE })
  })

  it('follows the workflow to completion and adopts its exit code when --follow is set', async () => {
    const enc = (s: string) => s
    const frames = [
      enc(`data: ${JSON.stringify({ seq: 1, ts: 0, line: { kind: 'raw', text: 'working' } })}\n\n`),
      enc(`event: end\ndata: ${JSON.stringify({ status: 'error', exitCode: 1 })}\n\n`),
    ]
    // Route by URL, not call order — a build now reads /api/prd first, so counting calls broke.
    const impl = (async (url: string | URL) => {
      const u = String(url)
      if (u.includes('/api/workflow')) {
        return {
          ok: true,
          status: 201,
          json: async () => ({ workflowId: 'w1', phase: 'build', status: 'running', deduped: false }),
          text: async () => '',
        } as unknown as Response
      }
      const e = new TextEncoder()
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (const f of frames) c.enqueue(e.encode(f))
          c.close()
        },
      })
      return { ok: true, status: 200, body } as unknown as Response
    }) as unknown as typeof fetch
    const out: string[] = []
    const ctx = makeCtx({
      client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl }),
      cwd: '/repo',
      write: (s) => out.push(s),
    })
    expect(await cmdRunStart(ctx, 'build', 'tasks/p.md', { follow: true })).toBe(EXIT.ERROR)
    expect(out).toContain('working')
  })
})
