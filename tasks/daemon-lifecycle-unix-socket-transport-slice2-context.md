# Context: daemon-lifecycle-unix-socket-transport slice 2

## Source and scope

Source PRD: `tasks/prd-daemon-lifecycle-unix-socket-transport.md`, Slice 2. This slice owns UID-scoped state-file correctness: atomic spawn locking, PID liveness validation, stale-state cleanup, and duplicate-spawn suppression. Slice 1 is committed at `ad06abb` and reviewed. Explicit `saki backend start|stop|status` remains Slice 3; Unix socket transport remains Slice 4.

## Graphify findings

`graphify-out/GRAPH_REPORT.md` is absent. The relevant scope is under 20 files, so targeted reads are sufficient.

## Non-derivable context

`docs/project-context.md:6-22` defines two independent deployables (`saki` and `saki-backend`) joined by loopback HTTP; `docs/project-context.md:32` requires loopback-only access; `docs/project-context.md:31` requires restart-safe run rehydration. Slice 2 changes only CLI daemon bookkeeping and must not create an in-process CLI/backend dependency.

## Shipped anchors from slice 1

- `src/daemon.ts:27-50` derives the UID-scoped state directory and parses `{pid,socketPath,goUrl}`; invalid or non-positive PIDs return `null`.
- `src/daemon.ts:59-67` implements `isAlive(pid)` with `process.kill(pid, 0)` and currently treats all errors as stale.
- `src/daemon.ts:136-163` creates the state directory and uses `open(path, 'wx')` (`O_CREAT|O_EXCL`) with a `pid:-1` sentinel.
- `src/daemon.ts:186-224` performs existing-state health reuse, stale cleanup, lock acquisition, spawn, PID write, liveness, and cleanup on failure. The loser path polls 10 × 100 ms, then removes the state and recursively retries.
- `src/daemon.test.ts:14-102` covers wire parsing, malformed/lock state parsing, liveness bounds, missing/non-executable binaries, explicit URL reuse, and stale stop cleanup, but does not prove concurrent duplicate-spawn suppression or all stale-state variants.
- `src/index.ts:343-349` reads state before `ensureDaemon` and emits `daemon:autostart` only when a new positive PID appears; preserve this behavior.
- `backend/cmd/server/main.go:199-229` derives the same state path and writes backend-owned PID/socket state after startup. Slice 2 must keep this wire format compatible; no Go changes are required.

## Existing consumers and compatibility

`ensureDaemon` is called by `src/index.ts:347` and `src/commands/backend.ts:18`; `readDaemonState` is used by `src/index.ts`, `src/commands/backend.ts`, and tests; `stopDaemon` is used by `src/commands/backend.ts`. `daemonStatePath` and `SAKI_DAEMON_STATE_PATH` are shared with the Go backend. The plan preserves all signatures and the existing JSON keys, so consumers are unaffected. `spawnDaemon` is private (`git grep` finds only its definition and `ensureDaemon` call); no external consumer exists.

## Risks and boundaries

- The lock file is the cross-process single-spawn invariant. A winner must claim the path before spawning; a loser must wait for a real PID or safely reclaim only a dead sentinel.
- A PID that passes `kill(pid, 0)` is not sufficient: `healthy()` must still probe `/api/health` to reject PID reuse by an unrelated process.
- A state file with malformed JSON, non-numeric/NaN PID, `pid <= 0`, `EPERM`, `ESRCH`, or unhealthy HTTP is stale and must be removed before a fresh spawn.
- Never auto-start lifecycle commands, relax loopback binding, alter exit code 3, add restart-on-crash, add explicit backend commands, or add Unix-socket transport in this slice.
- No database schema or migration changes.
