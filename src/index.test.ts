import { describe, it, expect } from 'vitest'
import { main, matchCommand, helpText, isDirectInvocation } from './index.js'
import { EXIT } from './exit.js'

describe('isDirectInvocation', () => {
  // npm installs the bin as a symlink, so argv[1] is the (often relative) LINK path while
  // import.meta.url is the realpath'd target. Comparing the raw strings makes `saki --help` print
  // nothing and exit 0 — the CLI silently does nothing. Compare realpaths.
  const realpath = (p: string) =>
    p === 'node_modules/.bin/saki' ? '/repo/apps/cli/dist/index.js' : p

  it('is true when the symlinked bin resolves to this module', () => {
    expect(
      isDirectInvocation('node_modules/.bin/saki', 'file:///repo/apps/cli/dist/index.js', realpath),
    ).toBe(true)
  })

  it('is true for a direct, unsymlinked invocation', () => {
    expect(
      isDirectInvocation('/repo/apps/cli/dist/index.js', 'file:///repo/apps/cli/dist/index.js', realpath),
    ).toBe(true)
  })

  it('is false when another entrypoint imported this module (a test run)', () => {
    expect(
      isDirectInvocation('/repo/node_modules/vitest/bin.mjs', 'file:///repo/apps/cli/dist/index.js', realpath),
    ).toBe(false)
  })

  it('is false when argv[1] is absent', () => {
    expect(isDirectInvocation(undefined, 'file:///x.js', realpath)).toBe(false)
  })

  it('is false rather than throwing when the path cannot be resolved', () => {
    expect(
      isDirectInvocation('/gone', 'file:///x.js', () => {
        throw new Error('ENOENT')
      }),
    ).toBe(false)
  })
})

// `env` defaults to {} — i.e. NO SAKI_STUDIO_URL, which after I8 means the STANDALONE shape: one
// backend, everything routed to Go. The handful of cases that genuinely need the second server
// (anything reading /api/session or the artifact routes) must opt in with EXPRESS_ENV.
const EXPRESS_ENV = { SAKI_STUDIO_URL: 'http://s.test' }

function run(
  argv: string[],
  routes: Record<string, { status?: number; body?: unknown }> = {},
  env: Record<string, string | undefined> = {},
) {
  const out: string[] = []
  const err: string[] = []
  const urls: string[] = []
  const bodies: unknown[] = []
  const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
    const u = String(url)
    urls.push(u)
    if (init?.body) bodies.push(JSON.parse(String(init.body)))
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
  return {
    out,
    err,
    urls,
    bodies,
    code: main(argv, { write: (s) => out.push(s), writeErr: (s) => err.push(s), env, cwd: '/repo', fetchImpl }),
  }
}

describe('matchCommand', () => {
  it('prefers the most specific path — `run tail` beats `run`', () => {
    expect(matchCommand(['run', 'tail', 'r1'])?.def.path).toEqual(['run', 'tail'])
    expect(matchCommand(['run', 'build', 'x'])?.def.path).toEqual(['run'])
  })

  it('prefers `branch switch` over `branch`', () => {
    expect(matchCommand(['branch', 'switch', 'x'])?.def.path).toEqual(['branch', 'switch'])
    expect(matchCommand(['branch'])?.def.path).toEqual(['branch'])
  })

  it('returns the remaining argv as rest', () => {
    expect(matchCommand(['prd', 'show', 'E12'])?.rest).toEqual(['E12'])
  })

  it('returns undefined for an unknown command', () => {
    expect(matchCommand(['frobnicate'])).toBeUndefined()
  })
})

describe('helpText', () => {
  it('lists every command group and the exit-code contract', () => {
    const h = helpText()
    for (const c of ['status', 'roadmap list', 'run tail', 'runs', 'prd show', 'proto', 'branch', 'mr create', 'artifacts', 'screenshots']) {
      expect(h).toContain(c)
    }
    expect(h).toContain('SAKI_STUDIO_URL')
    expect(h).toContain('3 studio unreachable')
  })
})

describe('main', () => {
  it('prints help and exits OK with no arguments', async () => {
    const r = run([])
    expect(await r.code).toBe(EXIT.OK)
    expect(r.out.join('\n')).toContain('Commands:')
  })

  it('exits USAGE on an unknown command and names it', async () => {
    const r = run(['frobnicate'])
    expect(await r.code).toBe(EXIT.USAGE)
    expect(r.err.join('\n')).toContain('unknown command: frobnicate')
    expect(r.err.join('\n')).toContain('status')
  })

  it('exits USAGE on an unknown flag', async () => {
    const r = run(['runs', '--bogus'])
    expect(await r.code).toBe(EXIT.USAGE)
    expect(r.err.join('\n')).toContain('--bogus')
  })

  it('rejects a flag that belongs to a different command', async () => {
    const r = run(['runs', '--create'])
    expect(await r.code).toBe(EXIT.USAGE)
  })

  it('exits USAGE on an unknown run verb, listing the valid ones', async () => {
    const r = run(['run', 'deploy', 'x'])
    expect(await r.code).toBe(EXIT.USAGE)
    expect(r.err.join('\n')).toContain('unknown run verb: deploy')
    expect(r.err.join('\n')).toContain('build')
  })

  // REGRESSION (review NIT): stray positionals were folded into the argument with join(' '), so
  // `saki run build a.md oops` sent prompt `/saki-builder:build a.md oops` AND a matching laneKey —
  // a lane nothing else can ever match — instead of an obvious usage error.
  it('rejects stray positionals on a run verb instead of folding them into the argument', async () => {
    const r = run(['run', 'build', 'tasks/a.md', 'oops'])
    expect(await r.code).toBe(EXIT.USAGE)
    expect(r.err.join('\n')).toContain('exactly one argument')
  })

  it('prints per-command help for --help', async () => {
    const r = run(['prd', 'show', '--help'])
    expect(await r.code).toBe(EXIT.OK)
    expect(r.out.join('\n')).toContain('saki prd show')
  })

  it('surfaces a CliError message AND its hint on stderr', async () => {
    const r = run(['roadmap', 'list'], { '/api/roadmap': { body: { found: false } } })
    expect(await r.code).toBe(EXIT.NOT_FOUND)
    expect(r.err[0]).toContain('no tasks/roadmap.md')
    expect(r.err[1]).toContain('roadmap init')
  })

  it('exits UNREACHABLE when the studio refuses the connection', async () => {
    const out: string[] = []
    const err: string[] = []
    const code = await main(['runs'], {
      write: (s) => out.push(s),
      writeErr: (s) => err.push(s),
      env: {},
      cwd: '/repo',
      fetchImpl: (async () => {
        throw new TypeError('fetch failed')
      }) as unknown as typeof fetch,
    })
    expect(code).toBe(EXIT.UNREACHABLE)
    expect(err.join('\n')).toContain('cannot reach the studio')
    expect(err.join('\n')).toContain('run.sh')
  })

  it('threads --cwd through to the request', async () => {
    const r = run(['runs', '--cwd', '/other'], { '/api/runs': { body: [] } })
    await r.code
    expect(r.urls[0]).toContain('cwd=%2Fother')
  })

  it('resolves a relative --cwd against the process cwd', async () => {
    const r = run(['runs', '--cwd', 'sub'], { '/api/runs': { body: [] } })
    await r.code
    expect(r.urls[0]).toContain('cwd=%2Frepo%2Fsub')
  })

  it('honours --json end to end', async () => {
    const r = run(['runs', '--json'], { '/api/runs': { body: [] } })
    expect(await r.code).toBe(EXIT.OK)
    expect(r.out[0]).toBe('[]')
  })

  it('routes a full build invocation to POST /api/run', async () => {
    const r = run(['run', 'build', 'tasks/prd-x.md'], {
      // A build confirms its PRD exists before spawning.
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md' } },
      '/api/run': { status: 201, body: { runId: 'r9' } },
    })
    expect(await r.code).toBe(EXIT.OK)
    expect(r.out.join('\n')).toContain('r9')
  })

  it('collects a multi-word roadmap add intent from the positionals', async () => {
    const r = run(['roadmap', 'add', 'let buyers save a cart', '--feature'], {
      '/api/run': { status: 201, body: { runId: 'r1' } },
    })
    expect(await r.code).toBe(EXIT.OK)
  })

  it('returns exit 5 for a studio-refused branch switch', async () => {
    const r = run(['branch', 'switch', 'x'], {
      '/api/switch-branch': { status: 200, body: { ok: false, error: 'dirty tree' } },
    })
    expect(await r.code).toBe(EXIT.REMOTE_FAILED)
    expect(r.err.join('\n')).toContain('dirty tree')
  })

  // Top-level aliases for the verbs an operator types most. `saki run build X` works, but `build`
  // is the most-used command and was the most buried — nesting it under `run` was implementation
  // convenience leaking into the interface. `proto` is deliberately NOT aliased: `saki proto <id>`
  // already means "print the URL of an already-rendered gallery", so a top-level `saki proto` can
  // not also mean "render one".
  describe('top-level run-verb aliases', () => {
    const PRD_OK = { '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md' } } }

    it('saki build <path> is identical to saki run build <path>', async () => {
      const viaAlias = run(['build', 'tasks/prd-x.md'], { ...PRD_OK, '/api/run': { status: 201, body: { runId: 'r1' } } })
      expect(await viaAlias.code).toBe(EXIT.OK)
      const aliasPost = viaAlias.bodies[0]

      const viaNested = run(['run', 'build', 'tasks/prd-x.md'], { ...PRD_OK, '/api/run': { status: 201, body: { runId: 'r1' } } })
      expect(await viaNested.code).toBe(EXIT.OK)
      expect(aliasPost).toEqual(viaNested.bodies[0])
    })

    it('saki pickup / saki rplan send the right verb and no lane meta', async () => {
      for (const [verb, id] of [['pickup', 'E12'], ['rplan', 'I7']] as const) {
        const r = run([verb, id], { '/api/run': { status: 201, body: { runId: 'r1' } } })
        expect(await r.code).toBe(EXIT.OK)
        expect(r.bodies[0]).toMatchObject({ prompt: `/saki-builder:${verb} ${id}` })
        expect((r.bodies[0] as { meta?: unknown }).meta).toBeUndefined()
      }
    })

    it('aliases accept --follow and --profile', async () => {
      const r = run(['pickup', 'E12', '--profile', '/home/me/.claude-work'], {
        '/api/run': { status: 201, body: { runId: 'r1' } },
      })
      expect(await r.code).toBe(EXIT.OK)
      expect(r.bodies[0]).toMatchObject({ configDir: '/home/me/.claude-work' })
    })

    it('aliases reject stray positionals, like the nested form', async () => {
      const r = run(['build', 'tasks/a.md', 'oops'])
      expect(await r.code).toBe(EXIT.USAGE)
      expect(r.err.join('\n')).toContain('exactly one argument')
    })

    it('aliases require an argument', async () => {
      const r = run(['build'])
      expect(await r.code).toBe(EXIT.USAGE)
    })

    it('`proto` is NOT aliased to a run — it still prints a gallery url', async () => {
      const r = run(['proto', 'E12'], {
        '/api/roadmap': { body: { found: true, epics: [{ id: 'E12', n: 12, type: 'Epic', track: 'PRD', title: 't', status: 'Planned', goal: 'g', childPrd: 'prd-x.md', childPlan: null, phaseChain: null }] } },
        '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md', protoPreviewFile: 'tasks/proto-x/i.html' } },
      })
      expect(await r.code).toBe(EXIT.OK)
      expect(r.out.join('\n')).toContain('/api/proto/')
      expect(r.urls.some((u) => u.includes('/api/run'))).toBe(false)
    })

    it('aliases appear in --help so they are discoverable', async () => {
      const h = helpText()
      for (const v of ['saki build', 'saki pickup', 'saki rplan']) expect(h).toContain(v)
    })
  })

  // Dispatch wiring: every COMMANDS entry routed through main() to the endpoint it claims to call.
  // The per-command tests exercise the FUNCTIONS; these pin the TABLE — a row wired to the wrong
  // handler, or missing a flag from its spec, is invisible to those tests.
  describe('every command dispatches to its endpoint', () => {
    const PRD = { found: true, path: '/repo/tasks/prd-x.md', content: '# p', protoPreviewFile: 'tasks/proto-x/i.html' }
    const ROADMAP = {
      found: true,
      epics: [{ id: 'E12', n: 12, type: 'Epic', track: 'PRD', title: 't', status: 'Planned', goal: 'g', childPrd: 'prd-x.md', childPlan: null, phaseChain: null }],
    }
    // 4th element = env. Present only on the cases that need an EXPRESS studio; everything else runs
    // in the standalone shape, which is now the default.
    const cases: Array<
      [string[], Record<string, { status?: number; body?: unknown }>, string, Record<string, string | undefined>?]
    > = [
      [['status'], { '/api/health': { body: { ok: true } }, '/api/session': { body: { devMode: true } } }, '/api/session', EXPRESS_ENV],
      [['roadmap', 'list'], { '/api/roadmap': { body: ROADMAP } }, '/api/roadmap'],
      [['roadmap', 'add', 'an idea', '--bug'], { '/api/run': { status: 201, body: { runId: 'r1' } } }, '/api/run'],
      [['runs'], { '/api/runs': { body: [] } }, '/api/runs'],
      [['run', 'stop', 'r1'], { '/stop': { body: { stopped: true } } }, '/stop'],
      [['prd', 'show', 'E12'], { '/api/roadmap': { body: ROADMAP }, '/api/prd': { body: PRD } }, '/api/prd'],
      [['prd', 'lock', 'E12'], { '/api/roadmap': { body: ROADMAP }, '/api/prd': { body: PRD }, '/api/lock-prd': { body: { ok: true, locked: true } } }, '/api/lock-prd'],
      [['proto', 'E12'], { '/api/roadmap': { body: ROADMAP }, '/api/prd': { body: PRD } }, '/api/prd'],
      [['workitems'], { '/api/workitems': { body: { prds: [], plans: [] } } }, '/api/workitems'],
      [['branch'], { '/api/branch': { body: { branch: 'main' } } }, '/api/branch'],
      [['branch', 'list'], { '/api/branches': { body: { branches: [] } } }, '/api/branches'],
      [['branch', 'switch', 'x'], { '/api/switch-branch': { body: { ok: true, branch: 'x' } } }, '/api/switch-branch'],
      [['mr', 'create'], { '/api/create-mr': { body: { ok: true, url: 'u' } } }, '/api/create-mr'],
      [['screenshots'], { '/api/screenshots': { body: { shots: [] } } }, '/api/screenshots'],
      [['artifacts', 'r1'], { '/artifacts': { body: { artifacts: [] } } }, '/artifacts', EXPRESS_ENV],
    ]

    for (const [argv, routes, expected, env] of cases) {
      it(`saki ${argv.join(' ')} -> ${expected}`, async () => {
        const r = run(argv, routes, env)
        expect(await r.code).toBe(EXIT.OK)
        expect(r.urls.some((u) => u.includes(expected))).toBe(true)
      })
    }
  })

  // I8 — the standalone shape end-to-end through the real dispatcher. These three commands are the
  // only ones whose behavior differs by configuration, so they are the ones worth pinning here.
  describe('with no express configured (the standalone saki-cli shape)', () => {
    it('status reports one backend and exits OK without asking for a session', async () => {
      const r = run(['status'], { '/api/health': { body: { ok: true, service: 'saki-backend' } } })
      expect(await r.code).toBe(EXIT.OK)
      expect(r.urls.some((u) => u.includes('/api/session'))).toBe(false)
      expect(r.out.join('\n')).toContain('express   not configured')
    })

    it('artifacts refuses with exit 3 and names the fix, before any request', async () => {
      const r = run(['artifacts', 'r1'], { '/artifacts': { body: { artifacts: [] } } })
      expect(await r.code).toBe(EXIT.UNREACHABLE)
      expect(r.err.join('\n')).toContain('SAKI_STUDIO_URL')
      expect(r.urls.some((u) => u.includes('/artifacts'))).toBe(false)
    })

    // The consumer the plan's first draft missed: proto built its URL from the raw `baseUrl` field,
    // so it bypassed routing entirely and would print a dead :8787 link for a Go-served route.
    it('proto builds its preview URL on the go origin, not a phantom express one', async () => {
      const ROADMAP_P = {
        found: true,
        epics: [{ id: 'E12', n: 12, type: 'Epic', track: 'PRD', title: 't', status: 'Planned', goal: 'g', childPrd: 'prd-x.md', childPlan: null, phaseChain: null }],
      }
      const PRD_P = { found: true, path: '/repo/tasks/prd-x.md', content: '# p', protoPreviewFile: 'tasks/proto-x/i.html' }
      const r = run(['proto', 'E12'], { '/api/roadmap': { body: ROADMAP_P }, '/api/prd': { body: PRD_P } })
      expect(await r.code).toBe(EXIT.OK)
      const printed = r.out.join('\n')
      // Assert the POSITIVE (it points at the go backend), not merely the absence of :8787 — an
      // empty output would satisfy a not-contains check and hide a regression.
      expect(printed).toContain('127.0.0.1:8788/api/proto/')
      expect(printed).not.toContain('localhost:8787')
    })
  })

  it('every command accepts --json and --cwd (the common flag spec is on every row)', async () => {
    for (const argv of [['runs'], ['branch'], ['workitems'], ['screenshots']]) {
      const routes = {
        '/api/runs': { body: [] },
        '/api/branch': { body: { branch: 'm' } },
        '/api/workitems': { body: { prds: [], plans: [] } },
        '/api/screenshots': { body: { shots: [] } },
      }
      const r = run([...argv, '--json', '--cwd', '/other'], routes)
      expect(await r.code).toBe(EXIT.OK)
      expect(r.urls[0]).toContain('cwd=%2Fother')
    }
  })

  it('--engine opencode reaches the request body', async () => {
    const r = run(['build', 'tasks/prd-x.md', '--engine', 'opencode'], {
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md' } },
      '/api/run': { status: 201, body: { runId: 'r1' } },
    })
    expect(await r.code).toBe(EXIT.OK)
    expect(r.bodies[0]).toMatchObject({ engine: 'opencode' })
  })

  it('omits engine entirely when not given (backend default = claude)', async () => {
    const r = run(['build', 'tasks/prd-x.md'], {
      '/api/prd': { body: { found: true, path: '/repo/tasks/prd-x.md' } },
      '/api/run': { status: 201, body: { runId: 'r1' } },
    })
    await r.code
    expect((r.bodies[0] as Record<string, unknown>).engine).toBeUndefined()
  })

  it('rejects an unknown engine as USAGE, naming the valid ones', async () => {
    const r = run(['build', 'tasks/prd-x.md', '--engine', 'gpt'])
    expect(await r.code).toBe(EXIT.USAGE)
    expect(r.err.join('\n')).toContain('unknown engine: gpt')
    expect(r.err.join('\n')).toContain('opencode')
  })

  // Needs an EXPRESS studio: the 401-with-explanation path only exists when there is a server to
  // refuse us. Unconfigured, artifacts now fails earlier and differently (exit 3) — covered above.
  it('returns exit 6 with the session explanation for artifacts', async () => {
    const r = run(['artifacts', 'r1'], { '/artifacts': { status: 401, body: { error: 'unauthenticated' } } }, EXPRESS_ENV)
    expect(await r.code).toBe(EXIT.AUTH_REQUIRED)
    expect(r.err.join('\n')).toContain('requires a browser session')
  })
})
