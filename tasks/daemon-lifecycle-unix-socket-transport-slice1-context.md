# Context: daemon-lifecycle-unix-socket-transport slice 1

## Source and scope

Source PRD: `tasks/prd-daemon-lifecycle-unix-socket-transport.md`, Slice 1. This slice owns auto-start and liveness only: binary discovery, detached spawn, health polling, stderr observability, and lifecycle-command/explicit-backend bypasses.

## Graphify findings

No `graphify-out/GRAPH_REPORT.md` exists; scope is under 20 directly relevant files, so targeted reads are sufficient.

## Verified anchors

- `src/index.ts:317-377` parses commands, handles per-command `--help` before preflight, skips daemon setup for the `backend` command group, calls `ensureDaemon`, and injects the discovered socket path into `StudioClient`.
- `src/daemon.ts:52-115` resolves the binary, spawns a detached child, and maps startup errors. `spawn()` emits OS errors asynchronously, so the current synchronous `try/catch` does not satisfy missing/non-executable binary criteria.
- `src/daemon.ts:69-88` polls `GET /api/health` with a 10-second deadline and returns `EXIT.UNREACHABLE` on timeout.
- `src/client.ts:93-120` already has the fetch injection seam and only enables socket transport for Go-only, non-explicit-backend contexts.
- `src/exit.ts:8-16` freezes `EXIT.UNREACHABLE` at 3.
- `backend/cmd/server/main.go:147-196` already binds loopback TCP and writes daemon state; Slice 1 probes TCP health directly and does not change Go transport.

## Consumers

`ensureDaemon` is called from `src/index.ts:347` and `src/commands/backend.ts:18`. `spawnDaemon` is private and has no direct consumers. `waitForLiveness` is called from `ensureDaemon:197` and direct tests. No existing signature or API response changes are required for Slice 1.

## Risks and boundaries

- Preserve `EXIT.UNREACHABLE === 3` and loopback-only backend behavior.
- Do not add restart-on-crash, explicit backend commands, PID stale cleanup, or Unix-socket transport in this slice; those are later PRD slices.
- Do not route auto-start through `backendFor()`; the health probe must target Go's loopback URL directly.
