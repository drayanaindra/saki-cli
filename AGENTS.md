# saki-cli project rules

## Identity

`saki-cli` is a headless build orchestrator. It supervises PRD → plan → build → QA → review
runs across Claude, Codex, and OpenCode. The CLI is the user-facing contract; exit codes are
semantics, stdout is for human/JSON output, and the backend owns run state.

## Stack and commands

- TypeScript/Node 20 CLI in `src/`; strict ESM TypeScript.
- Go backend in `backend/`; build with `npm run backend:build`.
- Type-check with `npm run typecheck` (`tsc --noEmit`).
- Unit/integration tests: `npm test` (Vitest).
- Go checks: `npm run backend:test`.
- End-to-end engine tests use Playwright and require real engine binaries.

## Architecture and bounded contexts

| Context | Ownership |
|---|---|
| CLI contract | `src/index.ts`, `src/commands/` and exit-code behavior |
| Run orchestration | `backend/domain/`, `backend/usecase/`, `backend/infra/` |
| Engine adapters | backend engine-specific infrastructure and spawn contracts |
| Journey API | backend HTTP adapters and `src/api.ts` |

The CLI talks to the loopback backend over HTTP. The backend spawns the selected engine and
journals runs; do not make the CLI own orchestration state.

## Architecture stage

Stage 3: the runtime has multiple bounded contexts and engine adapters. Deepen stable seams before
adding features. A new engine should extend the shared engine mapping, environment scrubbing,
spawn contract, doctor proof, and real-binary e2e coverage.

## Non-negotiable checks

- Preserve exit-code compatibility and loopback-only security.
- Validate user input before network calls; keep paths inside the repository where required.
- Never leak credentials or another engine's environment namespace into a spawn.
- A fake binary cannot prove an engine invocation; use real-engine e2e coverage for that claim.
- Run `npm run typecheck`, `npm test`, and relevant Go tests before handoff.

## Shared engineering guidance

Keep modules deep: expose narrow interfaces and hide policy behind them. Prefer explicit error
types/statuses over string matching. Record durable project-specific patterns in
`.claude/memory/patterns.md`; raw observations belong in `lessons-learned.md`.
