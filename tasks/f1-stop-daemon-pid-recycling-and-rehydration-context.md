# F1 stop ownership and restart verification context

## Scope

Harden `stopDaemon()` so `saki backend stop` does not signal an unrelated process after PID recycling; add a regression test; verify AC 3.5 with an isolated stop/start cycle and an in-flight Go-owned run.

## Graphify Findings

`graphify-out/GRAPH_REPORT.md` is absent; this task crosses the Node CLI ↔ Go backend process boundary. The non-derivable coupling is documented in `docs/project-context.md` § Topology and § Invariants: HTTP/SSE plus the journal directory are the only edges, and Inv-2 requires restart rehydration.

## Verified anchors

- `src/daemon.ts:527-560` — `stopDaemon()` reads the state record, checks PID liveness, sends `SIGTERM`, escalates to `SIGKILL`, and compare-and-releases the state file.
- `src/daemon.ts:334-350` — `probeBackendHealth()` is the bounded health probe available for ownership validation.
- `src/daemon.test.ts:177-263` — stop signal, escalation, unhealthy-peer, lock, and replacement-state tests.
- `src/daemon.spawn.test.ts:127-134` — existing auto-start PID-recycling coverage: live PID plus failed health probe is stale for reuse.
- `backend/cmd/server/main.go:64-71` — generic journal rehydration runs before the backend serves requests.
- `backend/infra/journal.go:47-60` — `SAKI_RUNS_DIR` selects the durable root and Go journals live under `<root>/go`.
- `backend/adapter/http.go:475-522` — `POST /api/run` creates a run and returns `{runId}`; `GET /api/runs` lists runs.
- `package.json:42-49` — Vitest, backend tests, and Playwright commands.
- `scripts/free-e2e-ports.sh:13-19` — existing port cleanup is destructive and is not suitable for an isolated verification that must preserve unrelated processes; use a dedicated `PORT` instead.

## Ownership decision

The stop path will require both PID liveness and a healthy `{ok:true}` response from the state record's `goUrl` before sending a signal. A live PID with failed health is treated as not-owned for signaling; the regression test asserts no signal is sent and the record is handled as stale. This is the same ownership discriminator already used by `ensureDaemon()` for PID recycling (`src/daemon.ts:418-435`) and avoids platform-specific process-inspection code.

## Restart verification shape

Use a temporary `SAKI_RUNS_DIR`, `SAKI_DAEMON_STATE_DIR`, and non-default `PORT` so the test cannot touch the operator's daemon or journals. Start the real `dist/saki-backend`, create a deliberately long-lived Go-owned run through `POST /api/run`, stop the daemon via the CLI, start it again, then query `GET /api/runs` and assert the same `runId` remains present with its journal-backed status. Clean up only the isolated processes/directories in a `finally` path.

## Compatibility inventory

- `stopDaemon(env, options)` signature: `grep -RIn "stopDaemon(" src` finds `src/daemon.ts` exports and `src/commands/backend.ts:12`; no signature change, caller unaffected.
- `probeBackendHealth`: `grep -RIn "probeBackendHealth(" src` finds `src/daemon.ts` and `src/commands/backend.ts:32`; reuse only, no signature change.
- State wire format: no change; `goUrl` already exists in `DaemonState` and is consumed by stop/reuse/status.
- Backend HTTP API: no change; verification consumes existing `POST /api/run` and `GET /api/runs`.
- Environment keys: `SAKI_RUNS_DIR`, `SAKI_DAEMON_STATE_DIR`, and `PORT` already exist; verification-only use, no consumer changes.

Forward compatibility: additive test coverage plus a behavior hardening in an existing command; no API, schema, or deploy-order change.
