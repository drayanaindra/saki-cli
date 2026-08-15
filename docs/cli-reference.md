# `@saki/cli` — command line for saki studio

Lets an agent (or you) drive the studio from a terminal instead of the web UI: start a build, follow
it, lock a PRD, switch a branch, open an MR.

It is a **thin client** over the studio server's existing HTTP API — it adds no routes and
re-implements no orchestration. Anything the CLI does, the studio does, the same way the UI does it.

## Install

The bin is published into the workspace root's `.bin` by npm, but only once `dist/` exists — so
**build before the link appears**:

```bash
npm run build -w @saki/cli   # emits apps/cli/dist
npm install                  # links node_modules/.bin/saki -> apps/cli/dist/index.js
node_modules/.bin/saki --help
```

Put `./node_modules/.bin` on your `PATH` (or alias it) to just type `saki`.

## Point it at a studio

**One backend by default.** The CLI talks to the Go backend, which serves every journey command —
runs, roadmap, prd, branch, MRs, proto, screenshots. Neither the web UI (`:5180`) nor the Express
server (`:8787`) is needed.

| Variable | Default | Serves |
|---|---|---|
| `SAKI_BACKEND_URL` | `http://127.0.0.1:8788` | Go (`backend/`) — **everything the CLI needs** |
| `SAKI_STUDIO_URL` | *(unset — no Express)* | **Opt-in.** Express (`apps/server`), when you are running the full pipeline-studio dev studio. Adds exactly two things: `saki artifacts`, and the `devMode`/`auth` lines in `saki status`. |

```bash
npm run dev:backend                           # that's all the CLI needs
saki status
```

Setting `SAKI_STUDIO_URL` switches the CLI into **two-server mode**, where it splits traffic by path
exactly the way the web UI's Vite proxy does (`src/routes.ts`, a port of
`frontend/src/proxy-routes.ts`). Start the full studio with `./run.sh` (it also starts the web UI), or
`npm run dev:server` + `npm run dev:backend`.

> **Why the default flipped.** The two-server split is a *pipeline-studio* concern. `backend/` +
> `apps/cli` are being extracted into a standalone open-source repo where there is no Express at all,
> so one backend is the normal case and the split is the special one. When that extraction lands, the
> `SAKI_STUDIO_URL` branch and the route table it drives are deleted outright.

> **Loopback hosts only.** Both servers reject any request whose Host header isn't `localhost` /
> `127.0.0.1` / `::1` (`apps/server/src/originGuard.ts:33`, `backend/adapter/originguard.go:32`), and
> the Go backend additionally *binds* the loopback interface only. Pointing either variable at a LAN
> address or hostname gets a 403 "cross-origin blocked", not a connection error. This is a local
> single-operator tool by design.

```bash
saki status                                   # local studio
SAKI_STUDIO_URL=http://localhost:8799 saki status
```

Every command is repo-scoped and defaults to the current directory; override with `--cwd <dir>`.

## ⚠ `DEV_MODE=1` — only when you set `SAKI_STUDIO_URL`

**Not applicable in the default one-backend mode.** The Go backend has no session gate at all, so
there is nothing to be exempted from. This section applies only when you have opted into Express.

The CLI holds no session cookie by design — it is a **local, single-operator** tool. `DEV_MODE=1` is
what exempts it from the studio's session gate (`apps/server/src/authGate.ts:71`).

Without it, every gated route answers 401 and the CLI exits **6**:

```console
$ saki branch
error: authentication required
  the studio is gating this route — restart it with DEV_MODE=1 (check with `saki status`)
```

`saki status` probes **both** servers and tells you which mode Express is in:

```console
$ saki status
studio    http://localhost:8787
reachable yes (pipeline-studio-server)
backend   http://127.0.0.1:8788
reachable yes (pipeline-studio-backend)   <- must also be up: runs/roadmap/prd/branch live here
devMode   on              <- must be "on" for the rest of the CLI
auth      authenticated
runs      allowed
```

Either server down → the report still prints (so you see which one), stderr names what that server
serves, and the exit code is **3**. Each `reachable` line belongs to the URL above it, and each
server names itself in the parentheses, so a probe that reached the same process twice is visible.

## Exit codes — the machine contract

Branch on these, not on stdout. **Two studio routes report failure inside an HTTP 200 body**
(`/api/switch-branch` and `/api/create-mr` return `{ok:false, error}` when git or `glab` fails), so
the HTTP status is *not* a reliable success signal. The exit code is.

| Code | Name | Meaning |
|---|---|---|
| 0 | `OK` | Succeeded. For `run tail`, the run ended `done` |
| 1 | `ERROR` | Unexpected failure — also: `run tail` on a run that ended `error` (incl. stopped) |
| 2 | `USAGE` | Bad arguments: unknown command/flag, missing or extra positional |
| 3 | `UNREACHABLE` | A studio server is not answering, **or the one you asked for is not configured**. For `status`, the Go backend `:8788` — plus Express `:8787` when `SAKI_STUDIO_URL` is set. Also `saki artifacts` with no Express configured: the arguments were valid, the server simply isn't there (which is why it is 3, not 2) |
| 4 | `NOT_FOUND` | Unknown run, no roadmap, item has no PRD, PRD not on disk |
| 5 | `REMOTE_FAILED` | Studio reached, operation refused (`{ok:false}` — git/glab stderr) |
| 6 | `AUTH_REQUIRED` | Studio gated the route (401/403) — usually a studio without `DEV_MODE=1`; also artifacts |

Run status vocabulary is the server's: **`running` / `done` / `error`** (`runManager.ts:50`). `done`
is the only success value — a stopped run ends `error` with a null exit code.

Errors and hints go to **stderr**; results go to **stdout**, so `2>/dev/null` leaves clean data.

## Commands

Add `--json` to any command for one compact machine-readable line.

```bash
saki status                                  # is the studio up, and will it let me in
saki roadmap list                            # work items in this repo
saki roadmap add "<intent>" --feature        # also --epic --improvement --bug (one is required)

saki build  <roadmap-id|prd-path> [--follow] [--engine <e>] [--profile <dir>]   # alias
saki pickup <roadmap-id>                       [--follow] [--engine <e>]       # alias
saki rplan  <roadmap-id|plan-path>             [--follow] [--engine <e>]       # alias

saki run build  <roadmap-id|prd-path> [--follow] [--engine <e>] [--profile <dir>]
saki run pickup <roadmap-id>
saki run proto  <roadmap-id|prd-path>
saki run rplan  <roadmap-id|plan-path>
saki run tail <runId>                        # stream; exits with the RUN's verdict
saki run stop <runId>
saki runs                                    # runs the studio still holds

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

### Engines — `--engine claude|opencode|codex`

Every run-start command takes `--engine`, choosing which agent runtime executes the run. **Omit it for
`claude`** — the default, and the only one needing no extra setup.

| `--engine` | Binary | How it resolves a `/saki-builder:*` command | Provision with |
|---|---|---|---|
| `claude` *(default)* | `claude` | from the message | already true if the studio works |
| `opencode` | `opencode` | via `--command` — its `run` never expands a slash command that arrives in the message | `opencode plugin @saketek/saki-builder --global` + `npx @saketek/saki-builder install --global` |
| `codex` | `codex` | from the message, like claude — via the saki-builder plugin's skills | `codex plugin add saki-builder@saketek` |

```bash
# one-time provisioning
codex plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git
codex plugin add saki-builder@saketek
bash scripts/install-codex-skills.sh   # verifies it; prints the fix if unprovisioned

# then, from any repo
saki build E22 --engine codex --follow
```

`--profile <dir>` pins that run's engine config dir, and means a different variable per engine:
`CLAUDE_CONFIG_DIR=<dir>` (claude), `XDG_CONFIG_HOME=<dir>` (opencode → reads `<dir>/opencode/`),
`CODEX_HOME=<dir>/codex` (codex). One profile dir can hold all three side by side.

**An unprovisioned engine is refused at the spawn** — exit `1`, with the fix in the message, before
anything launches:

```console
$ saki build E22 --engine codex
error: engine profile cannot resolve the saki-builder commands: codex profile does not resolve
@saketek/saki-builder: /Users/me/.codex/config.toml registers no enabled saki-builder plugin and
/Users/me/.codex/skills/build/SKILL.md is absent — run `codex plugin add saki-builder@saketek`
(or bash scripts/install-codex-skills.sh to check)
```

This is deliberate: left to run, an unprovisioned engine **exits 0** — the model just says it cannot
find the command — so the studio would park a build that never started. Install state is proven by
reading the profile, never inferred from a run's exit code.

### Driving a build end to end

```bash
saki roadmap list
saki prd lock E12
saki run build tasks/prd-checkout.md --follow && echo "build green"
saki mr create
```

`--follow` blocks until the run settles and adopts the run's own exit code, so `&&` chaining works.

### Build runs are de-duplicated

`saki run build <arg>` resolves `<arg>` (a roadmap id **or** a path) to the **absolute PRD path** and
sends `meta = {kind:'build', laneKey:<that absolute path>}` — the same key the web UI sends
(`frontend/src/App.tsx:1447`) and the one the server dedupes on (`apps/server/src/index.ts:236`,
`runManager.ts:659`). Because both surfaces agree on the key, a build started in the UI and a
`saki run build` for the same PRD share one lane rather than running twice:

```console
$ saki run build tasks/prd-x.md --json
{"runId":"09e16cc4-…","deduped":false}
$ saki run build tasks/prd-x.md --json      # retry
{"runId":"09e16cc4-…","deduped":true}       # same run, nothing new spawned
```

## Known limitations

**1. `saki artifacts` needs a browser session (exit 6).**
`GET /api/runs/:id/artifacts` reads the session directly (`index.ts:2195`) rather than relying on
the auth middleware, so `DEV_MODE=1` does **not** grant access — verified against a live studio. The
guard is an IDOR protection (its sibling route returns 404 instead of 403 specifically to avoid
leaking artifact existence across users), so the CLI **explains** the 401 rather than weakening the
server. View artifacts in the studio UI.

**2. Hosted / multi-tenant studios are out of scope.** No `saki login`, no device-code flow, no
token storage. Local only.

## Command namespace

The CLI always emits the canonical `/saki-builder:<verb>`. The **server** rewrites the namespace to
match the target Claude profile at spawn time (`index.ts:1270` → `cmdNs.ts:26,41`), so a bare/symlink
profile receives `/build` and a plugin profile `/saki-builder:build`. The CLI deliberately does not
try to detect this itself — guessing from the client side is the exact bug `cmdNs.ts:20-24` was
written to fix.

## Development

Bins are hoisted to the repo root, so run tooling **from this workspace with the root `.bin`**:

```bash
cd apps/cli
../../node_modules/.bin/vitest run          # tests
../../node_modules/.bin/vitest run --coverage
../../node_modules/.bin/tsc --noEmit        # typecheck
../../node_modules/.bin/tsc                 # build to dist/
```

Do **not** use `npm run test -w @saki/cli` locally — RTK intercepts `npm run` and returns stale
results (see the root `CLAUDE.md`).

Zero runtime dependencies: node's built-in `fetch`, `node:*` builtins, and a hand-rolled arg parser.
