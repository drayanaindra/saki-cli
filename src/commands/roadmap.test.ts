import { describe, it, expect } from 'vitest'
import { cmdRoadmapList, cmdRoadmapAdd, cmdRoadmapInit, ADD_FLAG, resolveAddType } from './roadmap.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import type { RoadmapItem } from '../types.js'

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

const item = (over: Partial<RoadmapItem> = {}): RoadmapItem => ({
  id: 'E12',
  n: 12,
  type: 'Epic',
  track: 'PRD',
  title: 'Checkout redesign',
  status: 'Planned',
  goal: 'g',
  childPrd: null,
  childPlan: null,
  phaseChain: null,
  ...over,
})

describe('ADD_FLAG — mirrors frontend/src/lib/addCommand.ts:9', () => {
  it('maps all four work-item types', () => {
    expect(ADD_FLAG).toEqual({
      Epic: '--epic',
      Feature: '--feature',
      Improvement: '--improvement',
      Bug: '--bug',
    })
  })
})

describe('resolveAddType', () => {
  it('resolves a single type flag', () => {
    expect(resolveAddType({ feature: true })).toBe('Feature')
    expect(resolveAddType({ bug: true })).toBe('Bug')
  })

  it('rejects no type flag as USAGE naming all four', () => {
    try {
      resolveAddType({})
      throw new Error('expected throw')
    } catch (err) {
      const e = err as { code: number; message: string }
      expect(e.code).toBe(EXIT.USAGE)
      expect(e.message).toContain('--epic')
      expect(e.message).toContain('--feature')
      expect(e.message).toContain('--improvement')
      expect(e.message).toContain('--bug')
    }
  })

  it('rejects two type flags as USAGE', () => {
    expect(() => resolveAddType({ epic: true, bug: true })).toThrowError(/exactly one/i)
  })
})

describe('cmdRoadmapList', () => {
  it('renders a table of items', async () => {
    const { ctx, out } = ctxFor({ body: { found: true, epics: [item()] } })
    expect(await cmdRoadmapList(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('ID')
    expect(out[0]).toContain('E12')
    expect(out[0]).toContain('Checkout redesign')
  })

  it('emits the raw payload under --json', async () => {
    const { ctx, out } = ctxFor({ body: { found: true, productName: 'Shop', epics: [item()] } }, true)
    await cmdRoadmapList(ctx)
    const parsed = JSON.parse(out[0])
    expect(parsed.epics).toHaveLength(1)
    expect(parsed.epics[0].id).toBe('E12')
  })

  it('exits NOT_FOUND with an init hint when the repo has no roadmap', async () => {
    const { ctx } = ctxFor({ body: { found: false } })
    await expect(cmdRoadmapList(ctx)).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
  })

  it('treats a found roadmap with zero items as empty, not missing', async () => {
    const { ctx, out } = ctxFor({ body: { found: true, epics: [] } })
    expect(await cmdRoadmapList(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('no work items')
  })
})

describe('cmdRoadmapInit', () => {
  it('spawns /saki-builder:roadmap init', async () => {
    const { ctx, posts, out } = ctxFor({ status: 201, body: { runId: 'r1' } })
    expect(await cmdRoadmapInit(ctx)).toBe(EXIT.OK)
    expect(posts[0]).toMatchObject({ prompt: '/saki-builder:roadmap init', cwd: '/repo' })
    expect(out[0]).toContain('r1')
  })

  it('fails when the studio returns no runId', async () => {
    const { ctx } = ctxFor({ status: 201, body: {} })
    await expect(cmdRoadmapInit(ctx)).rejects.toMatchObject({ code: EXIT.ERROR })
  })

  it('forwards --profile and --engine as configDir/engine', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await cmdRoadmapInit(ctx, { profile: '/prof', engine: 'codex' })
    expect(posts[0]).toMatchObject({ configDir: '/prof', engine: 'codex' })
  })
})

describe('cmdRoadmapAdd', () => {
  it('spawns /saki-builder:add with the type flag ahead of the intent', async () => {
    // There is no POST /api/roadmap — GET /api/roadmap is read-only (index.ts:759), so an add is
    // a headless skill run. The flag is what keeps /add non-interactive.
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    expect(await cmdRoadmapAdd(ctx, 'let buyers save a cart', { feature: true })).toBe(EXIT.OK)
    expect(posts[0]).toMatchObject({
      prompt: '/saki-builder:add --feature let buyers save a cart',
      cwd: '/repo',
    })
  })

  it('sends no lane meta — an add is not a build lane', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await cmdRoadmapAdd(ctx, 'idea', { bug: true })
    expect((posts[0] as { meta?: unknown }).meta).toBeUndefined()
  })

  it('rejects a missing type flag WITHOUT posting anything', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await expect(cmdRoadmapAdd(ctx, 'idea', {})).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('rejects an empty intent WITHOUT posting anything', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await expect(cmdRoadmapAdd(ctx, '   ', { epic: true })).rejects.toMatchObject({ code: EXIT.USAGE })
    expect(posts).toHaveLength(0)
  })

  it('forwards --profile and --engine as configDir/engine', async () => {
    const { ctx, posts } = ctxFor({ status: 201, body: { runId: 'r1' } })
    await cmdRoadmapAdd(ctx, 'idea', { feature: true }, { profile: '/prof', engine: 'opencode' })
    expect(posts[0]).toMatchObject({ configDir: '/prof', engine: 'opencode' })
  })
})
