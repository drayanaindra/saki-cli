# Research context — F6 slice 1 (`saki init-env`, codex adapter)

Read-only research for `tasks/saki-init-env-provision-engine-profile-slice1-plan.md`.
No graph (`graphify-out/` absent, <20 files read → skipped silently per rplan Step 1).

## Non-derivable context

`docs/project-context.md` § Invariants — Inv-1 (Go journals stay in their own subdir) and Inv-2
(a restart must never lose or mis-report an in-flight run) are **not touched** by this slice:
`init-env` is a synchronous request/response route with no journal, no run id, no dedupe lane. It is
deliberately *not* the privileged `/init-env` Claude **run** (`SpawnInit`, `kind:init`) — PRD §10
non-goal.

## What already exists (committed as-found in `c42ef7c`)

| Layer | File | What it does today |
|---|---|---|
| CLI | `src/commands/init-env.ts:8` | `cmdInitEnv` — validates engine via `assertRunEngine`, resolves `--profile`, POSTs `/api/init-env`, maps `status!=='ok'` → `EXIT.ERROR` |
| CLI | `src/commands/init-env.test.ts` | 5 unit tests (engine typo, happy post, failed+fix, positionals, escaping relative profile) |
| CLI | `src/index.ts:127`,`src/routes.ts:34`,`src/types.ts:115` | command registration, `/api/init-env` → Go, `InitEnvResult` |
| usecase | `backend/usecase/initenv.go:35` | `InitEnvService.Provision` → `(status int, body map[string]any)`; 422 guards, claude short-circuit, adapter, then proofs |
| usecase | `backend/usecase/initenv_test.go` | 3 tests (claude not-verified, proof-after-provision, bad cwd before adapter) |
| infra | `backend/infra/initenv.go:21` | `EngineProvisioner.Provision` — binary check, fingerprint before/after, fixed argv per engine |
| adapter | `backend/adapter/http.go:103,249` | `POST /api/init-env` behind `OriginGuard`, `initEnvHandler` |
| wiring | `backend/cmd/server/main.go:111,123` | `usecase.NewInitEnvService(infra.EngineProvisioner{}, infra.EngineProofChecker{})` |

## Anchors verified

| Anchor | Where | Fact that matters |
|---|---|---|
| `infra.EngineBinaryCheck` | `backend/infra/spawner.go:181` | codex-missing → `fmt.Errorf("%w (codex)", usecase.ErrBinaryNotFound)` — **already names the binary** (criterion 1.4) |
| `infra.CodexSkillsProof` | `backend/infra/codex.go:60` | proof reads the child-visible home: plugin table in `config.toml` **or** `<home>/skills/build/SKILL.md` |
| `infra.codexHomePath` | `backend/infra/codex.go:103` | pinned → `<profile>/codex`; unpinned → `~/.codex`. Same helper the spawn uses |
| `usecase.CodexInstallFix` | `backend/usecase/doctor.go:15` | the two-line remediation doctor's `Fix` uses — reuse, never re-author |
| `infra.scrubProfileEnv` | `backend/infra/spawner.go:293` | drops every *other* engine's marker namespace; `pinned` skips own-profile shedding |
| `infra.EngineProofChecker` | `backend/infra/doctor.go:8` | implements `usecase.EngineProofs` by delegating to the shared funcs |
| `adapter.OriginGuard` | `backend/adapter/originguard.go:47` | 403s non-loopback Host / cross-origin Origin |
| `adapter.Handler.Routes` | `backend/adapter/http.go:60` | method-anchored mux; `Handler{field: …}` literal is the test-construction idiom |
| `doctorHandlerFor` | `backend/adapter/doctor_http_test.go:28` | **the pattern to copy** for an adapter-level init-env test (struct literal + `httptest`) |
| `EXIT` | `src/exit.ts:8` | `OK 0 · ERROR 1 · USAGE 2` — the codes criteria 1.1/1.2/1.4 assert |
| e2e standing rule | `e2e/codex-spawn.spec.ts:10-17` | a fake-binary test cannot prove an engine invocation; absent binary must FAIL LOUDLY, only `SAKI_CODEX_E2E=0` skips |

## The load-bearing finding — the installer's exit code must not decide `status`

PRD §9 rule 2: *"Exit `0` means the selected engine's shared proof passed. A child installer exit `0`
is insufficient."* Today `backend/usecase/initenv.go:57-65` returns `status:failed` the moment the
adapter errors, **without consulting the proof**. That breaks criterion 1.3 on the most likely real
path: a second `saki init-env --engine codex` runs `codex plugin marketplace add <url>` again, codex
exits non-zero with "already added", and an already-correct profile is reported as failed.

The fix is the same rule read in the other direction — **the proof decides, not the installer**:

1. If the shared proof already passes, do not invoke the installer at all → `changed:false`, `ok`.
2. If the installer errors, still run the proof; a passing proof wins (the profile is provisioned,
   whatever the child said). Only a *failing* proof reports the installer's error.

This closes 1.3 and 2.3 without parsing installer stderr for engine-specific "already exists" strings
(which would be exactly the installer-drift coupling PRD §11 warns about).

## Consumers of the one existing surface this slice changes

`adapter.NewHandler` — `grep -rn 'NewHandler(' backend/` → 2 call sites:
`backend/cmd/server/main.go:123` and `backend/adapter/doctor_http_test.go:74`. Slice 1 turns the
`initEnv ...usecase.InitEnvService` variadic (added in `c42ef7c` purely to avoid touching call sites)
into a normal 18th parameter; both call sites are updated in the same step.

## CLI path policy — already correct, verified not assumed

PRD §6: a default profile may legitimately live outside the repo (`~/.codex`), so containment binds
`cwd` and repo-relative inputs, not an explicitly selected absolute profile.
`src/commands/init-env.ts:20-28` checks containment **only** when `isAbsolute(rawProfile)` is false.
That matches the PRD exactly — no change needed. (For contrast, `src/commands/doctor.ts:22` passes
`--profile` through with no validation at all, which is right for a read-only command.)
