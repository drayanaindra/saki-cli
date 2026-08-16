import http from 'node:http'
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
function ctxAgainstStub(opts: { artifacts?: unknown[]; denyArtifacts?: boolean } = {}, json = true) {
  const stub = createStubStudio(opts)
  const out: string[] = []
  const build = async () => {
    const port = await stub.listen(0)
    const ctx = makeCtx({
      client: new StudioClient({ baseUrl: `http://127.0.0.1:${port}` }),
      cwd: '/repo',
      json,
      write: (s) => out.push(s),
      writeErr: () => {},
    })
    return { ctx, port }
  }
  return { stub, out, build }
}

// Raw node:http request so a Host header can be set explicitly (fetch forbids overriding it) —
// the only way to actually exercise the cross-origin guard rather than merely asserting it exists.
function requestWithHost(port: number, path: string, host: string): Promise<{ status: number; body: unknown }> {
  return new Promise((resolvePromise, reject) => {
    const req = http.request({ host: '127.0.0.1', port, path, headers: { Host: host } }, (res) => {
      let data = ''
      res.on('data', (chunk: Buffer) => (data += chunk))
      res.on('end', () => resolvePromise({ status: res.statusCode ?? 0, body: JSON.parse(data) }))
    })
    req.on('error', reject)
    req.end()
  })
}

describe('cmdArtifacts against the stub studio (real HTTP)', () => {
  const running: Array<{ close: () => Promise<void> }> = []
  afterEach(async () => {
    while (running.length) await running.pop()!.close()
  })

  it('returns EXIT.OK and the canned artifacts array', async () => {
    const { stub, out, build } = ctxAgainstStub()
    running.push(stub)
    const { ctx } = await build()
    expect(await cmdArtifacts(ctx, 'r1')).toBe(EXIT.OK)
    expect(JSON.parse(out[0])).toEqual({ artifacts: DEFAULT_ARTIFACTS })
  })

  it('prints no artifacts recorded for an empty stub in human mode', async () => {
    const { stub, out, build } = ctxAgainstStub({ artifacts: [] }, false)
    running.push(stub)
    const { ctx } = await build()
    expect(await cmdArtifacts(ctx, 'r1')).toBe(EXIT.OK)
    expect(out.join('\n')).toContain('no artifacts recorded for r1')
  })

  it('surfaces the session gate as EXIT.AUTH_REQUIRED when the stub denies', async () => {
    const { stub, build } = ctxAgainstStub({ denyArtifacts: true })
    running.push(stub)
    const { ctx } = await build()
    await expect(cmdArtifacts(ctx, 'r1')).rejects.toMatchObject({
      code: EXIT.AUTH_REQUIRED,
      message: expect.stringContaining('requires a browser session'),
    })
  })

  it('rejects a non-loopback Host header with 403 cross-origin blocked', async () => {
    const { stub, build } = ctxAgainstStub()
    running.push(stub)
    const { port } = await build()
    const res = await requestWithHost(port, '/api/health', 'evil.example.com')
    expect(res.status).toBe(403)
    expect(res.body).toEqual({ error: 'cross-origin blocked' })
  })
})

describe('cmdStatus against the stub studio (real HTTP)', () => {
  const running: Array<{ close: () => Promise<void> }> = []
  afterEach(async () => {
    while (running.length) await running.pop()!.close()
  })

  it('reports both servers as stub-studio with devMode on', async () => {
    const { stub, out, build } = ctxAgainstStub({}, false)
    running.push(stub)
    const { ctx } = await build()
    expect(await cmdStatus(ctx)).toBe(EXIT.OK)
    expect(out.join('\n')).toContain('stub-studio')
    expect(out.join('\n')).toContain('devMode   on')
  })
})
