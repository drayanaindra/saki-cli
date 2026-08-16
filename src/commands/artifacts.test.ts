import { describe, it, expect, afterEach } from 'vitest'
import { cmdArtifacts } from './artifacts.js'
import { cmdStatus } from './status.js'
import { StudioClient } from '../client.js'
import { makeCtx } from '../ctx.js'
import { EXIT } from '../exit.js'
import { createStubStudio, DEFAULT_ARTIFACTS } from '../../scripts/stub-studio.mjs'

// I2 — the companion-orchestrator exercise path, driven against a REAL HTTP socket. Everything else
// in the suite stubs fetch; these cases boot scripts/stub-studio.mjs on an ephemeral loopback port
// and run cmdArtifacts / cmdStatus against it, proving `saki artifacts` is exercisable end-to-end in
// this repo without the out-of-repo Express studio.
async function ctxAgainstStub(opts: { artifacts?: unknown[]; denyArtifacts?: boolean; json?: boolean }) {
  const stub = await createStubStudio(opts)
  const port = await stub.listen(0)
  const out: string[] = []
  const err: string[] = []
  const ctx = makeCtx({
    client: new StudioClient({ baseUrl: `http://127.0.0.1:${port}` }),
    cwd: '/repo',
    json: opts.json ?? true,
    write: (s) => out.push(s),
    writeErr: (s) => err.push(s),
  })
  return { ctx, out, err, stub }
}

describe('cmdArtifacts against the stub studio (real HTTP)', () => {
  const running: Array<{ close: () => Promise<void> }> = []
  afterEach(async () => {
    while (running.length) await running.pop()!.close()
  })

  it('returns EXIT.OK and the canned artifacts array', async () => {
    const { ctx, out, stub } = await ctxAgainstStub({})
    running.push(stub)
    expect(await cmdArtifacts(ctx, 'r1')).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ artifacts: DEFAULT_ARTIFACTS })
  })

  it('prints no artifacts recorded for an empty stub in human mode', async () => {
    const { ctx, out, stub } = await ctxAgainstStub({ artifacts: [], json: false })
    running.push(stub)
    expect(await cmdArtifacts(ctx, 'r1')).toBe(EXIT.OK)
    expect(out.join('\n')).toContain('no artifacts recorded for r1')
  })

  it('surfaces the session gate as EXIT.AUTH_REQUIRED when the stub denies', async () => {
    const { ctx, stub } = await ctxAgainstStub({ denyArtifacts: true })
    running.push(stub)
    await expect(cmdArtifacts(ctx, 'r1')).rejects.toMatchObject({
      code: EXIT.AUTH_REQUIRED,
      message: expect.stringContaining('requires a browser session'),
    })
  })
})

describe('cmdStatus against the stub studio (real HTTP)', () => {
  it('reports both servers as stub-studio with devMode on', async () => {
    const stub = await createStubStudio({})
    const port = await stub.listen(0)
    const out: string[] = []
    const ctx = makeCtx({
      client: new StudioClient({ baseUrl: `http://127.0.0.1:${port}` }),
      cwd: '/repo',
      json: false,
      write: (s) => out.push(s),
      writeErr: () => {},
    })
    try {
      expect(await cmdStatus(ctx)).toBe(EXIT.OK)
      expect(out.join('\n')).toContain('stub-studio')
      expect(out.join('\n')).toContain('devMode   on')
    } finally {
      await stub.close()
    }
  })
})
