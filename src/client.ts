import { existsSync } from 'node:fs'
import { EXIT, CliError, type ExitCode } from './exit.js'
import { backendFor, DEFAULT_GO_URL, type Backend } from './routes.js'
import { socketFetch } from './daemon.js'

// HTTP client for the studio orchestrator (apps/server/src/index.ts, PORT default 8787).
//
// Local-only by design: DEV_MODE=1 makes resolveAuthenticated() short-circuit the session gate
// (apps/server/src/authGate.ts:71), so there is no token, cookie jar, or login flow here.

export const DEFAULT_BASE_URL = 'http://localhost:8787'

// Shown on any 401. The CLI holds no cookie by design, so DEV_MODE=1 is how a local studio lets it
// through the session gate; `saki status` reports whether the running studio has it.
export const AUTH_HINT =
  'the studio is gating this route — restart it with DEV_MODE=1 (check with `saki status`)'

export function resolveBaseUrl(env: Record<string, string | undefined>): string {
  const raw = env.SAKI_STUDIO_URL?.trim()
  return (raw && raw.length > 0 ? raw : DEFAULT_BASE_URL).replace(/\/+$/, '')
}

// The Go backend's base URL. The studio is two servers and the UI splits its traffic between them
// (see routes.ts); the CLI must split the same way or it talks to stale implementations with their
// own separate run store.
export function resolveGoUrl(env: Record<string, string | undefined>): string {
  const raw = env.SAKI_BACKEND_URL?.trim()
  return (raw && raw.length > 0 ? raw : DEFAULT_GO_URL).replace(/\/+$/, '')
}

export interface StudioClientOptions {
  baseUrl?: string
  // Override the Go backend independently. Defaults to SAKI_BACKEND_URL, then :8788.
  goUrl?: string
  fetchImpl?: typeof fetch
  env?: Record<string, string | undefined>
  socketPath?: string
}

export type Query = Record<string, string | number | boolean | undefined>

// Liveness path. BOTH servers serve it (apps/server/src/index.ts:320, backend/adapter/http.go), each
// naming itself in `service` — which is why it is reached by backend rather than by path.
export const HEALTH_PATH = '/api/health'

export interface HealthPayload {
  ok?: boolean
  service?: string
}

// Which env var re-points each backend. Named in the unreachable hint so a dead Go backend doesn't
// send the operator off to set SAKI_STUDIO_URL, which would not have changed anything.
const URL_ENV: Record<Backend, string> = { ts: 'SAKI_STUDIO_URL', go: 'SAKI_BACKEND_URL' }

// Map an HTTP status onto the CLI's exit contract. Anything not called out is a generic ERROR.
//
// 403 belongs with 401, not with ERROR: POST /api/run carries a SECOND gate beyond the session one
// — requireClaudeCodeAccess (index.ts:1248 → whitelist.ts) — which 403s unless DEV_MODE is on or the
// session's email is allowlisted. Reporting that as "unexpected failure" told the operator nothing.
export function codeForStatus(status: number): ExitCode {
  if (status === 401 || status === 403) return EXIT.AUTH_REQUIRED
  if (status === 404) return EXIT.NOT_FOUND
  if (status === 422) return EXIT.USAGE
  return EXIT.ERROR
}

// True when a status means "the studio refused you", which on a local studio almost always means it
// was started without DEV_MODE=1.
export function isAuthStatus(status: number): boolean {
  return status === 401 || status === 403
}

// Join an origin, a path and a query into a request URL. Shared by url() and the fetch path so a
// probe against an explicit backend builds its URL exactly the way a routed request does.
function joinUrl(origin: string, path: string, query?: Query): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v !== undefined) qs.set(k, String(v))
  }
  const suffix = qs.toString()
  return `${origin}${path}${suffix ? `?${suffix}` : ''}`
}

// Pull the server's own error string out of a body when it has one, so the user sees git's stderr
// or express's validation message rather than a bare status code.
function errorTextOf(body: unknown, fallback: string): string {
  if (body && typeof body === 'object') {
    const e = (body as { error?: unknown }).error
    if (typeof e === 'string' && e.trim()) return e
  }
  return fallback
}

export class StudioClient {
  readonly baseUrl: string
  readonly goUrl: string
  // Is there a SECOND server (Express) to talk to at all? (I8)
  //
  // False is the standalone shape the extracted `saki-cli` ships: the Go backend serves every route
  // the CLI needs, and nothing is ever sent to Express. True is the pipeline-studio dev studio, where
  // Express additionally serves /api/session and the artifact routes.
  //
  // Deliberately truthiness on `opts.baseUrl`, mirroring the `opts.baseUrl ? …` test on the line
  // below — `!== undefined` would diverge and treat `baseUrl: ''` as "configured, with an empty
  // origin", which is a URL that can only fail confusingly.
  readonly expressConfigured: boolean
  private readonly fetchImpl: typeof fetch
  private readonly socketFetchImpl?: typeof fetch

  constructor(opts: StudioClientOptions = {}) {
    const env = opts.env ?? process.env
    this.baseUrl = opts.baseUrl ?? resolveBaseUrl(env)
    // A test that passes only `baseUrl` gets BOTH pointed there, so a single stubbed origin keeps
    // working; real runs resolve the Go backend separately.
    this.goUrl = opts.goUrl ?? (opts.baseUrl ? opts.baseUrl : resolveGoUrl(env))
    this.expressConfigured = Boolean(opts.baseUrl) || (env.SAKI_STUDIO_URL?.trim() ?? '') !== ''
    this.fetchImpl = opts.fetchImpl ?? fetch
    // existsSync is the "no hang on dead socket" guard (AC 4.3): a state file can outlive the socket
    // it names (a crashed backend leaves both behind, an operator can delete one). Injecting a
    // dispatcher pinned to a path nothing is serving would fail every Go request; falling back to TCP
    // lets the pre-flight auto-start recover instead.
    if (opts.socketPath && existsSync(opts.socketPath) && !opts.fetchImpl && !this.expressConfigured && !env.SAKI_BACKEND_URL) {
      this.socketFetchImpl = socketFetch(opts.socketPath)
    }
  }

  // Base URL of a named backend, independent of any path. `health()` is the caller that needs this:
  // every other request derives its origin FROM the path.
  //
  // Asking for 'ts' when no Express is configured is a programming error, not a runtime condition —
  // there is no URL to return, and inventing the :8787 default would dial a server the operator never
  // said they were running. Callers that legitimately may or may not have Express (status, artifacts,
  // proto) check `expressConfigured` first.
  originOf(backend: Backend): string {
    if (backend === 'ts' && !this.expressConfigured) {
      throw new CliError(
        'no express studio is configured',
        EXIT.UNREACHABLE,
        'set SAKI_STUDIO_URL to point at one (the go backend alone serves every journey command)',
      )
    }
    return backend === 'go' ? this.goUrl : this.baseUrl
  }

  // Base URL for a specific path — Go or Express, matching the UI's proxy split (routes.ts). With no
  // Express configured every path resolves to Go, so this never reaches the throw above.
  originFor(path: string): string {
    return this.originOf(backendFor(path, this.expressConfigured))
  }

  url(path: string, query?: Query): string {
    return joinUrl(this.originFor(path), path, query)
  }

  // Raw request — used by the SSE stream, which needs the Response body rather than parsed JSON.
  async request(path: string, init?: RequestInit, query?: Query): Promise<Response> {
    return this.requestOn(backendFor(path, this.expressConfigured), path, init, query)
  }

  // Same, against an EXPLICIT backend rather than the one the path implies.
  private async requestOn(backend: Backend, path: string, init?: RequestInit, query?: Query): Promise<Response> {
    const origin = this.originOf(backend)
    const requestUrl = joinUrl(origin, path, query)
    try {
      const fetcher = backend === 'go' && this.socketFetchImpl ? this.socketFetchImpl : this.fetchImpl
      return await fetcher(requestUrl, init)
    } catch {
      if (backend === 'go' && this.socketFetchImpl) {
        try {
          return await this.fetchImpl(requestUrl, init)
        } catch {
          // Report the original transport failure below.
        }
      }
      // A rejected fetch here means the socket never opened (studio down, wrong port, wrong host).
      // Distinguishing that from an HTTP error is the whole point of exit code 3.
      throw new CliError(
        `cannot reach the studio at ${origin}`,
        EXIT.UNREACHABLE,
        `start it with ./run.sh (or set ${URL_ENV[backend]} if it listens elsewhere)`,
      )
    }
  }

  // Liveness probe against ONE named backend, bypassing the path→origin split.
  //
  // The split table (a port of the Vite proxy) can only send the shared /api/health to one server —
  // Express — so probing by path can never see the Go backend. `saki status` did exactly that and
  // printed a green studio while the backend serving runs, roadmap, prd, branch and proto was dead;
  // every command after it failed with exit 3 for no visible reason.
  async health(backend: Backend): Promise<HealthPayload> {
    return this.parse<HealthPayload>(await this.requestOn(backend, HEALTH_PATH), HEALTH_PATH)
  }

  private async parse<T>(res: Response, path: string): Promise<T> {
    let body: unknown
    try {
      body = await res.json()
    } catch {
      body = undefined
    }
    if (!res.ok) {
      throw new CliError(
        errorTextOf(body, `${path} failed with HTTP ${res.status}`),
        codeForStatus(res.status),
        // A 401 on a local studio almost always means it was started without DEV_MODE=1, which is
        // what exempts the CLI from the session gate (authGate.ts:71). Say so — the bare
        // "authentication required" the server returns leaves the operator with nowhere to go.
        isAuthStatus(res.status) ? AUTH_HINT : undefined,
      )
    }
    return body as T
  }

  async get<T>(path: string, query?: Query): Promise<T> {
    return this.parse<T>(await this.request(path, undefined, query), path)
  }

  async post<T>(path: string, body: unknown): Promise<T> {
    const res = await this.request(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    })
    return this.parse<T>(res, path)
  }

  // POST for the routes that report failure INSIDE a 200 body rather than via status:
  // /api/switch-branch (index.ts:811) and /api/create-mr (index.ts:831) both answer
  // `{ok:false, error}` with HTTP 200 when git or glab fails. Reading only the status would
  // report a failed operation as success, so `ok:false` becomes exit 5 here.
  async postOk<T extends { ok?: boolean; error?: string }>(path: string, body: unknown): Promise<T> {
    const out = await this.post<T>(path, body)
    if (out && out.ok === false) {
      throw new CliError(errorTextOf(out, `${path} was refused by the studio`), EXIT.REMOTE_FAILED)
    }
    return out
  }
}
