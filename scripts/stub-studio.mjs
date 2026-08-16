// I2 — a dependency-free companion orchestrator, so `saki artifacts <runId>` is exercisable
// end-to-end inside this repo without the out-of-repo Express studio (apps/server, :8787).
//
// Serves the three routes the CLI actually reads from Express — /api/health, /api/session and
// GET /api/runs/:id/artifacts — mirroring the real contract ({ artifacts: [...] } / { error }) so
// cmdArtifacts and cmdStatus talk to it exactly as they would a real studio. Loopback-only by
// design (see plan No-Gos): `node:http` with zero dependencies.

import http from 'node:http'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'

export const DEFAULT_ARTIFACTS = [
  { runId: 'r1', path: 'tasks/prd-x.md', kind: 'prd', size: 1234 },
  { runId: 'r1', path: 'tasks/proto-x/i.html', kind: 'proto', size: 2345 },
]

// Create a stub studio. `artifacts` overrides the canned fixture; `denyArtifacts: true` makes the
// artifact route answer 401 the way a real studio without a browser session does.
export function createStubStudio({ artifacts = DEFAULT_ARTIFACTS, denyArtifacts = false } = {}) {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://127.0.0.1')
    const send = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify(body))
    }

    if (req.method === 'GET' && url.pathname === '/api/health') {
      send(200, { ok: true, service: 'stub-studio' })
    } else if (req.method === 'GET' && url.pathname === '/api/session') {
      send(200, { devMode: true, authenticated: true, claudeCodeAccess: true })
    } else if (req.method === 'GET' && /^\/api\/runs\/[^/]+\/artifacts$/.test(url.pathname)) {
      if (denyArtifacts) send(401, { error: 'unauthenticated' })
      else send(200, { artifacts })
    } else {
      send(404, { error: 'not found' })
    }
  })

  return {
    server,
    listen: (port = 0) =>
      new Promise((resolve, reject) => {
        server.once('error', reject)
        server.listen(port, '127.0.0.1', () => {
          server.removeListener('error', reject)
          resolve(server.address().port)
        })
      }),
    close: () => new Promise((resolve) => server.close(resolve)),
  }
}

// `node scripts/stub-studio.mjs` — print the listening line and stay up until Ctrl-C.
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const port = Number(process.env.PORT ?? '8799')
  const stub = createStubStudio()
  const bound = await stub.listen(port)
  console.log(`stub studio listening on http://127.0.0.1:${bound}`)
}
