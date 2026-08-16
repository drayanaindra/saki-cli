# I2 Context — `saki artifacts` companion orchestrator

**Item:** I2 · `saki artifacts` companion orchestrator (roadmap, Plan track, Improvement).
**Status:** Blocked → In-progress (flipped by /rplan Step 0.6).
**Date:** 2026-08-16

## The dependency (identified)

`saki artifacts <runId>` reads artifacts from the **out-of-repo Express studio** (`apps/server`,
port `:8787`, reached via `SAKI_STUDIO_URL`). The artifact routes exist ONLY in Express:

- CLI calls `GET /api/runs/:id/artifacts` — routed to the `ts` backend by `backendFor`
  (`src/routes.ts:76`; `/api/runs/abc/artifacts` is NOT the Go list route, `src/routes.test.ts:36`).
- The Go backend deliberately serves no artifact route; standalone it is `NullProxy`
  (`backend/infra/nullproxy.go:34-44`) and the CLI refuses before dialing
  (`src/commands/artifacts.ts:23-29`, exit 3 UNREACHABLE).
- The real route requires a browser session (IDOR guard, `index.ts:2195`) — `DEV_MODE` does not
  seed it, so even a configured studio 401s a CLI (`src/commands/artifacts.ts:48-54`, exit 6
  AUTH_REQUIRED). This guard is deliberate and must NOT be weakened.

## Why the item is blocked

The roadmap "What" names three acceptable resolutions: vendor the dependency, **stub it behind a
port**, or document it as an external requirement. The dependency is now identified and already
documented (`docs/cli-reference.md` § Known limitations #1, `docs/saki-cli-agent-guide.md` §7.1,
README "Not built yet"), but the item stays Blocked because the command still **cannot be exercised
end-to-end in this repo**. Docs alone don't satisfy the blocker.

- **Vendored** — rejected: `apps/server` is the whole pipeline-studio (Express + auth middleware +
  web UI), out of scope for a runtime-only repo (`docs/project-context.md` § Deliberate non-goals:
  "No UI, no browser").
- **Documented** — already done, insufficient on its own.
- **Stubbed behind a port** — the substantive resolution. A minimal dependency-free Node HTTP
  server in this repo that serves the artifact route (+ `/api/health`, `/api/session` so `saki
  status` works), letting `saki artifacts` be exercised end-to-end here.

## Resolution chosen

Build `scripts/stub-studio.mjs` (node:http, zero new deps, loopback bind, configurable port),
prove it end-to-end with a REAL-HTTTP vitest test (`src/commands/artifacts.test.ts`) that boots the
stub on an ephemeral port and drives `cmdArtifacts` / `cmdStatus` against it, and update the three
docs + README. The real studio's session gate is documented as the external requirement it stays.

## Key anchors verified

- `src/commands/artifacts.ts:15-57` — `cmdArtifacts` guard (exit 3), GET, emit `{artifacts}`.
- `src/client.ts:103-114, 195` — `expressConfigured`, `get()`, `originFor('ts')`.
- `src/routes.ts:76` — `backendFor` sends artifact paths to `ts`.
- `src/commands/status.ts:54-96, 132-140` — two-server status, session read, UNREACHABLE.
- `src/index.test.ts:319, 341-346, 407-411` — existing dispatch / exit-3 / exit-6 unit coverage
  (fetch-stub based; the new test adds the real-HTTP path).
- `src/exit.ts:8-16` — exit-code contract (OK 0, UNREACHABLE 3, AUTH_REQUIRED 6).
- `package.json` — `files` already ships `scripts/`; zero new deps needed (`node:http`).
- `vitest.config.ts` — unit tests under `src/**/*.test.ts`; test files excluded from `tsc`
  (`tsconfig.json` `exclude`), so importing a `.mjs` from a test needs no `.d.ts`.
- Prior plans (`tasks/*-slice*-plan.md`) establish the CLI/backend-only **Behavior Spec: N/A**
  convention — headless repo, no web page to describe.

## Consumers of the changed surface

Additive-only. No existing CLI source, signature, endpoint, field, config key, or event payload
changes. New files: `scripts/stub-studio.mjs`, `src/commands/artifacts.test.ts`; one additive npm
script `stub:studio`. Doc rewording touches no code consumer.

## Forward compatibility

Additive-only — the CLI, Go backend, and real studio contract are untouched.
