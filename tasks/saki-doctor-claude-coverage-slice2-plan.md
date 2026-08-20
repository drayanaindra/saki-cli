# EXECUTION PLAN: F4 Slice 2 — report Claude through doctor without side effects

**Date:** 2026-08-20
**Blocking items:** 0
**Risk Score:** MED
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only)
**Source PRD:** `tasks/prd-saki-doctor-claude-coverage.md` §8 Slice 2
**Prior slices:** `tasks/saki-doctor-claude-coverage-slice1-plan.md` read; shipped proof shape wins over PRD assumptions
**Appetite:** ~4 agent tasks (4 acceptance criteria; within the PRD's small appetite)
**Kill-if:** Stop if reporting Claude requires changing `EngineReport`/`DoctorResult`, CLI exit codes, or adding a second proof path; those cross PRD §11 non-goals.

## Problem Statement

When a local operator runs doctor against a Claude profile, I want the result to report Claude beside Codex and OpenCode, so a failed Claude profile is diagnosed before dispatch without installing, repairing, or spawning anything.

---

## Concrete Example Output

A pinned profile whose Claude proof fails returns HTTP 200 with the existing five-field report shape, exactly three engine reports, and no plugin metadata:

```json
{
  "engines": [
    {"engine":"codex","profile":"/tmp/profile","status":"failed","reason":"engine binary not found on PATH (codex)","fix":""},
    {"engine":"opencode","profile":"/tmp/profile","status":"failed","reason":"opencode profile does not resolve @saketek/saki-builder","fix":""},
    {"engine":"claude","profile":"/tmp/profile","status":"failed","reason":"engine profile cannot resolve the saki-builder commands: claude profile does not resolve saki-builder","fix":""}
  ]
}
```

The existing CLI receives that body with `--json`, preserves the Claude report, and returns process exit code `1`; it does not add a new field or invoke an engine.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Add `domain.EngineClaude` to `usecase.DoctorEngines` after Codex and OpenCode, preserving the fixed report order and the existing `DoctorService.Check` / `engineInstallFix` behavior; do not add Claude remediation text. | `backend/usecase/doctor.go` | MED | Test-Along: `TestDoctorService_Check` and `TestDoctorService_Check_ClaudeFailure` in `backend/usecase/doctor_test.go` | Yes |
| 2 | Update `TestDoctorService_Check` and add table-driven assertions for exactly three reports, one report per engine, existing five report fields, Claude failed-proof propagation, profile threading, and exactly one binary/profile proof call per reported engine. | `backend/usecase/doctor_test.go` | MED | `go test ./infra -run 'TestDoctorService_Check' -count=1` from `backend/` | Yes |
| 3 | Extend the real HTTP doctor fixtures to assert `GET /api/doctor` returns exactly three reports with unique Codex/OpenCode/Claude engine values, preserves the existing `?profile` forwarding, keeps production `EngineProofChecker` wiring clean, and rejects non-loopback requests before any proof call. | `backend/adapter/doctor_http_test.go` | MED | Test-Along: `TestDoctorHandler_ReturnsEngines`, `TestDoctorHandler_RealWiring`, and `TestDoctorHandler_RejectsNonLoopbackHost` | Yes |
| 4 | Extend the CLI doctor regression fixture with a failed Claude `EngineReport`; preserve `src/commands/doctor.ts`, `src/types.ts`, and `src/exit.ts` unchanged while proving JSON output retains the report and `cmdDoctor` returns `EXIT.ERROR` (`1`). | `src/commands/doctor.test.ts` | LOW | `npm test -- src/commands/doctor.test.ts` | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Local operator / CI caller | Call existing `saki doctor` / `GET /api/doctor` and inspect three pre-dispatch engine reports | Install, repair, edit settings, or spawn any engine through doctor | Existing `OriginGuard` on `/api/doctor`; backend remains loopback-only | None — headless CLI/backend |

---

## Plan Wiring

### Flow 1: CLI doctor with Claude failure

```text
`saki doctor --json` (`src/commands/doctor.ts:18-43`)
  → `ctx.client.get<DoctorResult>('/api/doctor', profile)` (`src/commands/doctor.ts:23-24`)
  → GET `/api/doctor`
  → `OriginGuard` (`backend/adapter/originguard.go:48-55`)
  → `Handler.doctorHandler` (`backend/adapter/http.go:240-248`)
  → `DoctorService.Check(configDir)` (`backend/usecase/doctor.go:42-48`)
  → `DoctorEngines` (`backend/usecase/doctor.go:5-8`) = codex, opencode, claude
  → `EngineProofChecker.BinaryCheck/ProfileProof` (`backend/infra/doctor.go:10-16`)
  → `EngineProfileProof` (`backend/infra/spawner.go:197-212`)
  → `ClaudeProfileProof` (`backend/infra/claude.go:35-39`)
  → `{engine, profile, status, reason, fix}` report
  → `EXIT.ERROR == 1` when any report is not `ok` (`src/commands/doctor.ts:27-41`, `src/exit.ts:8-18`)
```

### Flow 2: Doctor read-only/origin protection

```text
GET `/api/doctor` (`backend/adapter/http.go:103`)
  → `OriginGuard` (`backend/adapter/originguard.go:48-55`)
  → reject non-loopback Host/Origin with HTTP 403 before `doctorHandler`
  → on loopback, `DoctorService.Check`
  → `BinaryCheck` + `ProfileProof` only
  → Claude proof reads selected profile files; no install, repair, write, or process spawn
```

### Flow 3: Production backend wiring

```text
`main()` (`backend/cmd/server/main.go:110-123`)
  → `usecase.NewDoctorService(infra.EngineProofChecker{})`
  → `adapter.NewHandler(..., doctorSvc, ...)`
  → `Handler.Routes()` (`backend/adapter/http.go:67-104`)
  → guarded GET `/api/doctor`
```

No database, journal, migration, frontend component, or engine process is part of this slice.

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `usecase.DoctorEngines` (`backend/usecase/doctor.go:5-8`) | fixed report set | `DoctorService.Check` (`backend/usecase/doctor.go:42-48`); `TestDoctorService_Check` (`backend/usecase/doctor_test.go`); HTTP and CLI consumers through `DoctorResult` | updated in steps 2–4 | Additive third report; existing five fields and order remain unchanged; tests assert exact three-engine contract |
| `GET /api/doctor` response engine count | API behavior | `backend/adapter/doctor_http_test.go`; `src/commands/doctor.ts:24-41`; `src/commands/doctor.test.ts` | updated in steps 3–4 | Tolerant existing renderer; CLI already evaluates every report and preserves `EXIT.ERROR == 1` |
| `EngineReport` / `DoctorResult` fields | API response shape | `backend/domain/doctor.go:15-26`; `src/types.ts:101-114`; `src/commands/doctor.ts` | unaffected — fields and types do not change | No selected plugin ID/version or profile contents are added |
| `EXIT.ERROR` | CLI contract | `src/commands/doctor.ts:36-41`; `src/exit.ts:8-18` | unaffected — value remains `1` | Add a failed-Claude fixture only; no command implementation or constant change |
| `/api/doctor` OriginGuard | security boundary | `backend/adapter/http.go:103`; `backend/adapter/originguard.go:48-55` | unaffected — guard remains in place | Add regression assertion that rejected requests do not reach proof calls |

**Forward compatibility:** additive-only report-set change; existing tolerant readers already handle arbitrary engine names and fixed report fields. No deploy-order constraint beyond deploying the backend and CLI versions that already share the existing response contract.

---

## Migration Checklist

No database schema, journal schema, or migration changes. Doctor reads existing local profile files only.

| Change | Table | Column/Index | Migration File | Command |
|--------|-------|--------------|----------------|---------|
| None | — | — | — | — |

---

## Branch Points (pre-declared)

- Step 1: If appending Claude changes any existing report field or order unexpectedly, keep the fixed order `codex`, `opencode`, `claude`; this is reversible and required by the additive contract.
- Step 2: If an existing two-engine assertion fails only because the report set is now three, update the assertion to the PRD's exact three-engine contract; do not weaken it to `>= 3`.
- Step 3: If a non-loopback request reaches `DoctorService`, stop and fix the existing `OriginGuard` wiring; do not add a second guard inside the doctor service.
- Step 4: If the CLI needs source changes to support Claude, stop and treat that as plan drift; the existing `EngineReport` renderer and exit rule are required to remain the compatibility path.
- Any path that installs, repairs, writes profile files, spawns an engine, exposes profile contents, changes exit-code numbers, or relaxes loopback/origin protection crosses a No-Go and is blocked.

---

## Unknowns (must be <= 2)

None. The report shape, route guard, CLI exit behavior, production wiring, and Slice 1 proof dispatcher are verified in the cited source anchors and prior-slice context.

---

## No-Gos

- Will NOT change `EngineReport`, `DoctorResult`, CLI flags, human formatting, or exit-code values.
- Will NOT add Claude installation, repair, settings mutation, remediation text, version migration, or process invocation.
- Will NOT create a parallel Claude proof or CLI-local readiness check.
- Will NOT expose plugin IDs, versions, full profile contents, credentials, or filesystem data beyond the existing profile label/reason fields.
- Will NOT alter loopback binding, `OriginGuard`, engine environment scrubbing, or run supervision.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Local operator/CI role is listed.
- [x] Full CLI → HTTP → usecase → infra proof call chain is in Plan Wiring.
- [x] Existing OriginGuard is cited; no new auth surface exists.
- [x] Missing/failed Claude, malformed response, non-loopback request, and side-effect paths are covered by tests.

**Database & Migrations**
- [x] No model or schema field changes.
- [x] No migration command applies.
- [x] No destructive schema operation exists.

**API Layer**
- [x] Existing `EngineReport` and `DoctorResult` types are named with exact paths.
- [x] Existing HTTP method/path and handler are cited; no new endpoint is added.
- [x] Existing `OriginGuard` dependency is listed.

**Service / Business Logic**
- [x] `DoctorEngines` and `DoctorService.Check` are named with exact paths.
- [x] Side effects are explicitly prohibited and tested through proof-call and filesystem/process assertions.
- [x] Failed Claude and binary short-circuit behavior are covered.

**Frontend**
- [x] No UI or frontend source change; only the existing CLI command test is extended.
- [x] Existing CLI output, failed, empty, and malformed-body behavior remains covered.

**Compatibility & Consumers**
- [x] Every changed existing surface has consumers, verdict, and mitigation.
- [x] Prior Slice 1 plan was read and its shipped proof shape is pinned.

**Plan Wiring**
- [x] Every major flow has an end-to-end call chain.
- [x] Every step names exact files and functions.
- [x] No migration, schema, or frontend component is implied without a named target.

---

## Evidence Ledger

### Blocking (must be empty to present — each row a binary, cited predicate)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|
| — | — | None | All anchors verified in `tasks/saki-doctor-claude-coverage-slice2-context.md`; prior Slice 1 proof is implemented and its plan is cited; no unknowns above LOW. |

### Advisory

| Step | Note | Evidence |
|------|------|----------|
| 4 | The CLI test is the contract-level exit assertion; no CLI source change is needed because `cmdDoctor` already evaluates arbitrary report names. | `src/commands/doctor.ts:27-41` |
| — | All anchors are verified, all targets have creating steps, no unchecked items remain on state-changing steps, and no unknowns exceed LOW. | self-audit |

**Blocking: 0 → READY.**

---

## Success Criteria

- [ ] Given the backend doctor service is called with Codex, OpenCode, and Claude in the fixed set, when `go test ./usecase -run 'TestDoctorService_Check' -count=1` runs from `backend/`, then it returns exactly three reports in order `codex`, `opencode`, `claude`, each with the existing five fields and exactly one proof pass per engine. → 5.2
- [ ] Given a Claude profile proof returns an `ErrEngineNotProvisioned`-equivalent error, when `go test ./infra -run 'TestDoctorService_Check_ClaudeFailure' -count=1` runs from `backend/`, then the Claude report has `engine:"claude"`, `status:"failed"`, the proof reason, and no new fix text, while Codex/OpenCode reports remain present. → 5.1
- [ ] Given a pinned profile and the real HTTP handler, when `go test ./adapter -run 'TestDoctorHandler_(ReturnsEngines|RealWiring|RejectsNonLoopbackHost)' -count=1` runs from `backend/`, then `/api/doctor` returns exactly one report for each engine with only `engine`, `profile`, `status`, `reason`, and `fix`; production wiring remains non-panicking; and a non-loopback request returns HTTP 403 without reaching proofs. → 5.2 / privacy
- [ ] Given a doctor JSON body containing a failed Claude report, when `npm test -- src/commands/doctor.test.ts` runs, then the CLI preserves the Claude report in JSON and returns `EXIT.ERROR` equal to `1`; no CLI source or exit constant changes are required. → 5.1
- [ ] Given the completed backend change, when `go test ./... -count=1 -timeout 60s`, `go vet ./...`, `go build ./...`, and `npm run test:coverage` run, then all tests/build/vet pass and coverage is strictly above the repository's 80% floor. → security / regression

**Manual verification:** None — all Slice 2 criteria are automatable; no browser or UI exists.

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---
Status: [x] Draft  [x] Annotated  [x] Approved  [x] In Progress  [x] Complete

QA evidence: focused doctor tests passed (491 Go tests, 8 CLI tests); full backend suite passed (701 tests); `go vet ./...` passed; `go build ./...` passed; full CLI suite passed with expected stub warnings; `gofmt` completed; `git diff --check` passed.
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
