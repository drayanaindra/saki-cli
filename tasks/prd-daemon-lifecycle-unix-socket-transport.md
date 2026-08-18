<!-- prd-blocking: 0 -->
<!-- slices: 4 -->
<!-- appetite: medium -->
<!-- revision-passes: 2 -->
<!-- prd-locked: @codex · 2026-08-16 · ui:none -->

# PRD: Daemon lifecycle + unix-socket transport

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-16 · **Appetite:** medium — a few days · **Item:** F1

## 1. TL;DR

`saki` auto-starts its own Go backend the first time any command is run (finding and spawning `dist/saki-backend`, waiting for liveness, then proceeding). A UID-scoped state file tracks the running daemon across invocations. Explicit `saki backend start|stop|status` commands give operators direct lifecycle control. The backend also binds a unix socket alongside its existing TCP port; the CLI connects via the socket by default, falling back to TCP when the socket isn't available or `SAKI_BACKEND_URL` is set.

## 2. Problem & Evidence

Today every `saki` command fails with exit 3 (UNREACHABLE) if the operator hasn't manually run `./dist/saki-backend &` in a separate terminal first. `src/client.ts:158-159` throws `CliError(EXIT.UNREACHABLE)` on `ECONNREFUSED` — no recovery, no hint about auto-starting. The CLI's `--help` output (`src/index.ts:284`) documents `SAKI_BACKEND_URL` but does not mention how to start the backend. CLAUDE.md's command table names `./dist/saki-backend &` as a required pre-step: the friction is load-bearing.

**Load-bearing assumption:** Operators are blocked from using the CLI without a manual background launch step. — `observed` (source: `src/client.ts:158-159` + CLAUDE.md command table)

## 3. Primary Job to be Done

**J1:** When I run any `saki` command on a machine with `saki-backend` installed, I want the backend to start automatically if it's not already running, so I can use the CLI without managing a separate process.

## 4. Related Jobs

**J2:** When I need to explicitly control the saki backend lifecycle (for scripting, CI, or debugging), I want `saki backend start|stop|status` commands, so I can manage the daemon independently of normal commands.

## 5. Desired Outcomes / Success Metrics

| # | Outcome | Target | Basis | Method | JTBD |
|---|---------|--------|-------|--------|------|
| 5.1 | Minimize sessions where an operator must manually start saki-backend to use any `saki` command (binary installed) | 0 sessions requiring manual pre-launch | baseline: 100% of sessions require manual pre-launch today — `src/client.ts:158-159` always throws UNREACHABLE without a running backend; CLAUDE.md names it as a required pre-step | event: emit `daemon:autostart {result:"success"\|"timeout"\|"not-found", latencyMs}` debug log to stderr when auto-start path fires | J1 |
| 5.2 | Minimize backend-ready latency from first `saki` command | p50 ≤ 3 s, hard ceiling 10 s | aspirational (Go binary startup is typically fast; 3 s is a conservative p50 target) | query: integration test measures wall-clock from `child_process.spawn` call to first successful `GET /api/health` response | J1 |
| 5.3 | Minimize orphaned saki-backend processes (stale state cleaned; daemon survives between commands) | 0 orphaned processes per session | aspirational | query: test harness checks `process.kill(pid, 0)` alive-probe and state file consistency after each test scenario | J1 · J2 |
| 5.4 | Maximize `saki backend stop` reliability (daemon receives SIGTERM and terminates cleanly) | 100% clean termination | aspirational | query: integration test start → stop → verify `GET /api/health` returns ECONNREFUSED | J2 |
| 5.5 | Maximize commands that exit cleanly when saki-backend binary is missing or liveness times out (no hangs) | 100% — CLI exits ≤ 10 s on all timeout / not-found paths | aspirational | query: integration test asserts process exits within 10 s on binary-not-found and liveness-timeout scenarios | guards 5.1: auto-start gaming via broken binary or hanging liveness → blocked users |

## 6. Appetite & Kill Criteria

**Appetite:** medium — a few days (≤ 4 slices)

**Kill criteria:** If, after shipping Slices 1–2, auto-start fails or hangs in more than occasional manual integration test runs (measured by the 5.5 test suite), stop and diagnose before shipping Slices 3–4. A fragile auto-start that sometimes hangs is worse than the current explicit manual step.

## 7. Solution Shape

**Chosen shape:** Detached child-process daemon + UID-scoped state file + unix socket transport.

- The CLI spawns `saki-backend` as a detached `child_process` (outlives the CLI invocation), tracks it via a JSON state file in a UID-scoped directory (`os.tmpdir()/saki-<uid>/`), and uses an atomic `O_CREAT|O_EXCL` sentinel write to prevent concurrent double-spawns.
- The Go backend adds a unix socket listener (`net.Listen("unix", socketPath)`) alongside its existing TCP bind (additive — TCP is retained). The socket path is included in the state file so the CLI can discover it.
- The CLI (`StudioClient`) detects the socket path from the state file before constructing the client
  and injects an `undici` socket-aware `fetchImpl`, scoped to Go-origin requests only; it falls back
  to TCP when the socket is absent, `SAKI_BACKEND_URL` is explicitly set, or `expressConfigured=true`.

**Alternatives considered / Decision:**

| Alternative | Verdict | Why not |
|---|---|---|
| TCP-only auto-start (no unix socket) | Rejected | Does not satisfy F1's stated goal: "can talk to it over a unix socket rather than a TCP port" |
| systemd / launchd service | Rejected | Platform-invasive, much larger scope, requires root or plist management — overkill for a developer-local tool |
| In-process backend (CGO or Node addon) | Ruled out by invariant | `docs/project-context.md` — "No shared in-process state between the CLI and the backend" is a hard architectural invariant |

## 8. Vertical Slices

**Slice 1 — Auto-start on first command (walking skeleton)**
Serves: J1 · 5.1 · 5.2

Assumes: `--help` / `--version` fast-path parsing in `src/index.ts` precedes the pre-flight health check (so `saki --help` never triggers auto-start).
Assumes: `waitForLiveness` probes `http://127.0.0.1:8788/api/health` directly — not via `backendFor()` — so it always reaches the Go backend regardless of `expressConfigured`.

A new `src/daemon.ts` module: binary path resolution (`SAKI_BACKEND_BIN` env → `path.join(__dirname, 'saki-backend')` → PATH), a `spawnDaemon()` function (detached `child_process.spawn`, stdout/stderr suppressed — throws EACCES/ENOEXEC on non-executable binary → callers catch and exit UNREACHABLE), and `waitForLiveness(goUrl, { timeoutMs, intervalMs })` (polls `GET /api/health` until `{ok:true}` or timeout). In `src/index.ts`, before command dispatch: run a pre-flight health check; if UNREACHABLE and binary resolves → call `spawnDaemon()` + `waitForLiveness()`, emit `daemon:autostart` event to stderr, then dispatch normally. Skip auto-start for `saki backend stop`, `saki backend status`, and `saki backend start` (they explicitly manage lifecycle and must report actual state, not attempt recovery).

**Slice 2 — PID tracking + stale-state cleanup**
Serves: J1 · 5.3

Assumes: UID-scoped state directory `path.join(os.tmpdir(), 'saki-' + os.userInfo().uid)` created on first use (synchronous `fs.mkdirSync(stateDir, { recursive: true })`). Same path in Go: `filepath.Join(os.TempDir(), fmt.Sprintf("saki-%d", os.Getuid()))`.

**Spawn-lock protocol** (prevents concurrent double-spawn, resolves §12.4):

1. Winner path (`O_CREAT|O_EXCL|O_RDWR` succeeds): immediately write sentinel `{"pid":-1,"socketPath":null,"goUrl":""}` to state file (file is now claimed); then call `spawnDaemon()` (catching EACCES/ENOEXEC → delete state file, exit UNREACHABLE); after `child.pid` is available, overwrite file with `{"pid":child.pid,"socketPath":null,"goUrl":"http://127.0.0.1:8788"}`.
2. Loser path (`EEXIST`): poll state file (retry 100 ms × 10 = max 1 s) until `pid !== -1`; then proceed to the alive-check below with the loaded PID. If PID never appears (winner crashed pre-write): delete state file, restart from step 1.

**Alive-check algorithm** (runs on every subsequent invocation and after EEXIST resolve):

- Parse `pid` from state file JSON. If file missing, malformed (parse error), or `pid ≤ 0` → stale.
- `process.kill(pid, 0)`: ESRCH → stale; EPERM (different-user process holds PID) → stale; success → health check.
- `GET /api/health`: `{ok:true}` → daemon alive, reuse. ECONNREFUSED or non-200 → stale (PID recycled by OS for unrelated process).
- Stale path: `fs.unlinkSync(statePath)`, fall through to spawn-lock from step 1.

**Slice 3 — `saki backend start|stop|status` explicit commands**
Serves: J2 · 5.4

New `src/commands/backend.ts` wired into `src/index.ts` as the `backend` command group. `start` calls `spawnDaemon()` + `waitForLiveness()`; idempotent when daemon already running (alive-check passes → exit 0, "already running"). `stop` reads PID from state file → SIGTERM; if process still alive after 5 s → SIGKILL; removes state file after termination regardless of signal used. Stop is also idempotent when no state file (exit 0, "not running") or state file holds a stale PID (cleanup + exit 0, "not running (stale PID cleaned)"). `status` reports PID liveness + `GET /api/health` verdict; with `--json` outputs `{ pid, healthy, goUrl, socketPath: string|null }` where `socketPath` is the socket path from the state file if present (Slice 4), or the string `"pending-slice4"` if the state file exists but no `socketPath` key is set (distinguishes "not provisioned" from "socket unavailable").

**Slice 4 — Unix socket transport**
Serves: J1 · 5.2

Assumes: `undici` npm package added as production dependency (or a `node:http`-based socket-fetch wrapper in `src/socket-fetch.ts`).
Assumes: `src/index.ts` reads the state file before constructing `StudioClient`, so socket path is available for `fetchImpl` injection at startup.
Assumes: socket transport (`fetchImpl`) is scoped to Go-backend requests only — not injected when `expressConfigured=true` or when `SAKI_BACKEND_URL` is set explicitly.
Assumes: Go bind path includes a pre-Listen path-length guard (`len(socketPath) > 103` on macOS, `> 107` on Linux → return error, not EINVAL from kernel).

Go: in `backend/cmd/server/main.go`, after the TCP `http.ListenAndServe` goroutine:
1. Remove stale socket: `os.Remove(socketPath)` (ignoring ENOENT — safe pre-bind cleanup).
2. `net.Listen("unix", socketPath)`.
3. `os.Chmod(socketPath, 0600)` immediately after Listen (before first accept) — overrides umask-set permissions.
4. `http.Serve(l, mux)` in a goroutine.
5. Register SIGTERM handler that calls `os.Remove(socketPath)` explicitly in the handler body (NOT via `defer` — `defer` does not run through `os.Exit()`), then `os.Exit(0)`.
6. Write socket path to state file: overwrite with `{"pid":pid,"socketPath":socketPath,"goUrl":"http://127.0.0.1:8788"}`.

CLI: `src/daemon.ts` reads socket path from state file; when found and Go-only context applies, creates `undici.Agent({ connect: { socketPath } })` and injects as `fetchImpl` into `StudioClient`. `SAKI_BACKEND_URL` env set OR `expressConfigured=true` → TCP always wins (backwards compat, no socket injection).

## 9. Acceptance Criteria per Slice

### Slice 1

- 1.1 [auto] Given saki-backend binary exists at the resolved path and the backend is NOT running, when `saki status` is run without pre-launching the backend, then the backend is auto-started, command exits 0, and stderr contains a `daemon:autostart` event log line. → 5.1 · observability
- 1.2 [auto] Given auto-start fires, when the backend becomes healthy (GET /api/health returns {ok:true}), then the wall-clock time from spawn to liveness is ≤ 10 s. → 5.2
- 1.3 [auto] Given saki-backend binary is NOT found at any resolved path (`SAKI_BACKEND_BIN`, `__dirname/saki-backend`, PATH), when any saki command is run, then exit code is 3 (UNREACHABLE) and stderr contains "saki-backend binary not found". → validation
- 1.4 [auto] Given saki-backend binary exists but the process does NOT become healthy within 10 s, when liveness timeout elapses, then exit code is 3 (UNREACHABLE) and the CLI process exits within 10 s (no hang). → guards 5.5
- 1.5 [auto] Given saki-backend binary exists but is not executable (EACCES or ENOEXEC), when any saki command fires, then exit code is 3 (UNREACHABLE) and stderr contains "permission denied" or "exec format error". → error-path

### Slice 2

- 2.1 [auto] Given daemon is running (state file exists with valid PID and GET /api/health returns {ok:true}), when a second saki command fires, then no second saki-backend process is spawned and state file is unchanged. → 5.3
- 2.2 [auto] Given a stale state file exists (PID no longer alive, `process.kill(pid, 0)` throws ESRCH), when any saki command fires, then the state file is removed and a fresh daemon is spawned. → 5.3
- 2.3 [auto] Given no state file exists and `spawnDaemon()` is called twice in the same Node process, when the first call creates the state file with `O_CREAT|O_EXCL`, then the second call throws EEXIST and enters the poll path (not a second spawn). → 5.3
- 2.4 [auto] Given state file holds a PID owned by a different user (EPERM on kill-0) or non-numeric content (NaN), when any saki command fires, then state file is removed and a fresh daemon is spawned. → error-path
- 2.5 [auto] Given state file holds a PID of an alive process that is NOT saki-backend (`process.kill(pid, 0)` succeeds but GET /api/health returns ECONNREFUSED), when any saki command fires, then stale-state cleanup fires, state file is removed, and a fresh daemon is spawned. → error-path

### Slice 3

- 3.1 [auto] Given daemon is not running, when `saki backend start` is run, then daemon starts, exit code is 0, and stdout includes the spawned PID. → J2
- 3.2 [auto] Given daemon is running (PID tracked), when `saki backend stop` is run, then the daemon terminates, state file is removed, and exit code is 0. → 5.4
- 3.3 [auto] Given daemon is already running, when `saki backend start` is run, then exit code is 0 and stdout contains "already running" with PID. → J2
- 3.4 [auto] Given daemon is running, when `saki backend status --json` is run, then stdout is valid JSON matching `{pid: number, healthy: boolean, goUrl: string, socketPath: string|null}` where `socketPath` is `null` (pre-Slice 4) or a path string (post-Slice 4), and exit code is 0. → J2
- 3.5 [manual] Given a run in building state, when `saki backend stop && saki backend start` are run, then `saki runs --json` shows the run rehydrated and no data is lost. → Rule 7 / Inv-2

### Slice 4

- 4.1 [auto] Given socket path present in state file and SAKI_BACKEND_URL unset, when `saki status` runs, then exit 0 and subsequent `saki backend status --json` shows socketPath matching the state file. → 5.2
- 4.2 [auto] Given `SAKI_BACKEND_URL` env var is set explicitly, then CLI uses TCP regardless of socket file presence (unix socket path is ignored). → error-path
- 4.3 [auto] Given daemon exits and socket file is removed, when the next `saki` command fires, then auto-start (Slice 1) triggers within 10 s and the command succeeds (no hang on dead socket). → guards 5.5
- 4.4 [auto] Given daemon running with unix socket enabled, when the socket file is stat'd immediately after backend start, then its Unix permissions are 0600 (owner-read-write only). → security
- 4.5 [auto] Given auto-start fires with unix socket enabled, when socket is ready, then GET /api/health via unix socket responds within 3 s (single-measurement p50 proxy). → 5.2
- 4.6 [auto] Given a stale socket file exists from a prior crash, when the backend starts, then it
  removes the stale path before `net.Listen`, binds successfully, and serves health; a request sent
  through the unix socket carries a loopback Host and passes OriginGuard with HTTP 200. → security
- 4.6 [auto] Given a stale socket file exists from a prior crash, when the backend starts, then it
  removes the stale path before `net.Listen`, binds successfully, and serves health; a request sent
  through the unix socket carries a loopback Host and passes OriginGuard with HTTP 200. → security

## 10. Business Rules & Invariants

1. **🔒 INVARIANT: Backend binds loopback TCP or owner-local unix socket only.** The TCP listener is `127.0.0.1:PORT` (existing `main.go:141`). The unix socket file has permissions `0600`. No flag or env var relaxes this to an off-host address.
   - Failure criterion: AC 4.4 verifies socket permissions are 0600.
   - Edge criterion: the spawned backend process MUST inherit the same loopback constraint as a manually launched one — verified by AC 1.1 (health check via the same loopback path).

2. **🔒 INVARIANT: At most one saki-backend process per UID per machine.** The state file write uses `O_CREAT|O_EXCL` with a sentinel (atomic create-before-spawn). If the file already exists and the PID is alive with a healthy backend, no new process is spawned. The state directory is UID-scoped (`os.tmpdir()/saki-<uid>/`) so two different users each get an independent daemon.
   - Failure criterion: AC 2.3 verifies exactly one O_CREAT|O_EXCL open succeeds under concurrent calls in the same process.
   - Edge criterion: AC 2.1 verifies that an alive PID + healthy backend suppresses a second spawn.

3. Binary-not-found MUST exit UNREACHABLE (3), never ERROR (1) — agents branch on exit 3 to detect "backend missing or unreachable". The hint MUST name a next step ("build saki-backend with `npm run backend:build`").

4. Auto-start is skipped for `saki backend stop`, `saki backend status`, and `saki backend start` — these manage lifecycle and must report the actual state, not attempt recovery before doing so.

5. On `saki backend stop`: send SIGTERM first; escalate to SIGKILL after 5 s if process is still alive. Always remove the state file after termination, regardless of which signal was used. Stop is idempotent: no state file → exit 0 "not running"; stale PID → cleanup + exit 0.

6. The unix socket file MUST be removed by the Go binary at shutdown — via explicit `os.Remove(socketPath)` in the SIGTERM handler body BEFORE calling `os.Exit()`. Do NOT use `defer os.Remove(socketPath)` in the handler — defers do not execute when `os.Exit()` is called.

7. A restart of the Go backend (any cause) MUST NOT break Inv-2 (in-flight runs must be rehydrated). The daemon auto-start spawns the same `saki-backend` binary, so rehydration via `backend/usecase/rehydrate.go` runs naturally on every spawn.
   - Edge criterion: AC 3.5 verifies that `stop && start` with an in-flight run rehydrates correctly.

## 11. Non-Goals

- ✗ **No system daemon integration** (systemd, launchd, Windows Service). The daemon is an on-demand background process, not a session-persistent service.
- ✗ **No auto-restart on crash.** If the daemon dies unexpectedly, the next `saki` command detects it via stale state (Slice 2) and auto-starts a fresh one.
- ✗ **No structured log file for daemon stdout/stderr.** On auto-start, the daemon's stdout/stderr are suppressed (piped to `/dev/null` or equivalent). The `daemon:autostart` event log is the only operator signal.
- ✗ **No Windows unix-socket support.** The unix socket path in Slice 4 targets POSIX systems (macOS/Linux). On Windows, the CLI falls back to TCP (the `SAKI_BACKEND_URL` / TCP path stays fully functional).
- ✗ **No off-host binding.** The loopback-only invariant is strengthened here (unix socket is even more local than loopback), never relaxed.
- ✗ **No auto-start for MCP in-process dispatch.** `src/commands/mcp.ts` calls `cmdXxx()` functions directly in-process, bypassing `src/index.ts:main()` where the pre-flight health check lives. If the backend dies while the MCP server is running, tool calls fail with UNREACHABLE (exit 3) — MCP callers must handle this error. Adding auto-start to the MCP dispatch path is deferred.

## 12. Rabbit Holes & Open Questions

1. **Unix socket fetch adapter choice — resolved:** add `undici` as a production dependency and use
   `new Agent({ connect: { socketPath } })` with the existing fetch injection seam. This preserves
   the native fetch response contract and avoids a second HTTP implementation. The adapter is created
   only for Go-origin requests and sends `Host: localhost` so OriginGuard accepts unix-socket traffic.

2. **Binary path discovery after npm publish (I1)** — When `saki` is installed globally via npm, `dist/saki-backend` must be co-packaged and discoverable. How the Go binary is distributed alongside the npm package is scoped to I1 (Publish to npm). Until I1, binary discovery assumes `path.join(path.dirname(fileURLToPath(import.meta.url)), 'saki-backend')` (relative to installed CLI). If I1 ships before this feature, revisit the path resolution.

3. **OriginGuard + unix socket Host header — resolved:** the socket fetch adapter explicitly sends
   `Host: localhost`; AC 4.6 verifies the request passes OriginGuard. A raw curl reproduction is
   retained as a diagnostic: `curl --unix-socket /path/to/backend.sock -H 'Host: localhost'
   http://localhost/api/health`.

4. **Concurrent spawn race on O_CREAT|O_EXCL — RESOLVED.** The spawn-lock protocol in Slice 2 writes a sentinel `{"pid":-1,...}` before calling `spawnDaemon()`, so the EEXIST loser always finds a locked (not empty) file. The loser polls until `pid !== -1` (max 1 s, 10 × 100 ms retries); if the winner crashes before writing the real PID, the loser detects the stale sentinel (after timeout) and re-enters the spawn-lock loop. This closes the race window stated previously as "verify the timing window is acceptable."

5. **`saki backend stop` with in-flight runs** — Rule 7 mandates that a restart must not break Inv-2, but `saki backend stop` followed by NO restart leaves in-flight runs frozen until the user manually re-starts or triggers auto-start. This is acceptable: the operator explicitly chose to stop the daemon. The next `saki` command auto-starts the backend and rehydration runs. No special handling needed; documented here to prevent future over-engineering.

## 13. Technical Constraints

- `child_process.spawn` with `{ detached: true, stdio: 'ignore' }` + `subprocess.unref()` is required for the daemon to outlive the CLI invocation. Without `unref()`, Node's event loop stays alive until the child exits.
- Go's `net.Listen("unix", socketPath)` requires the socket directory to exist before listen. Create it with `os.MkdirAll(stateDir, 0700)` at startup (0700 keeps the directory owner-only).
- The UID-scoped state directory is `filepath.Join(os.TempDir(), fmt.Sprintf("saki-%d", os.Getuid()))` in Go and `path.join(os.tmpdir(), 'saki-' + os.userInfo().uid)` in Node. On Windows, `os.Getuid()` returns -1 — but unix socket transport is a Non-Goal on Windows (TCP fallback applies).
- `undici` must be added to `package.json` production dependencies, not dev-only.
- The socket path (`stateDir + '/backend.sock'`) must be ≤ 103 bytes on macOS (POSIX `sun_path` limit = 104 bytes including null terminator) and ≤ 107 bytes on Linux. Go must check this length before calling `net.Listen` and return a descriptive error, not let the kernel return `EINVAL`.

## 14. Dependencies

- **I1 (Publish `saki` to npm):** binary discovery path in Slice 1 assumes `saki-backend` is colocated with the CLI `dist/`. If I1 ships and changes the distribution layout, Slice 1's `binaryPath()` resolution must be updated to match.
- **F4 (`saki doctor` — claude coverage):** `saki doctor` reads the backend's provisioning results — unaffected by this feature since `doctorSvc` lives in the Go backend that this feature merely manages.

## 16. Technical Contract (thin)

**Entities (data):**

| Entity | Reuse / Change / New | Evidence | Serves |
|---|---|---|---|
| Backend state file (`os.tmpdir()/saki-<uid>/backend.state.json`) | NEW | n/a | 8.2 · 5.3 |

**Wire format for `backend.state.json`:**
```json
{ "pid": 1234, "socketPath": "/var/folders/.../saki-501/backend.sock", "goUrl": "http://127.0.0.1:8788" }
```
- `pid`: integer, process ID of running daemon; `-1` = spawn lock held (sentinel, transient).
- `socketPath`: string path to unix socket, or `null` before Slice 4 ships.
- `goUrl`: always `"http://127.0.0.1:8788"` (TCP baseline); present for CLI fallback when socket unavailable.

**Endpoints (API):**

| Method + path — purpose | Reuse / Change / New | Evidence | Serves |
|---|---|---|---|
| `GET /api/health` — liveness probe for daemon startup poll | REUSE | `backend/adapter/http.go:69` | 8.1 · 5.2 |

**Architecture decision (one, load-bearing):**

The CLI manages `saki-backend` as a **detached child process** — the only model compatible with "No shared in-process state between the CLI and the backend" (`docs/project-context.md`). The CLI and backend remain independently started and independently versioned; the daemon lifecycle layer is pure process management, not a coupling.

- NEW: `src/daemon.ts` — binary discovery, detached spawn, liveness poll, state file R/W/cleanup. Serves 8.1 · 8.2 · 5.1 · 5.3.
- NEW: `src/commands/backend.ts` — `start|stop|status` subcommands; wired into `src/index.ts`. Serves 8.3 · 5.4.
- CHANGE: `backend/cmd/server/main.go:141` — add `net.Listen("unix", socketPath)` goroutine + pre-bind `os.Remove` + `os.Chmod(0600)` + SIGTERM handler with explicit `os.Remove` in handler body. ↳ Breaks: none (additive — existing TCP bind and all callers unaffected; e2e suite uses TCP and continues to work unchanged). Serves 8.4 · 5.2.
- CHANGE: `src/client.ts:40` (`opts.fetchImpl`) — when socket path is present in state file AND `expressConfigured=false` AND `SAKI_BACKEND_URL` unset, inject a socket-aware `fetchImpl`; otherwise TCP. ↳ Breaks: none (additive — `opts.fetchImpl` injection point already exists; all existing tests that pass `fetchImpl` explicitly continue to work unchanged). Serves 8.4 · 5.2.
