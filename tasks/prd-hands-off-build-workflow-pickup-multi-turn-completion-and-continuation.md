<!-- prd-blocking: 0 -->
<!-- slices: 6 -->
<!-- appetite: medium -->
<!-- prd-locked: @saki · 2026-09-02 · ui:tasks/proto-hands-off-build-workflow-pickup-multi-turn-completion-and-continuation/ -->

# PRD: Hands-off build workflow — pickup, multi-turn completion, and continuation

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-09-02 · **Appetite:** medium — days · **Item:** F7

## 1. TL;DR

Make `saki build <roadmap-id> --follow` one durable workflow, not a request to run one
`/saki-builder:build` child. The backend owns the workflow state and advances the correct track:

- PRD track: resolve the item → create/review the PRD when absent → render and lock the proto → build
  every slice, including multi-turn continuation.
- Plan track: write/review the plan → approve/implement → QA → reviewer → wrap.

The CLI starts or re-adopts a workflow and follows the workflow stream. A child run finishing is an
internal transition, not workflow success. Success is durable only after a verifier sees the required
artifacts, commits, lock/evidence, and final gate state. Parked and awaiting-decision states are
durable failures until an explicit `continue` supplies the requested action.

This PRD extends the existing Go run/journal substrate. It does not move `/saki-builder:*` skills
into this repository; the separate `saki-builder` package remains the source of workflow prompts.

## 2. Problem & evidence

F7 is an observed contract gap, not a claim about measured operator time. The roadmap explicitly
promises a single resumable command, but the present seams only supervise individual child runs.

| Claim | Tag | Source |
|---|---|---|
| F7 requires pickup/proto-lock/build, Plan-track parity, whole-workflow follow, durable dedupe, verified completion, and explicit continuation | observed | `tasks/roadmap.md:69-74` |
| The CLI currently resolves `build` to an existing PRD and fails before spawn when the item has no PRD | observed | `src/commands/run.ts:121-134`; `src/commands/prd.ts:16-43` |
| `--follow` currently tails one returned run id and exits 0 when that run says `done` | observed | `src/commands/run.ts:213-231`; `src/commands/runs.ts:54-73` |
| The Go adapter dispatches a build POST to `BuildEngineService.SpawnBuild`, while pickup/proto/rplan are plain independent runs | observed | `backend/adapter/http.go:471-521`; `backend/usecase/buildengine.go:80-124` |
| The build engine already has useful durable turn state, backoff, pending successors, restart rehydration, and lane reservation | observed | `backend/domain/run.go:67-112`; `backend/usecase/buildengine.go:410-567`; `backend/usecase/run_ports.go:37-61` |
| A build's terminal decision and auto-push currently use a line-anchored spoken sentinel; no workflow-level evidence gate exists | observed | `backend/usecase/buildclass.go:213-243,327-332`; `backend/usecase/buildengine.go:235-298` |
| A parsed `NEEDS_DECISION` can be classified as awaiting, but no continue/answer endpoint is exposed | observed | `backend/usecase/buildclass.go:172-210,223-243`; `backend/adapter/http.go:77-81` |
| The current work-item verifier can prove some plan artifacts and approved commits, but QA/reviewer completion is not itself an artifact-backed workflow terminal state | observed | `backend/domain/workitems.go:89-135`; `backend/usecase/workitems.go:50-87` |

### Failure this closes

Today, a build can be green at the child-run level while its next successor is scheduled, while the
PRD still needs proto/lock, or while a later workflow phase is absent. Conversely, a parked or
awaiting build has only a breadcrumb and no typed command to resume it. A caller cannot safely use
`saki build <id> --follow && deploy` as the success boundary.

## 3. Primary job to be done

**J1 — Hands-off delivery:** As an operator or coding agent, when I run
`saki build <roadmap-id> --follow`, I want one command to drive the item through its required track,
survive child turns and backend restarts, and exit 0 only when the repository has durable evidence of
verified completion, so shell chaining cannot advance on a pending or false-green workflow.

## 4. Related jobs

- **J2 — Safe retry:** Repeating the same command while the workflow is running, waiting, or
  recovering should re-adopt it rather than spawn a second workflow or child on the same lane.
- **J3 — Actionable recovery:** When a workflow parks or asks for a decision, the operator needs a
  typed continuation path that preserves the workflow and records the answer.

## 5. Desired outcomes and metrics

| # | Outcome | Target | Method | JTBD |
|---|---|---|---|---|
| 5.1 | A PRD-track `saki build <id> --follow` drives all required phases from an absent PRD through verified build completion | 100% of deterministic fixture journeys | workflow integration test with pickup/proto/build child doubles and durable artifacts | J1 |
| 5.2 | A Plan-track item receives equivalent end-to-end treatment | 100% of deterministic plan fixtures | integration test covering rplan → review → approved → qa → reviewer → wrap | J1 |
| 5.3 | A repeat request during a child run, backoff, usage-limit wait, or parked/awaiting state creates no second workflow or live child | exactly one workflow identity and at most one live child per lane | concurrent POST + restart tests inspect journal/store/spawn count | J2 |
| 5.4 | `--follow` returns 0 only for a durable verified-success workflow | zero false-green cases in the completion decision table | CLI/API tests for pending successor, child done, parked, awaiting, and verified terminal states | J1 |
| 5.5 | Every non-success terminal state is restart-visible and actionable | reason, current phase, workflow id, and continuation instruction survive restart | journal rehydrate and JSON contract tests | J3 |

**Counter-metric:** workflow orchestration must not bypass existing engine/profile preflight,
loopback guards, exit codes, engine environment scrubbing, or exactly-one-live process behavior.

## 6. Appetite and kill criteria

**Appetite:** medium — approximately six implementation slices. The existing run engine, content
services, and CLI stream are reused; this is a workflow coordinator and contract expansion, not a
rewrite of the engines or a new UI.

**Kill criteria:** stop and re-scope if either is true:

1. The required phases cannot be made restart-safe without making the workflow journal share ownership
   with the existing flat studio journal, violating Inv-1, or allowing two live children, violating
   Inv-2 / the exactly-one-live rule.
2. Completion cannot be expressed as checks against durable repository artifacts and gate results. A
   spoken `PRD_BUILD_COMPLETE` alone is not an acceptable F7 success signal.

## 7. Solution shape

### 7.1 Workflow aggregate

Add a backend-owned `Workflow` aggregate separate from `domain.Run`. A workflow owns the stable lane,
track, target, current phase, child run id, phase history, continuation state, and terminal result.
Child agent invocations remain ordinary journaled `Run` records and keep their engine/config directory.

The minimum durable state is:

```text
workflowId, laneKey, cwd, target, track, phase
childRunId, phaseHistory[], status
pendingUntil, retry/resume counters
awaitingDecision?, parkedReason?, completionEvidence?, idempotencyKey
```

`laneKey` is canonical and stable for the original repo + roadmap item (or a contained explicit path),
so creating a PRD does not change the dedupe identity. A workflow journal lives under the Go-owned
runs directory and is rehydrated before the server accepts requests. Child runs continue to use the
existing run journal and `BuildEngineService` process safeguards.

### 7.2 Workflow API and CLI contract

Add a loopback- and origin-guarded workflow start route and a continuation route:

- `POST /api/workflow` accepts `{cwd, target, engine?, configDir?, idempotencyKey?}` and returns
  `{workflowId, childRunId?, deduped}`. It resolves the roadmap item and track in the backend; it
  must not require an already-existing PRD for a PRD-track item.
- `GET /events/{workflowId}` streams workflow transitions and child output, then emits an end frame
  only when the workflow is terminal. A child `done` with a next phase or pending successor is not an
  end frame for the workflow.
- `POST /api/workflow/{workflowId}/continue` accepts an optional decision option. It is valid only
  for a parked or awaiting workflow, validates an awaiting option against the durable decision, clears
  the gate atomically, records the operator action, and starts the next child. A running workflow is
  re-adopted; a verified terminal workflow is not restarted.

`saki build <id> --follow` uses this workflow start contract. The build alias and nested `run build`
must not diverge: both use the workflow contract and whole-workflow follow. The existing non-build
step verbs remain available for deliberate step-by-step operation. `saki run continue <workflowId>
[--option <value>]` is the explicit recovery command, with JSON containing the workflow id, phase,
status, and reason.

The existing exit contract remains frozen: only a verified workflow terminal state maps to 0;
parked, awaiting, child error, failed verification, and dropped stream map to non-zero. Errors stay
on stderr and machine results on stdout.

### 7.3 Phase policy

The coordinator is the single owner of phase transitions. It invokes the existing skill names and
does not duplicate their content.

| Track | Ordered phases | Resume rule |
|---|---|---|
| PRD | resolve → pickup if PRD absent → proto → lock → build slices/turns → verify | existing PRD skips pickup; locked PRD skips proto/lock only after the required artifacts are verified |
| Plan | resolve → rplan → rplan-review → approved → qa → reviewer → wrap → verify | completed phases are skipped only when their durable artifact/verdict checks pass |

Every transition is journaled before the next child is spawned. A child outcome is classified using
the existing engine rules, then either advances the phase, schedules a durable retry/successor, parks,
awaits a decision, or records terminal failure. A restart rehydrates the workflow first, re-adopts a
live child, fires due work once, and never spawns beside a live process.

### 7.4 Verified completion evidence

The verifier must return a typed evidence object and persist it with the workflow before publishing
`status:done`. The exact artifact filenames remain an implementation detail of the existing skills,
but the checks are fixed here:

- the target resolves inside `cwd` and the expected track is known;
- PRD track has a present, locked PRD and a build manifest whose every slice is done;
- Plan track has a present plan with all required plan checkboxes/gates complete;
- every recorded implementation commit exists in the target repository;
- QA, reviewer, and wrap results are explicitly successful in durable workflow/manifest state;
- the final child output may carry `PRD_BUILD_COMPLETE`, but that sentinel is only an input to the
  verifier and can never satisfy the workflow by itself;
- if auto-push is enabled, push result is recorded; a failed push is actionable and cannot be hidden by
  a prior child `done` status.

Evidence includes the checked paths, commit ids, phase verdicts, and verification timestamp. A
missing, malformed, stale, or contradictory fact yields durable failure with the first failed check.

### Alternatives considered / decision

- **Chosen: a durable workflow aggregate around child runs.** It gives phase transitions and waiting
  states one identity while preserving the proven child process/journal substrate.
- **Rejected: teach the CLI to run `pickup`, `proto`, `build`, and plan steps in a shell loop.** The
  CLI cannot own restart state, would lose the workflow during a sleeping successor, and would make
  direct callers and the backend disagree.
- **Rejected: treat a child `done` or `PRD_BUILD_COMPLETE` as workflow success.** It is exactly the
  false-green boundary F7 exists to remove.
- **Rejected: put workflow metadata only on `domain.Run`.** One workflow owns multiple sequential child
  runs; overloading one run record makes phase history and parked waits ambiguous and complicates
  rehydrate/dedupe.

## 8. Vertical slices

### Slice 1 — Durable workflow identity and state machine

Add the domain aggregate, phase/status vocabulary, lane key, journal record, rehydrate policy, and
pure transition decisions. Prove malformed/old records fail closed and the workflow journal stays in
the Go-owned directory. Serves J1/J2.

### Slice 2 — PRD-track orchestration

Add backend resolution of a roadmap id, pickup-on-missing-PRD, proto/lock sequencing, phase-child
adoption, and idempotent skip checks for already-complete artifacts. Serves J1.

### Slice 3 — Plan-track orchestration

Add the equivalent Plan-track phase sequence and durable artifact/verdict checks. A Plan-track item
must not be sent through PRD pickup or proto. Serves J1.

### Slice 4 — Whole-workflow follow, retry dedupe, and restart survival

Add workflow start/re-adoption, workflow SSE end semantics, durable pending waits, concurrent lane
dedupe, and boot recovery around the existing child build engine. Serves J1/J2.

### Slice 5 — Completion evidence and explicit continuation

Add the typed verifier, durable success/failure evidence, parked and awaiting decision state, option
validation, and `continue` behavior. Stop must cancel workflow continuation and never leave a successor
armed. Serves J1/J3.

### Slice 6 — CLI contract, docs, and real-engine journey coverage

Route `saki build <id> --follow` and `saki run build <id> --follow` through the workflow contract,
add `saki run continue`, document the JSON/exit behavior, and cover the full journey with backend,
CLI, and real-engine e2e tests. Serves J1/J2/J3.

## 9. Acceptance criteria per slice

### Slice 1

- **1.1 [auto]** Given a new workflow request, the backend persists a workflow identity and phase
  before spawning its first child; a restart can load the same identity.
- **1.2 [auto]** Given two concurrent requests for the same canonical repo/item lane, exactly one
  workflow and one live child exist; both responses identify the same workflow.
- **1.3 [auto]** Given a malformed, missing, or foreign workflow journal, rehydrate does not create a
  false-success workflow and does not spawn a child beside an unknown live process.

### Slice 2

- **2.1 [auto]** Given a Planned PRD-track item with no Child PRD, a workflow runs pickup, waits for
  the reviewed PRD, then runs proto/lock before build; a child `done` between phases does not end the
  workflow.
- **2.2 [auto]** Given an existing unlocked PRD, pickup is skipped and proto/lock runs exactly once;
  given a locked PRD with valid artifacts, both preparation phases are skipped.
- **2.3 [auto]** Given pickup, proto, or lock fails or parks, the workflow records the phase and an
  actionable reason and does not spawn a later build child.

### Slice 3

- **3.1 [auto]** Given a Planned Plan-track item, the workflow runs rplan → rplan-review → approved
  → qa → reviewer → wrap in order, with each transition durable before the next child.
- **3.2 [auto]** A Plan-track workflow never invokes pickup, proto, PRD lock, or the PRD build lane.
- **3.3 [auto]** On restart, a completed plan phase is skipped only when its expected artifact and
  verdict/commit verify; otherwise the workflow resumes at the earliest unverified phase.

### Slice 4

- **4.1 [auto]** `saki build <id> --follow` remains connected while a child schedules a successor,
  waits for a usage reset, or advances to another phase; it exits only on workflow terminal state.
- **4.2 [auto]** Repeating the same request during a child run, pending backoff, usage-limit wait, or
  after backend restart re-adopts the existing workflow and never creates a second live child.
- **4.3 [auto]** A restart with a live child re-attaches to it; a restart with a due pending successor
  fires it once; concurrent sweep/timer/manual re-adoption results in at most one successor.
- **4.4 [auto]** Existing engine/profile preflight, engine choice, environment scrubbing, stop race,
  stall watchdog, and Go journal ownership remain intact for every child phase.

### Slice 5

- **5.1 [auto]** A child ending with `PRD_BUILD_COMPLETE` but missing required manifest, commit, lock,
  QA, reviewer, wrap, or push evidence produces durable failure, not workflow `done`.
- **5.2 [auto]** A workflow reaches `done` only after the typed completion evidence verifies all
  track-specific checks; the persisted evidence can be read after restart.
- **5.3 [auto]** A parsed `NEEDS_DECISION` parks in `awaiting-decision` with durable question/options;
  `continue` accepts only a valid option, records it, and resumes the same workflow.
- **5.4 [auto]** A parked workflow can be explicitly continued from its recorded phase; a running or
  verified workflow cannot be accidentally duplicated or restarted by `continue`.
- **5.5 [auto]** Stop cancels a running child and all pending workflow continuation, and a later sweep
  cannot spawn a successor.

### Slice 6

- **6.1 [auto]** The CLI sends the roadmap id/path to the workflow start route without first requiring
  a PRD, validates arguments before network calls, and preserves loopback-only routing.
- **6.2 [auto]** Human and JSON output identify workflow id, current phase, terminal reason/evidence,
  and continuation guidance; stdout/stderr and exit-code semantics remain compatible.
- **6.3 [auto]** The two build spellings (`saki build` and `saki run build`) have identical workflow
  and follow behavior; direct step verbs remain available.
- **6.4 [e2e]** Against real Claude, Codex, and OpenCode binaries where available, a fixture journey
  proves the actual command invocation and follows through at least one multi-turn continuation;
  fake binaries may cover argv/error plumbing only, never engine invocation proof.
- **6.5 [auto]** `docs/cli-reference.md`, the agent guide, and relevant tests describe the new
  workflow/continue routes; no shipped route is undocumented.

## 10. Business rules and invariants

1. **One workflow lane, one live child.** All starts, retries, timers, sweeps, and continuation calls
   pass through one atomic reservation. A workflow may have many historical children but never two
   live children for its lane.
2. **Success is workflow-level.** Child `done`, exit code 0, and spoken sentinels are insufficient
   without the completion verifier.
3. **Durability precedes action.** Persist a phase transition or pending successor before spawning;
   persist a terminal evidence record before exposing `done`.
4. **Explicit human action.** Parked and awaiting states do not auto-resume. Only `continue` can
   clear them, and awaiting options are validated against the durable question.
5. **Stop wins races.** Stop clears pending work under the same lock used by timer/sweep firing and
   signals the entire child process group.
6. **Security remains unchanged.** New workflow and continuation routes are loopback-only and
   origin-guarded. Targets resolve inside the requested repository; no user-controlled target becomes
   an unvalidated filesystem path or shell argument.
7. **Engine identity is inherited.** Every child and successor uses the workflow's recorded engine and
   profile; a restart never silently changes runtime or leaks another engine's environment namespace.
8. **Existing step commands remain usable.** F7 adds the hands-off path; it does not remove deliberate
   `pickup`, `proto`, `rplan`, `qa`, reviewer, or wrap runs.

## 11. Non-goals

- ✗ Adding workflow skills, agents, hooks, or prompt content to this repository.
- ✗ Building a browser UI or hosted multi-tenant workflow service.
- ✗ Changing the existing retry/backoff/stall defaults except where required to attach them to the
  workflow state.
- ✗ Treating a git push, merge request, or remote deployment as automatic proof of product semantics.
- ✗ Replacing the existing direct run API or removing manual step-by-step commands.
- ✗ Making `continue` an unrestricted shell or arbitrary prompt execution endpoint.

## 12. Rabbit holes and open questions

- Rabbit hole: reimplementing each skill's PRD, plan, QA, or review logic in Go. The coordinator only
  schedules named skills and verifies their durable outputs.
- Rabbit hole: using one giant workflow output transcript as the state machine. State transitions and
  evidence must be typed/journaled; output remains replayable context.
- Open question (non-blocking): whether workflow event lines should use a new event envelope or reuse
  the existing `StreamEvent` line shape. The rplan chooses the smallest backward-compatible envelope;
  it cannot change the terminal/end semantics.
- Open question (non-blocking): whether an existing locked PRD with no proto preview should rerun proto
  or fail with a repair instruction. The verifier and current proto artifact conventions decide this
  before Slice 2 implementation; either result must be explicit and non-zero, never a silent skip.

## 13. Technical constraints

- Go domain code remains free of HTTP, filesystem, process, and infra imports; usecase ports own those
  effects.
- Go journals remain under their own subdirectory (`docs/project-context.md:31-32`), and restart
  rehydrate is part of every state-changing slice.
- The CLI remains a thin HTTP/SSE client. It may normalize and validate syntax, but backend workflow
  state is authoritative.
- Tests must distinguish pure transition/verifier tests, adapter contract tests, CLI contract tests,
  and real-engine Playwright coverage.
- New endpoints must be mounted in both the backend router and the CLI route map, origin guarded, and
  documented in `docs/cli-reference.md`.

## 14. Dependency and rollout notes

Slice 1 can start against the current Go run/journal/store ports. Slices 2–3 depend on the existing
roadmap/content/lock/plan-track services but do not require a UI. Slice 4 depends on the current build
engine's pending-resume and rehydrate behavior. Slice 5 fixes the success boundary before Slice 6
advertises the command as hands-off. Existing direct run verbs remain the fallback during rollout.

## 15. Evidence ledger

All current-state claims in §2 are direct source observations. The following are deliberate design
contracts to verify in `/saki-builder:rplan`, not claims that the current code already implements:

| Contract | First implementation proof |
|---|---|
| workflow journal and aggregate | Slice 1 domain/usecase/infra tests + restart fixture |
| PRD and Plan phase policy | Slices 2–3 coordinator tests with stateful child-run doubles |
| whole-workflow stream/end behavior | Slice 4 adapter + CLI SSE tests |
| completion evidence | Slice 5 verifier decision table and malformed/stale artifact tests |
| real engine invocation | Slice 6 Playwright tests using real installed binaries |

No external measurement is required to start: F7 is a contract correction grounded in the roadmap and
the existing single-child implementation.

## 16. Technical contract (thin)

**Entities:** `Workflow` (durable aggregate), `WorkflowPhase`, `WorkflowStatus`, `AwaitingDecision`,
`CompletionEvidence`; existing `Run` remains the child execution record.

**Endpoints:**

| Endpoint | Change | Owner | Notes |
|---|---|---|---|
| `POST /api/workflow` | NEW | Go adapter/usecase | start/re-adopt by canonical lane/idempotency key |
| `GET /events/{workflowId}` | EXTEND | Go stream adapter + CLI SSE | workflow transitions; end only at verified terminal |
| `POST /api/workflow/{workflowId}/continue` | NEW | Go adapter/usecase | explicit parked/decision continuation |
| `POST /api/run` | REUSE | existing child spawn | coordinator uses the same child spawn contract internally; direct callers remain |

**CLI commands:**

| Command | Change | Result |
|---|---|---|
| `saki build <id> --follow` | CHANGE | starts and follows a workflow; 0 only for verified success |
| `saki run build <id> --follow` | CHANGE | exact same workflow behavior as the alias |
| `saki run continue <workflowId> [--option <value>]` | NEW | resumes a parked/awaiting workflow explicitly |
| direct step verbs | REUSE | remain available and retain child-run semantics |

**Load-bearing architecture decision:** workflow state is a separate Go-owned aggregate around existing
child runs. It is not owned by the CLI, not encoded only in model output, and not placed in the
separate `saki-builder` repository. This is the seam that makes multi-phase continuation, dedupe during
waits, restart survival, and verified terminal status one coherent contract.

---

Status: [x] Draft  [x] Annotated  [x] Approved  [x] In Progress  [x] Complete
Readiness Gate: [x] Evidence ledger present and every blocking item cited  [x] Blocking set empty  [x] Unknowns <= 2
