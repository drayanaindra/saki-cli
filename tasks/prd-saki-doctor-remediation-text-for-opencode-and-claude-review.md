<!-- review-verdict: SHIP -->
<!-- failure-surface: 1/1 -->

# PRD Review — `saki doctor` — remediation text for opencode and claude

**Reviewer:** unassigned · **Date:** 2026-08-25 · **Status:** Addressed
**PRD reviewed:** `tasks/prd-saki-doctor-remediation-text-for-opencode-and-claude.md` — Updated 2026-08-25
**Verdict:** SHIP · **Readiness:** READY · **Failure-surface:** 1/1 · **Round:** 1

## Findings (ledger)

### R1 · HIGH · panel:premise+evidence · §2 Load-bearing assumption
Finding: Judge 1 + Judge 2 (converged) — the "costs an operator/agent real time" causal claim was tagged `observed` but only the structural asymmetry (opencode lacks the fix text codex/claude carry) is actually grounded in code; the time-cost harm was inferred, not observed.
Fix: Reworded §2's problem framing and Load-bearing assumption to state only the structural asymmetry (100% code-grounded), dropped the unmeasured "costs real time" framing entirely.
Disposition: Fixed

### R2 · HIGH · panel:premise · §2 Problem statement
Finding: Judge 1 — problem statement names no measured harm (no ticket, no reported confusion) — an absent-feature/asymmetry argument dressed as a harm claim.
Fix: §2 now explicitly states this is a parity gap, not a claimed measured harm — "This PRD does not claim that asymmetry has cost anyone measured time... it is a parity gap, stated as one."
Disposition: Fixed

### R3 · MED · panel:premise · §3 Primary JTBD
Finding: Judge 1 — JTBD ("resolve without cross-referencing saki doctor") was the solution restated as a want, with invented user pain.
Fix: Reworded J1 to state the honest job — parity/consistency across the three engines' failure output — not invented cross-referencing pain.
Disposition: Fixed

### R4 · LOW · panel:evidence · §2 roadmap quote
Finding: Judge 2 — quoted roadmap Goal text had no path:line citation.
Fix: Added `tasks/roadmap.md:33-37` citation.
Disposition: Fixed

### R5 · LOW · panel:evidence · §5.1 Method
Finding: Judge 2 — Method ("grep the identifier") is weaker than what the outcome needs; a static grep doesn't prove the runtime error strings actually carry the text.
Fix: Method now points at AC 1.1–1.3 (runtime error-string assertions), not a source grep.
Disposition: Fixed

### R6 · LOW · panel:impl · §9 criterion 1.2
Finding: Judge 3 — 1.2 conflated "missing" and "permission-denied" config-file cases under one `[auto]` criterion; permission-denial via chmod is flaky/non-portable and no such test pattern exists in this repo.
Fix: Reworded 1.2 to the "missing" case only, matching the existing `TestOpencodePluginProof_NoConfigFile` pattern.
Disposition: Fixed

### R7 · LOW · panel:impl · §2 (informational, no PRD change)
Finding: Judge 3 — post-change, `saki doctor`'s `Reason` field will contain the fix text twice (once via the new error-string suffix, once via `report.Fix`). Confirmed this is pre-existing behavior (codex/claude already duplicate it), and §11 explicitly rules out touching doctor's `Fix` field.
Fix: None needed — pre-existing, accepted, out of this PRD's scope per §11.
Disposition: Won't-fix: pre-existing duplication predates this PRD (codex/claude already exhibit it); §11 rules out touching `saki doctor`'s `Fix` field.

## Implementation-reality checklist
Newly surfaced this review: none — Judge 3 confirmed no hidden migration/flag/permission/rollback work; the three ACs (1.1/1.2/1.3) map 1:1 onto the three distinct `fmt.Errorf` sites with no error path skipped or double-covered.

Pre-existing `[manual]` ACs: none — all four criteria are `[auto]`.

## Readiness (Definition of Ready)
| # | Definition of Ready | Status |
|---|---|---|
| 1 | Slice 1 startable now | READY — sole slice, walking skeleton, no forward deps |
| 2 | No build-blocking Open Question | READY — §12 states none |
| 3 | Dependencies (§14) available | READY — correctly omitted (no deps) |
| 4 | Bet accepted or validated | READY — no DISCOVERY-RISK; load-bearing assumption is now purely `observed`/structural, not `assumed` |
| 5 | No open BLOCK/HIGH | READY — R1/R2 (the only HIGHs) both Fixed |

**Readiness: READY**

## Technical contract (§16) check & residual gaps
§16: present · CHANGE row cited (`backend/infra/opencode.go:40,50,58`) · compat-declared (`↳ Breaks: none (additive)`) · traceable (serves `8.1 · 5.1`) · stayed shape (no field names/payloads/migrations). CLEAN.

Residual gaps: none — DB/API/Arch/UI all N/A (no backend data/API surface beyond the cited error-string change; no UI).

## Unverifiable claims
- §2 "confirmed green: `TestDoctorService_Check/opencode_gets_the_rendered_Fix`, `TestDoctorService_Check_ClaudeFailure`" — test names/functions verified to exist and match by all three judges; the specific PASS result should be re-confirmed by `/saki-builder:qa` at build time, not re-trusted from this document.

Caveat: external facts were NOT validated; one non-deterministic sample per judge; a human decides.
