<!-- review-verdict: SHIP -->
<!-- failure-surface: 4/4 -->

# PRD Review — MCP surface (`saki mcp`)

**Reviewer:** unassigned · **Date:** 2026-08-16 · **Status:** Addressed
**PRD reviewed:** `tasks/prd-mcp-surface-saki-mcp.md` — blocking 0 · Updated 2026-08-16
**Verdict:** SHIP · **Readiness:** READY · **Failure-surface:** 4/4 · **Round:** 2

## Round 1 → Round 2 convergence

Round 1: 27 findings (R1–R27), including 3 BLOCK (5 untested tools, invariant-coverage inflated, a
mechanism-description defect where the CLI's real failure signal — thrown `CliError` — was described as
a plain return value). Fixed 24, deferred 2, resolved 1 via scope cut (17→13 tools).

Round 2 (fresh judge panel + pair review re-run against the round-1-fixed PRD): 2 BLOCK, 5 HIGH, 4 MED, 3
LOW. All BLOCK/HIGH resolved this round (fixed, or explicitly Won't-fix/Deferred with reasoning — see
ledger). **Round 2: 0 carried · 24 fixed (round 1) · 16 new (round 2), all closed.**

## Findings (ledger)

Round 1 IDs (R1–R27): see prior ledger below (§ "Round 1 detail"). All BLOCK/HIGH from round 1 were
Fixed or Deferred-with-trigger in round 1's fix pass; MED/LOW residuals (R18's `saki_runs` empty-state
remainder) are absorbed into R42 below.

### R28 · HIGH · panel:premise (round 2) · §3/§7
Finding: J1 justifies the exit-code-translation layer but not the decision to expose 13 separate
per-command tools instead of one generic wrapper — the schema-granularity choice was un-argued.
Fix: state why per-command typed schemas (not a generic `saki_exec`) serve J1.
Disposition: Fixed (round 2) — added to §7's opening.

### R29 · HIGH · panel:premise (round 2) · §6
Finding: medium appetite (a few days, 13 tools) seems large for a self-admitted "capability bet" with
zero measured harm.
Fix: either shrink appetite or justify keeping it.
Disposition: Won't-fix (round 2) — appetite is a fixed Shape-Up time-box by design, not scope-derived;
the bet's risk is mitigated by the §6 Kill Criterion 2 demand checkpoint (pause before Slice 2 without
real usage), not by a smaller appetite. Recorded here as the reasoned decision, not silently dropped.

### R30 · MED · panel:premise (round 2) · §6
Finding: the demand checkpoint's bar (one self-triggered invocation) doesn't actually test sustained
demand.
Fix: raise the bar.
Disposition: Fixed (round 2) — raised to ≥3 separate sessions.

### R31 · MED · panel:premise (round 2) · Bet line
Finding: "accepted... on an item the maintainer already placed on the roadmap" implies prior roadmap
placement is new evidence; it isn't — circular even though transparently labeled a bet.
Fix: reword to state plainly this is intent-only, no external validation.
Disposition: Fixed (round 2) — reworded.

### R32 · LOW · panel:premise (round 2) · §3
Finding: §11's backend-down limitation narrows J1's "without shelling out" to steady-state only, but §3
doesn't carry that caveat.
Disposition: Won't-fix (round 2) — already stated honestly and specifically in §11; repeating in §3
would be redundant, not load-bearing.

### R33 · HIGH · panel:evidence (round 2) · §7/§16
Finding: `fail()` cited at `src/exit.ts:29` — that line is the `CliError` constructor's `this.code =
code`; `fail()` is actually declared at line 35.
Fix: correct citation.
Disposition: Fixed (round 2) — verified against the file, corrected to `src/exit.ts:35`.

### R34 · HIGH · panel:evidence (round 2) · §7
Finding: `client.ts:127,159` misattributed — the real `UNREACHABLE` throws are at 125/157, and
`AUTH_REQUIRED` (cited as the same "transport failure") is actually a distinct HTTP 401/403 path at
183-185.
Fix: correct + separate the two failure classes.
Disposition: Fixed (round 2) — verified against the file (`grep -n` confirms 125/157/183/185), corrected
and reworded.

### R35 · HIGH · panel:evidence (round 2) · §10 — duplicate, see R37
Finding: "four of the four slices" claim in §10 is false; cited ACs span only 3 slices.
Disposition: Merged into R37 (Judge 3 raised the same defect at BLOCK severity — higher severity kept
per synthesis dedup rule).

### R36 · LOW · panel:evidence (round 2) · §7
Finding: `cmdDoctor` cited at `doctor.ts:16` — that's a comment line; the function starts at 18.
Fix: correct citation.
Disposition: Fixed (round 2) — verified, corrected to `doctor.ts:18`.

### R37 · BLOCK · panel:impl (round 2) · §10
Finding: the 🔒 invariant's "tested by... four of the four slices" claim is false against its own
citation list (1.2/1.5 → Slice 1, 2.2 → Slice 2, 3.2/3.3 → Slice 3; Slice 4 contributed zero) — the same
inflated-coverage defect class round 1 (R11) fixed, reintroduced by omission.
Fix: correct the claim or add a genuine Slice 4 failure-path AC.
Disposition: Fixed (round 2) — added AC 4.2 (Slice 4 failure path), "tested by" list now cites 6 criteria
genuinely spanning all 4 slices.

### R38 · BLOCK · panel:impl (round 2) · §9 Slice 4
Finding: the exact scenario this PRD's §2 problem statement is built around — a studio route reporting
failure inside an HTTP 200 body — is never tested for either `saki_branch_switch` or `saki_mr_create`,
the two tools §13 names as exhibiting it. The single case the whole PRD exists to prove was untested.
Fix: add a dedicated AC.
Disposition: Fixed (round 2) — AC 4.2 added, asserting `isError:true` + `REMOTE_FAILED` for the
HTTP-200-body-false-success case on `switch-branch`/`create-mr`, citing `src/commands/repo.ts:57`.

### R39 · HIGH · panel:impl (round 2) · §9 (old 4.2)
Finding: one AC bundled `saki_branch_list`, `saki_branch_switch`, and `saki_mr_create` — a failure
doesn't identify which tool broke, and it was the only test either switch or create got.
Fix: split for per-tool attribution.
Disposition: Fixed (partial, round 2) — `saki_mr_create` now has its own dedicated AC (4.4); the failure
path (4.2) is per-scenario, not per-tool, but names both tools explicitly; `saki_branch_list` +
`saki_branch_switch`'s happy path stays bundled in 4.3 (2 tools, down from 3) to fit the 5-AC cap.

### R40 · HIGH · panel:impl (round 2) · §8 Slice 3
Finding: concurrent-call and disconnect-mid-run behaviors are asserted as required but shipped with zero
test coverage, admitted in-text.
Fix: either free a slot for a real AC, or explicitly move both to §12 as a scoped-out risk with a
trigger (judge's own sanctioned alternative).
Disposition: Fixed (round 2) — moved to §12 as an explicit "shipped untested" risk with a stated trigger
(first real report from the maintainer's own dogfooding), rather than left ambiguously inside §8.

### R41 · MED · panel:impl (round 2) · §8/§11
Finding: 13 kept + 4 deferred = 17, but `saki_artifacts` is a 5th excluded tool with no accounting —
math doesn't add up as stated.
Fix: clarify the candidate-pool arithmetic.
Disposition: Fixed (round 2) — §8 now states the pool was 18 total commands, `saki_artifacts` excluded
up front (blocked on I2, not part of this cut) leaving 17 real candidates.

### R42 · MED · panel:impl (round 2, absorbs round-1 R18 remainder) · §9
Finding: `saki_runs`/`saki_branch_list` empty-state, and `saki_run_stop`/`saki_prd_lock` failure paths,
remain untested (5-AC-per-slice cap).
Disposition: Deferred (round 2) — added to §12 as an explicit residual-gap note with a trigger (surfaced
by `/saki-builder:rplan` or `/saki-builder:qa`), rather than silently dropped.

### R43 · LOW · panel:impl (round 2) · §16
Finding: §16's "Serves" column used "8.1"/"8.2" notation that §8 never defines (§8 only says "Slice 1"…"Slice 4").
Fix: align notation.
Disposition: Fixed (round 2) — §16 now cites "Slice 1"–"Slice 4" matching §8's actual headings.

### R44 · LOW · panel:impl (round 2) · §8
Finding: Slice 4 bundles two domains (git ops + PRD lock) to hit a 5-tool count.
Disposition: Won't-fix (round 2) — self-acknowledged in the slice title already; minor INVEST/cohesion
smell, not a functional gap.

## Implementation-reality checklist

All `[auto]`, no `[manual]` criteria in this PRD. Newly-surfaced this review (both rounds, now in the
PRD):
- ☐ R10/R11/R37/R38 — 5 untested tools, inflated invariant coverage, and the untested HTTP-200-body
  case → scope cut to 13 tools (4 deferred), `saki_branch` given a dedicated AC, and AC 4.2 added
  specifically for the HTTP-200-body scenario.
- ☐ R12 — Slice-2-native ACs for `saki_prd_show`/`saki_runs` → 2.3, 2.4.
- ☐ R13 — invalid-verb path for `saki_run_start` → 3.2.
- ☐ R20 — stdio-purity invariant → 1.4.
- ☐ R22 — `saki mcp`'s own process-exit path → 1.5.
- ☐ R24 — exit-code-vs-thrown-`CliError` mechanism gap → §7/§16 rewritten with correct, verified citations.
- ☐ R14/R23/R40 — run_tail concurrency + disconnect-mid-run → explicit scoped-out risk in §12 with a trigger.
- ☐ R42 (absorbs R18) — `saki_runs`/`saki_branch_list` empty-state, `saki_run_stop`/`saki_prd_lock`
  failure paths → explicit residual-gap note in §12 with a trigger.

## Readiness (Definition of Ready)

| # | Definition of Ready | Status |
|---|---------------------|--------|
| 1 | Slice 1 startable now | ✅ walking skeleton, forward-deps only, inputs exist |
| 2 | No build-blocking Open Question | ✅ §12's items are either explicitly non-blocking or scoped-out-with-trigger, none gate Slice 1 |
| 3 | Dependencies (§14) available | ✅ `@modelcontextprotocol/sdk` is a real, published package |
| 4 | Bet accepted or validated | ✅ explicitly accepted (header **Bet:** line, reworded round 2 to remove circularity), re-surfaced at the `/saki-builder:proto` human gate |
| 5 | No open BLOCK/HIGH | ✅ round 2: all BLOCK/HIGH fixed or dispositioned Won't-fix/Deferred with stated reasoning |

**Readiness: READY.**

## Technical contract (§16) check & residual gaps

§16: present · every row REUSE (`path:line`, all verified/corrected round 2) or NEW · no CHANGE rows ·
every row serves a real §8 slice / §5 outcome, using consistent "Slice N" notation (fixed round 2) ·
stayed at shape altitude. CLEAN.

Residual gaps (flagged, not designed — unchanged from round 1):
| Layer | Undefined load-bearing surface | Handoff |
|-------|--------------------------------|---------|
| API/integration | Exact per-tool JSON-schema (parameter types) for each of the 13 tools | → `/saki-builder:rplan` |
| Architecture | `exitCodeToToolResult` helper's exact signature/module location | → `/saki-builder:rplan` |

## Unverifiable claims

- §2 "~47k → ~400 tokens after moving off upfront MCP tool-loading, mcp-cli" — needs grounding.
- §2 "mcp2cli generates a full CLI from an MCP server's tool catalog" — needs grounding.
- §2 sources "oneuptime.com/blog" and "jannikreinhard.com" — needs grounding.
- §12 "MCP clients such as Claude Code already tolerate long-running tool calls" — self-flagged
  unverified in-text, routed to `/saki-builder:rplan`.

## Round 1 detail

R1–R27 (structural/premise/evidence/implementation findings from the initial 17-tool draft, and the pair
review's stdio-purity, `--open` side-effect, and exit-code-mechanism findings) — all Fixed or Deferred in
the round-1 fix pass that produced the 13-tool, 4-slice PRD reviewed above. Full text preserved in this
file's git history / the round-1 write (superseded by this consolidated version).
