import { describe, it, expect } from 'vitest'
import { StudioClient, resolveBaseUrl, resolveGoUrl, DEFAULT_BASE_URL } from './client.js'
import { EXIT, CliError } from './exit.js'

// Build a fetch stub returning one canned response.
function stubFetch(res: { status?: number; body?: unknown; text?: string }) {
  const calls: Array<{ url: string; init?: RequestInit }> = []
  const impl = async (url: string | URL, init?: RequestInit) => {
    calls.push({ url: String(url), init })
    const status = res.status ?? 200
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => res.body,
      text: async () => res.text ?? JSON.stringify(res.body ?? ''),
    } as unknown as Response
  }
  return { impl: impl as unknown as typeof fetch, calls }
}

function client(res: Parameters<typeof stubFetch>[0]) {
  const { impl, calls } = stubFetch(res)
  return { c: new StudioClient({ baseUrl: 'http://studio.test', fetchImpl: impl }), calls }
}

async function codeOf(p: Promise<unknown>): Promise<number> {
  try {
    await p
    throw new Error('expected the call to throw a CliError')
  } catch (err) {
    if (!(err instanceof CliError)) throw err
    return err.code
  }
}

describe('resolveBaseUrl', () => {
  it('defaults to the local studio port', () => {
    expect(resolveBaseUrl({})).toBe(DEFAULT_BASE_URL)
    expect(DEFAULT_BASE_URL).toBe('http://localhost:8787')
  })

  it('honours SAKI_STUDIO_URL', () => {
    expect(resolveBaseUrl({ SAKI_STUDIO_URL: 'http://other:9000' })).toBe('http://other:9000')
  })

  it('strips a trailing slash so path joining never doubles it', () => {
    expect(resolveBaseUrl({ SAKI_STUDIO_URL: 'http://other:9000/' })).toBe('http://other:9000')
  })
})

// The studio is TWO servers. The UI proxies most /api/* and all /events/ to Go :8788; only what Go
// has not taken over stays on Express :8787. Sending everything to Express — the original bug — put
// the CLI on a SEPARATE run store from the UI, so a UI build was invisible and `saki build` on the
// same PRD spawned a second concurrent one.
describe('StudioClient — per-path backend routing', () => {
  const c = () =>
    new StudioClient({
      baseUrl: 'http://ts.test',
      goUrl: 'http://go.test',
      fetchImpl: (async () => ({ ok: true, status: 200, json: async () => ({}) })) as unknown as typeof fetch,
    })

  it('sends the run vertical to the Go backend', () => {
    for (const p of ['/api/run', '/api/runs', '/api/run/abc/stop', '/events/abc']) {
      expect(c().url(p)).toContain('http://go.test')
    }
  })

  it('sends the read + git surface to the Go backend', () => {
    for (const p of ['/api/roadmap', '/api/prd', '/api/workitems', '/api/branch', '/api/create-mr']) {
      expect(c().url(p)).toContain('http://go.test')
    }
  })

  it('keeps health, session and artifacts on Express', () => {
    for (const p of ['/api/health', '/api/session', '/api/runs/abc/artifacts']) {
      expect(c().url(p)).toContain('http://ts.test')
    }
  })

  it('names the ORIGIN it actually tried when unreachable', async () => {
    const dead = new StudioClient({
      baseUrl: 'http://ts.test',
      goUrl: 'http://go.test',
      fetchImpl: (async () => {
        throw new TypeError('fetch failed')
      }) as unknown as typeof fetch,
    })
    // A Go-routed path must not report the Express URL — that would send the operator to the wrong
    // server to debug.
    await expect(dead.get('/api/runs')).rejects.toMatchObject({
      message: expect.stringContaining('http://go.test'),
    })
    await expect(dead.get('/api/health')).rejects.toMatchObject({
      message: expect.stringContaining('http://ts.test'),
    })
  })

  it('a lone baseUrl points BOTH backends at it, so single-origin stubs keep working', () => {
    const single = new StudioClient({ baseUrl: 'http://one.test' })
    expect(single.url('/api/runs')).toContain('http://one.test')
    expect(single.url('/api/health')).toContain('http://one.test')
  })

  it('resolves the Go backend from SAKI_BACKEND_URL', () => {
    expect(resolveGoUrl({ SAKI_BACKEND_URL: 'http://other:9999/' })).toBe('http://other:9999')
    expect(resolveGoUrl({})).toBe('http://127.0.0.1:8788')
  })
})

describe('StudioClient.get', () => {
  it('returns the parsed body on success', async () => {
    const { c } = client({ body: { found: true, epics: [] } })
    expect(await c.get('/api/roadmap')).toEqual({ found: true, epics: [] })
  })

  it('appends query params, skipping undefined ones', async () => {
    const { c, calls } = client({ body: {} })
    await c.get('/api/prd', { cwd: '/repo', path: undefined })
    expect(calls[0].url).toBe('http://studio.test/api/prd?cwd=%2Frepo')
  })

  it('maps a connection failure to UNREACHABLE with a hint naming the base url', async () => {
    const impl = (async () => {
      throw new TypeError('fetch failed')
    }) as unknown as typeof fetch
    const c = new StudioClient({ baseUrl: 'http://studio.test', fetchImpl: impl })
    try {
      await c.get('/api/roadmap')
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.UNREACHABLE)
      expect(e.message).toContain('http://studio.test')
      expect(e.hint).toContain('run.sh')
    }
  })

  it('maps 404 to NOT_FOUND', async () => {
    const { c } = client({ status: 404, body: { error: 'nope' } })
    expect(await codeOf(c.get('/api/runs/x/artifacts'))).toBe(EXIT.NOT_FOUND)
  })

  it('maps 401 to AUTH_REQUIRED', async () => {
    const { c } = client({ status: 401, body: { error: 'unauthenticated' } })
    expect(await codeOf(c.get('/api/runs/x/artifacts'))).toBe(EXIT.AUTH_REQUIRED)
  })

  it('attaches a DEV_MODE hint to a 401 — the raw message leaves the operator nowhere to go', async () => {
    // Observed against a real studio started WITHOUT DEV_MODE: every gated route answers
    // {"error":"authentication required"}, which says nothing about the local fix.
    const { c } = client({ status: 401, body: { error: 'authentication required' } })
    try {
      await c.get('/api/branch')
      throw new Error('expected throw')
    } catch (err) {
      expect((err as CliError).hint).toContain('DEV_MODE=1')
    }
  })

  // REGRESSION (review WARN): POST /api/run carries a SECOND gate beyond the session one —
  // requireClaudeCodeAccess (index.ts:1248 -> whitelist.ts) — which answers 403, not 401. Without
  // this branch `saki run build` exited 1 "unexpected failure" on a non-DEV_MODE studio.
  it('maps 403 to AUTH_REQUIRED with the same hint (the /api/run claude-access gate)', async () => {
    const { c } = client({ status: 403, body: { error: 'claude code access required' } })
    try {
      await c.post('/api/run', {})
      throw new Error('expected throw')
    } catch (err) {
      expect((err as CliError).code).toBe(EXIT.AUTH_REQUIRED)
      expect((err as CliError).hint).toContain('DEV_MODE=1')
    }
  })

  it('does not attach the auth hint to non-401 failures', async () => {
    const { c } = client({ status: 404, body: { error: 'nope' } })
    try {
      await c.get('/api/x')
      throw new Error('expected throw')
    } catch (err) {
      expect((err as CliError).hint).toBeUndefined()
    }
  })

  it('maps 422 to USAGE and surfaces the server error text', async () => {
    const { c } = client({ status: 422, body: { error: 'cwd is required' } })
    try {
      await c.get('/api/workitems')
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.USAGE)
      expect(e.message).toContain('cwd is required')
    }
  })

  it('maps any other non-2xx to ERROR', async () => {
    const { c } = client({ status: 500, body: { error: 'boom' } })
    expect(await codeOf(c.get('/api/roadmap'))).toBe(EXIT.ERROR)
  })
})

describe('StudioClient.post', () => {
  it('sends a JSON body and returns the parsed response', async () => {
    const { c, calls } = client({ status: 201, body: { runId: 'r1' } })
    const out = await c.post('/api/run', { prompt: '/saki-builder:build x' })
    expect(out).toEqual({ runId: 'r1' })
    expect(calls[0].init?.method).toBe('POST')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ prompt: '/saki-builder:build x' })
    expect((calls[0].init?.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  it('does NOT throw on a 200 body carrying ok:false (that is postOk\'s job)', async () => {
    const { c } = client({ body: { ok: false, error: 'dirty tree' } })
    expect(await c.post('/api/switch-branch', {})).toEqual({ ok: false, error: 'dirty tree' })
  })
})

describe('StudioClient.postOk — the HTTP-200-failure trap', () => {
  it('maps a 200 body of {ok:false} to REMOTE_FAILED carrying the server error text', async () => {
    // apps/server/src/index.ts:811 returns HTTP 200 with ok:false when git fails. An agent
    // branching on HTTP status alone would read this as success.
    const { c } = client({ status: 200, body: { ok: false, error: 'error: Your local changes…' } })
    try {
      await c.postOk('/api/switch-branch', { cwd: '/r', branch: 'x' })
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.REMOTE_FAILED)
      expect(e.message).toContain('Your local changes')
    }
  })

  it('returns the body when ok is true', async () => {
    const { c } = client({ body: { ok: true, url: 'https://gitlab/mr/1' } })
    expect(await c.postOk('/api/create-mr', { cwd: '/r' })).toEqual({
      ok: true,
      url: 'https://gitlab/mr/1',
    })
  })

  it('still maps transport-level failures normally', async () => {
    const { c } = client({ status: 422, body: { error: 'cwd (string) is required' } })
    expect(await codeOf(c.postOk('/api/create-mr', {}))).toBe(EXIT.USAGE)
  })
})

// I8 — expressConfigured is the flag that decides whether this CLI is a one-backend tool (the shape
// the extracted saki-cli ships) or the two-server dev-studio client. Getting it wrong is silent: the
// CLI would either dial a server nobody configured, or refuse a route that is genuinely available.
describe('expressConfigured', () => {
  it('is false with an empty env — the default, one-backend shape', () => {
    expect(new StudioClient({ env: {} }).expressConfigured).toBe(false)
  })

  it('is true when SAKI_STUDIO_URL is set', () => {
    expect(new StudioClient({ env: { SAKI_STUDIO_URL: 'http://s.test' } }).expressConfigured).toBe(true)
  })

  // The TS twin of Go's TestSelectProxy_WhitespaceIsNull. A var set to spaces in a shell script is a
  // configuration typo, not a request to dial the host named " ".
  it('treats a whitespace-only SAKI_STUDIO_URL as unset', () => {
    expect(new StudioClient({ env: { SAKI_STUDIO_URL: '   ' } }).expressConfigured).toBe(false)
    expect(new StudioClient({ env: { SAKI_STUDIO_URL: '\t' } }).expressConfigured).toBe(false)
  })

  // Preserves the long-standing test convenience: passing only baseUrl points BOTH backends at it.
  it('is true when an explicit baseUrl is passed, even with an empty env', () => {
    const c = new StudioClient({ baseUrl: 'http://one.test', env: {} })
    expect(c.expressConfigured).toBe(true)
    expect(c.goUrl).toBe('http://one.test')
  })

  // Truthiness, not `!== undefined`: baseUrl:'' must not count as "configured with an empty origin",
  // which would produce a URL that can only fail confusingly.
  it('treats an empty-string baseUrl as NOT configured', () => {
    expect(new StudioClient({ baseUrl: '', env: {} }).expressConfigured).toBe(false)
  })

  // A client pointed at Go alone is the standalone case — it must not think it has an Express.
  it('is false when only goUrl is given', () => {
    expect(new StudioClient({ goUrl: 'http://go.test', env: {} }).expressConfigured).toBe(false)
  })
})

describe('originOf / routing with no express configured', () => {
  it('sends every path to the go origin, including the Express-only ones', () => {
    const c = new StudioClient({ goUrl: 'http://go.test', env: {} })
    for (const p of ['/api/session', '/api/runs/abc/artifacts', '/api/roadmap', '/api/health']) {
      expect(c.originFor(p)).toBe('http://go.test')
    }
  })

  // Rather than silently returning the :8787 default and dialling a server the operator never named.
  it('throws exit 3 when asked for the express origin', () => {
    const c = new StudioClient({ goUrl: 'http://go.test', env: {} })
    expect(() => c.originOf('ts')).toThrow(CliError)
    try {
      c.originOf('ts')
    } catch (e) {
      expect((e as CliError).code).toBe(EXIT.UNREACHABLE)
      expect((e as CliError).message).toContain('no express studio is configured')
    }
  })

  it('still returns the express origin when one IS configured', () => {
    const c = new StudioClient({ env: { SAKI_STUDIO_URL: 'http://s.test' } })
    expect(c.originOf('ts')).toBe('http://s.test')
    expect(c.originFor('/api/session')).toBe('http://s.test')
  })
})
