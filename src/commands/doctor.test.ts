import { describe, it, expect } from 'vitest'
import { cmdDoctor } from './doctor.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import type { EngineReport } from '../types.js'

// routedCtx pattern from src/commands/run.test.ts:82-100 — its `gets: string[]` array records every
// GET URL. The sibling `ctxFor` (run.test.ts:7) only records POST bodies and has no URL capture, so it
// cannot be used to assert a --profile query param made it onto the request (test (d) below).
function routedCtx(routes: Record<string, { status?: number; body?: unknown }>, json = false) {
  const gets: string[] = []
  const impl = (async (url: string | URL) => {
    const u = String(url)
    gets.push(u)
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
  const errOut: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: 'http://s.test', fetchImpl: impl }),
    cwd: '/repo',
    json,
    write: (s) => out.push(s),
    writeErr: (s) => errOut.push(s),
  })
  return { ctx, out, errOut, gets }
}

const engine = (over: Partial<EngineReport> = {}): EngineReport => ({
  engine: 'codex',
  profile: 'default',
  status: 'ok',
  reason: '',
  fix: '',
  ...over,
})

describe('cmdDoctor', () => {
  it('both engines ok — exit 0', async () => {
    const { ctx } = routedCtx({
      '/api/doctor': { body: { engines: [engine({ engine: 'codex' }), engine({ engine: 'opencode' })] } },
    })
    const code = await cmdDoctor(ctx, [], {})
    expect(code).toBe(EXIT.OK)
  })

  it('codex failed — exit 1', async () => {
    const { ctx } = routedCtx({
      '/api/doctor': {
        body: {
          engines: [
            engine({ engine: 'codex', status: 'failed', reason: 'not provisioned' }),
            engine({ engine: 'opencode' }),
          ],
        },
      },
    })
    const code = await cmdDoctor(ctx, [], {})
    expect(code).toBe(EXIT.ERROR)
  })

  it('--json shape', async () => {
    const { ctx, out } = routedCtx(
      { '/api/doctor': { body: { engines: [engine({ engine: 'codex' }), engine({ engine: 'opencode' })] } } },
      true,
    )
    await cmdDoctor(ctx, [], {})
    const parsed = JSON.parse(out[0]) as { engines: EngineReport[] }
    expect(parsed.engines).toHaveLength(2)
    for (const e of parsed.engines) {
      expect(Object.keys(e).sort()).toEqual(['engine', 'fix', 'profile', 'reason', 'status'])
    }
  })

  it('threads --profile as a query param', async () => {
    const { ctx, gets } = routedCtx({
      '/api/doctor': { body: { engines: [engine({ engine: 'codex' }), engine({ engine: 'opencode' })] } },
    })
    await cmdDoctor(ctx, [], { profile: '/tmp/x' })
    const url = gets.find((u) => u.includes('/api/doctor'))
    expect(url).toContain('profile=%2Ftmp%2Fx')
  })

  it('a malformed body ({}) exits ERROR cleanly, no uncaught TypeError', async () => {
    const { ctx } = routedCtx({ '/api/doctor': { body: {} } })
    const code = await cmdDoctor(ctx, [], {})
    expect(code).toBe(EXIT.ERROR)
  })
})
