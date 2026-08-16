# EXECUTION PLAN: I2 — `saki artifacts` companion orchestrator (stub behind a port)

**Date:** 2026-08-16
**Item:** I2
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (new files only — stub server + real-HTTP test; no CLI source, no DB, no auth
change; the real studio's IDOR session guard is untouched)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A — backend/CLI-only. `saki-cli` is headless by design (`docs/project-context.md`
§ Deliberate non-goals: "No UI, no browser"); there is no web page for a Gherkin flow to describe.
**Source PRD:** N/A (standalone plan-track item — roadmap "What" is the spec)
**Prior slices:** N/A — standalone
**Appetite:** ~4 agent tasks (test file, stub server, 3 doc/README edits, /qa flip)
**Kill-if:** N/A (no §5 metric on a plan-track item)

## Problem Statement

`saki artifacts <runId>` is not exercisable end-to-end in this repo: its artifact route
(`GET /api/runs/:id/artifacts`) lives only in the **out-of-repo** Express studio (`apps/server`,
`:8787`, reached via `SAKI_STUDIO_URL`), and the Go backend deliberately serves no artifact route.
The roadmap blocks I2 until that dependency is identified and either vendored, stubbed behind a
port, or documented as an external requirement. The dependency is now identified (and already
documented), but the item stays Blocked because the command still cannot be run end-to-end here.
This plan **stubs the companion orchestrator behind a port** — a dependency-free Node HTTP server
in this repo that serves the artifact route (plus `/api/health` and `/api/session` so `saki
status` works), making `saki artifacts` exercisable end-to-end and testable against a real socket.

---

## Concrete Example Output

An operator starts the stub and drives the CLI against it, all inside this repo:

```bash
$ node scripts/stub-studio.mjs                 # binds 127.0.0.1:8799 (PORT overrides)
stub studio listening on http://127.0.0.1:8799

$ SAKI_STUDIO_URL=http://127.0.0.1:8799 saki artifacts r1 --json
{"artifacts":[{"runId":"r1","path":"tasks/prd-x.md","kind":"prd","size":1234},{"runId":"r1","path":"tasks/proto-x/i.html","kind":"proto","size":2345}]}

$ SAKI_STUDIO_URL=http://127.0.0.1:8799 saki artifacts r1        # human mode: one JSON per artifact
{"runId":"r1","path":"tasks/prd-x.md","kind":"prd","size":1234}
{"runId":"r1","path":"tasks/proto-x/i.html","kind":"proto","size":2345}

$ SAKI_STUDIO_URL=http://127.0.0.1:8799 saki status               # the stub stands in for Express too
backend   http://127.0.0.1:8799
reachable yes (stub-studio)
studio    http://127.0.0.1:8799
reachable yes (stub-studio)
devMode   on
auth      authenticated
runs      allowed
```

This is exactly what the CLI already emits for those payloads (`src/commands/artifacts.ts:36-45`,
`src/commands/status.ts:104-120`) — the stub simply makes a real server answer instead of exit 3.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Write `src/commands/artifacts.test.ts` (TDD RED): boot the stub via `createStubStudio()` on an ephemeral port, build a `StudioClient({ baseUrl: 'http://127.0.0.1:'+port })` via `makeCtx`, and drive `cmdArtifacts` / `cmdStatus` against the REAL HTTP socket. Cases: (a) `cmdArtifacts(ctx,'r1')` → `EXIT.OK` and captured JSON contains the canned `artifacts` array; (b) stub with `{ artifacts: [] }` → human output `no artifacts recorded for r1`; (c) stub with `{ denyArtifacts: true }` → `EXIT.AUTH_REQUIRED` and stderr contains `requires a browser session`; (d) `cmdStatus(ctx)` → `EXIT.OK` and output contains `stub-studio` and `devMode   on`. Import the factory from `../../scripts/stub-studio.mjs` (test files are excluded from `tsc`, so no `.d.ts` needed — `tsconfig.json` `exclude`). | `src/commands/artifacts.test.ts` (new) | MED | the file itself (Test-First — this step is the RED) | Yes |
| 2 | Implement `scripts/stub-studio.mjs` (TDD GREEN): `node:http` only, zero deps. Export `createStubStudio({ artifacts?, denyArtifacts? })` returning `{ server, listen(port=0) -> Promise<port>, close() }`; the handler routes `GET /api/health` → `{ok:true, service:'stub-studio'}`, `GET /api/session` → `{devMode:true, authenticated:true, claudeCodeAccess:true}`, `GET /api/runs/<id>/artifacts` → `{artifacts}` (or 401 `{error:'unauthenticated'}` when `denyArtifacts`), anything else → 404. Bind loopback only (`server.listen(port, '127.0.0.1', …)`). Add a `main` block (guarded by `import.meta.url` vs `process.argv[1]`) that reads `PORT` (default `8799`) and prints the listening line. Export a `DEFAULT_ARTIFACTS` array (`runId`/`path`/`kind`/`size`) as the canned fixture. | `scripts/stub-studio.mjs` (new) | MED | step 1's `src/commands/artifacts.test.ts` (Test-First — this step turns it GREEN) | Yes |
| 3 | Update docs to name the stub as the local exercise path: in `docs/cli-reference.md` § Known limitations #1 (lines 268-273) add "to exercise it without the studio, run `node scripts/stub-studio.mjs` and point `SAKI_STUDIO_URL` at it"; in `docs/saki-cli-agent-guide.md` §7.1 (lines 524-526) add the same one-liner; in `docs/cli-reference.md:31` amend the `SAKI_STUDIO_URL` row so "adds exactly two things" notes the stub serves both; reword the README "Not built yet" bullet (line 103) from "needs a companion orchestrator" to "session-gated via the studio; local stub `scripts/stub-studio.mjs` serves the route". | `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md`, `README.md` | LOW | `grep -rn "stub-studio" docs/cli-reference.md docs/saki-cli-agent-guide.md README.md` (existing-suite; doc change) | Yes |

> The roadmap flip Blocked → In-progress (Step 0.6) is already done; the flip to **Shipped** is
> `/qa`'s job when every criterion below passes — the `**Item:** I2` stamp in this header is the hook.

---

## User Role Coverage

Single-operator local tool (`docs/project-context.md` § Deliberate non-goals: local single-operator
only). The "role" is the operator (human or agent) running the CLI; the stub is a dev/test double,
not a second user.

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Operator (human or agent) | `saki artifacts <runId>` against the stub → exit 0 + artifact list; `saki status` against the stub → two-server report | Read artifacts from the REAL studio via CLI (that stays exit 6 — the studio's IDOR session gate is preserved and documented) | stub is an open local double (loopback bind only); the real studio's gate is untouched | terminal, `SAKI_STUDIO_URL=http://127.0.0.1:8799` |

---

## Plan Wiring

### Flow 1: `saki artifacts <runId>` against the stub
```
cmdArtifacts(ctx, runId)                                  (src/commands/artifacts.ts:15)
  → ctx.client.expressConfigured === true                 (src/client.ts:112, from SAKI_STUDIO_URL)
  → StudioClient.get('/api/runs/<id>/artifacts')          (src/client.ts:195)
  → backendFor(path, true) → 'ts'                         (src/routes.ts:76; artifact path is not a Go route)
  → originFor('ts') → baseUrl                             (src/client.ts:123-137)
  → GET http://127.0.0.1:<stub-port>/api/runs/<id>/artifacts
  → stub-studio handler → 200 { artifacts: DEFAULT_ARTIFACTS } | 401 { error }
  → emit({ artifacts }, …)                                (src/commands/artifacts.ts:36-45) → exit 0
```

### Flow 2: `saki status` against the stub (two-server mode)
```
cmdStatus(ctx)                                            (src/commands/status.ts:41)
  → probe('ts'): health() → GET /api/health → { ok:true, service:'stub-studio' }
  → probe('go'): health() → GET /api/health on goUrl      (baseUrl, since only baseUrl is set)
  → session = get('/api/session') → { devMode, authenticated, claudeCodeAccess }
  → emit(StatusReport, …)                                 (src/commands/status.ts:81-120) → exit 0
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| — none — this plan is additive-only | — | — | — | — |

**Forward compatibility:** additive-only. No existing CLI source, Go backend route, endpoint
response shape, config key, event payload, or npm script is modified or removed. The real studio's
`GET /api/runs/:id/artifacts` contract is untouched (the stub mirrors `{ artifacts: [...] }` /
`{ error }`, which is what `cmdArtifacts` already reads).

---

## Migration Checklist

None — no DB schema change (`None — no schema change`).

---

## Branch Points (pre-declared)

- Step 2: Stub port default → **decide** `8799` (clear of the real studio `:8787` and backend
  `:8788`; `PORT` overrides). Reversible — record `AUTO-RESOLVED: stub default port → 8799 — the
  repo's other ports are 8787/8788/5180, and the CLI reads the URL, not the port number`.
- Step 1: If vitest cannot import `../../scripts/stub-studio.mjs` (`.mjs` from a `.ts` test) →
  **decide** move the factory into the test file's own `src/` helper and have the script import it;
  reversible (only test plumbing moves). Expected not to fire — vitest resolves `.mjs` natively.

---

## Unknowns

1. [LOW] Vitest importing a `.mjs` module from `scripts/` in a `.ts` test. → resolution: standard
   vitest/esbuild behavior; verified empirically by `npm test` in step 2. Not a blocking unknown.

---

## No-Gos

- Will NOT weaken or route around the real studio's IDOR session guard — `cmdArtifacts`'s exit-6
  path (`src/commands/artifacts.ts:48-54`) stays exactly as-is; the stub's `denyArtifacts` mode only
  lets the test exercise it against a real socket.
- Will NOT vendor `apps/server` or any part of the pipeline-studio into this repo.
- Will NOT bind the stub to anything but loopback (`127.0.0.1`).
- Will NOT add any npm dependency (stub uses `node:http` only).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Operator role covered (single-operator local tool — the only role; repo convention)
- [x] Full call chain for both flows in Plan Wiring
- [x] The real-studio auth gate is preserved and documented, not bypassed
- [x] Edge cases per role: no studio configured (exit 3, pre-existing), studio 401s (exit 6, tested
      via `denyArtifacts`), empty artifacts (human `no artifacts recorded`), stub down

**Database & Migrations**
- [x] No schema change (Migration Checklist = none)

**API Layer**
- [x] Stub response shapes named: `{ ok, service }`, `{ devMode, authenticated, claudeCodeAccess }`,
      `{ artifacts }`, `{ error }` (`scripts/stub-studio.mjs`)
- [x] HTTP methods + paths written out: `GET /api/health`, `GET /api/session`,
      `GET /api/runs/:id/artifacts`
- [x] No auth middleware on the stub (open local double, loopback bind) — documented

**Service / Business Logic**
- [x] Stub functions named: `createStubStudio`, handler closure, `listen`, `close` (`scripts/stub-studio.mjs`)
- [x] No side effects (in-memory canned data; no file/network/DB writes)
- [x] Error paths: 401 (`denyArtifacts`), 404 (unknown route), connection refused → CLI exit 3 (pre-existing)

**Frontend**
- [x] N/A — headless CLI, no UI (project-context § Deliberate non-goals)

**Compatibility & Consumers**
- [x] Compatibility & Consumers = `None — additive only`; forward-compat answered
- [x] Prior slices: N/A — standalone

**Plan Wiring**
- [x] Both flows wired end-to-end with file:function anchors
- [x] No vague "update X" steps
- [x] No "add endpoint" without method+path+shape

---

## Evidence Ledger

### Blocking (must be empty to present — each row a binary, cited predicate)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|
| — | — | None — all anchors verified, all targets have creating steps, no unchecked items on state-changing steps, no unknowns above LOW | self-audit |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 3 | Doc wording is prose — a grep, not a behavior test | step 3 Test column cites `grep -rn "stub-studio" …` |

### Anchor verification (Step 4a walk)

| Reference | Class | Evidence |
|-----------|-------|----------|
| `src/commands/artifacts.ts:15,23-29,36-45,48-54` | anchor | read (cmdArtifacts, expressConfigured guard, emit, exit-6 path) |
| `src/client.ts:103-114,195,123-137` | anchor | read (expressConfigured, get, originOf) |
| `src/routes.ts:76` | anchor | read (artifact path → `ts`) |
| `src/routes.test.ts:36` | anchor | grep/read (`/api/runs/abc/artifacts` → `ts`) |
| `src/commands/status.ts:41-120` | anchor | read (probe, session, emit) |
| `src/index.test.ts:319,341-346,407-411` | anchor | read (existing fetch-stub coverage; new test adds real socket) |
| `src/exit.ts:8-16` | anchor | read (OK 0 / UNREACHABLE 3 / AUTH_REQUIRED 6) |
| `vitest.config.ts` `include` + `tsconfig.json` `exclude` | anchor | read (tests under `src/**/*.test.ts`, excluded from `tsc`) |
| `package.json` `files` incl. `scripts` | anchor | read (stub ships; no dependency added) |
| `docs/cli-reference.md:31,268-273` | anchor | read (SAKI_STUDIO_URL row + Known limitation #1) |
| `docs/saki-cli-agent-guide.md:524-526` | anchor | read (§7.1 artifacts limitation) |
| `README.md:103` | anchor | read ("Not built yet" artifacts bullet) |
| `scripts/stub-studio.mjs` | target | anchor parent `scripts/` exists; creating step 2; unique export `createStubStudio` |
| `src/commands/artifacts.test.ts` | target | anchor parent `src/commands/` exists; creating step 1; unique describe name |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] `npm run typecheck` passes (test files are excluded from `tsc` per `tsconfig.json` `exclude`, so the `.mjs` import in the test needs no `.d.ts` — verify this holds)
- [x] `npm test` passes — `src/commands/artifacts.test.ts` (4 cases) green against a real HTTP socket
- [x] Manual: `node scripts/stub-studio.mjs` prints `stub studio listening on …:8799`
- [x] Manual: `SAKI_STUDIO_URL=http://127.0.0.1:8799 saki artifacts r1 --json` exits 0 and prints
      `{"artifacts":[…]}` with the canned entries (concrete example above)
- [x] Manual: `SAKI_STUDIO_URL=http://127.0.0.1:8799 saki artifacts r1` (human) prints one JSON line
      per artifact; with an empty-artifact stub run, prints `no artifacts recorded for r1`
- [x] Manual: `SAKI_STUDIO_URL=http://127.0.0.1:8799 saki status` exits 0 and shows both servers
      `yes (stub-studio)`, `devMode on`, `runs allowed`
- [x] `grep -rn "stub-studio" docs/cli-reference.md docs/saki-cli-agent-guide.md README.md` finds the
      stub named in all three (docs updated)
- [x] `/qa` flips I2 in `tasks/roadmap.md` to **Shipped** (all of the above pass)

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---

Status: [ ] Draft  [ ] Annotated  [x] Approved  [ ] In Progress  [x] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
