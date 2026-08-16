// I2 — a dependency-free companion orchestrator, so `saki artifacts <runId>` is exercisable
// end-to-end inside this repo without the out-of-repo Express studio (apps/server, :8787).
//
// Serves the three routes the CLI actually reads from Express — /api/health, /api/session and
// GET /api/runs/:id/artifacts — loopback-only by design (see plan No-Gos): `node:http` with zero
// dependencies. NOTE: the /api/runs/:id/artifacts default (200 + artifacts, denyArtifacts:false)
// is a FIXTURE for exercising the happy path — it does not model what the real studio actually
// returns to a cookie-less CLI (always 401, per src/commands/artifacts.ts:7-9 and
// docs/cli-reference.md's Known limitations). `denyArtifacts:true` is the behavior a real,
// unauthenticated CLI session actually observes.

import http from 'node:http'
import { fileURLToPath } from 'node:url'
import { resolve as resolvePath } from 'node:path'

export const DEFAULT_ARTIFACTS = [
  { runId: 'r1', path: 'tasks/prd-x.md', kind: 'prd', size: 1234 },
  { runId: 'r1', path: 'tasks/proto-x/i.html', kind: 'proto', size: 2345 },
]

export const DEFAULT_PORT = 8799

const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '::1'])

// Same DNS-rebind/cross-origin check as backend/adapter/originguard.go's localhostOnly, so a stub
// that already binds loopback-only doesn't also present as an off-host-reachable service to a
// browser that resolves an attacker-controlled hostname to 127.0.0.1.
function hostnameOf(value) {
  if (!value) return ''
  const withScheme = value.includes('://') ? value : `http://${value}`
  try {
    return new URL(withScheme).hostname.toLowerCase()
  } catch {
    return ''
  }
}

function isLocalhostOnly(req) {
  const host = hostnameOf(req.headers.host ?? '')
  if (!host || !LOOPBACK_HOSTS.has(host)) return false
  const origin = req.headers.origin
  if (origin) {
    const originHost = hostnameOf(origin)
    if (!originHost || !LOOPBACK_HOSTS.has(originHost)) return false
  }
  return true
}

// Create a stub studio. `artifacts` overrides the canned fixture; `denyArtifacts: true` makes the
// artifact route answer 401 the way a real studio without a browser session does.
export function createStubStudio({ artifacts = DEFAULT_ARTIFACTS, denyArtifacts = false } = {}) {
  const server = http.createServer((req, res) => {
    const send = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify(body))
    }

    if (!isLocalhostOnly(req)) {
      send(403, { error: 'cross-origin blocked' })
      return
    }

    const url = new URL(req.url ?? '/', 'http://127.0.0.1')
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
      new Promise((resolvePromise, reject) => {
        server.once('error', reject)
        server.listen(port, '127.0.0.1', () => {
          server.removeListener('error', reject)
          // A listening-socket error AFTER startup (e.g. the OS drops the interface) would
          // otherwise be an uncaught exception and take down the process — attached only once
          // listen() has actually succeeded, so a startup failure is reported by `reject` above
          // alone, not this handler too.
          server.on('error', (err) => console.error(`stub-studio: ${err.message}`))
          resolvePromise(server.address().port)
        })
      }),
    close: () => new Promise((resolvePromise) => server.close(resolvePromise)),
  }
}

// `node scripts/stub-studio.mjs` — print the listening line and stay up until Ctrl-C.
if (process.argv[1] && resolvePath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const port = Number(process.env.PORT ?? String(DEFAULT_PORT))
  const stub = createStubStudio()
  try {
    const bound = await stub.listen(port)
    console.log(`stub studio listening on http://127.0.0.1:${bound}`)
  } catch (err) {
    const hint = err?.code === 'EADDRINUSE' ? ` — port ${port} is in use, set PORT=<other> to pick a different one` : ''
    console.error(`stub-studio: failed to start${hint}: ${err.message}`)
    process.exit(1)
  }
}
