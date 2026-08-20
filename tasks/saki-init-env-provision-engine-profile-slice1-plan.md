# EXECUTION PLAN: F6 slice 1 — CLI contract and codex adapter

**Date:** 2026-08-18
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (new mutating route + child-process execution; loopback-guarded, no DB, no auth surface)
**Unknown Count:** 1 / 2 max
**Behavior Spec:** N/A — backend + CLI only, no UI (source PRD is stamped `ui:none`)
**Source PRD:** `tasks/prd-saki-init-env-provision-engine-profile.md` § slice 1 (Locked — `<!-- prd-locked: @codex · 2026-08-16 · ui:none -->`)
**Prior slices:** N/A — slice 1
**Item:** F6
**Research context:** `tasks/saki-init-env-provision-engine-profile-slice1-context.md`
**Appetite:** ~5 agent tasks (5 acceptance criteria → one iteration per INVEST; within the PRD's `medium` band)
**Kill-if:** outcome 5.1 cannot be met — i.e. a codex profile provisioned by this command still fails
`saki doctor --json --profile <dir>`, meaning setup and proof disagree about which profile runs

## Problem Statement

When an operator selects the codex engine for a repo, I want one command to provision its profile and
prove readiness immediately, so I can dispatch a run without hand-editing `config.toml` or memorising
`codex plugin marketplace add …`.

---

## Concrete Example Output

```console
$ saki init-env --engine codex --profile /tmp/p1 --json
{"engine":"codex","profile":"/tmp/p1","changed":true,"status":"ok","reason":"","fix":""}
$ echo $?
0

$ saki init-env --engine codex --profile /tmp/p1 --json     # repeat — idempotent
{"engine":"codex","profile":"/tmp/p1","changed":false,"status":"ok","reason":"","fix":""}
$ echo $?
0

$ saki doctor --json --profile /tmp/p1                       # setup and doctor agree
{"engines":[{"engine":"codex","profile":"/tmp/p1","status":"ok","reason":"","fix":""}, …]}

$ PATH=/empty saki init-env --engine codex --profile /tmp/p2 --json
{"engine":"codex","profile":"/tmp/p2","changed":false,"status":"failed",
 "reason":"engine binary not found (codex)",
 "fix":"codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git\ncodex plugin add saki-builder@saketek"}
error: engine binary not found (codex)
fix (codex): codex plugin marketplace add …
$ echo $?; ls /tmp/p2
1
ls: /tmp/p2: No such file or directory      # 1.4 — nothing was created

$ saki init-env --engine nope --json
error: unknown engine "nope" (claude|opencode|codex)
$ echo $?
2                                            # 1.1 — no HTTP request was made
```

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Make the proof decide, not the installer: in `InitEnvService.Provision`, call `s.proofs.BinaryCheck` then `s.proofs.ProfileProof` **before** the adapter and short-circuit `ok`/`changed:false` when the profile already proves; after a provision attempt, run `ProfileProof` again and let it (not the adapter's error) set `status`. Adapter error text is kept and reported only when the proof still fails. | `backend/usecase/initenv.go` | MED | `TestInitEnvService_AlreadyProvenSkipsAdapter`, `TestInitEnvService_ProofWinsOverInstallerError` (`backend/usecase/initenv_test.go`) — Test-First | Yes |
| 2 | Add `CodexInstallFix` to the `fix` field on **every** failed codex result (today it is omitted on the `BinaryCheck` path) by setting `base["fix"]` once, at the point the engine is known, in `InitEnvService.Provision`. | `backend/usecase/initenv.go` | LOW | `TestInitEnvService_MissingBinaryReportsFixAndWritesNothing` (`backend/usecase/initenv_test.go`) — Test-First | Yes |
| 3 | Make `EngineProvisioner.Provision` idempotency-safe: collect the first `runProvision` error instead of returning early, run **both** codex commands, and return `(changed, firstErr)` so step 1's proof can overrule a benign "already added". Fingerprint stays `profileFingerprint` before/after. | `backend/infra/initenv.go` | MED | `TestEngineProvisioner_RunsBothCodexCommandsAndReportsFirstError` (`backend/infra/initenv_test.go`, NEW file) — Test-First | Yes |
| 4 | Prove fixed argv + BR4 env scrubbing with a fake `codex` on `PATH` that dumps `argv`+`env` to a file: assert argv is exactly `codex plugin marketplace add <url>` / `codex plugin add saki-builder@saketek`, that `CODEX_HOME=<profile>/codex` is present, and that `CLAUDE_*`/`OPENCODE_*` are absent. (Fake binary is permitted here — argv + error plumbing only, per BR6.) | `backend/infra/initenv_test.go` (NEW) | LOW | `TestEngineProvisioner_FixedArgvAndScrubbedEnv` — Test-First | Yes |
| 5 | Replace the `initEnv ...usecase.InitEnvService` variadic with a normal 18th parameter on `adapter.NewHandler`, updating both call sites. | `backend/adapter/http.go`, `backend/cmd/server/main.go`, `backend/adapter/doctor_http_test.go` | LOW | existing suite (`go test ./...` — compile-time proof; `TestDoctorHandler_RealWiring` still passes) | Yes |
| 6 | Add the adapter-level route tests for criterion 1.5: `POST /api/init-env` with `Host: evil.com` → 403; invalid engine → 422; relative/escaping profile → 422; a spy `EngineProvisioner` records zero calls on both 422 paths and on the 403 path. | `backend/adapter/initenv_http_test.go` (NEW) | LOW | `TestInitEnvHandler_RejectsNonLoopbackHost`, `TestInitEnvHandler_RejectsInvalidEngineWithoutAdapter`, `TestInitEnvHandler_RejectsEscapingProfileWithoutAdapter` — Test-First | Yes |
| 7 | Add the CLI unit test proving 1.1's "before any HTTP request" for an **empty** `--engine` (today only a typo is covered) and 1.4's exit-1 + remediation-naming-the-binary mapping. | `src/commands/init-env.test.ts` | LOW | `it('rejects an empty --engine before making a request')`, `it('maps a missing-binary failure to EXIT.ERROR and prints the binary in the fix')` — Test-First | Yes |
| 7.5 | **Prerequisite discovered during research — wire the e2e runner.** `@playwright/test` was not a dependency and no `playwright.config.ts` existed, so `e2e/` was documented (`docs/project-context.md:46`) but unrunnable: criterion 1.2 had no harness. Add the devDep, `playwright.config.ts` (`testDir:'./e2e'`, `baseURL:'http://127.0.0.1:8788'`, `webServer` = `backend:build && ./dist/saki-backend` gated on `GET /api/health`, `workers:1`, `retries:0`), and a `test:e2e` script. | `package.json`, `playwright.config.ts` (NEW) | LOW | `node node_modules/@playwright/test/cli.js test --list` lists the 3 specs — Test-After | Yes |
| 8 | Add the REAL-binary e2e for criterion 1.2 + 1.3: provision a throwaway `CODEX_HOME` profile through `saki init-env`, assert exit 0 and `changed:true`, re-run and assert exit 0 with `changed:false`, then assert `saki doctor --json --profile <dir>` reports codex `ok`. Absent binary FAILS LOUDLY; only `SAKI_CODEX_E2E=0` skips (the standing rule at `e2e/codex-spawn.spec.ts:10-17`). | `e2e/codex-init-env.spec.ts` (NEW) | MED | the spec itself (`npx playwright test e2e/codex-init-env.spec.ts`) — Test-After (it verifies steps 1–7) | Yes |
| 9 | Document the command: add `saki init-env --engine <e> [--profile <dir>]` to the command list and a `### saki init-env — provision an engine profile` section stating exit `0` = the shared proof passed, `1` = setup or proof failed, `2` = bad arguments, and that `doctor` stays read-only. | `docs/cli-reference.md` | LOW | existing suite (project rule: a route absent from the reference is unshipped) | Yes |

> Steps 1–3 are one behavioural change split by layer; each is independently committable because the
> tests added with it pass on its own.

---

## User Role Coverage

Single-actor CLI/loopback tool — there is no authenticated multi-role surface (PRD §10 explicitly
excludes credentials and auth).

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Operator (local shell) | `saki init-env --engine codex [--cwd <repo>] [--profile <dir>]` — mutate **only** the selected engine's namespace in the selected profile | mutate another engine's namespace; read/print credentials; provision claude (returns not-verified, slice 3) | `OriginGuard` on `POST /api/init-env` — loopback Host **and** loopback-or-absent Origin (`backend/adapter/originguard.go:47`) | CLI only — no UI |
| Bootstrapping agent (MCP / another `saki` process) | the same command, `--json` for a machine-readable line | same as above | same `OriginGuard`; the backend binds `127.0.0.1` only | `saki init-env --json` |
| Remote/cross-origin caller | nothing — 403 before the handler body runs | reach `initEnvHandler` at all | `OriginGuard` (criterion 1.5) | none |

Edge cases per role: unknown engine → exit 2 (client-side, no request); escaping relative `--profile`
→ exit 2 (client-side); missing `codex` binary → exit 1 + `CodexInstallFix`, no files written;
already-provisioned profile → exit 0, `changed:false`; malformed request body → 422.

---

## Plan Wiring

### Flow 1: Operator provisions a codex profile (happy path → `changed:true`, `ok`)
```
$ saki init-env --engine codex --profile /tmp/p1
  → cmdInitEnv()                       (src/commands/init-env.ts:8)
      assertRunEngine()                (src/commands/run.ts:94)      — exit 2 before any HTTP
      isAbsolute/relative containment  (src/commands/init-env.ts:20) — exit 2 before any HTTP
  → StudioClient.post('/api/init-env') (src/client.ts:104; routed to Go by src/routes.ts:34)
  → POST /api/init-env                 (backend/adapter/http.go:103, OriginGuard-wrapped)
  → Handler.initEnvHandler()           (backend/adapter/http.go:249)
  → usecase.InitEnvService.Provision() (backend/usecase/initenv.go:35)
      guards: abs cwd, existing dir, known engine, abs profile   → 422, adapter never called
      proofs.BinaryCheck(codex)        (infra/spawner.go:181)     → ErrBinaryNotFound (codex)
      proofs.ProfileProof(codex, p)    (infra/codex.go:60)        → already ok? short-circuit changed:false
  → infra.EngineProvisioner.Provision()(backend/infra/initenv.go:21)
      profileFingerprint(before)       (backend/infra/initenv.go:81)
      exec codex plugin marketplace add <url>   — fixed argv, env = scrubProfileEnv + CODEX_HOME
      exec codex plugin add saki-builder@saketek
      profileFingerprint(after)        → changed = before != after
  → proofs.ProfileProof(codex, p) AGAIN — THE decider (BR2); reads <profile>/codex/config.toml
  → {engine, profile, changed, status, reason, fix}  → 200
  → emit() (src/output.ts) → stdout JSON; status!=='ok' → EXIT.ERROR (src/commands/init-env.ts:36)
```

### Flow 2: Cross-origin / malformed caller (criterion 1.5 — adapter never runs)
```
POST /api/init-env  Host: evil.com
  → OriginGuard      (backend/adapter/originguard.go:47) → 403 {"error":"cross-origin blocked"}
                     ✗ initEnvHandler never entered, ✗ EngineProvisioner.Provision never called

POST /api/init-env  {"cwd":"/repo","engine":"nope"}
  → initEnvHandler   (backend/adapter/http.go:249)
  → InitEnvService.Provision → engine guard (backend/usecase/initenv.go:42) → 422
                     ✗ EngineProvisioner.Provision never called
```

### Flow 3: Setup → doctor agreement (outcome 5.1)
```
saki init-env --engine codex --profile /tmp/p1   → infra.CodexSkillsProof(&"/tmp/p1")
saki doctor    --json        --profile /tmp/p1   → usecase.DoctorService.Check → infra.EngineProofChecker.ProfileProof
                                                 → infra.EngineProfileProof → infra.CodexSkillsProof(&"/tmp/p1")
```
Both paths land on the **same** `CodexSkillsProof` over the **same** `codexHomePath` — so setup and
doctor cannot disagree about which profile was proven (PRD §5 kill criterion).

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `adapter.NewHandler(… , initEnv ...usecase.InitEnvService)` → normal 18th param | signature | 2 (`backend/cmd/server/main.go:123`, `backend/adapter/doctor_http_test.go:74`) | updated in step 5 | step 5 (same commit — compile-time enforced) |
| `usecase.InitEnvService.Provision` result: `status` no longer follows the adapter's error | behaviour | 2 (`backend/adapter/http.go:262`, `src/commands/init-env.ts:36`) | unaffected — both read `status`/`reason`/`fix`, whose **shape** is unchanged; only which input decides `status` changes | — |
| `infra.EngineProvisioner.Provision` early-return → collect-first-error | behaviour | 1 (`backend/cmd/server/main.go:111` via `NewInitEnvService`) | updated in step 3 | step 3 |
| `POST /api/init-env` request/response JSON | API | 1 (`src/commands/init-env.ts:30`) | unaffected — additive only, no field removed or renamed | — |
| `src/types.ts` `InitEnvResult` | TS type | 1 (`src/commands/init-env.ts:5`) | unaffected — no field changes this slice | — |

**Forward compatibility:** additive-only at every published boundary (a new CLI command, a new HTTP
route, a new docs section). The one signature change (`NewHandler`) is internal to the Go module with
both call sites in-repo, so it is compile-time checked, not a runtime break. No deploy-order
constraint: the CLI and backend ship as one artifact pair and the route is new in this slice.

---

## Migration Checklist

**N/A — no database in this project.** `saki-cli` persists run state as NDJSON journals on disk
(`docs/project-context.md` § Invariants, Inv-1); this slice adds no persisted state at all — it
mutates only the selected engine's own profile directory through that engine's own installer.

- [x] No schema change → no migration file, no `migrate`/`alembic` command
- [x] No destructive operation (no `DROP`/`DELETE`/`TRUNCATE`; the ABSOLUTE NO-GO list is untouched)
- [x] Rollback: the command is additive to a profile; removing the plugin is `codex plugin remove`, outside this slice's scope

---

## Invariant Impact

Required because Steps 1–5 touch `backend/usecase/`, `backend/infra/`, and `backend/cmd/server/`.
"No impact" is stated per invariant with the reason, not by silence.

| Invariant | Impact | Why |
|---|---|---|
| **Inv-1** — Go journals stay in `<runsDir>/go` (`backend/infra/journal.go:58`) | **No impact** | `init-env` writes no journal, allocates no run id, and never calls `Journal`/`GoRunsDir`. It is a synchronous request→response route; nothing in Steps 1–9 imports the journal package. |
| **Inv-2** — a restart must never lose or mis-report an in-flight run | **No impact** | `init-env` creates no in-flight run: no `SpawnInit`, no `kind:init`, no dedupe lane, no rehydrate/reaper entry. A backend restart mid-`init-env` fails the HTTP request (CLI exits `3`, the existing `UNREACHABLE` contract) and leaves no durable state to mis-report. No restart-path test is therefore owed. |
| **Loopback-only** (`backend/adapter/originguard.go:47` + the `127.0.0.1` bind) | **Preserved, and newly regression-tested** | The route is mounted `OriginGuard`-wrapped at `backend/adapter/http.go:103`. No step adds a bind flag, a CORS header, or a Host-check exemption. Step 6 adds the missing 403 regression test for this specific route. |
| **Exit-code contract** (`src/exit.ts`) | **Reuses existing codes only** | `init-env` maps to `OK 0` (proof passed), `ERROR 1` (setup or proof failed), `USAGE 2` (bad engine/profile, client-side). No code is renumbered, repurposed, or added. Transport failures keep the client's existing `UNREACHABLE 3` / `AUTH_REQUIRED 6`. |
| **Env scrubbing** (`backend/infra/spawner.go:293`) | **Reused, not forked** | `provisionEnv` (`backend/infra/initenv.go:63`) calls the same `scrubProfileEnv` the spawner uses and appends the engine's own namespace var. Step 4 locks this with a test; no new per-engine branch is introduced. |
| **vitest / e2e split** (`vitest.config.ts` excludes `e2e/`) | **Respected** | Step 7's unit test lives at `src/commands/init-env.test.ts` (vitest); step 8's real-binary spec lives at `e2e/codex-init-env.spec.ts` (Playwright). Neither crosses the boundary. |

---

## Branch Points (pre-declared)

- **Step 3:** if `codex plugin marketplace add` exits non-zero on a repeat run ("already added") →
  auto-handle by letting the re-run proof decide (reversible; already the step's design). Record
  `AUTO-RESOLVED: repeat marketplace-add error → proof decides status — BR2 says an installer exit code is never the signal`.
- **Step 7.5 (AUTO-RESOLVED):** the repo documents `e2e/` as Playwright specs but never installed the
  runner, so criterion 1.2's real-binary evidence had no harness at all.
  `AUTO-RESOLVED: no e2e runner installed → add @playwright/test + playwright.config.ts (testDir './e2e') rather than invent a second harness — the repo already declares Playwright as the e2e runner (docs/project-context.md:46, vitest.config.ts:3-5), so wiring it honours the stated convention instead of forking it.`
  Reversible (one devDep + one config file). Side effect worth stating: this also resurrects the two
  pre-existing spawn specs, which have never run in CI; if they fail for pre-existing reasons, report
  that honestly and do not charge it to F6.
- **Step 8:** if the real `codex` binary is absent on this machine → the spec FAILS LOUDLY (standing
  rule). Do **not** add a silent skip; the documented opt-out is `SAKI_CODEX_E2E=0`, and if the run is
  opted out, record it in the build's E2E line rather than reporting 1.2 as verified.
- **Step 8:** if provisioning a real profile would require credentials or network auth → PAUSE with one
  question. PRD §10 makes credentials a Non-Goal, so the spec must reach `ok` via the profile shapes
  `CodexSkillsProof` already accepts, never by copying an auth file.
- **Any step:** if closing a criterion would require writing outside the selected engine's namespace,
  or reading/printing credentials → BLOCKED (crosses PRD §9 rule 3 / §10, and §12's "must not return
  credentials").

---

## Unknowns (must be <= 2)

1. **[MED] Does `codex plugin marketplace add` on an already-registered marketplace exit non-zero?**
   → resolution: this plan is designed to be *correct either way* — step 1's proof-decides rule makes
   the answer irrelevant to `status`, and step 8's real-binary e2e observes the actual behaviour. It
   is therefore not a blocking item: no step's correctness depends on the answer.

---

## No-Gos

- Will NOT touch the privileged Claude `/init-env` **run** path (`SpawnInit`, `kind:init`), its
  journal, or its dedupe lane — PRD §10.
- Will NOT make `saki doctor` write anything — it stays strictly read-only (PRD §9 rule 1).
- Will NOT install engine binaries, accept credentials, or print profile contents (PRD §10, §12).
- Will NOT copy or symlink the saki-builder plugin into the profile (PRD §10) — provisioning goes
  through codex's own installer commands.
- Will NOT implement the opencode or claude adapters here (slices 2 and 3).
- Will NOT assemble any child command through a shell — `exec.Command(name, args...)` with fixed argv
  only (PRD §6).
- Will NOT relax the loopback binding or `OriginGuard` (CLAUDE.md project rule 3).
- Will NOT let a fake binary stand in for engine-invocation evidence (PRD §9 rule 6).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Every role that touches this feature is in the Role Coverage matrix (operator, bootstrapping agent, cross-origin caller)
- [x] Each role has full call chain in Plan Wiring (Flows 1–3)
- [x] Permission/auth check listed for each role (`OriginGuard`, loopback bind)
- [x] Edge cases per role documented (Role Coverage § edge cases)

**Database & Migrations**
- [x] N/A — no database; stated with evidence in Migration Checklist
- [x] No breaking change without rollback strategy (nothing persisted)

**API Layer**
- [x] Request struct named and located — the anonymous `struct{Cwd, Engine, Profile string}` at `backend/adapter/http.go:250`, decoded via `io.LimitReader(r.Body, maxBody)`
- [x] Response struct named and located — `map[string]any{engine, profile, changed, status, reason, fix}` built in `backend/usecase/initenv.go:52`; TS mirror `InitEnvResult` at `src/types.ts:115`
- [x] HTTP method, path, router file written out — `POST /api/init-env`, `backend/adapter/http.go:103`
- [x] Dependencies listed — `OriginGuard` middleware; `usecase.EngineProvisioner` + `usecase.EngineProofs` ports

**Service / Business Logic**
- [x] Every service function modified/created named with file path (steps 1–3)
- [x] Side effects listed — two `exec.Command` child processes writing the codex profile; no email/webhook/cache/journal
- [x] Error paths documented — 403 (OriginGuard), 422 (bad body / cwd / engine / profile), 200+`status:failed` (binary missing, installer failed, proof failed); CLI maps to exit 1

**Frontend**
- [x] N/A — no UI in this slice; source PRD is stamped `ui:none` and no file under a frontend root is touched

**Compatibility & Consumers**
- [x] Compatibility & Consumers filled — 5 rows, every one with a grep-backed consumer cell and a verdict; forward-compat answered
- [x] Prior slices — N/A, slice 1

**Plan Wiring**
- [x] Every major flow has an end-to-end call chain written out (Flows 1–3)
- [x] No step uses vague verbs without exact file+function (all 9 steps name the function)
- [x] No "update frontend" without naming file and function (no frontend work)

---

## Evidence Ledger

### Blocking (must be empty to present)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|
| — | — | *(empty)* | — |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| — | All anchors verified, all targets have creating steps, no unchecked items on state-changing steps, no unknowns above LOW | self-audit |
| 1 | `InitEnvService.Provision` reports `profile:"default"` when `--profile` is omitted rather than the resolved `~/.codex` path. Harmless for slice 1's criteria (1.2 only requires the field be present) but PRD criterion 3.3 wants the path a run would spawn — revisit in slice 3. | `backend/usecase/initenv.go:45` |
| 3 | The opencode branch of `EngineProvisioner.Provision` is carried along by the collect-first-error refactor but is not tested here — it is slice 2's surface. | `backend/infra/initenv.go:34-40`, PRD §7 slice 2 |
| 8 | The e2e depends on a real `codex` binary; if the machine has none, criterion 1.2 is reported as opted-out, never as passed. | `e2e/codex-spawn.spec.ts:10-17` |

**Blocking: 0 → READY.**

---

## Success Criteria

- [ ] **1.1** `saki init-env` with an empty or unknown `--engine`, or a relative `--profile` escaping
  cwd, exits `2` and issues **zero** HTTP requests.
  *Given* a stub fetch that records calls, *when* `cmdInitEnv(ctx, [], {engine:''})` runs,
  *then* it rejects with `code: EXIT.USAGE` and the recorder is empty
  (`npm test -- src/commands/init-env.test.ts`).
- [ ] **1.2** With a real `codex` binary and an empty writable profile, `saki init-env --engine codex
  --profile <dir>` exits `0` and its stdout JSON carries `engine`, `profile`, `changed`, `status:"ok"`.
  (`npx playwright test e2e/codex-init-env.spec.ts` — real binary, per BR6.)
- [ ] **1.3** Re-running the same command exits `0` with `changed:false`, and the codex profile has no
  duplicate plugin registration (`codexPluginTableRE` matches exactly once in `config.toml`).
  (same e2e spec; plus `TestInitEnvService_AlreadyProvenSkipsAdapter` — `cd backend && go test ./usecase/ -run TestInitEnvService`.)
- [ ] **1.4** With no `codex` on `PATH`, the command exits `1`, the reason names the binary
  (`engine binary not found (codex)`), the `fix` is `usecase.CodexInstallFix`, and **no** profile
  directory or file is created.
  (`cd backend && go test ./usecase/ -run TestInitEnvService_MissingBinaryReportsFixAndWritesNothing`.)
- [ ] **1.5** `POST /api/init-env` returns `403` for `Host: evil.com`, `422` for an unknown engine, and
  `422` for a relative/escaping profile — and a spy provisioner records **zero** calls on all three.
  (`cd backend && go test ./adapter/ -run TestInitEnvHandler`.)
- [ ] **BR2** `status:"ok"` is set only after `ProfileProof` passes — never from a child exit code.
  (`cd backend && go test ./usecase/ -run TestInitEnvService_ProofWinsOverInstallerError`.)
- [ ] **BR4** The child receives fixed argv and a scrubbed env: `CODEX_HOME=<profile>/codex` present,
  no `CLAUDE_*` / `OPENCODE_*` vars.
  (`cd backend && go test ./infra/ -run TestEngineProvisioner_FixedArgvAndScrubbedEnv`.)
- [ ] Whole-repo gates stay green: `npm run typecheck` · `npm test` · `cd backend && go vet ./... && go test ./...`
- [ ] `docs/cli-reference.md` documents `saki init-env` with its exit codes (project rule: a route
  absent from the reference is unshipped) — `grep -c 'saki init-env' docs/cli-reference.md` ≥ 2.

---

## Annotation Space

### `/saki-builder:rplan-review` — 5 experts, findings and disposition

Phase 1 gate: FAILED first pass (no **Invariant Impact** section — this project's added scan rule).
Section written, gate re-run, PASSED. Then Go Backend · CLI Contract · Security · QA · Product ran in
parallel. Run Engine was **not** launched: its trigger is a step touching spawn/journal/rehydrate/
reaper, and `init-env` writes no journal and creates no run (Invariant Impact) — Go + Security both
read `spawner.go` regardless.

**Fixed in slice 1** (each verified against the code before acting, per the repo's "verify subagent
claims" rule — 3 of these were independently re-checked and confirmed by more than one expert):

| Finding | Verified how | Resolution |
|---|---|---|
| opencode is LIVE on the new route and its `npx … install --global` writes OUTSIDE the selected profile (PRD §9 rule 3) | `backend/infra/initenv.go` opencode branch + claude-only gate at the service | Service now refuses every non-codex engine; opencode is **unreachable**, not merely untested. Slice 2 widens one predicate |
| `runProvision` spawns unbounded children on a request path — `marketplace add` clones over the network | no `context`/timeout vs `backend/infra/execx.go:32` doing exactly this | 120s deadline, `Stdin=nil`, `WaitDelay`; timeout-kill test |
| A child's raw output flows into the HTTP body (PRD §12 — can carry config lines or a credentialed URL) | `CombinedOutput()` → `reason` → stderr | one line, capped at 512B, with a truncation + secret test |
| `NewHandler`'s variadic leaves a registered route backed by nil ports | `grep NewHandler(` → **11** sites / 8 files, not the 2 the plan claimed | Required parameter + `emptyInitEnv()` + `TestInitEnvHandlerRealWiring` |
| `provisionEnv` forks `engineProfileEnv` (project rule 6) | second per-engine switch in `initenv.go` vs `spawner.go:320` | Composes the two shared helpers; test asserts equality with `engineProfileEnv`, not a literal |
| Marketplace URL + plugin id duplicated across usecase and infra (PRD §11) | `doctor.go:15` vs `initenv.go:15,28,31` | `CodexProvisionArgv` is the single mapping; `CodexInstallFix` is derived + drift-locked by a test |
| Criterion 1.4's "no profile files created" was vacuous | usecase fake touches no disk; `infra.EngineProvisioner` is what could write | Real provisioner + emptied `PATH` in `backend/infra/initenv_test.go` |
| Concurrent init-env on one profile races `config.toml` and `changed` | no lock; `BuildEngineService.idemMu` is the precedent | Per-profile mutex + a `-race` re-entrancy test |
| `e2e/` had NO runnable harness (no `@playwright/test`, no config) | `git log -- playwright.config*` empty; `.claude/skills/qa/SKILL.md:76` records the gap | Harness added, incl. the `pree2e` hook `scripts/free-e2e-ports.sh` always documented |
| Exit-code criteria asserted return values, never `$?` | `cmdInitEnv` throws; a Go test cannot see a process code | e2e drives the real CLI via `spawnSync` |
| `saki init-env` absent from the reference, which still taught manual provisioning | `grep -c` → 0; reference lines 203-234 + agent guide 117-131 | Both docs **reconciled**, not appended to |
| `init-env.ts` imported `./run.js` (commands never import each other) | `src/commands/init-env.ts:6` | `src/engines.ts`; `run.ts` re-exports |
| No malformed-body guard; human line omitted the profile | vs `src/commands/doctor.ts:26` | Both fixed, both tested |
| `/api/init-env` routing untested | `grep init-env src/routes.test.ts` → none | Pinned in `routes.test.ts` |

**Accepted, not fixed — with the reason** (recorded so a later reader sees a decision, not an oversight):

- **`profile:"default"` is a label, not a round-trippable path.** Real (chaining `--profile default`
  misresolves), but `saki doctor` reports the identical label via the same `profileLabel`, and PRD §12
  freezes doctor this slice. Changing one alone would make the two commands disagree — the exact
  divergence the Kill-if guards. Documented in the reference instead. *Revisit with criterion 3.3.*
- **An absolute `--profile` is unbounded.** PRD §6 explicitly sanctions a profile outside the repo;
  a cwd-or-home rule would also break the PRD's own `--profile <tmpdir>` verification method. The
  Role Coverage overclaim ("mutate only…") was the defect; every write is bounded to the engine's own
  subdirectory. Also note `OriginGuard` is not authentication — it stops browsers, not local processes.
- **Symlinked profiles resolve lexically** (`filepath.Clean`, not `EvalSymlinks`) — see No-Gos.
- **No `saki_init_env` MCP tool.** `tasks/prd-mcp-surface-saki-mcp.md` §11 requires a deliberate
  follow-up PRD per new tool, and this would be that surface's first *mutating* tool. Out of scope;
  the Role Coverage cell claiming an MCP entry point was wrong and is struck.
- **`doctor`/spawn `fix` strings still name the manual commands**, not `saki init-env` — PRD §12
  freezes those surfaces. Trigger to revisit: slice 2, when one string can cover both engines.
- **`POST /api/run` is not `OriginGuard`-wrapped** while every route in its block is. Pre-existing,
  not introduced here, not this slice's to fix — recorded so the asymmetry is a known decision.
- **`src/daemon.ts` (22.8%) and `src/commands/backend.ts` (0%)** are uncovered, inherited from the
  unrelated daemon WIP in `c42ef7c`. Total coverage is 83.68%, above the floor; this slice's own files
  are 95–100%. Flagged for triage — it is not F6 work.

### What the real binary caught that 19 green Go tests did not

`codex plugin marketplace add` **fails** when `CODEX_HOME` names a missing directory, so provisioning
a fresh `--profile` — the feature's whole purpose — could never succeed. Every fake `codex` exits 0
regardless. Fixed in `4b558a5`; this is CLAUDE.md rule 2 paying for itself on first contact.

> Human: add notes, corrections, constraints here.

---
Status: [ ] Draft  [ ] Annotated  [x] Approved (TRUST MODE — `/saki-builder:build`)  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
