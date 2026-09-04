# Project Context — saki-cli

> Scope: ONLY what no tool can derive. God nodes, communities, module sizes and architecture
> tier are NOT here — `graphify-out/GRAPH_REPORT.md` and `/saki-builder:arch-check` own those.
> Last verified: 2026-08-15 (commit 2b43e38)

## Topology

| Deployable | Runtime | Entrypoint |
|---|---|---|
| `saki` (CLI) | Node ≥ 20, ESM | `src/index.ts:375` |
| `saki-backend` | Go 1.25 | `backend/cmd/server/main.go:35` |

**Cross-boundary edges** (invisible to graphify — same-language AST extraction only):

| From | To | Transport | Call site | Handler |
|---|---|---|---|---|
| `saki` | `saki-backend` | HTTP, `127.0.0.1:8788` | `src/client.ts:25` | `backend/adapter/http.go:69-74` |
| `saki` | `saki-backend` | SSE `GET /events/{id}` | `src/sse.ts:54` | `backend/adapter/http.go:74` |
| `saki-backend` | studio `apps/server` (**out of repo**, `:8787`) | HTTP forward, only when `SAKI_UPSTREAM` is set | `backend/infra/proxy.go:31` | external — unset ⇒ standalone (`main.go:49`) |
| `saki-backend` | agent process (`claude`/`codex`/`opencode`/`omp`) | detached `exec.Command` spawn | `backend/infra/spawner.go:126` | the engine binary on `PATH` |
| agent process | `saki-backend` | **NDJSON journal file on disk** (no call, no import — the process writes, the backend polls) | engine stdout → `backend/infra/journal.go:40` | `backend/infra/journalreader.go` |

That last row is the one that matters most: the agent and the backend are coupled entirely through a
file, so *no* static analysis will ever draw an edge between them.

## Invariants

| Invariant | Enforced at | Breaks if |
|---|---|---|
| **Inv-1** — Go journals live in their OWN subdir (`<runsDir>/go`) | `backend/infra/journal.go:58` (`GoRunsDir`) | a journal is written flat; `apps/server`'s flat readdir adopts it and mis-owns the run |
| **Inv-2** — a restart never loses or mis-reports an in-flight run | `backend/usecase/rehydrate.go:6` (rebuild) + `backend/usecase/reaper.go:12` (sweep) | rehydrate is skipped ⇒ a run that completed during downtime hangs `running` forever |
| Backend is reachable from loopback **only** | `backend/cmd/server/main.go:141` (`127.0.0.1` bind) **and** `backend/adapter/originguard.go:48` (non-loopback `Host` rejected) | either control is relaxed — agents are spawned with sandboxing disabled, so an off-host bind is remote code execution, not a config preference |
| Exit codes are a frozen contract | `src/exit.ts:8` | renumbered ⇒ every agent branching on them silently mis-reads success as failure. Two upstream routes return `{ok:false}` in an HTTP 200, so status alone cannot substitute |
| A spawn is refused when the engine profile cannot resolve the `saki-builder` commands | `backend/usecase/spawn.go:27` (`ErrEngineNotProvisioned`) | the run exits 0 having done nothing, and the build is parked on a run that never started |
| A non-claude spawn is scrubbed of the other engines' env namespaces | `backend/infra/spawner.go:263` (`scrubProfileEnv`) | one runtime inherits another's live session tokens |
| `domain` has zero outbound dependencies | layout convention, `backend/domain/` | a `domain` file imports `net/http`, `os/exec` or `infra` — the hexagon inverts and `usecase` ports stop being substitutable |

## Deliberate non-goals

- **No off-host binding, ever.** Loopback-only is architectural, not a default awaiting relaxation —
  see the invariant above. Do not add a `HOST`/`--bind` flag.
- **No workflow content in this repo.** Skills, agents and hooks live in `saki-builder`; this repo is
  the runtime that executes them. Adding a `/saki-builder:*` skill here forks the workflow into two
  sources of truth.
- **No UI, no browser.** `saki` is headless by design; `proto` serves artifacts, it does not render.
- **vitest never runs `e2e/`.** Those are Playwright specs and are excluded in `vitest.config.ts` on
  purpose — a fake-binary spec cannot prove an engine invocation, so they need the real runtime.
- **No shared in-process state between the CLI and the backend.** HTTP + SSE + the journal file are
  the only edges. A direct import across that boundary re-couples two independently-shipped binaries.
