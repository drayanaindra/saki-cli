import { afterEach, describe, expect, it } from 'vitest'
import { createServer, type Server } from 'node:http'
import { mkdtemp, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { StudioClient } from './client.js'
import { EXIT } from './exit.js'

// Slice 4 transport tests bind a REAL unix socket rather than stubbing fetch. A stub would prove the
// injection wiring and nothing about whether traffic actually leaves over the socket — and the socket
// is the entire slice. The Go backend's own bind is covered in backend/cmd/server/socket_test.go.

// A TCP port nothing listens on. Any request that reaches TCP fails here, which is what makes
// "did this go over the socket?" observable instead of asserted.
const DEAD_TCP = 'http://127.0.0.1:8799'

const cleanups: Array<() => Promise<void>> = []

afterEach(async () => {
  for (const cleanup of cleanups.splice(0)) await cleanup()
})

// mkdtemp under /tmp, not the platform temp root: on macOS os.tmpdir() already eats most of the
// 103-byte sun_path budget before a socket name is appended.
async function serveOverSocket(): Promise<string> {
  const dir = await mkdtemp('/tmp/saki-sock-')
  const socketPath = join(dir, 'backend.sock')
  const server: Server = createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ ok: true, service: 'go', via: 'unix', host: req.headers.host }))
  })
  await new Promise<void>((resolve) => server.listen(socketPath, resolve))
  cleanups.push(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()))
    await rm(dir, { recursive: true, force: true })
  })
  return socketPath
}

describe('unix socket transport', () => {
  // AC 4.1 — socket present and SAKI_BACKEND_URL unset: Go traffic rides the socket. goUrl points at
  // a dead port, so a response can only have come from the socket.
  it('routes go requests over the socket when one is provisioned', async () => {
    const socketPath = await serveOverSocket()
    const client = new StudioClient({ env: {}, goUrl: DEAD_TCP, socketPath })

    const response = await client.request('/api/health')

    expect(response.status).toBe(200)
    await expect(response.json()).resolves.toMatchObject({ ok: true, via: 'unix' })
  })

  // AC 4.6 (CLI half) — the adapter sends Host: localhost so OriginGuard accepts socket traffic.
  it('sends a loopback Host over the socket', async () => {
    const socketPath = await serveOverSocket()
    const client = new StudioClient({ env: {}, goUrl: DEAD_TCP, socketPath })

    const body = await (await client.request('/api/health')).json() as { host?: string }

    expect(body.host).toBe('localhost')
  })

  // AC 4.2 — an explicit SAKI_BACKEND_URL wins outright. The socket below is live and serving, so
  // reaching the dead TCP port is the proof it was ignored rather than merely deprioritised.
  it('ignores the socket when SAKI_BACKEND_URL is set explicitly', async () => {
    const socketPath = await serveOverSocket()
    const client = new StudioClient({ env: { SAKI_BACKEND_URL: DEAD_TCP }, socketPath })

    await expect(client.request('/api/health')).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
  })

  // §16 — socket injection is scoped to Go-origin requests. With Express configured the split is live
  // again, and the socket must not swallow either side's traffic.
  it('does not inject the socket when an express studio is configured', async () => {
    const socketPath = await serveOverSocket()
    const client = new StudioClient({ env: { SAKI_STUDIO_URL: DEAD_TCP }, goUrl: DEAD_TCP, socketPath })

    await expect(client.request('/api/health')).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
  })

  // AC 4.3 — a state file can name a socket that no longer exists. Falling back to TCP is what keeps
  // the next command recoverable by auto-start instead of failing against a dead path.
  it('falls back to TCP when the named socket no longer exists', async () => {
    const socketPath = await serveOverSocket()
    await rm(socketPath, { force: true })
    const client = new StudioClient({ env: {}, goUrl: DEAD_TCP, socketPath })

    await expect(client.request('/api/health')).rejects.toMatchObject({ code: EXIT.UNREACHABLE })
  })

  // An explicitly injected fetchImpl (every existing test, and the MCP in-process path) keeps
  // precedence — the socket must not silently override a caller's transport.
  it('leaves an explicit fetchImpl in charge', async () => {
    const socketPath = await serveOverSocket()
    const calls: string[] = []
    const fetchImpl = (async (input: string | URL) => {
      calls.push(String(input))
      return new Response(JSON.stringify({ ok: true, via: 'injected' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof fetch
    const client = new StudioClient({ env: {}, goUrl: DEAD_TCP, socketPath, fetchImpl })

    await expect((await client.request('/api/health')).json()).resolves.toMatchObject({ via: 'injected' })
    expect(calls).toHaveLength(1)
  })
})
