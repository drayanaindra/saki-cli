# EXECUTION PLAN: Harden stopDaemon PID ownership and verify F1 restart rehydration

**Date:** 2026-08-22
**Blocking items:** 0 (must be 0 to present — see Evidence Ledger)
**Risk Score:** HIGH
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only; no UI)
**Source PRD:** `tasks/prd-daemon-lifecycle-unix-socket-transport.md` §9 AC 2.5, AC 3.2, AC 3.5 and §10 rules 5, 7
**Prior slices:** `tasks/daemon-lifecycle-unix-socket-transport-slice1-plan.md`, `tasks/daemon-lifecycle-unix-socket-transport-slice2-plan.md` read; shipped implementation is the current source of truth
**Appetite:** ~2 agent tasks — recut if step count exceeds this
**Kill-if:** N/A — targeted F1 blocker fix; do not broaden scope

## Problem Statement

When a daemon PID is recycled by the OS, `stopDaemon()` currently sees a live PID and sends `SIGTERM` without proving that the process is still saki-backend. This can terminate an unrelated process. F1 also lacks isolated evidence that `saki backend stop && saki backend start` preserves an in-flight run through Inv-2 rehydration.

---

## Concrete Example Output

Today, a state file containing `{"pid":4242,"goUrl":"http://127.0.0.1:19999"}` can cause `stopDaemon()` to send `SIGTERM` to PID 4242 even when the health probe returns connection refused. After this change, the same test records no signal, returns `not-running`, and removes only the stale state record. In the isolated restart check, the same `runId` returned by `POST /api/run` is present in `GET /api/runs` after stop/start, with its journal-backed running/terminal status intact.

```text
PID-recycling test: signals = [], result = "not-running", state file absent
Restart check: before runId = "run-123", after GET /api/runs contains "run-123"
```

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Change `stopDaemon()` to call the existing bounded `probeBackendHealth()` against the recorded `goUrl` after `isAlive()` and before `SIGTERM`; if health fails, compare-and-release only the captured state and return `not-running` without signaling. Add `it('does not signal a recycled PID whose backend is unhealthy')` beside the existing stop tests, using `stoppableDaemon()` and a rejecting `fetch` stub. Update the existing `stops a live daemon whose backend has stopped answering health` test to assert the new safety behavior, and add a healthy-fetch stub to the SIGTERM/SIGKILL/replacement tests so they continue proving owned-daemon shutdown. Preserve the healthy stop, SIGKILL escalation, spawn-lock, and replacement-state behavior. | `src/daemon.ts:527-560`; `src/daemon.test.ts:105-263` | HIGH | Test-first: update/add the named `src/daemon.test.ts` cases; then `npm test -- --run src/daemon.test.ts`; assert unhealthy live PID returns `not-running` with `signals=[]` and state removed, while healthy live PID still records `SIGTERM` or `SIGTERM,SIGKILL` and replacement state survives. | Yes |
| 2 | Execute isolated AC 3.5 against the real CLI and Go binary: build both artifacts; allocate temporary `SAKI_RUNS_DIR` and `SAKI_DAEMON_STATE_DIR`; choose a free non-default `PORT`; start a long-lived Go-owned run through `POST /api/run`; invoke `saki backend stop`, then `saki backend start`; query `GET /api/runs`; assert the original `runId` remains present and the journal files remain readable; terminate only the isolated replacement and remove temporary directories. Record command output and pass/fail in the session result, not in production code. | `dist/index.js`; `dist/saki-backend`; `backend/cmd/server/main.go:64-71`; `backend/adapter/http.go:475-522`; `backend/infra/journal.go:47-60` | HIGH | `npm run build`; `npm run backend:build`; isolated shell/curl/node HTTP sequence; verify `/api/health`, `POST /api/run` 201 `{runId}`, stop exit 0, start exit 0, and `GET /api/runs` contains the same `runId`. | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Operator / CLI caller | Run `saki backend stop`, `saki backend start`, and inspect `saki runs --json` | Signal a process not proven to be the tracked healthy backend | Loopback/OriginGuard at the Go API; CLI state directory ownership validation | Headless CLI; no UI |

Edge cases: missing state, lock sentinel, dead PID, recycled PID, unhealthy live backend, SIGTERM ignored, replacement state written while stopping, and restart with journaled in-flight run are covered by existing tests plus Step 1/2.

---

## Plan Wiring

### Flow 1: PID-safe backend stop

```text
`saki backend stop` (`src/commands/backend.ts:11-15`)
  → `stopDaemon(ctx.env)` (`src/daemon.ts:527`)
  → `readDaemonRecord()` → `isAlive(pid)` → `probeBackendHealth(goUrl)` (`src/daemon.ts:334-350`)
  → SIGTERM, optional SIGKILL, `releaseState()`
  → `backend.state.json` ownership record
```

### Flow 2: Isolated restart and rehydration

```text
`POST /api/run` (`backend/adapter/http.go:475-522`)
  → `RunService.Spawn()` (`backend/usecase/spawn.go:58-99`)
  → `FileJournal.Write()` (`backend/infra/journal.go:81-94`)
  → `<SAKI_RUNS_DIR>/go/<runId>.json` + process journal

`saki backend stop` → SIGTERM to the isolated `saki-backend`
`saki backend start` → detached real `saki-backend`
  → `usecase.Rehydrate()` (`backend/cmd/server/main.go:64-71`)
  → `GET /api/runs` (`backend/adapter/http.go:555-558`)
  → same `runId` and journal-backed status
```

No database, frontend, or authenticated user route is involved.

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `stopDaemon(env, options)` (`src/daemon.ts:527`) | exported function behavior | `grep -RIn "stopDaemon(" src` → `src/daemon.ts`, `src/commands/backend.ts:12`, tests | updated in step 1; signature and return values remain unchanged | Step 1 preserves `not-running`/`stopped` contract and existing signal escalation |
| `probeBackendHealth(goUrl, timeoutMs, fetchImpl)` (`src/daemon.ts:334`) | existing helper reuse | `grep -RIn "probeBackendHealth(" src` → `src/daemon.ts`, `src/commands/backend.ts:32`, tests | unaffected; existing signature reused | Step 1 calls it with the recorded `goUrl` only |
| `DaemonState.goUrl` / `backend.state.json` | wire format read | `grep -RIn "goUrl\|backend.state.json" src backend` → daemon lifecycle and Go state writers/readers | unaffected; no field or shape change | Step 1 uses existing field as ownership probe target |
| `POST /api/run`, `GET /api/runs` | API behavior | `grep -RIn "/api/run\|/api/runs" backend src e2e` → existing handlers, client, tests | unaffected; Step 2 is verification-only | No endpoint change |
| `SAKI_RUNS_DIR`, `SAKI_DAEMON_STATE_DIR`, `PORT` | environment controls | `grep -RIn "SAKI_RUNS_DIR\|SAKI_DAEMON_STATE_DIR\|PORT" src backend scripts e2e` → existing readers | unaffected; isolated verification-only use | Temporary values only; no production defaults changed |

**Forward compatibility:** additive regression coverage plus behavior hardening; no schema/API/deploy-order change.

---

## Migration Checklist

No database schema changes. The daemon state file and journal files are existing filesystem contracts; this plan does not change their wire format.

| Change | Table | Column/Index | Migration File | Command |
|---|---|---|---|---|
| None | — | — | — | — |

---

## Branch Points (pre-declared)

- Step 1: If the health probe is unavailable or returns non-healthy → treat the record as stale, release only the captured state, and do not signal; this is the reversible safety-first choice required by PID-recycling protection.
- Step 2: If the default port is occupied → use a temporary non-default `PORT`; do not kill or alter the unrelated listener. Record `AUTO-RESOLVED: occupied default port → isolated non-default port — verification must not affect unrelated processes`.
- Step 2: If the spawned run exits before restart → use a controlled long-lived local command/profile so the criterion remains an in-flight rehydration check; do not weaken the assertion to a terminal-only record.
- Any action that would delete or signal a process outside the temporary state/run directory → BLOCKED; the isolation boundary and cleanup must not be crossed.

---

## Unknowns (must be <= 2)

None. Existing code supplies the health probe, state isolation, non-default port, journal root, run endpoint, and startup rehydration anchor.

---

## No-Gos

- Will NOT identify processes by command-line inspection or add platform-specific process identity code.
- Will NOT kill a PID when health ownership is not proven.
- Will NOT change the state-file wire format, exit-code contract, backend API, or Inv-1/Inv-2 journal ownership.
- Will NOT use the repository's destructive `scripts/free-e2e-ports.sh` for the isolated verification.
- Will NOT add a permanent integration harness for one manual criterion unless the verification exposes a reproducible regression requiring committed automation.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Operator role is listed; this is a headless CLI/backend task with no UI role.
- [x] Full stop and restart call chains are in Plan Wiring.
- [x] Loopback/OriginGuard and private state-directory guards are identified.
- [x] PID, health, signal, replacement, and journal edge cases are listed.

**Database & Migrations**
- [x] No model or database field changes; migration table explicitly says none.
- [x] No migration command applies.
- [x] No breaking schema change exists.

**API Layer**
- [x] Existing HTTP request/response shapes are named: `POST /api/run` `{runId}` and `GET /api/runs` run list.
- [x] Existing handler path and functions are cited.
- [x] Loopback/OriginGuard boundary is cited.

**Service / Business Logic**
- [x] `stopDaemon`, `probeBackendHealth`, `RunService.Spawn`, `FileJournal.Write`, and `Rehydrate` are named with paths.
- [x] Side effects are listed: signals, state release, journal persistence, rehydration.
- [x] Failure paths are covered: missing/dead/recycled PID, unhealthy peer, ignored SIGTERM, occupied port, premature run exit.

**Frontend**
- [x] No frontend/UI changes; backend-only/headless task explicitly noted.

**Compatibility & Consumers**
- [x] Every changed or reused existing surface has a grep-backed consumer row.
- [x] Forward compatibility is additive behavior hardening with unchanged contracts.

**Plan Wiring**
- [x] Stop and restart flows are wired end-to-end with exact functions and files.
- [x] No vague implementation step remains.

---

## Evidence Ledger

### Blocking (must be empty to present — each row a binary, cited predicate)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|---|---|---|
| — | — | None. All anchors verified, all targets have creating steps, all checklist items on state-changing steps satisfied, no unknowns above LOW. | `f1-stop-daemon-pid-recycling-and-rehydration-context.md`; cited source paths above |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|---|---|---|
| 2 | AC 3.5 remains a manual verification because it needs a real detached Go process and journal files; no permanent e2e harness is added in this targeted fix. | PRD AC 3.5 is explicitly `[manual]`; prior gate state records it blocked by port contention |
| — | Existing untracked gate file is pre-existing session state and is not part of the implementation scope. | `git status --short` at session start |

**Blocking: 0 → READY.**

---

## Success Criteria

- [ ] `npm test -- --run src/daemon.test.ts` passes, including the new `does not signal a recycled PID whose backend is unhealthy` test; the test asserts `signals=[]`, result `not-running`, and no state record remains.
- [ ] `npm run typecheck` passes.
- [ ] `npm run backend:test` and `cd backend && go vet ./...` pass; no Go production code is changed, but backend restart dependencies remain green.
- [ ] `npm test` passes.
- [ ] 🔲 MANUAL — Isolated AC 3.5: the operator runs the real CLI and Go binary in isolated temporary directories and verifies restart rehydration.
  1. Run `npm run build` and `npm run backend:build`.
  2. Create temporary directories for `SAKI_RUNS_DIR` and `SAKI_DAEMON_STATE_DIR`, choose an unused non-default `PORT`, export those values, and start the built backend without touching the default daemon state or port.
  3. Call `POST /api/run` with a controlled long-lived local command; verify HTTP `201` and capture the returned `runId`.
  4. Run `saki backend stop`; verify exit code `0` and that the isolated backend is no longer healthy.
  5. Run `saki backend start`; verify exit code `0` and `/api/health` returns HTTP `200` with `{\"ok\":true}`.
  6. Call `GET /api/runs`; verify HTTP `200`, the captured `runId` is present, and `<SAKI_RUNS_DIR>/go/<runId>.json` remains readable.
  7. Terminate only the isolated replacement backend, remove only the temporary directories, and verify the isolated daemon state directory is absent.
  Expected outcome: the same `runId` is present after stop/start with journal-backed running or terminal status, and no unrelated process, default state directory, or default port is changed.
  Command evidence: record the numbered commands, HTTP responses, exit codes, `runId`, journal path, and cleanup result in the session result.
  Playwright: `test.skip('AC 3.5 is headless CLI/backend verification; execute the numbered shell steps above')`.

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.
>
> Review notes (2026-08-22): Phase 1.5 manual criterion hardened with numbered shell steps, expected outputs, command evidence, and a headless Playwright stub. Step 1 explicitly updates the pre-existing unhealthy-live stop test so it cannot preserve the old signal-on-unhealthy behavior; healthy probe stubs are required for owned-daemon signal tests. AC 3.5 remains manual and must report `BLOCKED` if no real configured engine/profile can create the controlled in-flight run.

---
Status: [x] Draft  [ ] Annotated  [ ] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
