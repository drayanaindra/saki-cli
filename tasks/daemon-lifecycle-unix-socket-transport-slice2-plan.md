# EXECUTION PLAN: daemon-lifecycle-unix-socket-transport slice 2

**Date:** 2026-08-20
**Blocking items:** 0 (must be 0 to present — see Evidence Ledger)
**Risk Score:** MED
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only)
**Source PRD:** `tasks/prd-daemon-lifecycle-unix-socket-transport.md` § Slice 2
**Prior slices:** `tasks/daemon-lifecycle-unix-socket-transport-slice1-plan.md` read; shipped shape wins
**Appetite:** ~2 agent tasks (5 acceptance criteria) — recut if step count exceeds this
**Kill-if:** after Slices 1–2, auto-start fails or hangs in more than occasional manual integration runs under PRD §5.5

## Problem Statement

When multiple CLI invocations start while the backend is absent, I want one UID-scoped state-file lock to elect one spawner and every other invocation to reuse the healthy PID, so duplicate backend processes and stale state cannot block the CLI.

---

## Concrete Example Output

With `SAKI_DAEMON_STATE_DIR=/tmp/saki-501` and no backend running, two concurrent `ensureDaemon(env)` calls produce one child spawn and one final state file:

```text
spawn calls: 1
backend.state.json: {"pid":43120,"socketPath":null,"goUrl":"http://127.0.0.1:8788"}
```

With a stale state file containing `{"pid":999999,"socketPath":null,"goUrl":"http://127.0.0.1:8788"}`, the next ordinary command removes that file, spawns one fresh backend, and returns the fresh PID instead of reusing `999999`.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Harden `readDaemonState()`, `isAlive()`, `healthy()`, and the state-lock helpers so malformed state, `pid <= 0`, NaN/non-integer PID, `ESRCH`, `EPERM`, and an unhealthy HTTP endpoint are classified as stale; preserve UID-scoped `daemonStateDir()`, `backend.state.json`, `{pid,socketPath,goUrl}`, `O_CREAT|O_EXCL`, and the `pid:-1` sentinel. | `src/daemon.ts:27-67`, `src/daemon.ts:136-174` | MED | `src/daemon.test.ts`: malformed/non-numeric/zero PID, `isAlive` ESRCH/EPERM, and alive unrelated PID with unhealthy HTTP; Test-First | Yes |
| 2 | Rewrite `ensureDaemon()` loser/winner coordination so the winner writes the sentinel before `spawnDaemon()`, records the child PID before liveness polling, and a loser polls the claimed state for at most 10 × 100 ms, reuses only a healthy real PID, and reclaims the lock only after a dead/malformed sentinel; on spawn/liveness failure remove only the caller's claimed state and never leave a lock behind. Preserve direct Go health probing, explicit URL reuse, lifecycle-command bypass, `EXIT.UNREACHABLE`, and `daemon:autostart` semantics. | `src/daemon.ts:176-224`, `src/index.ts:343-349` | MED | `src/daemon.test.ts`: concurrent `ensureDaemon()` calls assert one spawn and unchanged state; stale PID, EPERM, NaN, and PID-recycled unhealthy backend assert cleanup + fresh spawn; timeout/error paths assert state removal. Test-First | Yes |

No Go, API, database, frontend, or transport files change in this slice.

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|---|---|---|---|---|
| Local operator / CI script | Run ordinary `saki` commands concurrently or after a crash and receive a healthy daemon without manual state cleanup | Create a second daemon for the same UID, bypass stale-state cleanup, relax loopback binding, or change exit-code semantics | OS user owns the UID-scoped state directory and detached process; no network auth in this local CLI flow | Terminal command |

---

## Plan Wiring

This is a CLI process-management flow with no UI, API schema, or database write. The backend remains a separate deployable reached through loopback HTTP.

### Flow 1: Existing healthy daemon reuse

```text
Terminal ordinary `saki <command>`
  → `src/index.ts:main()` parses command-local help and lifecycle bypass
  → `ensureDaemon(env)` (`src/daemon.ts`)
  → `readDaemonState()` → `isAlive(pid)` → `healthy(state)`
  → `GET http://127.0.0.1:8788/api/health`
  → `StudioClient` → ordinary Go API request
```

### Flow 2: Concurrent first-use auto-start

```text
Two terminal `saki <command>` processes
  → `ensureDaemon(env)`
  → `acquireStateLock(env)` → `open(statePath, 'wx')`
  → winner writes `{"pid":-1,...}` before `spawnDaemon(env)`
  → winner writes child PID → `waitForLiveness(goUrl)`
  → loser polls `readDaemonState()` and calls `healthy(state)`
  → both reuse the one healthy backend PID
```

### Flow 3: Stale-state recovery

```text
Terminal ordinary `saki <command>`
  → `ensureDaemon(env)`
  → `readDaemonState()` / `isAlive(pid)` / `healthy(state)` fails
  → `removeDaemonState(env)`
  → `acquireStateLock(env)` → `spawnDaemon(env)` → `writeState(pid)`
  → `waitForLiveness(goUrl)` → fresh state + ordinary Go API request
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `ensureDaemon(env)` (`src/daemon.ts:186`, callers `src/index.ts:347`, `src/commands/backend.ts:18`) | exported function behavior | `git grep -n 'ensureDaemon' -- src backend e2e` → `src/index.ts:347`, `src/commands/backend.ts:18`, `src/daemon.test.ts` | unaffected — return type, lifecycle bypass, explicit URL behavior, and exit-code contract remain unchanged | step 2 regression tests |
| `readDaemonState(env)` (`src/daemon.ts:36`, callers `src/index.ts:346`, `src/commands/backend.ts:17,23`, tests) | state-file parser | `git grep -n 'readDaemonState' -- src backend e2e` → listed callers | unaffected — valid `{pid,socketPath,goUrl}` wire format remains accepted; invalid states become stale by design | step 1 parser tests |
| `backend.state.json` / `SAKI_DAEMON_STATE_PATH` (`src/daemon.ts:25,119`; `backend/cmd/server/main.go:199-228`) | cross-process file contract | `git grep -nE 'backend\.state\.json|SAKI_DAEMON_STATE_PATH' -- src backend e2e docs` → CLI + Go server + tests | unaffected — keys and path remain unchanged; only transient sentinel and stale cleanup are clarified | steps 1–2 preserve wire format |
| `spawnDaemon()` (`src/daemon.ts:112`, private) | process helper | `git grep -n 'spawnDaemon' -- src backend e2e` → only `src/daemon.ts` | unaffected — detached child, ignored stdio, `unref()`, and env propagation remain | step 2 tests spawn count |
| `daemon:autostart` (`src/index.ts:348`) | stderr event | `git grep -n 'daemon:autostart' -- src` → `src/index.ts:348`, tests | unaffected — only a newly spawned positive PID emits it; healthy reuse remains silent | step 2 regression test |

**Forward compatibility:** tolerant-reader and additive behavior; no signature, endpoint, schema, or deploy-order change. Go already accepts the same state path and wire keys. No migration applies.

---

## Migration Checklist

No database schema changes. No migration file or command applies.

| Change | Table | Column/Index | Migration File | Command |
|---|---|---|---|---|
| None | N/A | N/A | N/A | N/A |

---

## Branch Points (pre-declared)

- Step 1: If `readDaemonState()` sees malformed JSON, non-integer/NaN PID, or `pid <= 0` → classify the state as stale and reclaim it; this is reversible and required by AC 2.4.
- Step 1: If `process.kill(pid, 0)` raises `ESRCH` or `EPERM` → classify the state as stale; do not treat another user's PID as reusable.
- Step 2: If the state path already exists → poll for a positive PID and healthy backend before reclaiming; never spawn concurrently while a healthy winner is starting.
- Step 2: If the winner fails before writing a usable PID or liveness never becomes healthy → remove the claimed state and return `EXIT.UNREACHABLE`; do not extend the 1-second loser wait or auto-restart indefinitely.
- Step 2: If a change would relax loopback-only binding, alter exit-code numbering, or create an in-process CLI/backend dependency → BLOCKED by project invariants; no implementation path crosses it.

---

## Unknowns (must be <= 2)

None. The state path, wire format, Node lock primitive, health endpoint, and consumers are verified in `src/daemon.ts:27-224`, `backend/cmd/server/main.go:199-228`, and `docs/project-context.md:6-22`.

---

## No-Gos

- Will NOT add `saki backend start|stop|status`; Slice 3 owns explicit lifecycle commands.
- Will NOT add Unix socket listeners, socket fetch, permissions, or socket cleanup; Slice 4 owns transport.
- Will NOT add auto-restart on crash beyond recovery on the next CLI invocation.
- Will NOT change the Go state-file writer, TCP bind, HTTP routes, or Inv-1/Inv-2 rehydration behavior.
- Will NOT change `EXIT.UNREACHABLE` from 3 or route liveness through `backendFor()`.
- Will NOT add a database migration, persistent daemon service, or cross-process lock outside the UID-scoped state file.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Local operator/CI role is listed in the Role Coverage matrix.
- [x] Every role's full terminal → CLI → daemon → health path is in Plan Wiring.
- [x] No network auth applies; OS ownership and loopback-only constraints are listed.
- [x] Missing, malformed, stale, concurrent, timeout, and unrelated-PID states are documented.

**Database & Migrations**
- [x] No model or schema fields change; Migration Checklist explicitly says none.
- [x] No database command or rollback is required.

**API Layer**
- [x] No new endpoint or request/response schema is introduced.
- [x] Existing `GET /api/health` is named as the liveness boundary.
- [x] Existing loopback origin guard and TCP bind remain unchanged.

**Service / Business Logic**
- [x] `readDaemonState`, `isAlive`, `healthy`, `acquireStateLock`, `stateIsLock`, and `ensureDaemon` are named with exact path/range.
- [x] Side effects are limited to UID-scoped state-file create/write/unlink and detached process reuse/spawn.
- [x] Error paths cover malformed state, PID errors, unrelated live PID, lock timeout, spawn failure, liveness timeout, and cleanup.

**Frontend**
- [x] CLI/backend-only; no frontend files or UI flow apply.

**Compatibility & Consumers**
- [x] Every existing changed surface has consumer evidence and a verdict.
- [x] Forward compatibility is explicitly tolerant-reader/additive.
- [x] Prior slice 1 plan was read and its shipped shape is recorded.

**Plan Wiring**
- [x] Existing healthy reuse, concurrent first-use, and stale recovery chains are complete.
- [x] No vague endpoint/service step remains; every implementation target has a path and function.

---

## Evidence Ledger

### Blocking (must be empty)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|---|---|---|
| | | | |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|---|---|---|
| — | All anchors verified, all targets have anchor parents and creating steps, all state-changing failure paths are covered, and no unknowns above LOW. | `tasks/daemon-lifecycle-unix-socket-transport-slice2-context.md`; direct reads of `src/daemon.ts:27-224`, `src/daemon.test.ts:14-102`, `src/index.ts:343-349`, `backend/cmd/server/main.go:199-228`, `docs/project-context.md:6-22`; `git grep` consumer inventory |

**Blocking: 0 → READY.**

---

## Success Criteria

- [ ] Given a valid state file with a live PID and `/api/health` returning `{ok:true}`, when two concurrent `ensureDaemon()` calls run against the same `SAKI_DAEMON_STATE_DIR`, then the controlled spawn stub is called exactly once, both calls resolve the same positive PID, and `backend.state.json` remains valid JSON (`npm test -- --run src/daemon.test.ts -t "suppresses duplicate spawn"`).
- [ ] Given a state file with a dead PID (`process.kill(pid, 0)` raises `ESRCH`), when `ensureDaemon()` runs, then it removes the stale file, spawns a fresh daemon, waits for healthy liveness, and returns the fresh PID (`npm test -- --run src/daemon.test.ts -t "cleans ESRCH stale state"`).
- [ ] Given a state file with a PID probe raising `EPERM` or a non-numeric/NaN PID, when `ensureDaemon()` runs, then it treats the state as stale, removes it, and starts a fresh daemon without reusing the PID (`npm test -- --run src/daemon.test.ts -t "cleans EPERM and invalid PID state"`).
- [ ] Given an alive PID whose health request returns `ECONNREFUSED` or a non-healthy response, when `ensureDaemon()` runs, then it removes the state as PID-recycled/unrelated, spawns a fresh daemon, and returns the fresh PID (`npm test -- --run src/daemon.test.ts -t "rejects unrelated live PID"`).
- [ ] Given a winner that owns the sentinel and then fails before healthy liveness, when a loser observes the state, then the loser does not double-spawn during the bounded 1-second poll, the failed winner removes its lock, and a subsequent invocation can acquire the lock (`npm test -- --run src/daemon.test.ts -t "reclaims failed spawn lock"`).
- [ ] `npm run typecheck`, `npm test`, `npm run backend:test`, `cd backend && go vet ./...`, and `npm run test:coverage` pass; total coverage remains at least 80% and changed TypeScript files meet the same floor.

---

## Annotation Space

> Build-driven Slice 2 plan. Slice 1's committed state parser and lock helper are the starting point; implementation must close the concurrency and stale-state gaps without adding Slice 3 or 4 scope.

Status: [x] Draft  [x] In Progress
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
