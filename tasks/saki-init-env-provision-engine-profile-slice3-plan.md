# EXECUTION PLAN: F6 slice 3 — typed Claude not-verified boundary after F4

**Date:** 2026-08-20
**Blocking items:** 0 for the executable pre-F4 boundary; F4 positive-path work is an explicit deferred continuation (see Evidence Ledger)
**Risk Score:** MED (mutating engine-profile surface is security-sensitive, but this plan's executable
path is an early no-write refusal; no DB, auth, or child process is added)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A — backend + CLI only, no UI (source PRD is stamped `ui:none`)
**Source PRD:** `tasks/prd-saki-init-env-provision-engine-profile.md` § slice 3 (Locked — `<!-- prd-locked: @codex · 2026-08-16 · ui:none -->`)
**Prior slices:** `tasks/saki-init-env-provision-engine-profile-slice1-plan.md`, `tasks/saki-init-env-provision-engine-profile-slice2-plan.md` (read; shipped shape wins)
**Research context:** `tasks/saki-init-env-provision-engine-profile-slice3-context.md`
**Appetite:** ~3 agent tasks for the executable pre-F4 boundary (3 slice criteria; within the PRD's `medium` band). The F4-gated positive path is not counted because its proof and installer contract do not exist here.
**Kill-if:** outcome 5.1 cannot be met for Claude after F4 — i.e. a successful Claude setup still fails the same selected-profile doctor proof. Do not claim this outcome before F4 supplies the proof.

## Problem Statement

When an operator selects Claude before F4's installed-plus-enabled profile proof exists, I want `saki init-env`
to stop with a machine-readable `not_verified` result rather than invoke an unproven installer, mutate a
profile, or report a false green. Once F4 supplies the proof and a canonical installer contract, the same
slice must be reopened for the positive Claude path; this plan does not invent either one.

---

## Concrete Example Output

The only honest output on the current committed branch is the no-write boundary:

```console
$ saki init-env --engine claude --profile /tmp/p4 --json
{"engine":"claude","profile":"/tmp/p4","changed":false,"status":"not_verified","reason":"engine provisioning is not verified for this engine (claude requires F4's installed + enabled plugin proof)","fix":""}
$ echo $?
1
$ find /tmp/p4 -type f -maxdepth 3 -print
# no files added
```

The positive target is explicitly F4-gated, not a current result:

```json
{"engine":"claude","profile":"/tmp/p4","changed":true,"status":"ok","reason":"","fix":""}
```

That target may be emitted only after F4 proves both `installed_plugins.json` installation and
`settings.json` → `enabledPlugins` enablement for the exact child-visible profile. No Claude installer argv
is specified in this plan because no canonical command is present in the committed code.

---

## Verified Boundary Before Implementation

- `tasks/roadmap.md:27-31` marks F4 Planned, with no child PRD.
- The F2 review's verified spike says Claude proof requires two files: the registry has install metadata but
  no enablement, while `settings.json` has `enabledPlugins`; two plugin-ID spellings have different versions,
  so resolution order must be pinned (`tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run-review.md:53-61`).
- `backend/infra/spawner.go:195-211` has no Claude proof and returns `nil` in its default branch. Adding
  Claude to doctor or shared proof now would be a false green.
- `backend/usecase/doctor.go:5-8` reports only codex and opencode and explicitly reserves Claude for F4.
- `backend/usecase/initenv.go:72-79` currently rejects Claude before binary check, profile lock, proof, and
  adapter invocation, but labels the result `failed`.
- `src/types.ts:116-123` currently permits only `'ok' | 'failed'`; `src/commands/init-env.ts:34-41` maps
  every non-`ok` result to `EXIT.ERROR`.
- Claude's selected profile environment already exists in the shared spawn path: `CLAUDE_CONFIG_DIR` is
  pinned only for an explicit profile (`backend/infra/spawner.go:214-220,273-295`).

**Decision:** add a distinct `not_verified` init-env status. Do not reuse doctor `unknown`: `unknown` is an
`EngineStatus` for doctor reports, while `not_verified` is the explicit init-env answer that F4 has not yet
made the selected engine verifiable. Existing failed setup remains `failed`; both non-`ok` statuses exit 1.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Add an init-env-only status vocabulary (`InitEnvStatus`) with `ok`, `failed`, and `not_verified`; update `newInitEnvResult` and `succeed` to emit the named values, and add an explicit Claude guard that returns `not_verified`, `changed:false`, `fix:""` before `BinaryCheck`, `profileGate.lock`, `ProfileProof`, or the provisioner. Keep `EngineProfileProof`, `DoctorEngines`, and Claude installer mapping unchanged. | `backend/domain/initenv.go` (NEW), `backend/usecase/initenv.go` | MED | `TestInitEnvServiceClaudeIsNotVerifiedWithoutWriting` plus a proof-call counter asserting binary/profile proof and adapter calls are all zero (`backend/usecase/initenv_test.go`) — Test-First | Yes |
| 2 | Widen the client contract and preserve the exit-code behavior: type `InitEnvResult.status` as `'ok' | 'failed' | 'not_verified'`; keep `cmdInitEnv`'s `status !== 'ok'` branch returning `EXIT.ERROR`; ensure human and JSON output expose the literal `not_verified` and the F4 reason without a remediation command. Update the route shape assertion and CLI fixtures. | `src/types.ts`, `src/commands/init-env.ts`, `src/commands/init-env.test.ts`, `backend/adapter/initenv_http_test.go` | LOW | `it('maps a not-verified Claude result to EXIT.ERROR and prints its reason')`; `TestInitEnvHandlerClaudeIsNotVerifiedWithoutAdapter` — Test-First | Yes |
| 3 | Update the process-level and operator documentation to make the pre-F4 state explicit: Claude remains accepted as an engine value, exits 1 with `status:"not_verified"`, writes nothing, and has no fix until F4 supplies proof; retain codex/opencode success and existing doctor scope. Strengthen the real CLI e2e fixture to assert the exact status/reason and byte-identical contents of a pre-seeded Claude profile. | `e2e/codex-init-env.spec.ts`, `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` | LOW | `npx playwright test e2e/codex-init-env.spec.ts` (Claude branch needs no Claude binary because the service guard precedes binary lookup); documentation grep for `not_verified` and the F4 dependency | Yes |
| 4 | Record the F4 continuation as deferred scope, not as a current implementation step. Do not add a Claude proof, `DoctorEngines` entry, installer argv, fingerprint, or positive-path e2e until F4 lands committed proof for both files and pins plugin-ID resolution plus the canonical installer command. | This plan's Evidence Ledger and progress manifest; no source change | LOW | `git grep -nE 'installed_plugins.json|enabledPlugins|ClaudeProfileProof|ClaudeProvisionArgv' -- backend tasks` confirms the continuation is not present; the current slice's focused tests remain green without it — Test-After | Yes (handoff) |

> Steps 1–3 are the executable pre-F4 boundary. Step 4 is a hard dependency handoff, not permission to
> guess an installer or weaken proof. The slice cannot be marked complete while step 4 remains unresolved.

---

## User Role Coverage

Single-actor local CLI/loopback tool — no authenticated multi-role surface and no UI (PRD §10).

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Operator (local shell) | Request Claude setup and receive a typed `not_verified` result; provision codex/opencode as shipped | mutate Claude before F4 proof; read/print credentials; treat exit 1 as success | `OriginGuard` on `POST /api/init-env`; backend remains loopback-only | CLI only — `saki init-env --json` |
| Bootstrapping agent | Branch on `status:"not_verified"` and stop/retry after F4; use existing codex/opencode setup | invoke an unproven Claude adapter or infer readiness from installer exit | Same `OriginGuard` and fixed backend bind | `saki init-env --engine claude --json` |
| Remote/cross-origin caller | Nothing — 403 before `initEnvHandler` | reach any provisioning path | `OriginGuard` | none |

Edge cases: malformed request/relative profile remain 422 before any adapter; non-loopback Host remains 403;
Claude with an absent, empty, malformed, or pre-seeded profile returns the same no-write `not_verified`
result; codex/opencode retain their shipped binary/proof/fingerprint paths.

---

## Plan Wiring

### Flow 1: Claude request before F4 — explicit no-write result

```text
saki init-env --engine claude --profile /tmp/p4 --json
  → cmdInitEnv()                                      (src/commands/init-env.ts:16)
      assertRunEngine + resolveProfileFlag             → CLI usage errors before HTTP
  → StudioClient.post('/api/init-env')                (src/client.ts; route table unchanged)
  → POST /api/init-env                                (backend/adapter/http.go:250, OriginGuard-wrapped)
  → Handler.initEnvHandler()                          (backend/adapter/http.go:250)
  → InitEnvService.Provision()                        (backend/usecase/initenv.go:67)
      normalizeProvisionRequest()                     → validates cwd/profile/engine
      Claude not-verified guard                       → status:not_verified, changed:false, fix:""
      ✗ EngineProofs.BinaryCheck                      → not called
      ✗ profileGate.lock                               → not called
      ✗ EngineProofs.ProfileProof                     → not called
      ✗ EngineProvisioner.Provision                   → not called
  → HTTP 200 JSON result                              → CLI emits JSON/human line
  → status !== "ok"                                  → EXIT.ERROR (1)
```

The guard deliberately happens after request normalization (boundary validation still applies) and before
any profile-dependent operation. It does not inspect Claude files, so it cannot accidentally treat an
installed-but-disabled plugin as ready.

### Flow 2: Existing codex/opencode success — regression handoff

```text
saki init-env --engine codex|opencode --profile <dir>
  → existing cmdInitEnv → POST /api/init-env
  → InitEnvService.Provision
  → shared BinaryCheck/ProfileProof → EngineProvisioner fixed argv + scrubbed env when needed
  → shared proof passes → status:ok
  → saki doctor --json --profile <dir>
  → DoctorService.Check → same EngineProofChecker / EngineProfileProof
```

The slice-2 real-binary e2e remains the evidence for codex/opencode profile-path agreement. This plan adds
no Claude entry to `DoctorEngines`, so it does not create a false three-engine doctor result.

### Flow 3: F4-gated positive Claude path — intentionally not executable yet

```text
[F4 committed installed_plugins.json + settings.json proof]
  → ClaudeProfileProof(configDir)                     (new F4-owned infra surface)
  → pinned Claude installer contract                  (must be supplied by F4/canonical source)
  → CLAUDE_CONFIG_DIR=<profile>                       (reuse backend/infra/spawner.go mapping)
  → ClaudeProfileProof(configDir) again               (shared proof decides status)
  → status:ok + doctor reports Claude ok
```

No file/function in Flow 3 is a creating target in this plan. Adding it now would violate PRD §10
(non-goal: no Claude doctor support without F4's two-file proof) and would make criterion 3.1 a fabricated
pass.

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `InitEnvResult.status` `'ok'|'failed'` → add `'not_verified'` | API/TS type | `src/commands/init-env.ts:30-41`; `src/commands/init-env.test.ts:33-116`; `backend/adapter/initenv_http_test.go:152-183`; docs at `docs/cli-reference.md:215-237` | updated in steps 2–3; existing non-`ok` branch remains safe | add enum member, assert exact reason/no-write semantics, document it; CLI/backend ship together |
| `InitEnvService.Provision` Claude result `status:"failed"` → `status:"not_verified"` | behavior | `backend/usecase/initenv.go:72-79`; `e2e/codex-init-env.spec.ts:129-136`; slice-2 plan's Claude fixture | updated in steps 1 and 3 | preserve HTTP 200, `changed:false`, exit 1; do not change ordinary failure semantics |
| `InitEnvStatus` vocabulary | new domain type | none found (grep: `git grep -nE 'InitEnvStatus|StatusNotVerified' -- backend src`) | created in step 1 | keep distinct from `EngineStatus.StatusUnknown`, which remains doctor-only |
| `DoctorEngines` and `EngineProfileProof` Claude behavior | deferred existing surface | `backend/usecase/doctor.go:5-8`; `backend/infra/spawner.go:195-211`; `backend/infra/doctor.go:14-16` | unaffected in steps 1–3; breaks only if someone tries to satisfy 3.1 without F4 | step 4 blocks positive path; F4 must own proof and doctor expansion |
| Claude installer argv/fingerprint | future surface | none found (grep: `git grep -nE 'ClaudeProvisionArgv|claude.*plugin|installed_plugins|enabledPlugins' -- backend scripts docs`) | none found; no command is assumed | step 4 requires canonical F4 evidence before creating it |
| `InitEnvService` constructor and `POST /api/init-env` request shape | signature/API request | all in-repo call sites found by `git grep -nE 'NewInitEnvService|/api/init-env' -- backend src e2e` | unaffected — no request fields or constructor ports change | no compatibility shim needed |

**Forward compatibility:** additive enum value on the response, with a tolerant existing client branch that
already treats every non-`ok` result as `EXIT.ERROR`; CLI and backend are one artifact pair, so no deploy-order
constraint. Doctor's response remains exactly the two-engine shape until F4.

---

## Migration Checklist

**N/A — no database in this project.** This plan adds no persisted application state and does not alter
journals, schemas, or migrations. The pre-F4 path performs no profile mutation at all.

- [x] No schema change → no migration file or migration command
- [x] No destructive operation (`DROP`/`DELETE`/`TRUNCATE`) added
- [x] Rollback: reverting the enum/result branch restores the prior response; no DB rollback exists

---

## Invariant Impact

| Invariant | Impact | Why |
|---|---|---|
| Inv-1 — Go journals stay in `<runsDir>/go` | No impact | The Claude pre-F4 path creates no run, journal, PID, or durable state. |
| Inv-2 — restart never loses/mis-reports an in-flight run | No impact | No child process or run is started; an interrupted HTTP request has no durable run to rehydrate. |
| Loopback-only + `OriginGuard` | Preserved | `POST /api/init-env` remains mounted behind the existing guard; no route/bind change. |
| Exit-code contract | Preserved | `not_verified` is a completed HTTP-200 verdict mapped to existing `EXIT.ERROR` 1; no code is renumbered. |
| Proof decides success | Strengthened | Claude cannot reach proof or adapter until F4; codex/opencode proof-decides behavior is untouched. |
| Environment scrubbing | Preserved | No Claude child is started pre-F4; the future path must reuse `scrubProfileEnv` + `engineProfileEnv`, not fork them. |
| Credential boundary | Preserved | No Claude files are read, returned, copied, or printed; no installer receives a token namespace. |

---

## Branch Points (pre-declared)

- **Step 1:** If a Claude profile already contains plausible plugin files → still return `not_verified` and
  perform no read/write. Presence without F4's installed-plus-enabled join is not proof.
- **Step 1:** If a caller expects `unknown` because doctor already has `StatusUnknown` → keep the separate
  `not_verified` init-env status. The two are different wire contracts; merging them would make a future
  doctor report indistinguishable from a setup dependency refusal.
- **Step 3:** If a real Claude binary is absent → the pre-F4 e2e must still pass because the service guard
  precedes binary lookup; do not add a binary probe to the unsupported path.
- **Step 4:** If F4 proof is absent when implementation reaches the positive criterion → **BLOCKED**, not
  auto-resolved. Do not guess plugin IDs, versions, enablement semantics, or installer argv. Reopen after
  F4's committed proof and tests exist.
- **Any step:** If satisfying a criterion requires reading/printing credentials, copying a profile, writing
  outside the selected engine namespace, relaxing `OriginGuard`, or shell-interpolating child commands →
  **BLOCKED** by PRD invariants.

---

## Unknowns (must be <= 2)

*None.* The absence of F4 is verified and recorded as a blocking dependency, not an unresolved technical
unknown. The future Claude installer command is intentionally unspecified rather than guessed.

---

## No-Gos

- Will NOT add Claude to `DoctorEngines` before F4's installed-plus-enabled proof exists.
- Will NOT change the `EngineProfileProof` default branch from `nil` to an invented Claude proof.
- Will NOT add a Claude installer argv, plugin ID, marketplace URL, fingerprint, or file parser from memory.
- Will NOT report `status:"ok"` from a child exit code or from plugin presence alone.
- Will NOT read, copy, symlink, print, or return Claude profile contents or credentials.
- Will NOT touch the privileged Claude `/init-env` run path (`SpawnInit`, `kind:init`), journals, or dedupe lanes.
- Will NOT make `saki doctor` mutate anything or expand its reported engine set as a side effect.
- Will NOT relax loopback binding, `OriginGuard`, fixed argv, or environment scrubbing.
- Will NOT use a fake binary as evidence of real Claude provisioning; the eventual positive criterion needs a real Claude binary and a committed F4 proof.
- Will NOT mark slice 3 or the PRD complete while criterion 3.1 remains F4-blocked.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Operator, bootstrapping agent, and cross-origin caller are listed.
- [x] The no-write flow is fully wired from CLI → route → usecase guard → result.
- [x] Loopback/OriginGuard protection is named for every external caller.
- [x] Pre-F4 empty, malformed, pre-seeded, missing-binary, and cross-origin cases are documented.

**Database & Migrations**
- [x] N/A — no database or schema change.
- [x] No breaking persisted-state change and no rollback migration required.
- [x] F4 continuation has no current migration; any future profile-proof files remain external profile state, not application DB schema.

**API Layer**
- [x] Existing request shape named: anonymous `{cwd, engine, profile}` at `backend/adapter/http.go:250-265`.
- [x] Response contract named: `{engine, profile, changed, status, reason, fix}` from `backend/usecase/initenv.go:157-168`, mirrored at `src/types.ts:116-123`.
- [x] `POST /api/init-env` and `OriginGuard` named.
- [x] No auth/credential dependency introduced; loopback guard remains the boundary.

**Service / Business Logic**
- [x] Every executable function change is named in Steps 1–2.
- [x] Pre-F4 side effects are explicitly none; no child, lock, proof read, or profile write.
- [x] Error/verdict paths are documented: 403, 422, HTTP-200 `not_verified`, HTTP-200 `failed`, and existing transport codes.
- [x] Positive Claude service/adapter/proof path is explicitly deferred to F4 and is not a current slice-3 deliverable; the deferral is recorded in the Advisory Ledger.

**Frontend**
- [x] N/A — CLI only; source PRD is `ui:none`.

**Compatibility & Consumers**
- [x] Every changed response/status consumer has a path, verdict, and mitigation.
- [x] Existing doctor surfaces are explicitly marked unaffected until F4.
- [x] Forward compatibility and deploy order are stated.

**Plan Wiring**
- [x] Pre-F4 no-write and existing-engine regression flows are fully wired.
- [x] F4-gated positive flow is mapped without pretending its missing functions exist.
- [x] No vague frontend or database step is present.

---

## Evidence Ledger

### Blocking (must be empty to present)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|
| — | — | None for the executable pre-F4 boundary. | Steps 1–3 have creating steps, exact tests, and no Claude side effect; Step 4 is a non-mutating dependency record. |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 4 | F4 continuation | F4's installed-plus-enabled Claude proof is not present, so the positive Claude path cannot be implemented or verified in this plan without inventing a proof/installer contract. Reopen the continuation after F4 lands. | `tasks/roadmap.md:27-31`; `backend/infra/spawner.go:195-211`; `backend/usecase/doctor.go:5-8`; target-branch search found no `installed_plugins.json` or `enabledPlugins`. |
| 1 | `StatusUnknown` | `StatusUnknown` remains unchanged and is not suitable for init-env because it belongs to `EngineStatus`/doctor; a separate `InitEnvStatus` avoids cross-command vocabulary drift. | `backend/domain/doctor.go:3-12`; `src/types.ts:101-110` |
| 2 | Existing clients | Existing clients that branch on `status !== "ok"` remain safe, but strict status unions need the additive member. | `src/commands/init-env.ts:34-41`; `src/types.ts:116-123` |
| 3 | E2E fixture | The current e2e Claude test only proves an empty directory remains empty; strengthen it with a pre-seeded sentinel file so the no-write claim covers existing profile contents. | `e2e/codex-init-env.spec.ts:129-140` |
| — | — | All anchors verified, all executable targets have creating steps, no unknowns above LOW; the F4 continuation is advisory and not hidden as a current blocker. | self-audit + committed-code reads |

**Blocking: 0 for the executable pre-F4 boundary; F4 continuation remains advisory until F4 lands.**

---

## Success Criteria — executable pre-F4 boundary

<!-- implementation: typed `not_verified` no-write boundary shipped; positive Claude proof remains F4-gated. -->

- [x] **3.2 boundary:** The operator runs `saki init-env --engine claude --profile <dir> --json` (execute with
  `cd backend && go test ./usecase/ ./adapter/`, `npx vitest run src/commands/init-env.test.ts`, and
  `npx playwright test e2e/codex-init-env.spec.ts`). The CLI exits `1`; HTTP 200 contains
  `status:"not_verified"`, `changed:false`, the explicit F4 reason, and `fix:""`; no binary lookup, proof
  read, profile lock, child process, or filesystem write occurs, asserted by the named unit, HTTP, and e2e
  tests.
- [ ] **3.3 codex/opencode regression:** The operator provisions codex and opencode, then runs doctor against
  the same selected profile (execute `npx playwright test e2e/codex-init-env.spec.ts` and the existing doctor
  tests via `cd backend && go test ./...`). Both engines retain their existing proof-backed success and
  doctor reports the same profile; the named e2e and Go tests pass.
- [ ] **BR1:** The operator invokes doctor and init-env (execute `cd backend && go test ./...`). Doctor remains
  read-only and reports exactly codex/opencode; only init-env retains mutation; all existing doctor tests pass.
- [ ] **BR2:** The CLI receives `not_verified`, `failed`, and `ok` results (execute
  `npx vitest run src/commands/init-env.test.ts` and `npm run typecheck`). Both non-`ok` results return
  `EXIT.ERROR` 1, while `ok` is returned only after shared proof; the named tests and typecheck pass.
- [ ] **BR3/BR4/BR5:** The operator submits a pre-F4 Claude request through the loopback route (execute
  `cd backend && go test ./adapter ./infra`). No credentials or foreign environment namespace reaches a
  child, no profile is written, and non-loopback requests remain rejected by `OriginGuard`; the named env and
  origin tests pass.
- [ ] **Whole-repo verification:** The maintainer runs `npm run typecheck`, `npx vitest run`,
  `npm run test:coverage`, `cd backend && go vet ./... && go test ./...`, and the focused F6 Playwright spec.
  Every command exits 0 and total coverage is strictly above 80%.

## Deferred PRD criteria — F4 continuation, not current deliverables

- **3.1:** After F4 commits installed-plus-enabled proof and a canonical installer contract, the operator
  runs Claude setup and doctor (future exact verification: `cd backend && go test ./...` plus a real Claude
  binary e2e). The selected profile is changed only within its Claude namespace, the post-setup shared proof
  passes, and doctor reports Claude `ok`. This plan does not claim or implement that result.
- **3.3 Claude half:** After F4, the operator runs successful Claude setup followed immediately by doctor
  (future real Claude-binary e2e). Both commands resolve the same child-visible profile and doctor reports
  `ok`; this continuation must be added only after F4's proof and installer contract are committed.
- **Final PRD completion:** The maintainer does not mark F6 complete until the deferred criteria pass review,
  security audit, QA, and full e2e after F4; no current command can honestly verify them.

---

## Annotation Space

### AUTO-RESOLVED decisions

- `StatusUnknown` vs `not_verified` → use a distinct init-env status. `StatusUnknown` is doctor's
  unverifiable-engine state; init-env needs an explicit dependency refusal that still maps to `EXIT.ERROR`.
- F4 absent → implement only the typed no-write boundary and stop. Reversible, evidence-backed, and required
  by PRD 3.2; guessing a Claude proof or installer would cross the PRD non-goal.

> Human: add notes, corrections, constraints here.

---

Status: [x] Draft  [ ] Annotated  [ ] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty for executable scope  [x] Unknowns <= 2
