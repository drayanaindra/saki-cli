import { describe, it, expect, vi, beforeEach } from 'vitest'

// `--open` hands the URL to the desktop's default handler, which means spawning a real process.
// Mock at the module boundary so the branch is exercised without ever launching a browser — the
// reason this path was previously untested, and why proto.ts sat exactly at the coverage floor.
const spawnMock = vi.fn(() => ({ unref: vi.fn() }))
const platformMock = vi.fn(() => 'darwin')
vi.mock('node:child_process', () => ({ spawn: (...a: unknown[]) => spawnMock(...(a as [])) }))
vi.mock('node:os', () => ({ platform: () => platformMock() }))

const { cmdProto } = await import('./proto.js')
const { StudioClient } = await import('../client.js')
const { makeCtx } = await import('../ctx.js')
const { EXIT, CliError } = await import('../exit.js')

function ctxFor(prd: Record<string, unknown>, env: Record<string, string | undefined> = {}) {
  const out: string[] = []
  const impl = (async (url: string | URL) => {
    const u = String(url)
    const body = u.includes('/api/prd') ? prd : { found: false }
    return { ok: true, status: 200, json: async () => body } as unknown as Response
  }) as unknown as typeof fetch
  const ctx = makeCtx({
    client: new StudioClient({ goUrl: 'http://go.test', fetchImpl: impl, env }),
    cwd: '/repo',
    json: false,
    write: (s) => out.push(s),
    writeErr: () => {},
  })
  return { ctx, out }
}

const PRD = { found: true, path: '/repo/tasks/prd-x.md', protoPreviewFile: 'tasks/proto-x/index.html' }

describe('cmdProto', () => {
  beforeEach(() => {
    spawnMock.mockClear()
    platformMock.mockReturnValue('darwin')
  })

  // The I8 regression: proto used to build this URL from the raw `client.baseUrl` field, bypassing the
  // routing table. With no Express configured that printed a dead localhost:8787 link for a route the
  // go backend already serves.
  it('builds the preview URL on the go origin when no express is configured', async () => {
    const { ctx, out } = ctxFor(PRD)
    expect(await cmdProto(ctx, 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(out.join('')).toContain('http://go.test/api/proto/')
    expect(out.join('')).not.toContain('8787')
  })

  it('uses the express origin for the proto route only when express is configured', async () => {
    // /api/proto/ is Go-owned in BOTH modes (GO_ROUTES), so this must still be the go origin.
    const { ctx, out } = ctxFor(PRD, { SAKI_STUDIO_URL: 'http://s.test' })
    expect(await cmdProto(ctx, 'tasks/prd-x.md', {})).toBe(EXIT.OK)
    expect(out.join('')).toContain('http://go.test/api/proto/')
  })

  it('does not open anything without --open', async () => {
    const { ctx } = ctxFor(PRD)
    await cmdProto(ctx, 'tasks/prd-x.md', {})
    expect(spawnMock).not.toHaveBeenCalled()
  })

  it('--open hands the URL to the platform opener, detached so the CLI can exit', async () => {
    const { ctx } = ctxFor(PRD)
    await cmdProto(ctx, 'tasks/prd-x.md', { open: true })
    expect(spawnMock).toHaveBeenCalledTimes(1)
    const [cmd, args, opts] = spawnMock.mock.calls[0] as unknown as [string, string[], Record<string, unknown>]
    expect(cmd).toBe('open') // darwin
    expect(args[0]).toContain('http://go.test/api/proto/')
    expect(opts.detached).toBe(true)
  })

  it('picks the right opener per platform', async () => {
    for (const [plat, cmd] of [
      ['win32', 'start'],
      ['linux', 'xdg-open'],
    ] as const) {
      spawnMock.mockClear()
      platformMock.mockReturnValue(plat)
      const { ctx } = ctxFor(PRD)
      await cmdProto(ctx, 'tasks/prd-x.md', { open: true })
      expect(spawnMock.mock.calls[0]?.[0]).toBe(cmd)
    }
  })

  // A PRD with no rendered gallery is a NOT_FOUND with an actionable next command, not a crash.
  it('exits 4 naming the command that renders one when there is no preview', async () => {
    const { ctx } = ctxFor({ found: true, path: '/repo/tasks/prd-x.md' })
    await expect(cmdProto(ctx, 'tasks/prd-x.md', {})).rejects.toMatchObject({
      code: EXIT.NOT_FOUND,
    })
    await expect(cmdProto(ctx, 'tasks/prd-x.md', {})).rejects.toBeInstanceOf(CliError)
  })
})
