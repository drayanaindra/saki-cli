// Which backend serves which path.
//
// The studio is TWO servers, and the web UI splits its traffic between them via a Vite proxy
// (frontend/vite.config.ts → frontend/src/proxy-routes.ts): most `/api/*` and all of `/events/`
// go to the **Go backend** on :8788, and only what Go has not taken over falls through to the
// Express server on :8787.
//
// The CLI must split the SAME way. It originally sent everything to Express, which meant it was
// talking to the older TS implementations while the UI talked to Go — two separate run stores. A
// build started in the UI was invisible to `saki runs`, and `saki build` on a PRD the UI was
// already building spawned a SECOND concurrent build, because the dedupe gate it consulted was in
// the wrong process. (Verified: same PRD, 3 concurrent runs across the two backends.)
//
// This table is a port of `frontend/src/proxy-routes.ts`. Keep them in step: a route Go takes over
// there must move here too, or the CLI silently drops back to a stale implementation.

export const DEFAULT_TS_URL = 'http://localhost:8787'
export const DEFAULT_GO_URL = 'http://127.0.0.1:8788'

export type Backend = 'go' | 'ts'

// Ported verbatim from proxy-routes.ts (order preserved; first match wins, as in the proxy).
const GO_ROUTES: RegExp[] = [
  /^\/api\/branch($|\?)/,
  /^\/api\/run($|\?)/,
  /^\/api\/runs($|\?)/,
  /^\/api\/run\/[^/]+\/stop$/,
  /^\/events\//,
  /^\/api\/branches($|\?)/,
  /^\/api\/switch-branch($|\?)/,
  /^\/api\/create-mr($|\?)/,
  /^\/api\/merge-to-main($|\?)/,
  /^\/api\/doctor($|\?)/,
  /^\/api\/roadmap($|\?)/,
  /^\/api\/workitems($|\?)/,
  /^\/api\/prd($|\?)/,
  /^\/api\/review($|\?)/,
  /^\/api\/review-state($|\?)/,
  /^\/api\/lock-prd($|\?)/,
  /^\/api\/slice-meta($|\?)/,
  /^\/api\/blockers($|\?)/,
  /^\/api\/resolve-blocker($|\?)/,
  /^\/api\/roadmap\/plan-start($|\?)/,
  /^\/api\/roadmap\/plan-ship($|\?)/,
  /^\/api\/roadmap\/plan-attach($|\?)/,
  /^\/api\/proto\//,
  /^\/api\/screenshots($|\?)/,
  /^\/api\/screenshot($|\?)/,
  /^\/api\/profiles($|\?)/,
  /^\/api\/env-state($|\?)/,
  /^\/api\/pick-folder($|\?)/,
  /^\/api\/new-project($|\?)/,
]

// Which server owns `path`.
//
// `expressConfigured` is THE EXTRACTION SEAM (I8). The two-server split is a *pipeline-studio* concern:
// the studio runs Express AND Go, so the CLI must divide traffic exactly as the UI's Vite proxy does.
// The standalone `saki-cli` has only the Go backend — there is no second server to split against — so
// when Express is not configured every path goes to Go and the table above is not consulted at all.
//
// Concretely: when this repo extracts backend/ + apps/cli, `expressConfigured` is always false, and
// the whole GO_ROUTES table plus the cross-workspace lockstep test in routes.test.ts get DELETED with
// it. That is why the flag is a required argument rather than an optional one defaulting to true —
// every caller must state which world it is in, so the dead branch is obvious when it is time to cut.
//
// When Express IS configured the behavior is byte-for-byte what it was before: anything not claimed by
// Go stays on Express — notably `/api/health`, `/api/session`, and the artifact routes.
//
// `/api/health` is the one path BOTH servers answer, and it must stay `ts` here (the Vite proxy
// routes it the same way). Moving it to `go` would not "fix" anything — it would just blind the
// probe to the other server. `saki status` therefore asks for it by BACKEND, via
// StudioClient.health(), instead of by path.
export function backendFor(path: string, expressConfigured: boolean): Backend {
  if (!expressConfigured) return 'go'
  return GO_ROUTES.some((re) => re.test(path)) ? 'go' : 'ts'
}
