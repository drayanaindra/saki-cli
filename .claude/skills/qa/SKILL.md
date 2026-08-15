---
name: qa
description: Run QA against the plan's acceptance criteria for saki-cli. Reads Success Criteria from the plan, runs each against BOTH the TS CLI and the Go backend, fills in the checklist, reports pass/fail per criterion.
user-invocable: true
---

# QA — saki-cli

Project override of the global `qa`. **The criteria-driven method is unchanged** — read the plan's
Success Criteria, classify each criterion, run the best available command for its type, give every
criterion a verdict. What this file adds is the project's concrete wiring, which the global skill
would otherwise have to guess at (and would guess wrong — see the Go module note below).

---

## Stack — known, do not re-detect

**Dual-stack, and the Go module is at `backend/go.mod`, NOT the repo root.** A generic `ls go.mod`
probe at the root finds nothing and silently skips the entire backend. Both sides run every pass:

| Side | Build | Test | Type check | Coverage |
|---|---|---|---|---|
| CLI (`src/`) | `npx tsc --noEmit` | `npx vitest run` | `npx tsc --noEmit` | `npx vitest run --coverage` |
| Backend (`backend/`) | `cd backend && go build ./...` | `cd backend && go test ./... -timeout 120s` | `cd backend && go vet ./...` | `cd backend && go test ./... -cover` |

Coverage floor: **80%**, both sides.

## Backend base URL + start command

- **Base URL:** `http://127.0.0.1:8788` — loopback only, by design. A request to `localhost` may
  resolve to `::1` and be refused by the origin guard; use the IPv4 literal.
- **Start it:**
  ```bash
  npm run backend:build && ./dist/saki-backend &
  ```
- **Reachability probe:** `node dist/index.js status` (or `curl -s -o /dev/null -w '%{http_code}'
  http://127.0.0.1:8788/api/branch`).
- Backend not running ⇒ mark the criterion `BLOCKED` with the start command above. **Never `FAIL`,
  never `SKIP`.**

## Auth strategy — there is none, and that is the design

No JWT, no cookie, no OAuth, no `TEST_JWT`. The security boundary is **network reachability**: the
backend binds `127.0.0.1` and additionally rejects any non-loopback `Host` header
(`backend/adapter/originguard.go`). Do not scaffold auth fixtures, and do not "fix" a 403 by adding
a token — a rejected request from off-host is the guard working.

Worth a QA criterion of its own whenever the guard is touched:

```bash
# must be refused (403), not served:
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: evil.example.com' http://127.0.0.1:8788/api/branch
```

## Exit codes are first-class criteria

The exit code is this CLI's API. Verify by running the command and reading `$?` — **never** by
matching stdout:

```bash
node dist/index.js runs --json >/dev/null 2>&1; echo "exit=$?"
```

`0` OK · `1` ERROR · `2` USAGE · `3` UNREACHABLE · `4` NOT_FOUND · `5` REMOTE_FAILED ·
`6` AUTH_REQUIRED (`src/exit.ts`). Two upstream routes report failure inside an HTTP 200 body, so
an HTTP 200 does **not** mean the criterion passed — check the exit code.

## Playwright / e2e — read before generating anything

**The global skill's Step 1.5 Playwright-generation template is project-agnostic and stays
unchanged**, but two facts gate it here:

1. **There is no UI.** saki is headless. A criterion needing a browser is almost certainly
   mis-written — re-derive it from the plan's wiring as a CLI or API criterion instead.
2. **Playwright is not currently installed.** `e2e/` holds two real specs
   (`codex-spawn.spec.ts`, `opencode-spawn.spec.ts`) but there is no `playwright.config.ts` and no
   `@playwright/test` dependency. So `playwright.config.ts` will NOT be detected, and auto-generation
   must not fire. Report an e2e criterion as `BLOCKED` naming this gap — do not install Playwright
   as a side effect of a QA run.

When the e2e suite *is* runnable, it needs **real** engine binaries (`claude`/`codex`/`opencode`) on
PATH plus a free port. `scripts/free-e2e-ports.sh` reclaims a port wedged by a killed run.

> **A fake binary cannot prove an engine invocation.** Standing project rule, documented at
> `e2e/codex-spawn.spec.ts:10-17`: the opencode command form once shipped green against fakes while
> every real run no-opped. If an engine-behaviour criterion can only be run against a stub, its
> verdict is `BLOCKED`, never `PASS`.

## Criterion types

| Type | Signal | Method |
|---|---|---|
| `CLI` | command, flag, `--json`, exit code | run it, read `$?`, compare to `src/exit.ts` |
| `API` | endpoint, route, SSE | `curl` against `http://127.0.0.1:8788` |
| `TEST` | usecase, domain, function | `npx vitest run -t [name]` / `go test -run [Name] ./...` |
| `FILE` | journal, artifact, scaffold | `ls -la [path]` |
| `BUILD` | always | both build commands above |
| `E2E` | spawn, engine, real binary | Playwright — `BLOCKED` while the config/dep gap stands |

## Invariant check — run on every pass that touches the engine

A plan touching spawn / journal / rehydrate / reaper **fails Inv-2** if it has no restart-path test,
even when every stated criterion passes. Report that explicitly in an `### Invariants touched`
section: Inv-1 · Inv-2 · loopback · exit-code contract · env scrubbing — each `held (test: path)`,
`not touched`, or `VIOLATED`.

## Rules (unchanged from global)

- **MANUAL is not SKIP** — list exact steps.
- **BLOCKED is not FAIL** — state exactly how to unblock.
- Every criterion gets a verdict. None may be silently omitted.
- Never end the report with setup suggestions; it ends with pass/fail status.
- Update the plan's Success Criteria checkboxes: `[ ]` → `[x]` PASS, `[!]` FAIL.
