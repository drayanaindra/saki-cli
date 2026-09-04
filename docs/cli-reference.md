# `saki` — command line for the saki build orchestrator

Drives an agent (or you) through a **PRD → plan → build → QA → review** journey from a terminal:
start a build, follow it, lock a PRD, switch a branch, open an MR.

It is a **thin client** over the Go backend's HTTP API — it adds no routes and re-implements no
orchestration. Anything the CLI does, the backend does, the same way.

## Install

```bash
npm install -g @saketek/saki-cli   # macOS/Linux, amd64/arm64 — backend binary downloads automatically
saki status
```

`postinstall` (`scripts/fetch-backend-binary.mjs`) fetches the platform-matching `saki-backend`
binary from the GitHub Release tagged `v<package version>`, verifies it against that release's
`SHA256SUMS.txt`, and places it next to `dist/index.js` — this never fails `npm install` (every
exit path returns 0, even on total failure); a missing binary just surfaces later as `saki backend`
being unable to find one to spawn.

**Or via Homebrew** (macOS/Linux — runs `saki-backend` as a persistent background service instead
of the CLI's lazy per-command auto-start; useful when something other than `saki` itself needs to
reach the backend independently):

```bash
brew tap drayanaindra/tap
brew trust --tap drayanaindra/tap
brew install saki
brew services start saki
```

If you've already used `saki` directly (which lazily spawns its own backend), run
`saki backend stop` first — `saki-backend` binds `127.0.0.1:8788` exclusively and exits immediately
if the port is already held.

**Or build from source** (any platform Go ≥ 1.25 targets, and the only path on Windows):

```bash
git clone https://github.com/drayanaindra/saki-cli.git && cd saki-cli
npm install
npm run build            # CLI  -> dist/index.js
npm run backend:build    # Go   -> dist/saki-backend

node dist/index.js status
```

Put the bin on your `PATH` (npm's global install does this for you) to just type `saki`.

## The backend is a lazily-spawned daemon

Every command except `saki backend` calls `ensureDaemon()` (`src/daemon.ts`) before doing anything
else: it reads the daemon state file, health-checks it, and if nothing usable is running, spawns
`saki-backend` detached, waits for `/api/health`, and records the new PID. **You never have to
start the backend yourself** — the first `saki` command of the day starts it, and it stays up
across commands and shells for the same user.

```console
$ saki roadmap list
daemon:autostart {result:"success",pid:41213}
...
```

That line goes to stderr, only on an actual cold start (not on every command — a healthy existing
daemon or one reused from an already-bound port produces no autostart line). The state file lives
at `$TMPDIR/saki-<uid>/backend.state.json` (override the directory with `SAKI_DAEMON_STATE_DIR`).

Manage the daemon directly with `saki backend`:

```bash
saki backend status   # {pid, healthy, goUrl, socketPath} — service-managed backends report pid: null
saki backend start    # idempotent — reports the existing pid if already up
saki backend stop     # SIGTERM, falls back to SIGKILL after 5s; also stops a service-managed process it finds
```

| Variable | Default | Meaning |
|---|---|---|
| `SAKI_BACKEND_URL` | `http://127.0.0.1:8788` | Go backend base URL — serves workflows, runs, roadmap, prd, branch, MRs, proto, screenshots, doctor, init-env |
| `SAKI_BACKEND_BIN` | *(adjacent `saki-backend`, else `PATH`)* | Override which binary `saki backend start` / autostart spawns |
| `SAKI_DAEMON_STATE_DIR` | `$TMPDIR/saki-<uid>` | Where the daemon's PID/socket state file lives |
| `SAKI_STUDIO_URL` | *(unset — no Express)* | **Opt-in.** A second, separately-run Express server for a pipeline-studio deployment that layers a UI/session model over this backend. Adds `saki artifacts` plus the `devMode`/`auth` lines in `saki status`. Setting it also disables backend auto-start (see below) — you're expected to run both servers yourself. |

Setting `SAKI_STUDIO_URL` switches the CLI into **two-server mode**, splitting traffic by path the
way that deployment's own UI proxy does (`src/routes.ts`). Standalone — the normal case for this
package — there is no second server and every request goes straight to the Go backend.

> **Loopback hosts only.** The backend rejects any request whose Host header isn't `localhost` /
> `127.0.0.1` / `::1` (`backend/adapter/originguard.go:32`), and additionally *binds* the loopback
> interface only. Pointing `SAKI_BACKEND_URL`/`SAKI_STUDIO_URL` at a LAN address or hostname gets a
> 403 "cross-origin blocked", not a connection error. This is a local single-operator tool by design.

Every command is repo-scoped and defaults to the current directory; override with `--cwd <dir>`.

## ⚠ `DEV_MODE=1` — only when you set `SAKI_STUDIO_URL`

**Not applicable in the default standalone mode.** The Go backend has no session gate at all, so
there is nothing to be exempted from. This section applies only when you've opted into the Express
layer above.

The CLI holds no session cookie by design — it is a **local, single-operator** tool. `DEV_MODE=1` is
what exempts it from that Express server's session gate.

Without it, every gated route answers 401 and the CLI exits **6**:

```console
$ SAKI_STUDIO_URL=http://localhost:8787 saki branch
error: authentication required
  the studio is gating this route — restart it with DEV_MODE=1 (check with `saki status`)
```

`saki status` probes both servers when Express is configured, and tells you which mode it's in:

```console
$ SAKI_STUDIO_URL=http://localhost:8787 saki status
backend   http://127.0.0.1:8788
reachable yes (saki-backend)     <- must be up: runs/roadmap/prd/branch live here
studio    http://localhost:8787
reachable yes (pipeline-studio-server)
devMode   on              <- must be "on" for the rest of the CLI
auth      authenticated
runs      allowed
```

Without `SAKI_STUDIO_URL` set, `saki status` reports only the Go backend plus one line:
`express   not configured (set SAKI_STUDIO_URL to include it)` — and that absence never counts
against the exit code.

Either configured server down → the report still prints (so you see which one), stderr names what
that server serves, and the exit code is **3**.

## Exit codes — the machine contract

Branch on these, not on stdout. **Two routes report failure inside an HTTP 200 body**
(`branch switch` and `mr create` surface `{ok:false, error}` when git or `glab` fails), so the HTTP
status is *not* a reliable success signal. The exit code is.

| Code | Name | Meaning |
|---|---|---|
| 0 | `OK` | Succeeded. For workflow `--follow`, durable verification ended `done` |
| 1 | `ERROR` | Unexpected failure — also: a run that ended `error` (incl. stopped), an unprovisioned engine refused at spawn |
| 2 | `USAGE` | Bad arguments: unknown command/flag, missing or extra positional |
| 3 | `UNREACHABLE` | A configured server is not answering, or the daemon could not be started. For `status`, the Go backend `:8788` — plus Express `:8787` when `SAKI_STUDIO_URL` is set. Also `saki artifacts` with no Express configured |
| 4 | `NOT_FOUND` | Unknown run/workflow, or target is not found |
| 5 | `REMOTE_FAILED` | Backend reached, operation refused (`{ok:false}` — git/glab stderr) |
| 6 | `AUTH_REQUIRED` | Studio gated the route (401/403) — only reachable with `SAKI_STUDIO_URL` set and no `DEV_MODE=1`; also artifacts |

Run status vocabulary is **`running` / `done` / `error`**. Workflow status additionally includes
**`parked`**, **`awaiting-decision`**, **`failed`**, and **`stopped`**. A workflow `done` is the only
success value, and it is published only after durable artifact/commit/gate verification.

Errors and hints go to **stderr**; results go to **stdout**, so `2>/dev/null` leaves clean data.

## Commands

Add `--json` to any command for one compact machine-readable line — except `saki mcp`, which has no
human-vs-JSON toggle (it's a long-lived stdio server, not a bounded request/response command) and
rejects `--json` as an unknown flag.

```bash
saki status                                  # is the backend (and, if configured, the studio) up
saki backend start|stop|status               # manage the lazily-spawned Go backend daemon
saki mcp                                     # start an MCP server exposing journey commands as typed tools
saki doctor [--profile <dir>]                # can each engine run a saki-builder command, before you dispatch
saki init-env --engine <e> [--profile <dir>] # provision ONE engine profile, then prove it
saki genesis "<product idea>" [--restart]    # start a product from scratch (spawns /saki-builder:genesis)
saki roadmap init [--profile <dir>] [--engine <e>]         # scaffold tasks/roadmap.md (spawns /saki-builder:roadmap init)
saki roadmap list                                           # work items in this repo
saki roadmap add "<intent>" --feature [--profile <dir>] [--engine <e>]  # also --epic --improvement --bug (one is required)

saki build  <roadmap-id|prd-path> [--follow] [--engine <e>] [--profile <dir>]   # alias
saki pickup <roadmap-id>                       [--follow] [--engine <e>]       # alias
saki rplan  <roadmap-id|plan-path>             [--follow] [--engine <e>]       # alias
saki prd-review | rplan-review | approved | qa | reviewer | wrap [--follow] [--engine <e>]  # aliases too

saki run build  <roadmap-id|prd-path> [--follow] [--engine <e>] [--profile <dir>]
saki run continue <workflowId> [--option <value>]       # resume parked/awaiting workflow
saki run pickup <roadmap-id>
saki run proto  <roadmap-id|prd-path>
saki run rplan  <roadmap-id|plan-path>
saki run tail <runId>                        # stream a deliberate child; exits with its verdict
saki run stop <runId>
saki runs                                    # runs the backend still holds

saki prd show <roadmap-id|path>              # resolves the id via the roadmap's Child PRD
saki prd lock <roadmap-id|path>              # idempotent — already-locked is success
saki proto    <roadmap-id|path> [--open]     # prints the gallery url; --open launches a browser

saki workitems                               # open PRDs and plans
saki branch                                  # current branch
saki branch list
saki branch switch <name> [--create]
saki mr create                               # push branch + open a merge request via glab
saki artifacts <runId>                       # see limitation below
saki screenshots                             # /qa screenshots + their urls
```

Every top-level run-verb alias (`build`, `pickup`, `rplan`, `prd-review`, `rplan-review`,
`approved`, `qa`, `reviewer`, `wrap`) is exactly `saki run <verb>` under a shorter name, routed
through the same `startRun` code path so the two forms never drift in argument handling. `proto` is
deliberately absent from the alias list — `saki proto <id>` already means "print the URL of an
already-rendered gallery", and one name can't mean both that and "render one". Only `wrap` accepts
`--heal`; passing it to any other verb is a usage error, not a silent no-op.

### `saki backend` — the daemon behind every other command

Every command except `saki backend *` runs a pre-flight health check first and, if the Go backend
isn't answering, starts it: `saki-backend` is spawned detached, tracked by a UID-scoped state file at
`$TMPDIR/saki-<uid>/backend.state.json`, and reused by later invocations. There is no manual
`./dist/saki-backend &` step. The auto-start emits one `daemon:autostart {result,pid}` line to
**stderr** and nothing to stdout, so `--json` output stays parseable.

`saki backend *` is the explicit lifecycle surface, and is the one command group that never
auto-starts — it must report the state it finds, not repair it first.

```bash
saki backend start     # start the daemon; idempotent — "already running (pid N)" and exit 0
saki backend stop      # SIGTERM, then SIGKILL after 5s; idempotent — "not running" and exit 0
saki backend status    # PID liveness + a health verdict
saki backend status --json
# {"pid":41233,"healthy":true,"goUrl":"http://127.0.0.1:8788","socketPath":"/tmp/saki-501/backend.sock"}
```

`socketPath` is the unix socket the backend binds alongside its loopback TCP port, owner-only
(`0600`). The CLI prefers it and falls back to TCP when it is absent, when `SAKI_BACKEND_URL` is set
explicitly, or on Windows (where the socket is a declared non-goal). `socketPath` is `null` whenever
the socket is unavailable.

`pid` is `null` when no daemon is tracked — which is not the same as "nothing is running". A backend
you launched yourself owns the port without a state record, so `status` reports
`{"pid":null,"healthy":true}` ("backend healthy (not daemon-tracked)"). Ordinary commands reuse that
backend rather than spawning a second one against the same port.

The state file and the socket both live in a UID-scoped directory that must be owned by you and not
group/other-writable. If it isn't, commands fail with exit 3 rather than trusting it — everything the
CLI dials and every PID it signals comes out of that directory.

Exit codes follow the usual contract: a backend that cannot be started is `3` (UNREACHABLE), never
`1`. All daemon waits share one wall-clock budget, so these commands return in ≤ 10 s even when the
binary is missing, unreachable, or wedged.

### `saki mcp` — the same journey commands as typed tools

Starts a stdio MCP server for agent harnesses that prefer a tool call over a shell. Registers all 13 tools
in the PRD's v1 scope — see `tasks/prd-mcp-surface-saki-mcp.md`:

| Tool | Wraps | Args |
|---|---|---|
| `saki_status` | `saki status` | none |
| `saki_doctor` | `saki doctor` | `profile?` |
| `saki_roadmap_list` | `saki roadmap list` | none |
| `saki_runs` | `saki runs` | none |
| `saki_prd_show` | `saki prd show` | `target` (roadmap id or `.md` path; a path resolving outside the repo cwd is refused before `cmdPrdShow` ever runs — an MCP tool's arguments can be steered by content already in the calling agent's context, unlike a human-typed CLI path) |
| `saki_run_start` | `saki run <verb>` | `verb`, `target?`, `profile?`, `engine?`, `heal?` (silently ignored for every verb but `wrap` — matches `cmdRunStart`'s own gate, not a CLI-parity `USAGE` rejection) — starts a run and returns immediately with a `runId`; no `--follow` (call `saki_run_tail` separately to block for the result). A `target` resolving outside the repo cwd is refused the same way `saki_prd_show`'s is, for every verb that takes one |
| `saki_run_tail` | `saki run tail` | `runId` — blocks until the run reaches a terminal state, mirroring the CLI's own untimed behavior. Output is capped at 200 content blocks (head + the terminal verdict) to avoid returning an unbounded transcript into the calling agent's context |
| `saki_run_stop` | `saki run stop` | `runId` |
| `saki_branch` | `saki branch` | none |
| `saki_branch_list` | `saki branch list` | none |
| `saki_branch_switch` | `saki branch switch` | `branch`, `create?` — the branch name needs no extra MCP-layer validation beyond what the backend already enforces (a leading `-`, `..`, and refname-invalid characters are rejected server-side on the create path; the switch-to-existing path is separately guarded by a fixed `--` argv separator) |
| `saki_mr_create` | `saki mr create` | none — the only tool here with `openWorldHint:true` (its own network call reaches a remote host via `glab` to push and open a real merge request; every other tool's own call stays within the local backend) |
| `saki_prd_lock` | `saki prd lock` | `target` (same shape and containment check as `saki_prd_show`'s) |

Every tool wraps the exact same `cmd*` function the CLI itself calls, so the exit-code contract is
translated once, never forked: a returned `ExitCode !== EXIT.OK` or a thrown `CliError` both map to
`isError:true`, with the numeric + symbolic exit code folded into the tool result's content (MCP's boolean
`isError` alone would collapse the CLI's six distinct codes into one bit). Requires the backend already
running — but since every `saki` command (including `saki mcp`) auto-starts the daemon, this is
satisfied automatically the first time an MCP client spawns it. Stdio only, no auth (matches the
backend's loopback-only, local-single-operator trust model).

Point your MCP client's config at the `saki` binary directly — the client spawns the process itself and
owns its stdin/stdout, so `saki mcp` is never started by hand in a shell (backgrounding it with `&` from
a terminal gives it a closed stdin, which the process reads as an immediate EOF and exits 0):

```json
{ "mcpServers": { "saki": { "command": "saki", "args": ["mcp"] } } }
```

### `saki doctor` — check before you dispatch

Probes each reported engine's binary (on PATH) and profile (resolves the saki-builder commands) —
without spawning anything. `--profile <dir>` pins which profile is checked; omitted, it's the default
one. Exit `0` means every reported engine is ready; exit `1` means at least one is not (`--json` shows
which, plus a `fix` command when one has been authored). This command reports `codex`, `opencode`,
`omp`, and `claude`. A studio-unreachable or gated-studio failure surfaces as the **existing** `3`/`6`
codes (see the exit-code table above), not a doctor-specific one.

To *fix* an engine doctor reports as not-ok, run `saki init-env --engine <e>` — doctor never repairs
anything, by design.

### `saki init-env` — provision an engine profile, then prove it

The mutating counterpart to `saki doctor`: it installs the saki-builder commands into ONE engine's
profile and then re-runs **the same proof** doctor uses. `--profile <dir>` pins which profile is
provisioned, exactly as it pins which profile doctor checks; omitted, it is the engine's default.

```console
$ saki init-env --engine codex --profile /tmp/p1 --json
{"engine":"codex","profile":"/tmp/p1","changed":true,"status":"ok","reason":"","fix":""}
$ saki init-env --engine codex --profile /tmp/p1 --json   # idempotent — nothing left to do
{"engine":"codex","profile":"/tmp/p1","changed":false,"status":"ok","reason":"","fix":""}
```

| Field | Meaning |
|---|---|
| `engine` | the engine that was provisioned |
| `profile` | the profile that was provisioned — `"default"` when `--profile` was omitted. **`"default"` is a label, not a path**: to check it afterwards call `saki doctor --json` with *no* `--profile`, never `--profile default` |
| `changed` | whether the selected namespace actually changed, from a before/after fingerprint of the files the proof reads. **Never** inferred from an installer's exit code |
| `status` | `ok` only if the shared proof passed; `failed` for setup/proof failure |
| `reason` / `fix` | why it failed, and the remediation — the same `fix` text doctor prints |

| Exit | When |
|---|---|
| `0` | the engine's shared proof passed |
| `1` | setup or the proof failed. **The same code `saki doctor` returns** for a failed engine report — not `5`, which is reserved for the `{ok:false}` refusal envelope |
| `2` | bad arguments: unknown/missing `--engine`, or a relative `--profile` escaping the repo |

`status` is decided by the proof, never by the installer. An unprovisioned codex still exits `0` (the
model just answers that it cannot find the command), so a child's exit code proves nothing — and a
*failing* child proves nothing either: a repeat `codex plugin marketplace add` reports "already
added" while the profile is perfectly fine. OMP follows the same rule: only its installed-plugin
registry plus the plugin's build skill settles readiness. Only reading the profile settles it.

**Scope today: claude, codex, opencode, and omp.** Claude provisioning runs the two user-scope
commands below and proves the resulting `$HOME/.claude`-compatible profile. An absolute `--profile` is
taken as given (a legitimate profile lives outside the repo, e.g. `~/.claude`); only a *relative* one
is confined to the repository.

### Engines — `--engine claude|opencode|codex|omp`

Every run-start command takes `--engine`, choosing which agent runtime executes the run. **Omit it for
`claude`** when using the default runtime; provision its profile first if the shared proof is not ready.

| `--engine` | Binary | How it resolves a `/saki-builder:*` command | Provision with |
|---|---|---|---|
| `claude` *(default)* | `claude` | from the message | **`saki init-env --engine claude`** (runs the user-scope marketplace/install commands, then proves the profile) |
| `opencode` | `opencode` | via `--command` — its `run` never expands a slash command that arrives in the message | **`saki init-env --engine opencode`** (runs `opencode plugin @saketek/saki-builder --global` with `XDG_CONFIG_HOME` pinned to the profile, then proves the result) |
| `codex` | `codex` | from the message, like claude — via the saki-builder plugin's skills | **`saki init-env --engine codex`** (runs `codex plugin marketplace add …` + `codex plugin add saki-builder@saketek`, then proves the result) |
| `omp` | `omp` | from the message, through the Claude-compatible saki-builder plugin | **`saki init-env --engine omp`** (adds the marketplace and installs `saki-builder@saketek` under OMP's user plugin registry, then proves the build skill) |

```bash
# one-time provisioning — one command, and it verifies itself
saki init-env --engine codex          # exit 0 means the profile really resolves the commands

# then, from any repo
saki build E22 --engine codex --follow
```

By hand, if you prefer — this is exactly what `saki init-env` runs:

```bash
codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git
codex plugin add saki-builder@saketek
bash scripts/install-codex-skills.sh   # legacy checker (and a --symlink fallback for pinned profiles)

# claude — user scope, with CLAUDE_CONFIG_DIR=<dir> for an explicit profile
claude plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git --scope user
claude plugin install saki-builder@saketek --scope user

# opencode — the single command form `saki init-env --engine opencode` runs
opencode plugin @saketek/saki-builder --global   # run with XDG_CONFIG_HOME=<dir> to target a profile

# omp — for an explicit saki profile, prefix both commands with HOME=<dir>; without a prefix they use
# OMP's default HOME.
HOME=<dir> omp plugin marketplace add https://github.com/drayanaindra/saki-builder.git
HOME=<dir> omp plugin install saki-builder@saketek --scope user
```

`--profile <dir>` pins that run's engine config dir, and means a different variable per engine:
`CLAUDE_CONFIG_DIR=<dir>` (claude), `XDG_CONFIG_HOME=<dir>` (opencode → reads `<dir>/opencode/`),
`CODEX_HOME=<dir>/codex` (codex), `HOME=<dir>` (omp → reads `<dir>/.omp/plugins/`). One profile dir
can hold all four side by side.
**Known limitation — claude profile isolation is best-effort.** codex (`CODEX_HOME`), opencode
(`XDG_CONFIG_HOME`) and OMP (`HOME`) genuinely isolate `plugin` state per profile; claude does not.
This Claude limitation is hand-verified against claude 2.1.235: `plugin marketplace add`/`plugin install`
write their install record (`installed_plugins.json` — version, installPath, git SHA) only to the real
`~/.claude`, for every `--scope` (`user`, `project`, `local`) and regardless of `CLAUDE_CONFIG_DIR`.
`--scope project`/`--scope local` only add an enabled-plugin pointer to the target cwd's own
`.claude/settings.json` — the install itself stays global. `saki init-env --engine claude --profile
<dir>` still pins `CLAUDE_CONFIG_DIR` (correct given codex/opencode's contract), but that pin cannot
redirect where claude's plugin state lands — there is currently no upstream mechanism that does.

**An unprovisioned engine is refused at the spawn** — exit `1`, with the fix in the message, before
anything launches:

```console
$ saki build E22 --engine codex
error: engine profile cannot resolve the saki-builder commands: codex profile does not resolve
@saketek/saki-builder: /Users/me/.codex/config.toml registers no enabled saki-builder plugin and
/Users/me/.codex/skills/build/SKILL.md is absent — run:
codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git
codex plugin add saki-builder@saketek
(or bash scripts/install-codex-skills.sh to check)
```

This is deliberate: left to run, an unprovisioned engine **exits 0** — the model just says it cannot
find the command — so the CLI would park a build that never started. Install state is proven by
reading the profile, never inferred from a run's exit code.

### Driving a build end to end

```bash
saki roadmap list
saki prd lock E12
saki run build tasks/prd-checkout.md --follow && echo "build green"
saki mr create
```

`--follow` follows the workflow, not one child turn. It exits 0 only after verified completion; a
parked, awaiting, failed, stopped, or dropped workflow stream is non-zero.

`saki build <id> --follow` starts (or re-adopts) `POST /api/workflow`. PRD-track work runs
`resolve → pickup (when needed) → proto → lock → build → verify`; Plan-track work runs
`resolve → rplan → rplan-review → approved → qa → reviewer → wrap → verify`. Child turns and
usage-limit waits remain internal transitions. Use `saki run continue <workflowId>` for an explicit
parked recovery, or `--option` for a recorded decision; options are validated by the backend.

### Workflows are de-duplicated

`saki run build <arg>` sends the roadmap id/path to the backend. The backend resolves it to a stable
repo/item lane, so an id and its Child PRD path share one workflow rather than running twice:

```console
$ saki run build tasks/prd-x.md --json
{"workflowId":"09e16cc4-…","phase":"build","status":"running","deduped":false}
$ saki run build tasks/prd-x.md --json      # retry
{"workflowId":"09e16cc4-…","phase":"build","status":"running","deduped":true}       # same workflow
```

## Known limitations

**1. `saki artifacts` needs a browser session (exit 6).**
The artifacts route requires the Express layer's session, so `DEV_MODE=1` does **not** grant access
by itself — it only lifts the auth gate, not the session requirement. The guard is an IDOR
protection, so the CLI **explains** the 401 rather than weakening the server. View artifacts in a
studio UI when one is layered on top. To exercise it without one, run `node scripts/stub-studio.mjs`
and point `SAKI_STUDIO_URL` at it.

**2. Hosted / multi-tenant studios are out of scope.** No `saki login`, no device-code flow, no
token storage. Local only.

**3. Windows is not yet supported.** The npm package ships macOS/Linux binaries only, and the daemon
lifecycle (`src/daemon.ts`) is untested on Windows; build from source there.

## Command namespace

The CLI always emits the canonical `/saki-builder:<verb>`. The **backend** rewrites the namespace to
match the target Claude profile at spawn time, so a bare/symlink profile receives `/build` and a
plugin profile `/saki-builder:build`. The CLI deliberately does not try to detect this itself —
guessing from the client side is the exact bug that rewrite was written to fix.

## Development

```bash
npm install
npm run build             # CLI -> dist/index.js
npm run backend:build     # Go  -> dist/saki-backend
npm run dev                       # tsx src/index.ts
npm test                          # vitest run
npm run test:coverage             # vitest run --coverage
npm run typecheck                 # tsc --noEmit
npm run backend:test              # cd backend && go test ./...
scripts/free-e2e-ports.sh         # reclaim ports wedged by a killed e2e run
npm run e2e                       # playwright test
```

Two runtime dependencies: `@modelcontextprotocol/sdk` + `zod` (for `saki mcp`, lazy-loaded — every other
command still pays no cost for them), plus `undici` (daemon Unix-socket transport). Otherwise: node's
built-in `fetch`, `node:*` builtins, and a hand-rolled arg parser.
