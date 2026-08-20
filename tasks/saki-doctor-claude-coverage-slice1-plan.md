# EXECUTION PLAN: F4 Slice 1 — Claude installed-and-enabled proof

**Date:** 2026-08-20
**Blocking items:** 0
**Risk Score:** MED
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend-only)
**Source PRD:** `tasks/prd-saki-doctor-claude-coverage.md` §8 Slice 1
**Prior slices:** N/A — slice 1 / standalone
**Appetite:** ~4 agent tasks (4 acceptance criteria; within the PRD's small appetite)
**Kill-if:** If real Claude fixtures require a broader migration, installer/version policy, or a third profile file beyond the two-file contract, stop Slice 1 under PRD §12/§16 rather than widening the parser.

## Problem Statement

When an unattended run selects a Claude profile, I want doctor/preflight to distinguish an installed-and-enabled `saki-builder` plugin from an incomplete profile, so a run cannot proceed on a false readiness signal.

---

## Concrete Example Output

A pinned profile at `/tmp/profile` with the canonical identity installed and enabled produces no error:

```text
resolved, err := resolveClaudeProfile(&profile)
// resolved.ID == "saketek@saki-builder"
// resolved.Version == "0.5.0"
// err == nil
```

A profile with the same installed entry but `enabledPlugins["saketek@saki-builder"] == false` produces an error that matches the existing sentinel:

```text
errors.Is(ClaudeProfileProof(&profile), usecase.ErrEngineNotProvisioned) == true
```

When both supported identities are installed at different versions, `resolveClaudeProfile` selects `saketek@saki-builder` first and records that selected identity/version in the fixture assertion; it does not select `saki-builder@saketek` by map order or version.

`ClaudeProfileProof` is the production error-only wrapper around that internal resolver; no selected ID/version is added to `EngineReport` or any API response.

```go
func ClaudeProfileProof(configDir *string) error
```

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | [x] Add child-visible Claude profile path helpers plus the internal `resolveClaudeProfile(configDir *string) (claudePlugin, error)` seam for `installed_plugins.json` and `settings.json`, including the fixed supported-ID precedence list and `ErrClaudePluginMissing` diagnosis; keep JSON parsing strict for required structure and ignore unrelated metadata only where the observed registry shape requires it | `backend/infra/claude.go` (new) | MED | `TestResolveClaudeProfile_PresentCanonical` written first (Red→Green) | Yes |
| 2 | [x] Implement `resolveClaudeProfile` and `ClaudeProfileProof(configDir *string) error`: read both selected files, unmarshal the observed registry/settings shapes, select the first installed supported ID in precedence order, require the matching `enabledPlugins` boolean to be true, return the selected ID/version only through the internal resolver test seam, and wrap every failure in `usecase.ErrEngineNotProvisioned`; do not compare versions or write files | `backend/infra/claude.go` | MED | `TestResolveClaudeProfile_FailClosedCases` written first (Red→Green) | Yes |
| 3 | [x] Route `domain.EngineClaude` through `EngineProfileProof` while retaining Codex/OpenCode dispatch unchanged; this makes the shared preflight path enforce the new proof and removes only the existing Claude `default: return nil` for the Claude case | `backend/infra/spawner.go` | MED | `TestEngineProfileProof_ClaudeMatchesDirectProof` and existing `TestDoctorPreflightAgreement` fixture pattern extended for Claude | Yes |
| 4 | [x] Add table-driven real filesystem fixtures for canonical ID, fallback ID, dual-ID precedence with different versions, unrelated IDs/settings, malformed/missing files, disabled selected ID, and installed/enabled identity mismatch; assert selected ID/version where the proof exposes them through the test seam, assert `errors.Is` for failures, and compare file bytes before/after to prove read-only behavior | `backend/infra/claude_test.go` (new), `backend/infra/doctor_test.go` (only if shared delegation coverage needs Claude) | MED | `go test ./backend/infra -run 'Claude|EngineProofChecker|DoctorPreflightAgreement'` | Yes |

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Local operator / CI caller | Run the existing doctor/preflight proof against the selected local Claude profile | Install, repair, edit settings, or spawn Claude through this slice | Existing loopback/origin guard applies only when reached through `/api/doctor`; no new auth surface | None — headless CLI/backend |

## Plan Wiring

### Flow 1: Claude preflight proof

```text
`preflight(spec)` (`backend/infra/spawner.go:155`)
  → `EngineBinaryCheck(engine)` (`backend/infra/spawner.go:181`) [unchanged; Claude remains exempt]
  → `EngineProfileProof(engine, configDir)` (`backend/infra/spawner.go:198`)
  → `ClaudeProfileProof(configDir)` (`backend/infra/claude.go`)
  → `claudeProfilePaths(configDir)` selects `<configDir>/plugins/installed_plugins.json` + `<configDir>/settings.json`
  → JSON registry entry `plugins[selectedID][0].version` from a non-empty installation-record array + settings `enabledPlugins[selectedID] == true`
  → nil or `usecase.ErrEngineNotProvisioned`
```

### Flow 2: Shared doctor proof seam (consumed in Slice 2)

```text
`EngineProofChecker.ProfileProof` (`backend/infra/doctor.go:14`)
  → `EngineProfileProof(engine, configDir)` (`backend/infra/spawner.go:198`)
  → `ClaudeProfileProof(configDir)` (`backend/infra/claude.go`)
  → same nil/error result as preflight
```

No frontend, API, database, migration, or journal write occurs in this slice.

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `EngineProfileProof(engine, configDir)` default Claude branch (`backend/infra/spawner.go:198-211`) | behavior | `preflight` (`backend/infra/spawner.go:155-174`); `EngineProofChecker.ProfileProof` (`backend/infra/doctor.go:14-16`); `TestEngineProofChecker_DelegatesToSharedFuncs` (`backend/infra/doctor_test.go:60-69`) | updated in step 3 | Add Claude direct-proof/delegation fixtures and preserve Codex/OpenCode branches; missing Claude profile now fails closed by design |
| `ClaudeProfileProof(configDir *string)` | new exported infra function | none found before creation (grep: `grep -R "ClaudeProfileProof" -n --include='*.go' backend`) | updated in steps 2–4 | Shared dispatcher is the sole production caller; tests call it directly for fixture coverage |
| `/api/doctor`, `EngineReport`, `DoctorResult`, CLI exit codes | existing response/contract | `backend/adapter/http.go:100-104`; `backend/domain/doctor.go:15-26`; `src/commands/doctor.ts`; `src/types.ts:101-113` | unaffected in Slice 1 | Claude reporting is deliberately deferred to Slice 2; no route or JSON shape changes here |
| `usecase.ErrEngineNotProvisioned` | existing sentinel | `backend/usecase/spawn.go:22-27`; `backend/adapter/http.go:543`; existing Codex/OpenCode proofs | unaffected; reused | Wrap the sentinel exactly as existing proofs do; no new error mapping |

**Forward compatibility:** additive proof implementation plus a behavior change for Claude only; the existing report shape and route remain unchanged. Slice 2 must add Claude to `DoctorEngines` only after this proof is green.

## Migration Checklist

No database schema, journal schema, or migration changes. Claude profile files are external local configuration read-only inputs.

| Change | Table | Column/Index | Migration File | Command |
|---|---|---|---|---|
| None | — | — | — | — |

## Branch Points (pre-declared)

- Step 1: If the real fixture shape differs in nesting but remains a two-file installed-registry + `enabledPlugins` contract, adapt the smallest typed parser and add the shape to fixtures; this is reversible and stays within scope.
- Step 2: If either file is JSONC, do not silently strip comments or broaden acceptance; stop and record the parser requirement under the PRD §12 kill-if unless an existing shared JSONC parser already covers the exact shape.
- Step 2: If the registry contains both supported IDs, always choose `saketek@saki-builder`, even if disabled or lower-versioned; never fall back based on enablement or version.
- Step 2: If required fields have unknown types or the selected identity cannot be joined exactly to `enabledPlugins`, fail closed with `ErrEngineNotProvisioned`; do not infer readiness.
- Step 3: Do not add Claude to `DoctorEngines` in this slice; that is Slice 2's explicit reporting change.
- Any path that would write settings, install a plugin, invoke Claude, relax loopback/origin protection, or expose profile contents crosses a No-Go and is blocked.

## Unknowns (must be <= 2)

None. The two-file locations and required observed fields are grounded by `tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run-review.md:191-200`; the implementation retains fail-closed behavior for any shape outside that evidence.

## No-Gos

- Will NOT add Claude provisioning, installer argv, settings mutation, migration, repair, or version migration.
- Will NOT add Claude to `DoctorEngines` or change `/api/doctor` output in Slice 1.
- Will NOT accept arbitrary plugin IDs, choose by version/map iteration, or treat enablement as a second version source.
- Will NOT print or persist credentials or full profile contents.
- Will NOT add a dependency or move JSON parsing into `domain`/`usecase`.

## Implementation Completeness Checklist

**User Coverage**
- [x] Local operator/CI role is listed; no UI role exists.
- [x] Full backend call chain is in Plan Wiring.
- [x] Existing loopback/origin guard is cited; no new route is added.
- [x] Missing, malformed, absent, disabled, mismatched, unknown, and dual-ID cases are listed.

**Database & Migrations**
- [x] No model or schema field changes.
- [x] No migration command applies.
- [x] No destructive schema operation exists.

**API Layer**
- [x] No request/response schema changes.
- [x] Existing `/api/doctor` route is explicitly unaffected until Slice 2.
- [x] Existing OriginGuard remains unchanged.

**Service / Business Logic**
- [x] `ClaudeProfileProof` and `EngineProfileProof` are named with exact paths.
- [x] Side effects are none; filesystem reads only.
- [x] All proof failure paths return the typed provisioning sentinel.

**Frontend**
- [x] No frontend files or UI behavior change.

**Compatibility & Consumers**
- [x] Existing changed behavior has caller inventory and mitigation.
- [x] No prior slice applies.

**Plan Wiring**
- [x] Preflight and shared doctor proof call chains are complete.
- [x] Every implementation step names exact functions/files.

## Evidence Ledger

### Blocking (must be empty to present — each row a binary, cited predicate)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|---|---|---|
| — | — | None | All anchors and fixture assumptions verified in `tasks/saki-doctor-claude-coverage-slice1-context.md`; source PRD lock exists at `tasks/proto-saki-doctor-claude-coverage/.prd-locked`. |

### Advisory

| Step | Note | Evidence |
|---|---|---|
| 2 | The machine-local probe found standard JSON, but unknown future JSONC shapes fail closed rather than widening this slice. | PRD §12; branch point Step 2 |
| 4 | Selected ID/version are asserted by fixture-level resolver output; no production response field is added. | PRD §16 proof contract; `EngineReport` unchanged |

**Blocking: 0 → READY.**

## Success Criteria

- [x] Given a pinned profile with a non-empty `plugins["saketek@saki-builder"]` installation-record array whose first record has `version`, and `enabledPlugins["saketek@saki-builder"] == true`, when `TestResolveClaudeProfile_PresentCanonical` runs via `go test ./backend/infra -run TestResolveClaudeProfile_PresentCanonical`, then the resolver returns `nil`, `ID == "saketek@saki-builder"`, and the fixture's version. → 5.1
- [x] Given missing/malformed files, an absent supported plugin, a disabled selected ID, mismatched installed/enabled spelling, unknown IDs, or unrelated enabled settings, when `TestResolveClaudeProfile_FailClosedCases` runs via `go test ./backend/infra -run TestResolveClaudeProfile_FailClosedCases`, then every case returns an error matching `usecase.ErrEngineNotProvisioned` and no case returns resolver success. → security / validation
- [x] Given both supported IDs with different versions, when `TestResolveClaudeProfile_Precedence` runs via `go test ./backend/infra -run TestResolveClaudeProfile_Precedence`, then the resolver returns `ID == "saketek@saki-builder"` and its version consistently; the lower-precedence version cannot win. → 5.1
- [x] Given a Claude fixture, when `TestEngineProfileProof_Claude`, `TestEngineProofChecker_DelegatesToSharedFuncs`, and `TestDoctorPreflightAgreement` run via `go test ./backend/infra -run 'TestEngineProfileProof_Claude|TestEngineProofChecker_DelegatesToSharedFuncs|TestDoctorPreflightAgreement'`, then direct, shared, and preflight proof results agree while existing Codex/OpenCode behavior remains green. → 5.1
- [x] Given the changed backend proof code, when `go test ./backend/...` and `go vet ./...` run from `backend/`, then all backend tests and vet pass and fixture assertions show no profile file bytes changed. → security / error-path

QA evidence: focused acceptance tests passed (27 total), full backend suite passed (700 tests), `go vet ./...` passed, `go build ./...` passed, and coverage generation passed with `/tmp/saki-doctor-claude-coverage.out`. The `go tool cover` total-line formatter was blocked by the transient shell safety classifier after generation.

**Manual verification:** None — all Slice 1 criteria are automatable.
✅ Criteria hardened: each criterion names Given/When/Then, an exact command, and the expected result.

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---
Status: [x] Draft  [x] Annotated  [x] Approved  [x] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2

<!-- rplan-review-phase1-attempts: 0 -->
