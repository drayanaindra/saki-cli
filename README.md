# saki

[![npm](https://img.shields.io/npm/v/%40saketek%2Fsaki-cli)](https://www.npmjs.com/package/@saketek/saki-cli)

A headless build orchestrator for coding agents. `saki` drives a disciplined
**PRD → plan → build → QA → review** journey from a terminal, spawning **claude**, **codex**,
**opencode** or **omp** and supervising the runs — no UI, no browser.

It is a *supervisor*, not a wrapper. Runs are journalled to disk, survive a restart, retry behind a
progress-tied circuit breaker with a hard budget, and dedupe so the same work can never double-fire.
That is the part that makes unattended agent builds survivable.

> **Status: v0.1, early.** The pieces below work and are tested, but this was extracted from a larger
> tool — see [Not built yet](#not-built-yet) for what's still missing. Expect sharp edges.

## How the two repos fit together

`saki` is the **runtime**. The workflow it runs — the `/saki-builder:*` commands — lives in a separate
MIT repo:

| Repo | What it is |
|---|---|
| **saki-cli** (this one) | the runtime: spawns the engine, tracks runs, serves the journey API, and the `saki` CLI |
| [**saki-builder**](https://github.com/drayanaindra/saki-builder) | the workflow: the skills, agents and hooks the engine executes |

**You need both.** Without the skills installed, a run still exits 0 while the model simply answers
that it cannot find the command — so the backend refuses such a spawn up front rather than parking a
build that never started.

## Requirements

- **Supported platforms:** macOS + Linux (amd64/arm64) via the npm package; Windows is not yet
  supported (build from source only, and the daemon lifecycle is untested there)
- **Node ≥ 20** (the CLI) and **Go ≥ 1.25** (to build the backend from source)
- **An engine on PATH**: `claude`, `codex`, `opencode`, or `omp`
- **`saki-builder` installed into that engine's profile** — `saki init-env --engine <engine>` runs the
  fixed marketplace install and then proves the profile resolves the workflow
- **Hermes / Oh My Pi agents**: select `--engine omp`; `saki init-env --engine omp` provisions the
  Claude-compatible `saki-builder` plugin and proves the OMP profile before a run
- **`git`**, plus **`glab`** if you want `saki mr create`

## Quickstart

**Install via npm** (macOS/Linux, amd64/arm64 — the backend binary downloads automatically):

```bash
npm install -g @saketek/saki-cli
saki status
```

**Or install via Homebrew** (macOS/Linux — runs `saki-backend` as a persistent background service
instead of the CLI's lazy per-command auto-start, useful if something other than `saki` itself needs
to reach the backend independently):

```bash
brew tap drayanaindra/tap
brew trust --tap drayanaindra/tap   # non-official tap — Homebrew asks you to confirm this once
brew install saki
brew services start saki
```

If you've already used the `saki` CLI directly (which lazily spawns its own backend), run
`saki backend stop` first — `saki-backend` binds `127.0.0.1:8788` exclusively and exits immediately
if the port's already held, so a lazily-spawned instance and the brew service can't both own it. Once
the service owns the port, `saki` CLI commands reuse it automatically — no conflict in that direction.

Note: `saki-backend` writes its own state file on every startup (keyed on your user + temp dir), so
`saki backend stop` can stop the brew-managed process too if it finds it — `brew services`' `keep_alive`
means it self-heals (restarts automatically), but you'll briefly see it as stopped. Use
`brew services stop saki` if you want it to actually stay down.

**Or build from source** (any platform Go ≥ 1.25 targets, and the only path on Windows):

```bash
git clone https://github.com/drayanaindra/saki-cli.git && cd saki-cli
npm install
npm run build            # CLI  -> dist/index.js
npm run backend:build    # Go   -> dist/saki-backend

node dist/index.js status
```

```console
$ node dist/index.js status
backend   http://127.0.0.1:8788
reachable yes (saki-backend)
express   not configured (set SAKI_STUDIO_URL to include it)
```

Then, from any repo that has a `tasks/roadmap.md`:

```bash
saki roadmap list
saki build <roadmap-id|prd-path> --follow    # follows the workflow; 0 only after verification
```

## Built for agents

Every command takes `--json`, and the **exit code is the contract** — branch on it, never on stdout.
Two upstream routes report failure inside an HTTP 200 body, so the status line is not a reliable
success signal; the exit code is.

| Code | Meaning |
|---|---|
| 0 | succeeded (for `--follow`, the run ended `done`) |
| 1 | error — including a run that ended `error` |
| 2 | bad arguments |
| 3 | a server is not answering, or not configured |
| 4 | not found (unknown run, no roadmap, no PRD) |
| 5 | reached, but refused (git/glab stderr in the message) |
| 6 | gated by a session |

Full reference: [`docs/cli-reference.md`](docs/cli-reference.md) ·
agent guide: [`docs/saki-cli-agent-guide.md`](docs/saki-cli-agent-guide.md)

## Security posture

Read this before running it anywhere but your own machine.

- **The backend binds `127.0.0.1` only** and additionally rejects any request whose `Host` header is
  not loopback. It is unreachable off-host by construction, and that is deliberate, tested behaviour —
  not a default to relax.
- **It spawns agents with their sandboxing disabled.** Headless autonomy requires it: claude gets
  `--dangerously-skip-permissions` (init only), codex `--dangerously-bypass-approvals-and-sandbox`,
  opencode `--auto`, and omp `--auto-approve`. **An agent run can therefore write anywhere your user
  can.** Run it on repos you control, on a machine you trust.
- **A non-claude spawn is scrubbed of the other engines' environment namespaces**, so one runtime never
  inherits another's live session tokens. Pinned OMP profiles additionally redirect `HOME` to the
  selected profile so OMP's plugin registry and cache stay isolated.

Given the above: do not expose this port, and do not run it as a privileged user.

## Not built yet

Honest list of what the packaging still lacks (see [Requirements](#requirements) for platform support):

- `saki artifacts` is session-gated via the studio; the local stub `scripts/stub-studio.mjs` serves the route

## License

MIT — see [LICENSE](LICENSE).
