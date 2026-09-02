<!-- review-verdict: SHIP -->
<!-- failure-surface: 4/4 -->

# PRD Review — Hands-off build workflow — pickup, multi-turn completion, and continuation

**Reviewer:** unassigned · **Date:** 2026-09-02 · **Status:** Addressed
**PRD reviewed:** `tasks/prd-hands-off-build-workflow-pickup-multi-turn-completion-and-continuation.md`
**Verdict:** SHIP · **Readiness:** READY · **Failure-surface:** 4/4 · **Round:** 1

## Findings (ledger)

### R1 · BLOCK · panel:impl · completion contract

Finding: The original F7 goal says “verified as achieved”, but a child sentinel or checkbox count is
not enough to tell an implementation what makes `done` true.

Fix: §7.4 and AC 5.1–5.2 define a typed completion evidence object: locked/track-correct artifact,
all slice/plan gates, existing commits, explicit QA/reviewer/wrap results, and push result. The
sentinel is explicitly non-sufficient.

Disposition: Fixed.

### R2 · HIGH · panel:architecture · identity and dedupe

Finding: Sequential child runs cannot safely reuse a single `Run` id, while deduping only the current
live child loses identity during backoff, phase transitions, and restart.

Fix: §7.1 makes `Workflow` a separate Go-owned aggregate with a stable canonical lane and child-run
history. §7.2/§10 require atomic reservation and journal-before-spawn ordering; AC 1.2, 4.2, and 4.3
cover concurrent, waiting, and restart cases.

Disposition: Fixed.

### R3 · HIGH · panel:contract · absent PRD and Plan parity

Finding: The current CLI resolves `build` through an existing Child PRD, so merely adding a workflow
backend would never reach pickup for the exact F7 command. The roadmap also requires Plan-track parity.

Fix: §7.2 sends the raw target to the workflow endpoint; §7.3 defines separate PRD and Plan phase
policies; Slices 2–3 and AC 2.1/3.1 prove both paths. The direct step verbs remain available.

Disposition: Fixed.

### R4 · MED · panel:recovery · awaiting and parked states

Finding: Existing build classification can detect `NEEDS_DECISION`, but the current HTTP surface has
no typed answer/continue path. “Actionable failure” would otherwise mean only a log message.

Fix: §7.2 adds `POST /api/workflow/{id}/continue`, with durable option validation and same-workflow
resume; §9 Slice 5 and AC 5.3–5.5 define parked, awaiting, running, terminal, and stop behavior.

Disposition: Fixed.

## Verification notes

- The current build child path and follow semantics were checked against `src/commands/run.ts:121-231`
  and `src/commands/runs.ts:54-73`.
- The current Go build dispatch, pending-resume/restart behavior, and sentinel classifier were checked
  against `backend/adapter/http.go:471-521`, `backend/usecase/buildengine.go:410-567`, and
  `backend/usecase/buildclass.go:213-243`.
- Loopback, journal ownership, environment scrubbing, and exit-code constraints are carried into
  §10/§13; the new route and real-engine proof are explicit Slice 6 work.

## Readiness decision

The blocking set is empty. Slice 1 has a concrete start seam (Go domain aggregate + existing journal
ports), both tracks have ordered phase contracts, and the success boundary is testable without
pretending a model sentinel is repository proof. The remaining implementation choices are the two
non-blocking transport/artifact details named in §12.

Status: [x] Draft  [x] Annotated  [x] Approved  [x] In Progress  [x] Complete
Readiness Gate: [x] Evidence ledger present and every blocking item cited  [x] Blocking set empty  [x] Unknowns <= 2
