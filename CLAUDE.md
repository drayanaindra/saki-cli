# saki-cli

Headless build orchestrator for coding agents. Drives a **PRD → plan → build → QA → review**
journey from a terminal, spawning `claude` / `codex` / `opencode` and *supervising* the runs —
journalled to disk, restart-survivable, retry behind a progress-tied circuit breaker with a hard
budget, deduped so work can never double-fire. No UI, no browser.

It is the **runtime**. The workflow it executes (`/saki-builder:*` skills, agents, hooks) lives in
the separate `saki-builder` repo. Both are required — without the skills installed a run exits 0
while the model just says it cannot find the command, so the backend refuses such a spawn up front.

## Tech stack

| Side | Runtime | Layout | Type check | Tests |
|---|---|---|---|---|
| CLI (`src/`) | Node ≥ 20, TS 5.5, ESM/NodeNext | Stage 2 modular (`commands/` + shared) | `npm run typecheck` | `npm test` (vitest) |
| Backend (`backend/`) | Go 1.25 | Stage 3 hexagonal (`domain`/`usecase`/`adapter`/`infra`) | `go vet ./...` | `npm run backend:test` |

## Commands

```bash
npm run build            # CLI -> dist/index.js          npm run dev   # tsx src/index.ts
npm run backend:build    # Go  -> dist/saki-backend      npm run test:coverage
./dist/saki-backend &    # listens on 127.0.0.1:8788
scripts/free-e2e-ports.sh        # reclaim ports wedged by a killed e2e run
scripts/install-codex-skills.sh  # provision a codex profile with saki-builder
```

## Bounded Contexts

| Context | Where | Owns |
|---|---|---|
| Run orchestration | `backend/{domain,usecase}/` run·spawn·stream·stop·reaper·rehydrate, `src/commands/run{,s}.ts` | spawn, supervise, journal, resume |
| Journey artifacts | `backend/domain/{prd,roadmap,plantrack,workitems}.go`, `src/commands/{prd,roadmap}.ts` | PRD / roadmap / plan state |
| Repo & git | `backend/{domain,infra}/{repo,branchname,gitcli,gitargs}.go`, `src/commands/repo.ts` | branch, MR, git writes |
| Proto & assets | `backend/domain/{protoasset,screenshot}.go`, `src/commands/proto.ts` | proto previews, screenshots |
| Env & provisioning | `backend/domain/{envstate,newproject,folderchoice,slashcmd}.go`, `backend/infra/{codex,opencode,osprofiles}.go` | engine profile detection, scaffolds |
| Blockers & locks | `backend/domain/{blockers,lock,resolveq}.go` | resolve queue, session gating |

Contexts talk through `usecase` ports (`ports.go`, `run_ports.go`, `content_ports.go`) — never by
reaching into another context's `infra`.

## Architecture Stage

- **Backend = Stage 3 (hexagonal), already there.** `domain` has zero outbound deps; `infra`
  implements `usecase` interfaces. Keep it that way — a `domain` file importing `net/http`,
  `os/exec` or `infra` is a layering break, not a shortcut.
- **CLI = Stage 2 (modular).** One file per command in `src/commands/`, shared plumbing at
  `src/{args,client,ctx,exit,output,sse,resolve,routes}.ts`. Commands never import each other.
  `saki mcp` (`src/commands/mcp.ts`) is the one sanctioned exception: it composes `src/mcp/` (the
  MCP tool-registration layer, one file per tool under `src/mcp/tools/`), which itself imports the
  other commands' `cmd*` functions unchanged — that reuse is the whole point of the MCP surface
  (never re-implement a command's logic for its MCP tool).
- Transition triggers + the read-only detector: `/saki-builder:arch-check`.

## Project rules

1. **The exit code is the API.** `src/exit.ts` — an agent branches on these numbers, so they are as
   load-bearing as stdout and must never be renumbered casually. Two upstream routes report failure
   inside an HTTP 200 body, so status alone is not a success signal. Every command takes `--json`.
2. **A fake-binary test cannot prove an engine invocation.** Standing rule, documented at
   `e2e/codex-spawn.spec.ts:10-17`: the opencode command form shipped green against fakes while
   every real run no-opped. Engine-invocation specs use the REAL binary or they prove nothing.
3. **Loopback-only is deliberate, tested behaviour — never relax it.** The backend binds `127.0.0.1`
   *and* rejects any non-loopback `Host` header (`backend/adapter/originguard.go`). It spawns agents
   with sandboxing disabled, so an off-host bind would be remote code execution.
4. **Never break Inv-1 / Inv-2** (see `docs/project-context.md`): Go journals stay in their own
   subdir, and a restart must never lose or mis-report an in-flight run.
5. **vitest owns `src/**/*.test.ts` only.** `e2e/` is Playwright; it must stay excluded from
   `vitest.config.ts` or the specs fail with "did not expect test.describe() to be called here".
6. **A non-claude spawn is scrubbed of the other engines' env namespaces**
   (`backend/infra/spawner.go:263`) so one runtime never inherits another's live session tokens.
   Adding a runtime means one case in `scrubProfileEnv` + `engineProfileEnv`, not a new branch.

## Checklists

**Before commit** — `npm run typecheck` · `npm test` · `npm run backend:test` · `go vet ./...`
(cd `backend`). Coverage floor is 80% (`npm run test:coverage`).

**Touching a route** — wire it in `src/routes.ts`, give it an exit code from `src/exit.ts`, add the
`--json` shape, and update `docs/cli-reference.md`. A route absent from the reference is unshipped.

**Touching spawn / journal / resume** — re-read Inv-1 + Inv-2 first, then add the regression test
*before* the fix. This is the retry-engine core: backoff, circuit breaker, hard budget and the
dedupe gate are load-bearing, never "hardening for later".

@~/.claude/docs/ddd-patterns.md
@~/.claude/docs/modular-architecture.md
@.claude/memory/patterns.md
