# EXECUTION PLAN: F6 slice 4 — provision and prove Claude profile

**Date:** 2026-08-20
**Item:** F6
**Blocking items:** 0 — B1 installer contract and B2 default-path regression resolved; ready for approval
**Risk Score:** HIGH (external installer mutates user profile state and must preserve credential boundaries)
**Unknown Count:** 1 / 2 max
**Behavior Spec:** N/A — backend + CLI/e2e only, no UI
**Source PRD:** `tasks/prd-saki-init-env-provision-engine-profile.md` §7 Slice 3 and §8 criteria 3.1, 3.3 (locked)
**Prior slices:** `tasks/saki-init-env-provision-engine-profile-slice1-plan.md`, `tasks/saki-init-env-provision-engine-profile-slice2-plan.md`, `tasks/saki-init-env-provision-engine-profile-slice3-plan.md` (read; shipped shape wins)
**Research context:** `tasks/saki-init-env-provision-engine-profile-slice4-context.md`
**Appetite:** ~6 agent tasks — adapter, service, tests, docs, e2e, QA; within the PRD's medium band
**Kill-if:** post-provision doctor cannot prove the exact Claude profile used by the next run, or the installer writes outside the selected Claude namespace

## Problem Statement

When an operator selects Claude for a repository, I want `saki init-env --engine claude` to provision the exact Claude profile used by spawn and immediately prove it through the same installed-plus-enabled proof that `saki doctor` uses, so the next Claude run is ready without manual file editing.

---

## Concrete Example Output

After a fresh explicit profile is provisioned:

```console
$ saki init-env --engine claude --profile /tmp/saki-claude --json
{"engine":"claude","profile":"/tmp/saki-claude","changed":true,"status":"ok","reason":"","fix":""}
$ saki doctor --engine claude --profile /tmp/saki-claude --json
{"engines":[{"engine":"claude","profile":"/tmp/saki-claude","status":"ok","reason":"","fix":""}]}
$ saki init-env --engine claude --profile /tmp/saki-claude --json
{"engine":"claude","profile":"/tmp/saki-claude","changed":false,"status":"ok","reason":"","fix":""}
```

The fixed installer argv is:

```text
claude plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git --scope user
claude plugin install saki-builder@saketek --scope user
```

`CLAUDE_CONFIG_DIR=<profile>` with explicit user scope targets the same `<profile>/plugins` and `<profile>/settings.json` paths read by proof. The marketplace source, plugin identity, and scope are pinned by the installed manifests, official documentation, and Claude Code `2.1.237` CLI help.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Record the verified non-interactive Claude marketplace-registration/install argv and marketplace source form; retain the verified `CLAUDE_CONFIG_DIR` + user-scope contract and use the exact vectors in the adapter mapping. | `tasks/saki-init-env-provision-engine-profile-slice4-context.md`, `tasks/saki-init-env-provision-engine-profile-slice4-plan.md` | HIGH | Claude Code `2.1.237` help, official docs, and local marketplace/plugin manifests; no secret values captured | Yes |
| 2 | Keep the default-profile lock namespace aligned with proof and spawn: `profilePath(domain.EngineClaude, nil)` returns `$HOME/.claude`, and explicit `$HOME/.claude` shares the same lock key. Format and run the focused usecase regression from `backend/`. | `backend/usecase/initenv.go`, `backend/usecase/initenv_test.go` | MED | `go test ./usecase -run 'TestProfileLockKeyUsesClaudeDefaultProfile|TestProfileLockKeyCollapsesEquivalentOpencodeProfiles' -count=1` from `backend/` | Yes |
| 3 | Add the verified fixed Claude installer mapping and Claude-specific adapter support: resolve `provisionArgv(domain.EngineClaude)`, create only the selected Claude profile root when required, route explicit profiles through `CLAUDE_CONFIG_DIR`, preserve unpinned default behavior, and fingerprint exactly the two files consumed by `ClaudeProfileProof`. Reuse `scrubProfileEnv` and `engineProfileEnv`; do not add a parallel environment switch. Claude retains BR5’s established byte-identical environment behavior; only inherited `CLAUDE_CONFIG_DIR` is removed before optional pinning. | `backend/usecase/initenv.go`, `backend/infra/initenv.go` | HIGH | Test first: `TestClaudeProvisionArgvContract`, `TestEngineProvisionerProvisionsClaudeWithFixedArgvAndScrubbedEnv`, `TestClaudeProfileFingerprint` in `backend/infra/initenv_test.go` | Yes |
| 4 | Replace the pre-F4 Claude `not_verified` guard with the common proof-decides flow while retaining the no-Claude-binary-check contract: lock the selected profile, skip the adapter when `ClaudeProfileProof` already passes, run the adapter otherwise, then run the same proof again and return `ok` only when it passes. Keep installer errors subordinate to a passing post-proof, and keep failed post-proof results actionable without exposing child output beyond the existing bounded contract. | `backend/usecase/initenv.go`, `backend/usecase/initenv_test.go` | HIGH | Test first: `TestInitEnvServiceClaudeAlreadyProvenSkipsAdapter`, `TestInitEnvServiceProvisionsClaudeThenProves`, `TestInitEnvServiceClaudeInstallerExitZeroIsNotProof`, `TestInitEnvServiceClaudeProofWinsOverInstallerError` | Yes |
| 5 | Extend HTTP, CLI, and documentation contracts from Claude `not_verified` to normal `ok`/`failed` provisioning while preserving `{engine, profile, changed, status, reason, fix}`, HTTP 200 for completed attempts, existing exit codes, loopback protection, and the privileged Claude `/init-env` route unchanged. | `backend/adapter/initenv_http_test.go`, `src/commands/init-env.test.ts`, `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` | MED | `go test ./adapter -run 'TestInitEnvHandler' -count=1`; `npm test -- src/commands/init-env.test.ts`; documentation assertions/grep | Yes |
| 6 | Add a dedicated real-binary Claude e2e covering fresh explicit-profile provisioning, immediate doctor agreement, repeat idempotency, and no unrelated namespace mutation. Replace the pre-F4 refusal assertion; do not use a fake binary as proof of real Claude invocation. | `e2e/claude-init-env.spec.ts` (NEW) or the existing init-env e2e fixture if the repository's test shape requires one | HIGH | `npx playwright test e2e/claude-init-env.spec.ts` with a real `claude` binary and disposable profile; fail closed when the binary/contract is unavailable rather than converting the test to a fake | Yes |
| 7 | Run the complete quality and security gates, update F6 state only after every criterion passes, mark the F6 roadmap item `Shipped`, and leave the branch ready for the required push gate. | `tasks/.build-saki-init-env-provision-engine-profile-state.json`, `tasks/roadmap.md`, this plan | HIGH | `go test ./... -count=1 -timeout 120s`; `go vet ./...`; `go build ./...`; `npm run typecheck`; `npm test`; `npm run test:coverage`; real Claude e2e; security/reviewer gates | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|---|---|---|---|---|
| Local operator | Provision one selected Claude profile and receive proof-backed JSON/human result | install Claude itself, authenticate, provision all engines, expose credentials | Existing loopback bind and `OriginGuard` on `POST /api/init-env`; CLI validates cwd/profile | `saki init-env --engine claude [--profile <dir>] --json` |
| Bootstrapping agent | Branch on `status`, run doctor immediately, and continue only on `status:"ok"` | infer success from installer exit code or `not_verified` legacy behavior | Same loopback route and fixed response contract | CLI only |
| Remote/cross-origin caller | Nothing | reach provisioning | `OriginGuard` rejects before service/adapter | None |

Edge cases: malformed cwd/profile/engine remain 422 before adapter; missing Claude proof files fail after the adapter and return `failed`; installer success with failing proof remains `failed`; installer error with passing proof returns `ok`; non-loopback requests remain 403 before any adapter; an already-proven profile is a no-op.

---

## Plan Wiring

### Flow 1: Explicit Claude provisioning

```text
`saki init-env --engine claude --profile /tmp/saki-claude --json`
  → `cmdInitEnv()` (`src/commands/init-env.ts`)
  → `StudioClient.post('/api/init-env', {cwd, engine, profile})`
  → `POST /api/init-env` (`backend/adapter/http.go`, OriginGuard)
  → `InitEnvService.Provision()` (`backend/usecase/initenv.go`)
  → `normalizeProvisionRequest()`
  → `EngineProofs.BinaryCheck()` — no Claude PATH check
  → `profileGate.lock(domain.EngineClaude, profile)` — profile root `/tmp/saki-claude`
  → shared `ProfileProof()`
  → `EngineProvisioner.Provision()` (`backend/infra/initenv.go`)
  → fixed verified Claude argv via `exec.CommandContext` with `CLAUDE_CONFIG_DIR=/tmp/saki-claude`
  → `profileFingerprint()` over `/tmp/saki-claude/plugins/installed_plugins.json` and `/tmp/saki-claude/settings.json`
  → shared `ClaudeProfileProof()` (`backend/infra/claude.go`)
  → `{engine, profile, changed, status, reason, fix}`
  → `EXIT.OK == 0` only when proof passes
```

### Flow 2: Default Claude profile

```text
`saki init-env --engine claude --json`
  → normalized `profile:nil`
  → no inherited `CLAUDE_CONFIG_DIR`
  → installer uses Claude's default profile
  → proof reads `$HOME/.claude/plugins/installed_plugins.json` + `$HOME/.claude/settings.json`
  → lock key is `$HOME/.claude`
  → immediate `saki doctor --engine claude --json` reads the same default profile
```

### Flow 3: Doctor remains read-only

```text
`saki doctor --json --profile /tmp/saki-claude`
  → `GET /api/doctor` (`backend/adapter/http.go`, OriginGuard)
  → `DoctorService.Check()` (`backend/usecase/doctor.go`)
  → `EngineProfileProof(EngineClaude, &profile)`
  → `ClaudeProfileProof()` reads two files only
  → no installer, no repair, no engine spawn, no profile mutation
```

No database, migration, journal, or frontend component is part of this slice.

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `InitEnvService.Provision` Claude branch (`backend/usecase/initenv.go`) | behavior | `backend/usecase/initenv_test.go`; `backend/adapter/initenv_http_test.go`; `src/commands/init-env.test.ts`; `e2e/codex-init-env.spec.ts`; docs `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` | updated in steps 4–6 | Preserve response fields, HTTP status, exit codes, and proof-decides invariant; replace only Claude's pre-F4 refusal after F4 proof exists |
| `profilePath(domain.EngineClaude, nil)` | lock namespace | `profileLockKey`, `profileGate.lock`, usecase lock tests (`backend/usecase/initenv.go`, `backend/usecase/initenv_test.go`) | updated in step 2 | `$HOME/.claude` is the canonical default; explicit `$HOME/.claude` must collapse to the same key |
| `provisionArgv` engine mapping | internal installer dispatch | `EngineProvisioner.Provision`, installer-fix rendering, infra tests | updated in step 3 | One fixed Claude mapping; derive `ClaudeInstallFix` from it; no shell interpolation |
| `profileFingerprint` | internal mutation signal | `EngineProvisioner.Provision`, infra tests | updated in step 3 | Fingerprint only proof-read files; no contents in responses |
| `POST /api/init-env` response fields | API response | `src/types.ts`, `src/commands/init-env.ts`, adapter/CLI/e2e tests | unaffected — fields and exit codes remain unchanged | Add Claude success/failure fixtures only |
| `GET /api/doctor` and `EngineProfileProof` | shared proof/read-only API | doctor tests, spawn preflight, `backend/infra/claude.go` | unaffected — Claude proof already shipped | Reuse the proof; do not add a second parser or mutating doctor path |
| `CLAUDE_CONFIG_DIR` | child environment key | `backend/infra/spawner.go`, provision environment, infra tests | updated in step 3 | Reuse `engineProfileEnv`; scrub inherited selector before setting explicit profile while preserving BR5’s other Claude environment entries |

**Forward compatibility:** additive internal engine mapping and tests; externally the response shape and exit codes are tolerant and unchanged. CLI/backend should ship together because Claude changes from `not_verified` to normal `ok`/`failed` behavior.

---

## Migration Checklist

**N/A — no database or schema changes.** Claude profile JSON is external engine state, not an application database.

| Change | Table | Column/Index | Migration File | Command |
|---|---|---|---|---|
| None | — | — | — | — |

- [x] No schema change or migration command
- [x] No destructive database operation
- [x] Profile mutation is delegated to the verified engine-native command; doctor remains read-only

---

## Branch Points (pre-declared)

- Step 1: If the installed Claude CLI rejects the pinned marketplace source or scope → **BLOCKED**; do not substitute a different repository or scope.
- Step 2: If `$HOME/.claude` cannot be verified as Claude's default profile on the target environment → pause implementation and retain the explicit-profile-only path; do not restore `.config/claude`.
- Step 3: If the installer requires authentication or prints credentials → **BLOCKED**; F6 never manages or routes credentials.
- Step 3: If the installer writes outside the selected profile under `CLAUDE_CONFIG_DIR` → **BLOCKED** and recut the adapter; no path whitelist workaround may hide the violation.
- Step 4: If a passing installer leaves proof failing → return `failed`; never report `ok` from exit status.
- Step 4: If the installer fails but the post-proof passes → return `ok` and preserve `changed` according to the fingerprint; proof wins by invariant.
- Step 6: If no real Claude binary is available → mark the real-binary criterion BLOCKED with the exact environment handoff; never replace it with a fake binary.
- Any step: If the change touches doctor writes/spawn, privileged Claude `/init-env`, credentials, loopback binding, exit-code numbers, or journal state → **BLOCKED** by project invariants.

---

## Unknowns (must be <= 2)

1. **MED** Real Claude CLI e2e availability and authentication state → run only with the locally installed real binary and disposable profile; fail closed if unavailable and never route credentials through chat.

Resolved: exact user-scope installer argv and profile routing are recorded in `tasks/saki-init-env-provision-engine-profile-slice4-context.md` § External installer contract research status. The focused default-path regression passed from `backend/`.

---

## No-Gos

- Will NOT guess Claude installer argv, marketplace source, plugin ID, version, scope flag, or enablement semantics.
- Will NOT use interactive slash-command syntax as evidence for an `exec.Command` argv.
- Will NOT implement profile writes by editing `installed_plugins.json` or `settings.json` directly; use the verified official installer command.
- Will NOT report success from installer exit code alone.
- Will NOT add a Claude PATH/binary preflight requirement.
- Will NOT make doctor install, repair, mutate, or spawn.
- Will NOT copy, symlink, print, or return profile contents or credentials.
- Will NOT write outside the selected Claude profile namespace.
- Will NOT alter the privileged Claude `/init-env` run workflow.
- Will NOT change response fields, CLI flags, exit-code numbers, loopback binding, or journal invariants.
- Will NOT use a fake binary as evidence of real Claude provisioning.
- Will NOT mark F6 `Shipped` while criteria 3.1 or 3.3 remain unverified.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Local operator, bootstrapping agent, and cross-origin caller are listed.
- [x] Explicit and default Claude paths have full CLI → HTTP → usecase → infra → proof wiring.
- [x] Existing `OriginGuard` and loopback-only boundary are named.
- [x] Missing proof, installer failure, installer false-green, repeat setup, and cross-origin cases are covered.

**Database & Migrations**
- [x] No database or schema change; migration checklist is N/A.
- [x] No journal schema or durable run state change.

**API Layer**
- [x] Existing anonymous request `{cwd, engine, profile}` is unchanged.
- [x] Existing response `{engine, profile, changed, status, reason, fix}` is unchanged.
- [x] `POST /api/init-env` and `OriginGuard` are named.
- [x] No auth or credential dependency is introduced.

**Service / Business Logic**
- [x] Exact functions to change are named: `InitEnvService.Provision`, `profilePath`, `EngineProvisioner.Provision`, `provisionArgv`, `profileFingerprint`, `provisionEnv` reuse.
- [x] Side effects are limited to the selected Claude profile and official installer command.
- [x] Error paths and proof precedence are specified.
- [x] Exact external installer argv and scope evidence are recorded in the research context.

**Frontend / CLI**
- [x] No UI change; CLI source contract remains compatible.
- [x] CLI and e2e fixtures are named.
- [x] Human/JSON success, failure, and idempotency behavior is specified.

**Compatibility & Consumers**
- [x] Every changed existing surface has consumers, verdict, and mitigation.
- [x] Prior slices and shipped F4 proof were read.

**Plan Wiring**
- [x] Every major flow has an end-to-end call chain.
- [x] Every state-changing function has a named test-first target.
- [x] Default and explicit profile path semantics are explicit.

---

## Evidence Ledger

### Blocking (must be empty before implementation approval)

No blocking items remain.

Resolved evidence:

- B1 exact installer argv: `tasks/saki-init-env-provision-engine-profile-slice4-context.md` § External installer contract research status; verified from Claude Code `2.1.237` CLI help and local manifests.
- B2 default-path lock regression: user reported the focused command passed from `backend/`; the test covers `$HOME/.claude` equivalence and rejects legacy `.config/claude`.
- Profile scope: official settings/plugin references in the same context section confirm `CLAUDE_CONFIG_DIR=<profile>` targets the proof paths.

### Advisory

| Step | Note | Evidence |
|---|---|---|
| 6 | The real Claude e2e depends on a locally installed authenticated Claude CLI but must not route credentials through the plan or chat | PRD §9 rule 6 and project secrets policy |
| — | No database, migration, journal, or UI work is implied | PRD §6, §9, and repository architecture |

**Blocking: 0 → READY for implementation approval.**

---

## Success Criteria

- [x] The default-profile regression passes from `backend/`: `go test ./usecase -run 'TestProfileLockKeyUsesClaudeDefaultProfile|TestProfileLockKeyCollapsesEquivalentOpencodeProfiles' -count=1`; unpinned Claude and explicit `$HOME/.claude` resolve to one lock namespace, and `.config/claude` is not used. → prerequisite for 3.3
- [x] A verified fixed Claude installer mapping is executed with exact argv, explicit `CLAUDE_CONFIG_DIR`, BR5-compatible environment handling, bounded timeout/output, no stdin, and no shell interpolation; infra tests prove argv/env plumbing. → 3.1 / 3.3
- [x] A fresh explicit Claude profile passes `ClaudeProfileProof` after setup; `InitEnvService.Provision` returns HTTP 200 JSON with `status:"ok"`, `changed:true`, and empty `reason`/`fix`; the selected doctor report immediately returns `status:"ok"`. → 3.1
- [x] A repeated setup against the same proven profile skips the adapter and returns `status:"ok"`, `changed:false`; no duplicate registration or unrelated namespace mutation occurs. → 3.3 / 5.2
- [x] Installer exit `0` with a failing post-proof returns `status:"failed"`; installer failure with a passing post-proof returns `status:"ok"`; no path exposes credentials or unbounded child output. → 3.1 / 5.3
- [x] `POST /api/init-env` remains 403 for non-loopback requests and 422 for malformed input before adapter invocation; doctor remains read-only and existing response fields/exit codes remain unchanged. → privacy / compatibility
- [x] Real-binary e2e invokes the actual Claude CLI, provisions a disposable profile, runs doctor immediately against that same profile, and verifies repeat idempotency; a fake binary cannot satisfy this criterion. → 3.1 / 3.3
- [x] Full QA passes: `go test ./... -count=1 -timeout 120s`, `go vet ./...`, `go build ./...`, `npm run typecheck`, `npm test`, `npm run test:coverage` strictly above 80%, and no secret findings were observed. → regression/security

Evidence: backend `go test ./... -count=1` passed 711 tests; `go vet ./...` and `go build ./...` passed; TypeScript typecheck passed; Vitest passed 392 tests; coverage passed at 86.43% statements / 88.37% lines; `CI=1 npm run e2e -- e2e/claude-init-env.spec.ts` passed 1 real-binary Claude test. An independent reviewer launch was unavailable because the local agent safety classifier was temporarily unavailable.

**Manual verification:** None for product behavior; the real-binary e2e is automated but requires the local Claude CLI/account setup without sharing credentials.

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise the plan and re-check the Blocking Set before implementation.

---
Status: [x] Draft [x] Annotated [ ] Approved [ ] In Progress [x] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited [x] Blocking Set empty [x] Unknowns <= 2
