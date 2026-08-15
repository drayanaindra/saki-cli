import { describe, it, expect } from 'vitest'
import { cmdRunStart, buildRunPrompt, RUN_VERBS, isRunVerb } from './run.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'

function ctxFor(res: { status?: number; body?: unknown; stream?: string[] }, json = false) {
  const bodies: unknown[] = []
  const impl = (async (url: string | URL, init?: RequestInit) => {
    if (init?.body) bodies.push(JSON.parse(String(init.body)))
    // A build now confirms its PRD exists before spawning, so this stub must answer /api/prd.
    // Echo the requested path straight back: the studio is the authority on the canonical path,
    // and echoing keeps these tests asserting the CLI's own resolution rather than the stub's.
    const u = String(url)
    if (u.includes('/api/prd')) {
      const path = new URL(u).searchParams.get('path') ?? ''
      return {
        ok: true,
        status: 200,
        json: async () => ({ found: true, path }),
        text: async () => '',
      } as unknown as Response
    }
    const status = res.status ?? 201
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

describe('cmdRunStart — resolving a roadmap id to the studio lane key', () => {
  it('turns E12 into the absolute Child PRD path, in both prompt and laneKey', async () => {
    const { ctx, posts } = routedCtx({
      '/api/roadmap': { body: { found: true, epics: [ITEM] } },
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-checkout.md' } },
      '/api/run': { status: 201, body: { runId: 'r1' } },
    })
    expect(await cmdRunStart(ctx, 'build', 'E12', {})).toBe(EXIT.OK)
    expect(posts[0].body).toMatchObject({
      prompt: '/saki-builder:build /repo/tasks/prd-checkout.md',
      meta: { kind: 'build', laneKey: '/repo/tasks/prd-checkout.md' },
    })
  })

  // Every id failure path must spawn NOTHING — a run against an unresolvable target is a wasted
  // `claude -p` on a lane no other surface can match.
  it('spawns nothing when the repo has no roadmap', async () => {
    const { ctx, posts } = routedCtx({ '/api/roadmap': { body: { found: false } } })
    await expect(cmdRunStart(ctx, 'build', 'E12', {})).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    expect(posts).toHaveLength(0)
  })

  it('spawns nothing when the id is not on the roadmap', async () => {
    const { ctx, posts } = routedCtx({ '/api/roadmap': { body: { found: true, epics: [ITEM] } } })
    await expect(cmdRunStart(ctx, 'build', 'B99', {})).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    expect(posts).toHaveLength(0)
  })

  it('spawns nothing when the item has no Child PRD yet', async () => {
    const { ctx, posts } = routedCtx({
      '/api/roadmap': { body: { found: true, epics: [{ ...ITEM, childPrd: null }] } },
    })
    await expect(cmdRunStart(ctx, 'build', 'E12', {})).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    expect(posts).toHaveLength(0)
  })

  it('spawns nothing when the roadmap read is gated', async () => {
    const { ctx, posts } = routedCtx({ '/api/roadmap': { status: 401, body: { error: 'auth' } } })
    await expect(cmdRunStart(ctx, 'build', 'E12', {})).rejects.toMatchObject({ code: EXIT.AUTH_REQUIRED })
    expect(posts).toHaveLength(0)
  })

  // RESIDUAL of BLOCKER 2, caught on re-review: a `.md` argument was path-resolved without an
  // existence check, so a typo spawned a real build, and running from a SUBDIRECTORY produced
  // `/repo/sub/tasks/prd-x.md` where the UI sends `/repo/tasks/prd-x.md` — the same lane split,
  // surviving in one input shape. Confirming the PRD exists turns both into a loud exit 4.
  it('spawns nothing for a .md path that is not on disk', async () => {
    const { ctx, posts } = routedCtx({ '/api/prd': { body: { found: false } } })
    await expect(cmdRunStart(ctx, 'build', 'tasks/typo.md', {})).rejects.toMatchObject({
      code: EXIT.NOT_FOUND,
    })
    expect(posts).toHaveLength(0)
  })

  it('adopts the path the STUDIO reports, so a subdirectory cwd cannot fork the lane', async () => {
    // cwd is /repo/sub, but the studio answers with the canonical /repo/tasks/prd-x.md.
    const { ctx, posts } = routedCtx(
      {
        '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md' } },
        '/api/run': { status: 201, body: { runId: 'r1' } },
      },
      '/repo/sub',
    )
    expect(await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect((posts[0].body as { meta: { laneKey: string } }).meta.laneKey).toBe('/repo/tasks/prd-x.md')
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
  it('posts the prompt and cwd, and prints the run id', async () => {
    const { ctx, out, bodies } = ctxFor({ body: { runId: 'r1' } }, true)
    expect(await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(bodies[0]).toMatchObject({ prompt: '/saki-builder:build /repo/tasks/prd-x.md', cwd: '/repo' })
    expect(JSON.parse(out[0])).toEqual({ runId: 'r1', deduped: false })
  })

  // REGRESSION (fresh-context review, BLOCKER): laneKey was the raw CLI argument. The studio's
  // build lane is the ABSOLUTE PRD path — the server says so outright (index.ts:236) and the UI
  // sends `laneKey: active.prd.path` (App.tsx:1447). activeBuild() matches on laneKey ALONE
  // (runManager.ts:659), so a relative path never matched the UI's absolute one and the CLI would
  // spawn a SECOND concurrent /build on a branch the studio was already building. The old stub
  // test passed because it only compared the CLI against itself.
  it('sends an ABSOLUTE PRD path as laneKey — the key the UI and server dedupe on', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})
    expect((bodies[0] as { meta?: { laneKey?: string } }).meta).toEqual({
      kind: 'build',
      laneKey: '/repo/tasks/prd-x.md',
    })
  })

  it('puts that same absolute path in the prompt, as the UI does', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})
    expect((bodies[0] as { prompt: string }).prompt).toBe('/saki-builder:build /repo/tasks/prd-x.md')
  })

  it('an absolute argument is passed through unchanged', async () => {
    const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
    await cmdRunStart(ctx, 'build', '/elsewhere/tasks/prd-y.md', {})
    expect((bodies[0] as { meta?: { laneKey?: string } }).meta?.laneKey).toBe('/elsewhere/tasks/prd-y.md')
  })

  it('sends NO meta for non-build verbs — dedupe is a build-lane concept', async () => {
    for (const verb of ['pickup', 'proto', 'rplan'] as const) {
      const { ctx, bodies } = ctxFor({ body: { runId: 'r1' } })
      await cmdRunStart(ctx, verb, 'E12', {})
      expect((bodies[0] as { meta?: unknown }).meta).toBeUndefined()
    }
  })

  it('reports a deduped in-flight build as success, not as a new run', async () => {
    // index.ts:1260 answers HTTP 200 {runId, deduped:true} when a build for the lane is live.
    const { ctx, out } = ctxFor({ status: 200, body: { runId: 'existing', deduped: true } }, true)
    expect(await cmdRunStart(ctx, 'build', 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ runId: 'existing', deduped: true })
  })

  it('mentions the dedupe in human output so a repeat is not mistaken for a new build', async () => {
    const { ctx, out } = ctxFor({ status: 200, body: { runId: 'existing', deduped: true } })
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

  it('follows the run to completion and adopts its exit code when --follow is set', async () => {
    const enc = (s: string) => s
    const frames = [
      enc(`data: ${JSON.stringify({ seq: 1, ts: 0, line: { kind: 'raw', text: 'working' } })}\n\n`),
      enc(`event: end\ndata: ${JSON.stringify({ status: 'error', exitCode: 1 })}\n\n`),
    ]
    // Route by URL, not call order — a build now reads /api/prd first, so counting calls broke.
    const impl = (async (url: string | URL) => {
      const u = String(url)
      if (u.includes('/api/prd')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ found: true, path: '/repo/tasks/p.md' }),
          text: async () => '',
        } as unknown as Response
      }
      if (u.includes('/api/run')) {
        return {
          ok: true,
          status: 201,
          json: async () => ({ runId: 'r1' }),
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
