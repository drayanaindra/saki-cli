<!-- review-verdict: SHIP -->
<!-- failure-surface: 2/2 -->

# PRD Review — `saki doctor` — Claude coverage

**Reviewer:** unassigned · **Date:** 2026-08-20 · **Status:** Addressed
**PRD reviewed:** `tasks/prd-saki-doctor-claude-coverage.md` — blocking 0 · Updated 2026-08-20
**Verdict:** SHIP · **Readiness:** READY · **Failure-surface:** 2/2 · **Round:** 1

## Findings (ledger)

No blocking findings remain.

### Resolved review findings

- **R1 · MED · metrics · §5.2** — The measurement was classified as `event` while naming no emitted event; changed to `query: doctor response contains one Claude report before any run spawn`.
- **R2 · HIGH · implementation · §2 / §12 / §16** — Claude's two-file shape and identity join were underspecified; the PRD now states the registry/settings proof contract, precedence, fail-closed ambiguity behavior, and hands exact file representation to `/saki-builder:rplan`.

## Implementation-reality checklist

### Newly surfaced this review

- No additional failure criterion is required beyond the existing Slice 1 missing/malformed/absent/disabled/mismatched cases and deterministic dual-ID criterion; those cover every failure path stated by Slice 1.
- No hidden migration, database, or rollback work is implied: the PRD explicitly states local profile-file reads only and no schema migration.
- `/saki-builder:rplan` must ground exact Claude file locations and JSON nesting with real fixtures; unknown shapes fail closed under the stated decision boundary.

### Pre-existing `[manual]` ACs

None. All criteria are `[auto]`.

## Readiness (Definition of Ready)

| # | Definition of Ready | Result |
|---|---|---|
| 1 | Slice 1 startable now | ✅ The proof behavior and fixture cases are defined; exact representation is a planning handoff and unknown shapes fail closed. |
| 2 | No build-blocking Open Question | ✅ The file-shape spike is named; the PRD has a fail-closed decision boundary and does not require external provisioning. |
| 3 | Dependencies available | ✅ F2 is shipped; F6 is an intentional downstream consumer, not a prerequisite. |
| 4 | Bet accepted or validated | ✅ The assumed shape is explicitly accepted as a compatibility bet with a fail-closed kill criterion. |
| 5 | No open BLOCK/HIGH | ✅ R2 addressed in the PRD; no open BLOCK/HIGH remains. |

**Readiness: READY**

## Technical contract (§16) check & residual gaps

- **§16:** present · rows cited or NEW · compatibility declared · slice/outcome references resolve · shape stays above schema detail.
- **DB/data:** no residual surface; the PRD states no schema migration.
- **API:** `/api/doctor` is an additive report change; exact response implementation remains with the existing `EngineReport` contract and `/saki-builder:rplan`.
- **Architecture:** shared `EngineProfileProof` + `DoctorEngines` path is named; exact JSON parser representation is a planning detail.
- **UI/UX:** none; this is a backend/CLI change.

## Unverifiable claims

- The external Claude profile files' exact JSON shapes and locations are not validated by this repository; `/saki-builder:rplan` must use real fixtures and retain fail-closed behavior.

## Recommendation

Proceed to `/saki-builder:proto` for the explicit no-UI lock. Do not run `/saki-builder:proto` until the human asks. After lock, run `/saki-builder:build` for F4; F6 Claude provisioning remains downstream.
