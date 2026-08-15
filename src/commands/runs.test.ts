import { describe, it, expect } from 'vitest'
import { cmdRuns, cmdRunStop, cmdRunTail } from './runs.js'
import { cmdStatus } from './status.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT, CliError } from '../exit.js'
import type { RunEvent } from '../types.js'

// Route-aware fetch stub: pick a canned response per URL fragment. `down: true` REJECTS the fetch
// the way a closed port does — an HTTP status can't model "nothing is listening".
function routed(routes: Record<string, { status?: number; body?: unknown; stream?: string[]; down?: boolean }>) {
  const calls: string[] = []
  const impl = (async (url: string | URL) => {
    const u = String(url)
    calls.push(u)
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
      ok: status >= 200 && status < 300,
      status,
      json: async () => r.body,
      text: async () => JSON.stringify(r.body ?? ''),
    } as unknown as Response
  }) as unknown as typeof fetch
  return { impl, calls }
}

function ctxFor(routes: Parameters<typeof routed>[0], json = false) {
  const { impl, calls } = routed(routes)
  const out: string[] = []
  const err: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl }),
    cwd: '/repo',
    json,
    write: (s) => out.push(s),
    writeErr: (s) => err.push(s),
  })
  return { ctx, out, err, calls }
}

// Two-origin ctx: status is the one command that talks to BOTH servers, so its tests need to stub
// them apart. Routes are matched against the full URL, so key them by origin.
function twoOriginCtx(routes: Parameters<typeof routed>[0], json = true) {
  const { impl, calls } = routed(routes)
  const out: string[] = []
  const err: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://ts.test', goUrl: 'http://go.test', fetchImpl: impl }),
    cwd: '/repo',
    json,
    write: (s) => out.push(s),
    writeErr: (s) => err.push(s),
  })
  return { ctx, out, err, calls }
}

const evFrame = (text: string) => {
  const ev: RunEvent = { seq: 1, ts: 0, line: { kind: 'raw', text } }
  return `data: ${JSON.stringify(ev)}\n\n`
}
const endFrame = (status: string, exitCode: number) =>
  `event: end\ndata: ${JSON.stringify({ status, exitCode })}\n\n`

// Both servers up, each naming itself. Keyed by origin so the two probes can't be confused.
const BOTH_UP = {
  'ts.test/api/health': { body: { ok: true, service: 'pipeline-studio-server' } },
  'go.test/api/health': { body: { ok: true, service: 'saki-backend' } },
  '/api/session': { body: { devMode: true, authenticated: true } },
}

describe('cmdStatus', () => {
  it('reports both servers + devMode and exits OK', async () => {
    const { ctx, out } = twoOriginCtx(BOTH_UP)
    expect(await cmdStatus(ctx)).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({
      expressConfigured: true,
      url: 'http://ts.test',
      reachable: true,
      service: 'pipeline-studio-server',
      backendUrl: 'http://go.test',
      backendReachable: true,
      backendService: 'saki-backend',
      devMode: true,
      authenticated: true,
      claudeCodeAccess: false,
      sessionRead: true,
    })
  })

  // I8 — the standalone shape the extracted saki-cli ships. `goUrl` alone, no baseUrl, empty env, so
  // expressConfigured is false and the CLI must behave as a one-backend tool.
  describe('with no express configured', () => {
    function goOnlyCtx(routes: Parameters<typeof routed>[0], json = true) {
      const { impl, calls } = routed(routes)
      const out: string[] = []
      const err: string[] = []
      const ctx = makeCtx({
        client: new StudioClient({ goUrl: 'http://go.test', fetchImpl: impl, env: {} }),
        cwd: '/repo',
        json,
        write: (s) => out.push(s),
        writeErr: (s) => err.push(s),
      })
      return { ctx, out, err, calls }
    }

    const GO_UP = { 'go.test/api/health': { body: { ok: true, service: 'saki-backend' } } }

    it('reports one backend, nulls the express fields, and exits OK', async () => {
      const { ctx, out } = goOnlyCtx(GO_UP)
      expect(await cmdStatus(ctx)).toBe(EXIT.OK)
      expect(JSON.parse(out[0])).toEqual({
        expressConfigured: false,
        backendUrl: 'http://go.test',
        backendReachable: true,
        backendService: 'saki-backend',
        // present-and-null, not absent: a pre-I8 reader sees a falsy value rather than a missing key
        devMode: null,
        authenticated: null,
        claudeCodeAccess: null,
        sessionRead: false,
      })
    })

    // The lie the first version told: url/reachable described an Express nobody configured or probed.
    it('emits no url/reachable claim about an express it never probed', async () => {
      const { ctx, out } = goOnlyCtx(GO_UP)
      await cmdStatus(ctx)
      const report = JSON.parse(out[0])
      expect(Object.hasOwn(report, 'url')).toBe(false)
      expect(Object.hasOwn(report, 'reachable')).toBe(false)
    })

    it('never touches the express origin — not even /api/health or /api/session', async () => {
      const { ctx, calls } = goOnlyCtx(GO_UP)
      await cmdStatus(ctx)
      expect(calls).toEqual(['http://go.test/api/health'])
    })

    it('names the missing configuration in human output instead of printing devMode off', async () => {
      const { ctx, out } = goOnlyCtx(GO_UP, false)
      await cmdStatus(ctx)
      expect(out.join('\n')).toContain('express   not configured (set SAKI_STUDIO_URL to include it)')
      expect(out.join('\n')).not.toContain('devMode')
    })

    // A dead GO backend is now the ONLY thing that makes the studio unusable.
    it('exits 3 when the go backend is down, naming what it serves', async () => {
      const { ctx, err } = goOnlyCtx({ 'go.test/api/health': { down: true } })
      expect(await cmdStatus(ctx)).toBe(EXIT.UNREACHABLE)
      expect(err.join('\n')).toContain('the go backend is not answering')
    })
  })

  it('probes the Go backend on ITS origin, not the Express one', async () => {
    const { ctx, calls } = twoOriginCtx(BOTH_UP)
    await cmdStatus(ctx)
    expect(calls).toContain('http://ts.test/api/health')
    expect(calls).toContain('http://go.test/api/health')
  })

  // REGRESSION: status probed Express alone, so a dead Go backend — the server that answers runs,
  // roadmap, prd, branch and proto — still printed a green `reachable yes` and exit 0. Every
  // command after it then failed with exit 3, which is exactly the surprise status exists to
  // prevent. Both servers must be up for OK.
  it('exits UNREACHABLE when only the Go backend is down, and says what breaks', async () => {
    const { ctx, out, err } = twoOriginCtx({
      ...BOTH_UP,
      'go.test/api/health': { down: true },
    })
    expect(await cmdStatus(ctx)).toBe(EXIT.UNREACHABLE)
    const report = JSON.parse(out[0])
    expect(report.reachable).toBe(true)
    expect(report.backendReachable).toBe(false)
    expect(err.join('\n')).toContain('http://go.test')
    expect(err.join('\n')).toContain('runs')
  })

  it('exits UNREACHABLE when only Express is down, and still reports the live backend', async () => {
    const { ctx, out, err } = twoOriginCtx({
      ...BOTH_UP,
      'ts.test/api/health': { down: true },
    })
    expect(await cmdStatus(ctx)).toBe(EXIT.UNREACHABLE)
    const report = JSON.parse(out[0])
    expect(report.reachable).toBe(false)
    expect(report.backendReachable).toBe(true)
    expect(err.join('\n')).toContain('http://ts.test')
  })

  // A server that answers with {ok:false} is as unusable as one that never answered.
  it('treats a non-ok health body as unreachable', async () => {
    const { ctx, out } = twoOriginCtx({
      ...BOTH_UP,
      'go.test/api/health': { body: { ok: false } },
    })
    expect(await cmdStatus(ctx)).toBe(EXIT.UNREACHABLE)
    expect(JSON.parse(out[0]).backendReachable).toBe(false)
  })

  it('still reports when the session read fails — health is the authority on reachability', async () => {
    const { ctx, out } = twoOriginCtx({
      ...BOTH_UP,
      '/api/session': { status: 500, body: { error: 'boom' } },
    })
    expect(await cmdStatus(ctx)).toBe(EXIT.OK)
    expect(JSON.parse(out[0]).reachable).toBe(true)
    expect(JSON.parse(out[0]).devMode).toBe(false)
    expect(JSON.parse(out[0]).sessionRead).toBe(false)
  })

  // Express down means the session read cannot succeed either; saying so twice is noise, so the
  // report marks it unknown and only the unreachable error is printed.
  it('skips the session read when Express is down rather than warning twice', async () => {
    const { ctx, out, err } = twoOriginCtx({
      ...BOTH_UP,
      'ts.test/api/health': { down: true },
    })
    await cmdStatus(ctx)
    expect(JSON.parse(out[0]).sessionRead).toBe(false)
    expect(err.join('\n')).not.toContain('/api/session')
  })

  it('renders both servers in the human table', async () => {
    const { ctx, out } = twoOriginCtx({ ...BOTH_UP, 'go.test/api/health': { down: true } }, false)
    await cmdStatus(ctx)
    expect(out[0]).toContain('studio    http://ts.test')
    expect(out[0]).toContain('backend   http://go.test')
    expect(out[0]).toContain('yes (pipeline-studio-server)')
    expect(out[0]).toMatch(/^reachable no$/m)
  })

  // A single stubbed origin (baseUrl only) points both backends at it — the shape most command
  // tests use. Status must not break under it.
  it('works against a single-origin studio', async () => {
    const { ctx, out } = ctxFor({
      '/api/health': { body: { ok: true, service: 'pipeline-studio-server' } },
      '/api/session': { body: { devMode: true } },
    }, true)
    expect(await cmdStatus(ctx)).toBe(EXIT.OK)
    expect(JSON.parse(out[0]).backendUrl).toBe('http://s.test')
  })
})

describe('cmdRuns', () => {
  it('renders a table with a header', async () => {
    const { ctx, out } = ctxFor({
      '/api/runs': {
        body: [
          { id: 'r1', status: 'running', prompt: '/saki-builder:build x', cwd: '/repo', startedAt: 1, meta: { kind: 'build' } },
        ],
      },
    })
    expect(await cmdRuns(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('ID')
    expect(out[0]).toContain('STATUS')
    expect(out[0]).toContain('running')
  })

  it('scopes the request to the ctx cwd', async () => {
    const { ctx, calls } = ctxFor({ '/api/runs': { body: [] } })
    await cmdRuns(ctx)
    expect(calls[0]).toContain('cwd=%2Frepo')
  })

  it('says so plainly when there are no runs', async () => {
    const { ctx, out } = ctxFor({ '/api/runs': { body: [] } })
    expect(await cmdRuns(ctx)).toBe(EXIT.OK)
    expect(out[0]).toContain('no runs held')
  })
})

describe('cmdRunStop', () => {
  it('stops a live run', async () => {
    const { ctx, out } = ctxFor({ '/stop': { body: { stopped: true } } })
    expect(await cmdRunStop(ctx, 'r1')).toBe(EXIT.OK)
    expect(out[0]).toContain('stopped r1')
  })

  it('maps an unknown run to NOT_FOUND naming the id', async () => {
    const { ctx } = ctxFor({ '/stop': { status: 404, body: { error: 'no running run with that id' } } })
    await expect(cmdRunStop(ctx, 'ghost')).rejects.toMatchObject({ code: EXIT.NOT_FOUND })
    await cmdRunStop(ctx, 'ghost').catch((e: CliError) => expect(e.message).toContain('ghost'))
  })

  it('rejects a missing run id as USAGE', async () => {
    const { ctx } = ctxFor({})
    await expect(cmdRunStop(ctx, '')).rejects.toMatchObject({ code: EXIT.USAGE })
  })
})

describe('cmdRunTail', () => {
  it('prints each event and exits OK on a done run', async () => {
    const { ctx, out } = ctxFor({ '/events/': { stream: [evFrame('one'), endFrame('done', 0)] } })
    expect(await cmdRunTail(ctx, 'r1')).toBe(EXIT.OK)
    expect(out[0]).toBe('one')
    expect(out[1]).toBe('run done (exit 0)')
  })

  // REGRESSION (fresh-context review, BLOCKER): success was checked as `status === 'completed'`,
  // a value the server NEVER emits — its vocabulary is 'running' | 'done' | 'error'
  // (runManager.ts:50, emitted verbatim at :953). Every green build therefore exited 1, breaking
  // `saki run build --follow && <next>`. The old fixtures fabricated 'completed', so 173 green
  // tests hid it. These pin the REAL vocabulary.
  it("treats the server's actual success value 'done' as exit 0", async () => {
    const { ctx } = ctxFor({ '/events/': { stream: [endFrame('done', 0)] } })
    expect(await cmdRunTail(ctx, 'r1')).toBe(EXIT.OK)
  })

  it("never treats the non-existent status 'completed' as success", async () => {
    const { ctx } = ctxFor({ '/events/': { stream: [endFrame('completed', 0)] } })
    expect(await cmdRunTail(ctx, 'r1')).toBe(EXIT.ERROR)
  })

  it('prints status alone for a stopped run, whose exitCode is null (runManager.ts:1165)', async () => {
    const { ctx, out } = ctxFor({
      '/events/': { stream: [`event: end\ndata: ${JSON.stringify({ status: 'error', exitCode: null })}\n\n`] },
    })
    expect(await cmdRunTail(ctx, 'r1')).toBe(EXIT.ERROR)
    expect(out[out.length - 1]).toBe('run error')
    expect(out[out.length - 1]).not.toContain('exit 0')
  })

  it('exits ERROR on a failed run so an agent can branch on it', async () => {
    const { ctx, out } = ctxFor({ '/events/': { stream: [endFrame('error', 1)] } })
    expect(await cmdRunTail(ctx, 'r1')).toBe(EXIT.ERROR)
    expect(out[out.length - 1]).toContain('run error (exit 1)')
  })

  it('emits a json terminal record under --json', async () => {
    const { ctx, out } = ctxFor({ '/events/': { stream: [endFrame('done', 0)] } }, true)
    await cmdRunTail(ctx, 'r1')
    expect(JSON.parse(out[out.length - 1])).toEqual({ runId: 'r1', status: 'done', exitCode: 0 })
  })

  it('rejects a missing run id as USAGE', async () => {
    const { ctx } = ctxFor({})
    await expect(cmdRunTail(ctx, '')).rejects.toMatchObject({ code: EXIT.USAGE })
  })
})
