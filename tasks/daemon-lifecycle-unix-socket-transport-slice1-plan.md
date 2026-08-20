# EXECUTION PLAN: daemon-lifecycle-unix-socket-transport slice 1

**Date:** 2026-08-20
**Blocking items:** 0
**Risk Score:** MED
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only)
**Source PRD:** `tasks/prd-daemon-lifecycle-unix-socket-transport.md` § Slice 1
**Prior slices:** N/A — slice 1
**Appetite:** ~2 agent tasks (5 acceptance criteria; existing implementation reduces scope)
**Kill-if:** auto-start fails or hangs in more than occasional manual integration runs after Slices 1–2 (PRD §6)

## Problem Statement

When the Go backend is not running, I want the first ordinary `saki` command to start the colocated backend and wait for `/api/health`, so I can use the CLI without a manual pre-launch.

## Concrete Example Output

With `SAKI_BACKEND_BIN=/repo/dist/saki-backend` and no running backend, `saki runs` starts the detached backend, waits for `GET http://127.0.0.1:8788/api/health` to return `{\"ok\":true}`, prints the normal runs output, and writes stderr:

```text
daemon:autostart {result:"success",pid:43120}
[]
```

If the binary cannot be executed, the process returns exit code `3` and stderr includes:

```text
error: failed to start saki-backend: permission denied
```

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Add asynchronous child-process error handling in `spawnDaemon()` so `error` events from `node:child_process.spawn` map ENOENT to `saki-backend binary not found` and EACCES/ENOEXEC to an `EXIT.UNREACHABLE` `CliError`, while preserving detached `stdio: ignore`, `unref()`, and `SAKI_DAEMON_STATE_PATH`. | `src/daemon.ts:99-115` | MED | `src/daemon.test.ts`: spawn error-path tests using a controlled child-process stub; Test-First | Yes |
| 2 | Keep `waitForLiveness()` bounded at 10 seconds, direct to `goUrl`, and verify `src/index.ts:343-355` runs preflight only after command-local `--help`, skips all `backend` lifecycle commands, emits the `daemon:autostart` stderr event only for a newly spawned PID, and injects no socket behavior into the Slice 1 path. | `src/daemon.ts:69-88`, `src/index.ts:317-355`, `src/index.test.ts`, `src/daemon.test.ts` | MED | Existing `src/index.test.ts` plus timeout/health tests; Test-Along | Yes |

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Local operator / CI script | Run ordinary `saki` commands and receive auto-start or exit 3 diagnostics | Relax loopback binding or bypass binary/liveness failure | OS user owns the detached process; no network auth in this local CLI flow | Terminal command |

## Plan Wiring

### Flow 1: Ordinary CLI command auto-start

```text
Terminal `saki runs`
  → `src/index.ts:main()` parses command and `--help`
  → `ensureDaemon(env)` (`src/daemon.ts:168`)
  → `spawnDaemon(env)` (`src/daemon.ts:99`) → detached `saki-backend`
  → `waitForLiveness('http://127.0.0.1:8788')` (`src/daemon.ts:69`)
  → `StudioClient` → `GET /api/health` and command endpoint on Go backend
```

### Flow 2: Explicit backend URL

```text
Terminal `SAKI_BACKEND_URL=http://127.0.0.1:9999 saki runs`
  → `main()` skips auto-start (`src/index.ts:345`)
  → `StudioClient` resolves `SAKI_BACKEND_URL` (`src/client.ts:110-118`)
  → `GET /api/runs` on explicit URL
```

### Flow 3: Lifecycle command bypass

```text
Terminal `saki backend status|start|stop`
  → `main()` identifies `match.def.path[0] === 'backend'` (`src/index.ts:343`)
  → skips auto-start
  → `cmdBackend()` (`src/commands/backend.ts:6`) reports actual daemon state
```

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `spawnDaemon()` error behavior (`src/daemon.ts:99`) | private process helper | `grep -RIn 'spawnDaemon' src backend e2e` → only `src/daemon.ts` definitions/calls | updated in step 1 | Preserve `ChildProcess` PID and detached options; only normalize asynchronous errors |
| `ensureDaemon()` (`src/daemon.ts:168`) | exported function | `src/index.ts:347`, `src/commands/backend.ts:18`, `src/daemon.test.ts` | unaffected — return shape and successful path unchanged | none |
| `EXIT.UNREACHABLE` (`src/exit.ts:12`) | exit-code contract | `src/client.ts`, `src/daemon.ts`, command tests | unaffected — remains numeric code 3 | none |

**Forward compatibility:** additive-only error handling; no API/schema or deploy-order change.

## Migration Checklist

No database schema changes. No migration command applies.

## Branch Points (pre-declared)

- If `spawn()` emits `ENOENT`, return `EXIT.UNREACHABLE` with the existing build hint; this is a recoverable binary-not-found path.
- If `spawn()` emits `EACCES` or `ENOEXEC`, return `EXIT.UNREACHABLE` with the OS error text; do not retry or hang.
- If `waitForLiveness()` reaches its deadline, remove state and return `EXIT.UNREACHABLE`; do not extend the deadline or auto-restart.
- If a change would relax loopback-only binding or alter exit-code numbering, block it as a PRD invariant violation.

## Unknowns

None. Existing `undici`, fetch injection, Go health endpoint, and binary path seams are verified in the current tree.

## No-Gos

- Will NOT implement PID stale cleanup or concurrent lock refinements beyond the existing code; Slice 2 owns that behavior.
- Will NOT add `saki backend start|stop|status`; Slice 3 owns command behavior.
- Will NOT add or alter Unix-socket transport; Slice 4 owns it.
- Will NOT alter `backend/cmd/server/main.go` transport or loopback binding in this slice.

## Implementation Completeness Checklist

**User Coverage**
- [x] Local operator/CI role is listed.
- [x] Full terminal → CLI → daemon → health path is in Plan Wiring.
- [x] No network auth applies; loopback invariant is preserved.
- [x] Binary-not-found, permission, timeout, and help/lifecycle bypass edges are documented.

**Database & Migrations**
- [x] No model or schema fields change; no migration applies.
- [x] No breaking schema change exists.

**API Layer**
- [x] Existing `GET /api/health` response `{ok:true}` is named and located at `backend/adapter/http.go:69-74`.
- [x] No new endpoint or request schema is introduced.
- [x] Existing loopback origin guard remains unchanged.

**Service / Business Logic**
- [x] `spawnDaemon`, `waitForLiveness`, and `ensureDaemon` are named with exact paths.
- [x] Side effects are limited to detached process spawn and state cleanup.
- [x] Error paths cover ENOENT, EACCES/ENOEXEC, timeout, and unhealthy health responses.

**Frontend**
- [x] Backend/CLI-only; no frontend files or UI flow apply.

**Compatibility & Consumers**
- [x] Existing changed surfaces have consumer rows and verdicts.
- [x] Prior slices line is explicitly N/A for Slice 1.

**Plan Wiring**
- [x] All three major flows have exact call chains.

## Evidence Ledger

### Blocking (must be empty)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|---|---|---|
| | | | |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|---|---|---|
| — | All anchors verified, all targets have creating steps, all state-changing failure paths are covered, and no unknowns above LOW. | `tasks/daemon-lifecycle-unix-socket-transport-slice1-context.md`; direct reads of cited files; `npm test -- --run src/daemon.test.ts src/index.test.ts`; `npm run typecheck`; `npm run backend:test` | 

**Blocking: 0 → READY.**

## Success Criteria

- [x] `npm test -- --run src/daemon.test.ts src/index.test.ts` passes, including bounded liveness success/timeout and CLI dispatch tests.
- [x] Given a missing executable path, `ensureDaemon()` returns/throws `EXIT.UNREACHABLE` and stderr includes `saki-backend binary not found`; verified by `src/daemon.test.ts` spawn-error test.
- [x] Given a non-executable/invalid executable path, `ensureDaemon()` returns/throws `EXIT.UNREACHABLE` without hanging; verified by `src/daemon.test.ts` spawn-error test.
- [x] `npm run typecheck` passes.
- [x] `npm run backend:test` passes.

## Annotation Space

> Build-driven Slice 1 plan. Existing partial implementation is treated as the starting point; only missing acceptance behavior is added.

Status: [x] Approved  [x] In Progress
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
